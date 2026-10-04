package testgenvalidate

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/managedtest"
	"unit-test-ide.local/test-service/internal/testgenpublish"
	"unit-test-ide.local/test-service/internal/testgenrender"
)

var ErrSelectedValidation = errors.New("selected managed output validation failed")

const maxSelectedOutput = 64 << 10
const maxSelectedCoverage = 8 << 20
const maxSelectedReceipt = 1 << 20

type SelectedPhase string

const (
	SelectedConfigure     SelectedPhase = "configure"
	SelectedBuild         SelectedPhase = "build"
	SelectedDiscover      SelectedPhase = "discover"
	SelectedRun           SelectedPhase = "run"
	SelectedCoveragePhase SelectedPhase = "coverage"
)

var selectedPhases = [...]SelectedPhase{SelectedConfigure, SelectedBuild, SelectedDiscover, SelectedRun, SelectedCoveragePhase}

type SelectedDirectory string

const (
	SelectedSourceDir    SelectedDirectory = "source"
	SelectedBuildDir     SelectedDirectory = "build"
	SelectedArtifactsDir SelectedDirectory = "artifacts"
)

// SelectedCommand is configured by the service, not supplied by IPC or a
// ManagedSelection. Executable is a pinned, direct regular file.
type SelectedCommand struct {
	Executable string            `json:"executable"`
	Args       []string          `json:"args"`
	Dir        SelectedDirectory `json:"dir"`
}

type SelectedStageResult struct {
	ExitCode              int
	Output                []byte
	DiscoveredCaseIDs     []string
	ExecutedCaseIDs       []string
	TestsRun, TestsPassed int
	CollectorRelativePath string
}

// SelectedToolchainRunner must honor ctx, execute the supplied argv without a
// shell, and never run outside Roots. A production adapter should use the
// prepared process/lease boundary; this package registers no default runner.
type SelectedToolchainRunner interface {
	Run(context.Context, SelectedPhase, Roots, SelectedCommand) (SelectedStageResult, error)
}

// SelectedStageExecutor is the production path for projects whose trusted
// build/test/coverage plans are resolved per run. It returns the digest of the
// exact service-owned phase plan that produced the result. VerifyPlan must
// re-attest the complete ordered plan before a receipt is accepted or reused.
// Static SelectedCommand remains available only for the closed legacy seam.
type SelectedStageExecutor interface {
	Execute(context.Context, testgenpublish.ManagedSelection, SelectedPhase, Roots) (SelectedStageResult, string, error)
	VerifyPlan(context.Context, testgenpublish.ManagedSelection, []SelectedPhaseReceipt, string) error
}

type SelectedFileCoverage struct {
	ID      string                 `json:"id"`
	Summary coveragedomain.Summary `json:"summary"`
}
type SelectedFunctionCoverage struct {
	ID      string                 `json:"id"`
	FileID  string                 `json:"fileId"`
	Summary coveragedomain.Summary `json:"summary"`
}
type SelectedCoverage struct {
	Project   coveragedomain.Summary     `json:"project"`
	Files     []SelectedFileCoverage     `json:"files"`
	Functions []SelectedFunctionCoverage `json:"functions"`
}

// SelectionContext must come from durable, trusted run/snapshot/toolchain and
// accepted-baseline state. PreviousReceipt is never trusted without its MAC.
type SelectionContext struct {
	SnapshotDigest, ToolchainID, SourceDigest string
	Baseline                                  SelectedCoverage
	PreviousReceipt                           []byte
}
type SelectionResolver func(context.Context, string) (SelectionContext, error)

type SelectedConfig struct {
	SourceRoot, TempRoot string
	Resolve              SelectionResolver
	Runner               SelectedToolchainRunner
	Executor             SelectedStageExecutor
	Commands             map[SelectedPhase]SelectedCommand
	ToolSHA256           map[string]string
	MACKey               []byte
	PhaseTimeout         time.Duration
}

type SelectedValidator struct{ config SelectedConfig }

type SelectedPhaseReceipt struct {
	Phase          SelectedPhase `json:"phase"`
	Status         string        `json:"status"`
	StartedAt      time.Time     `json:"startedAt"`
	FinishedAt     time.Time     `json:"finishedAt"`
	CommandDigest  string        `json:"commandDigest"`
	EvidenceDigest string        `json:"evidenceDigest"`
}

type SelectedReceipt struct {
	Version              string                 `json:"version"`
	RunID                string                 `json:"runId"`
	SelectedOutputDigest string                 `json:"selectedOutputDigest"`
	SnapshotDigest       string                 `json:"snapshotDigest"`
	ToolchainID          string                 `json:"toolchainId"`
	SourceDigest         string                 `json:"sourceDigest"`
	BaselineDigest       string                 `json:"baselineDigest"`
	CommandSetDigest     string                 `json:"commandSetDigest"`
	Coverage             SelectedCoverage       `json:"coverage"`
	Phases               []SelectedPhaseReceipt `json:"phases"`
	MAC                  string                 `json:"mac"`
}

func NewSelectedValidator(config SelectedConfig) (*SelectedValidator, error) {
	if config.Resolve == nil || !directDirectory(config.SourceRoot) || !directDirectory(config.TempRoot) || config.SourceRoot == config.TempRoot || within(config.SourceRoot, config.TempRoot) || within(config.TempRoot, config.SourceRoot) || len(config.MACKey) < 32 || config.PhaseTimeout <= 0 || config.PhaseTimeout > 5*time.Minute {
		return nil, ErrSelectedValidation
	}
	dynamic := config.Executor != nil
	if dynamic && (config.Runner != nil || len(config.Commands) != 0 || len(config.ToolSHA256) != 0) ||
		!dynamic && (config.Runner == nil || len(config.Commands) != len(selectedPhases) || len(config.ToolSHA256) == 0) {
		return nil, ErrSelectedValidation
	}
	commands := make(map[SelectedPhase]SelectedCommand, len(config.Commands))
	tools := make(map[string]string, len(config.ToolSHA256))
	for path, digest := range config.ToolSHA256 {
		if !validDigest(digest) || !directRegularPath(path) || digestFile(path) != digest {
			return nil, ErrSelectedValidation
		}
		tools[path] = digest
	}
	if !dynamic {
		for _, phase := range selectedPhases {
			command, ok := config.Commands[phase]
			if !ok || !validSelectedCommand(command, tools) {
				return nil, ErrSelectedValidation
			}
			command.Args = append([]string(nil), command.Args...)
			commands[phase] = command
		}
	}
	config.Commands = commands
	config.ToolSHA256 = tools
	config.MACKey = bytes.Clone(config.MACKey)
	return &SelectedValidator{config: config}, nil
}

func validSelectedCommand(command SelectedCommand, tools map[string]string) bool {
	if !filepath.IsAbs(command.Executable) || filepath.Clean(command.Executable) != command.Executable || tools[command.Executable] == "" || len(command.Args) > 128 || command.Dir != SelectedSourceDir && command.Dir != SelectedBuildDir && command.Dir != SelectedArtifactsDir {
		return false
	}
	base := strings.ToLower(filepath.Base(command.Executable))
	if strings.HasSuffix(base, ".bat") || strings.HasSuffix(base, ".cmd") || strings.HasSuffix(base, ".ps1") || strings.HasSuffix(base, ".sh") {
		return false
	}
	switch strings.TrimSuffix(base, ".exe") {
	case "cmd", "powershell", "pwsh", "sh", "bash", "dash", "zsh", "fish", "busybox":
		return false
	}
	for _, arg := range command.Args {
		if arg == "" || len(arg) > 4096 || strings.ContainsAny(arg, "\x00\r\n$;&|<>`") {
			return false
		}
	}
	return true
}

func digestFile(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, (256<<20)+1))
	if err != nil || n > 256<<20 {
		return ""
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func selectedFilePath(path string) bool {
	return safeRelative(path) && (strings.HasPrefix(path, "tests/generated/") && (strings.HasSuffix(path, "_test.c") || strings.HasSuffix(path, "_test.cpp")) || strings.HasPrefix(path, "tests/") && filepath.Base(path) == "CMakeLists.txt")
}

func selectedOutputDigest(files []testgenrender.StagedFile) (string, error) {
	if len(files) < 2 || len(files) > 128 {
		return "", ErrSelectedValidation
	}
	type output struct{ Path, Digest string }
	values := make([]output, 0, len(files))
	seen := map[string]bool{}
	cmake := 0
	total := 0
	for _, file := range files {
		if !selectedFilePath(file.Path) || seen[strings.ToLower(file.Path)] || len(file.Content) == 0 || len(file.Content) > maxEditBytes || total > 16<<20-len(file.Content) || file.AfterDigest != digestBytes(file.Content) {
			return "", ErrSelectedValidation
		}
		if strings.HasSuffix(file.Path, "/CMakeLists.txt") {
			cmake++
		}
		seen[strings.ToLower(file.Path)] = true
		total += len(file.Content)
		values = append(values, output{file.Path, file.AfterDigest})
	}
	if cmake != 1 {
		return "", ErrSelectedValidation
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Path < values[j].Path })
	encoded, _ := json.Marshal(values)
	return digestBytes(append([]byte("managed-selected-v1\x00"), encoded...)), nil
}

func expectedSelectedCases(files []testgenrender.StagedFile) ([]string, error) {
	ids := make([]string, 0)
	seen := map[string]bool{}
	for _, file := range files {
		if !strings.HasPrefix(file.Path, "tests/generated/") {
			continue
		}
		document, err := managedtest.ParseDocument(file.Content, maxEditBytes, 4096)
		if err != nil {
			return nil, ErrSelectedValidation
		}
		for _, block := range document.Blocks {
			if seen[block.CaseID] {
				return nil, ErrSelectedValidation
			}
			seen[block.CaseID] = true
			ids = append(ids, block.CaseID)
		}
	}
	if len(ids) == 0 || len(ids) > 10000 {
		return nil, ErrSelectedValidation
	}
	sort.Strings(ids)
	return ids, nil
}

func (v *SelectedValidator) context(ctx context.Context, selection testgenpublish.ManagedSelection) (SelectionContext, string, error) {
	if v == nil || ctx == nil || ctx.Err() != nil || !validHexN(selection.RunID, 32) || !validDigest(selection.SnapshotDigest) || !validDigest(selection.SelectedOutputDigest) || !validToolchainID(selection.ToolchainID) {
		if ctx != nil && ctx.Err() != nil {
			return SelectionContext{}, "", ctx.Err()
		}
		return SelectionContext{}, "", ErrSelectedValidation
	}
	selected, err := selectedOutputDigest(selection.Files)
	if err != nil || selected != selection.SelectedOutputDigest {
		return SelectionContext{}, "", ErrSelectedValidation
	}
	current, err := v.config.Resolve(ctx, selection.RunID)
	if err != nil || current.SnapshotDigest != selection.SnapshotDigest || current.ToolchainID != selection.ToolchainID || !validToolchainID(current.ToolchainID) || !validDigest(current.SourceDigest) || !validCoverage(current.Baseline) {
		return SelectionContext{}, "", ErrSelectedValidation
	}
	_, actual, err := sourceFingerprint(v.config.SourceRoot)
	if err != nil || actual != current.SourceDigest {
		return SelectionContext{}, "", ErrSelectedValidation
	}
	for path, digest := range v.config.ToolSHA256 {
		if !directRegularPath(path) || digestFile(path) != digest {
			return SelectionContext{}, "", ErrSelectedValidation
		}
	}
	return current, selected, nil
}

func validToolchainID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

func (v *SelectedValidator) commandDigest(phase SelectedPhase) string {
	command := v.config.Commands[phase]
	encoded, _ := json.Marshal(struct {
		Phase      SelectedPhase
		Command    SelectedCommand
		ToolSHA256 string
	}{phase, command, v.config.ToolSHA256[command.Executable]})
	return digestBytes(append([]byte("managed-command-v1\x00"), encoded...))
}

func (v *SelectedValidator) commandSetDigest() string {
	values := make([]string, 0, len(selectedPhases))
	for _, phase := range selectedPhases {
		values = append(values, v.commandDigest(phase))
	}
	encoded, _ := json.Marshal(values)
	return digestBytes(append([]byte("managed-command-set-v1\x00"), encoded...))
}

func selectedDynamicPlanDigest(phases []SelectedPhaseReceipt) string {
	type phasePlan struct {
		Phase  SelectedPhase `json:"phase"`
		Digest string        `json:"digest"`
	}
	values := make([]phasePlan, len(phases))
	for index, phase := range phases {
		values[index] = phasePlan{Phase: phase.Phase, Digest: phase.CommandDigest}
	}
	encoded, _ := json.Marshal(values)
	return digestBytes(append([]byte("managed-dynamic-plan-v1\x00"), encoded...))
}

func coverageDigest(value SelectedCoverage) string {
	encoded, _ := json.Marshal(value)
	return digestBytes(append([]byte("managed-baseline-v1\x00"), encoded...))
}

func (v *SelectedValidator) Validate(ctx context.Context, selection testgenpublish.ManagedSelection) (receipt []byte, err error) {
	current, _, err := v.context(ctx, selection)
	if err != nil {
		return nil, err
	}
	expectedCases, err := expectedSelectedCases(selection.Files)
	if err != nil {
		return nil, err
	}
	if len(current.PreviousReceipt) != 0 {
		if err := v.Verify(ctx, selection, current.PreviousReceipt); err != nil {
			return nil, ErrSelectedValidation
		}
		return bytes.Clone(current.PreviousReceipt), nil
	}
	original, _, err := sourceFingerprint(v.config.SourceRoot)
	if err != nil {
		return nil, ErrSelectedValidation
	}
	edits := make([]testgenrender.StagedFile, len(selection.Files))
	for i, file := range selection.Files {
		file.Content = bytes.Clone(file.Content)
		file.BeforeDigest = original[strings.ToLower(file.Path)]
		edits[i] = file
	}
	roots, root, err := snapshot(v.config.SourceRoot, v.config.TempRoot, edits, original)
	if root != "" {
		defer func() {
			if cleanupErr := cleanup(root); cleanupErr != nil {
				receipt = nil
				err = errors.Join(err, ErrSelectedValidation, cleanupErr)
			}
		}()
	}
	if err != nil {
		return nil, ErrSelectedValidation
	}
	staged, _, err := sourceFingerprint(roots.Source)
	if err != nil {
		return nil, ErrSelectedValidation
	}
	result := SelectedReceipt{Version: "1", RunID: selection.RunID, SelectedOutputDigest: selection.SelectedOutputDigest, SnapshotDigest: current.SnapshotDigest, ToolchainID: current.ToolchainID, SourceDigest: current.SourceDigest, BaselineDigest: coverageDigest(current.Baseline), Phases: make([]SelectedPhaseReceipt, 0, len(selectedPhases))}
	if v.config.Executor == nil {
		result.CommandSetDigest = v.commandSetDigest()
	}
	for _, phase := range selectedPhases {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !v.unchanged(ctx, selection, current, original, staged, roots) {
			return nil, ErrSelectedValidation
		}
		phaseCtx, cancel := context.WithTimeout(ctx, v.config.PhaseTimeout)
		started := time.Now().UTC().Truncate(time.Millisecond)
		var (
			outcome       SelectedStageResult
			phasePlanHash string
			runErr        error
		)
		if v.config.Executor != nil {
			outcome, phasePlanHash, runErr = v.config.Executor.Execute(phaseCtx, selection, phase, roots)
		} else {
			outcome, runErr = v.config.Runner.Run(phaseCtx, phase, roots, v.config.Commands[phase])
			phasePlanHash = v.commandDigest(phase)
		}
		phaseErr := phaseCtx.Err()
		cancel()
		finished := time.Now().UTC().Truncate(time.Millisecond)
		if runErr != nil || phaseErr != nil || outcome.ExitCode != 0 || !validDigest(phasePlanHash) || len(outcome.Output) > maxSelectedOutput || !v.unchanged(ctx, selection, current, original, staged, roots) {
			return nil, ErrSelectedValidation
		}
		if phase != SelectedDiscover && len(outcome.DiscoveredCaseIDs) != 0 || phase != SelectedRun && (outcome.TestsRun != 0 || outcome.TestsPassed != 0 || len(outcome.ExecutedCaseIDs) != 0) || phase != SelectedCoveragePhase && outcome.CollectorRelativePath != "" {
			return nil, ErrSelectedValidation
		}
		if phase == SelectedDiscover && (!validDiscovered(outcome.DiscoveredCaseIDs) || !reflect.DeepEqual(outcome.DiscoveredCaseIDs, expectedCases)) || phase == SelectedRun && (outcome.TestsRun != len(expectedCases) || outcome.TestsPassed != outcome.TestsRun || !reflect.DeepEqual(outcome.ExecutedCaseIDs, expectedCases)) {
			return nil, ErrSelectedValidation
		}
		if phase == SelectedCoveragePhase {
			if outcome.CollectorRelativePath != "selected-coverage.json" {
				return nil, ErrSelectedValidation
			}
			coverage, err := readSelectedCoverage(roots.Artifacts)
			if err != nil || !coverageNotRegressed(current.Baseline, coverage) {
				return nil, ErrSelectedValidation
			}
			result.Coverage = coverage
		}
		evidence, _ := json.Marshal(struct {
			OutputDigest          string
			DiscoveredCaseIDs     []string
			ExecutedCaseIDs       []string
			TestsRun, TestsPassed int
			CollectorDigest       string
		}{digestBytes(outcome.Output), outcome.DiscoveredCaseIDs, outcome.ExecutedCaseIDs, outcome.TestsRun, outcome.TestsPassed, coverageDigest(result.Coverage)})
		result.Phases = append(result.Phases, SelectedPhaseReceipt{Phase: phase, Status: "passed", StartedAt: started, FinishedAt: finished, CommandDigest: phasePlanHash, EvidenceDigest: digestBytes(evidence)})
	}
	if v.config.Executor != nil {
		result.CommandSetDigest = selectedDynamicPlanDigest(result.Phases)
		if v.config.Executor.VerifyPlan(ctx, selection, append([]SelectedPhaseReceipt(nil), result.Phases...), result.CommandSetDigest) != nil {
			return nil, ErrSelectedValidation
		}
	}
	if !v.unchanged(ctx, selection, current, original, staged, roots) {
		return nil, ErrSelectedValidation
	}
	result.MAC = v.sign(result)
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) == 0 || len(encoded) > maxSelectedReceipt {
		return nil, ErrSelectedValidation
	}
	return encoded, nil
}

func validDiscovered(ids []string) bool {
	if len(ids) == 0 || len(ids) > 10000 {
		return false
	}
	seen := map[string]bool{}
	previous := ""
	for _, id := range ids {
		if len(id) != 36 || !strings.HasPrefix(id, "utc_") || !validHexN(id[4:], 32) || seen[id] || id <= previous {
			return false
		}
		seen[id] = true
		previous = id
	}
	return true
}

func validHexN(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (v *SelectedValidator) unchanged(ctx context.Context, selection testgenpublish.ManagedSelection, initial SelectionContext, original, staged map[string]string, roots Roots) bool {
	if ctx.Err() != nil || !scanStageRoots(roots) {
		return false
	}
	current, err := v.config.Resolve(ctx, selection.RunID)
	if err != nil || current.SnapshotDigest != initial.SnapshotDigest || current.ToolchainID != initial.ToolchainID || current.SourceDigest != initial.SourceDigest || coverageDigest(current.Baseline) != coverageDigest(initial.Baseline) {
		return false
	}
	actual, _, err := sourceFingerprint(v.config.SourceRoot)
	if err != nil || !reflect.DeepEqual(actual, original) {
		return false
	}
	actual, _, err = sourceFingerprint(roots.Source)
	if err != nil || !reflect.DeepEqual(actual, staged) {
		return false
	}
	for path, digest := range v.config.ToolSHA256 {
		if !directRegularPath(path) || digestFile(path) != digest {
			return false
		}
	}
	return true
}

func readSelectedCoverage(artifactRoot string) (SelectedCoverage, error) {
	name := filepath.Join(artifactRoot, "selected-coverage.json")
	if !directRegularPath(name) || !singlyLinked(name) {
		return SelectedCoverage{}, ErrSelectedValidation
	}
	before, err := os.Lstat(name)
	if err != nil || !before.Mode().IsRegular() {
		return SelectedCoverage{}, ErrSelectedValidation
	}
	root, err := os.OpenRoot(artifactRoot)
	if err != nil {
		return SelectedCoverage{}, ErrSelectedValidation
	}
	defer root.Close()
	file, err := root.Open("selected-coverage.json")
	if err != nil {
		return SelectedCoverage{}, ErrSelectedValidation
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !os.SameFile(before, info) || info.Size() <= 0 || info.Size() > maxSelectedCoverage {
		return SelectedCoverage{}, ErrSelectedValidation
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSelectedCoverage+1))
	if err != nil || len(data) > maxSelectedCoverage {
		return SelectedCoverage{}, ErrSelectedValidation
	}
	after, err := os.Lstat(name)
	if err != nil || !os.SameFile(before, after) || !after.Mode().IsRegular() || !singlyLinked(name) {
		return SelectedCoverage{}, ErrSelectedValidation
	}
	var value SelectedCoverage
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil || decoder.Decode(new(any)) != io.EOF || !validCoverage(value) {
		return SelectedCoverage{}, ErrSelectedValidation
	}
	canonical, _ := json.Marshal(value)
	if !bytes.Equal(canonical, data) {
		return SelectedCoverage{}, ErrSelectedValidation
	}
	return value, nil
}

func validCoverage(value SelectedCoverage) bool {
	if _, err := coveragedomain.NewSummary(value.Project); err != nil || len(value.Files) == 0 || len(value.Files) > 10000 || len(value.Functions) == 0 || len(value.Functions) > 100000 {
		return false
	}
	files := map[string]coveragedomain.Summary{}
	var total coveragedomain.Summary
	previous := ""
	for _, file := range value.Files {
		if !validHexN(file.ID, 32) || file.ID <= previous {
			return false
		}
		if _, err := coveragedomain.NewSummary(file.Summary); err != nil {
			return false
		}
		var err error
		total, err = coveragedomain.AddSummary(total, file.Summary)
		if err != nil {
			return false
		}
		files[file.ID] = file.Summary
		previous = file.ID
	}
	if total != value.Project {
		return false
	}
	previous = ""
	functionCounts := map[string]coveragedomain.Metric{}
	for _, function := range value.Functions {
		_, knownFile := files[function.FileID]
		if !validHexN(function.ID, 32) || !validHexN(function.FileID, 32) || function.ID <= previous || !knownFile {
			return false
		}
		if _, err := coveragedomain.NewSummary(function.Summary); err != nil {
			return false
		}
		parent := files[function.FileID]
		if function.Summary.Functions.Covered > parent.Functions.Covered || function.Summary.Functions.Total > parent.Functions.Total || function.Summary.Lines.Covered > parent.Lines.Covered || function.Summary.Lines.Total > parent.Lines.Total || function.Summary.Branches.Covered > parent.Branches.Covered || function.Summary.Branches.Total > parent.Branches.Total {
			return false
		}
		count := functionCounts[function.FileID]
		if count.Covered > coveragedomain.MaxSafeInteger-function.Summary.Functions.Covered || count.Total > coveragedomain.MaxSafeInteger-function.Summary.Functions.Total {
			return false
		}
		count.Covered += function.Summary.Functions.Covered
		count.Total += function.Summary.Functions.Total
		functionCounts[function.FileID] = count
		previous = function.ID
	}
	for id, summary := range files {
		if functionCounts[id] != summary.Functions {
			return false
		}
	}
	return true
}

func coverageNotRegressed(before, after SelectedCoverage) bool {
	if !validCoverage(before) || !validCoverage(after) || len(before.Files) != len(after.Files) || len(before.Functions) != len(after.Functions) || !summaryNotRegressed(before.Project, after.Project) {
		return false
	}
	for i, file := range before.Files {
		if file.ID != after.Files[i].ID || !summaryNotRegressed(file.Summary, after.Files[i].Summary) {
			return false
		}
	}
	for i, function := range before.Functions {
		if function.ID != after.Functions[i].ID || function.FileID != after.Functions[i].FileID || !summaryNotRegressed(function.Summary, after.Functions[i].Summary) {
			return false
		}
	}
	return true
}

func summaryNotRegressed(before, after coveragedomain.Summary) bool {
	return before.Functions.Total == after.Functions.Total && before.Lines.Total == after.Lines.Total && before.Branches.Total == after.Branches.Total && after.Functions.Covered >= before.Functions.Covered && after.Lines.Covered >= before.Lines.Covered && after.Branches.Covered >= before.Branches.Covered
}

func (v *SelectedValidator) sign(receipt SelectedReceipt) string {
	receipt.MAC = ""
	encoded, _ := json.Marshal(receipt)
	mac := hmac.New(sha256.New, v.config.MACKey)
	_, _ = mac.Write(append([]byte("managed-selection-receipt-v1\x00"), encoded...))
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify authenticates a receipt against the exact selected bytes and current
// source/toolchain/baseline. It does not accept a digest-only prior receipt.
func (v *SelectedValidator) Verify(ctx context.Context, selection testgenpublish.ManagedSelection, data []byte) error {
	current, _, err := v.context(ctx, selection)
	if err != nil || len(data) == 0 || len(data) > maxSelectedReceipt {
		return ErrSelectedValidation
	}
	var receipt SelectedReceipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&receipt) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrSelectedValidation
	}
	canonical, _ := json.Marshal(receipt)
	if !bytes.Equal(canonical, data) || !validDigest(receipt.MAC) {
		return ErrSelectedValidation
	}
	want, _ := hex.DecodeString(v.sign(receipt))
	got, _ := hex.DecodeString(receipt.MAC)
	expectedPlanDigest := v.commandSetDigest()
	if v.config.Executor != nil {
		expectedPlanDigest = selectedDynamicPlanDigest(receipt.Phases)
	}
	if !hmac.Equal(got, want) || receipt.Version != "1" || receipt.RunID != selection.RunID || receipt.SelectedOutputDigest != selection.SelectedOutputDigest || receipt.SnapshotDigest != current.SnapshotDigest || receipt.ToolchainID != current.ToolchainID || receipt.SourceDigest != current.SourceDigest || receipt.BaselineDigest != coverageDigest(current.Baseline) || receipt.CommandSetDigest != expectedPlanDigest || !coverageNotRegressed(current.Baseline, receipt.Coverage) || len(receipt.Phases) != len(selectedPhases) {
		return ErrSelectedValidation
	}
	for i, phase := range receipt.Phases {
		if phase.Phase != selectedPhases[i] || phase.Status != "passed" || phase.StartedAt.IsZero() || phase.FinishedAt.Before(phase.StartedAt) || !validDigest(phase.CommandDigest) || !validDigest(phase.EvidenceDigest) ||
			v.config.Executor == nil && phase.CommandDigest != v.commandDigest(phase.Phase) {
			return ErrSelectedValidation
		}
	}
	if v.config.Executor != nil && v.config.Executor.VerifyPlan(ctx, selection, append([]SelectedPhaseReceipt(nil), receipt.Phases...), receipt.CommandSetDigest) != nil {
		return ErrSelectedValidation
	}
	return nil
}
