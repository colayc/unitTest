package artifactstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"unit-test-ide.local/test-service/internal/diagnostic"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

const (
	maxStreamArtifactBytes     = 64 * 1024 * 1024
	maxDiagnosticArtifactBytes = 32 * 1024 * 1024
	maxJSONArtifactBytes       = 4 * 1024 * 1024
	maxTestResultArtifactBytes = 128 * 1024 * 1024
	maxCoverageArtifactBytes   = 128 * 1024 * 1024
)

type taskSink struct {
	mu           sync.Mutex
	store        *Store
	taskID       string
	taskKind     task.Kind
	stdout       bytes.Buffer
	stderr       bytes.Buffer
	diagnostics  bytes.Buffer
	json         map[string]pendingArtifact
	finished     bool
	aborted      bool
	finalized    []task.Artifact
	capabilities []*finalizedArtifactCapability
	rolledBack   bool
	rollbackErr  error
	released     bool
	releaseErr   error
}

type pendingArtifact struct {
	id   string
	kind string
	data []byte
}

func (s *Store) OpenTask(
	ctx context.Context,
	taskID string,
	kind task.Kind,
) (task.ArtifactSink, error) {
	if s == nil || s.root == nil || ctx == nil {
		return nil, ErrStoreUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validGeneratedID(taskID) ||
		!supportedTaskKind(kind) {
		return nil, ErrInvalidArtifact
	}
	return &taskSink{
		store: s, taskID: taskID, taskKind: kind,
		json: make(map[string]pendingArtifact),
	}, nil
}

func (s *taskSink) AppendOutput(
	ctx context.Context,
	stepID string,
	stream string,
	data []byte,
) error {
	if ctx == nil {
		return ErrInvalidArtifact
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if stepID == "" || len(stepID) > 64 || strings.IndexByte(stepID, 0) >= 0 {
		return ErrInvalidArtifact
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unavailable() {
		return ErrStoreUnavailable
	}
	if s.taskKind == task.KindCoverageRun {
		return nil
	}
	if s.taskKind == task.KindSimulation || len(data) == 0 {
		return nil
	}
	destination := &s.stdout
	switch stream {
	case "stdout", "combined":
	case "stderr":
		destination = &s.stderr
	default:
		return ErrInvalidArtifact
	}
	if len(data) > maxStreamArtifactBytes-destination.Len() {
		return ErrStoreUnavailable
	}
	_, _ = destination.Write(data)
	return nil
}

func (s *taskSink) AppendDiagnostic(
	ctx context.Context,
	value diagnostic.Diagnostic,
) error {
	if ctx == nil {
		return ErrInvalidArtifact
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unavailable() {
		return ErrStoreUnavailable
	}
	if s.taskKind == task.KindCoverageRun {
		return nil
	}
	if s.taskKind == task.KindSimulation {
		return nil
	}
	if value.TaskID != "" && value.TaskID != s.taskID ||
		value.StepID == "" || len(value.StepID) > 64 ||
		value.Code == "" || value.Severity == "" ||
		strings.IndexByte(value.StepID, 0) >= 0 ||
		strings.IndexByte(value.Code, 0) >= 0 {
		return ErrInvalidArtifact
	}
	value.TaskID = s.taskID
	encoded, err := json.Marshal(projectArtifactDiagnostic(value))
	if err != nil || len(encoded)+1 > maxDiagnosticArtifactBytes-s.diagnostics.Len() {
		return ErrStoreUnavailable
	}
	_, _ = s.diagnostics.Write(encoded)
	_ = s.diagnostics.WriteByte('\n')
	return nil
}

func (s *taskSink) CommitJSON(
	ctx context.Context,
	artifactID string,
	kind string,
	value any,
) error {
	if ctx == nil {
		return ErrInvalidArtifact
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validGeneratedID(artifactID) {
		return ErrInvalidArtifact
	}
	var (
		encoded []byte
		err     error
	)
	switch {
	case s.taskKind == task.KindSimulation && kind == "task-summary":
		encoded, err = canonicalSummary(s.taskID, value)
	case s.taskKind == task.KindCMakeBuild && kind == "build-summary":
		encoded, err = canonicalSummary(s.taskID, value)
	case s.taskKind == task.KindCMakeBuild && kind == "execution-plan":
		encoded, err = safeExecutionPlanJSON(value)
	case (s.taskKind == task.KindTestDiscovery ||
		s.taskKind == task.KindTestRun) &&
		kind == "task-summary":
		encoded, err = canonicalSummary(s.taskID, value)
	case (s.taskKind == task.KindTestDiscovery ||
		s.taskKind == task.KindTestRun) &&
		kind == "execution-plan":
		encoded, err = safeExecutionPlanJSON(value)
	case s.taskKind == task.KindTestRun &&
		(kind == "test-selection" || kind == "test-run-summary"):
		encoded, err = safeDomainJSON(value)
	default:
		return ErrInvalidArtifact
	}
	if err != nil || len(encoded) > maxJSONArtifactBytes {
		return ErrInvalidArtifact
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unavailable() {
		return ErrStoreUnavailable
	}
	if _, exists := s.json[kind]; exists {
		return ErrInvalidArtifact
	}
	for _, pending := range s.json {
		if pending.id == artifactID {
			return ErrInvalidArtifact
		}
	}
	s.json[kind] = pendingArtifact{
		id: artifactID, kind: kind, data: append([]byte(nil), encoded...),
	}
	return nil
}

func (s *taskSink) CommitJSONLines(
	ctx context.Context,
	artifactID string,
	kind string,
	values []json.RawMessage,
) error {
	if ctx == nil || kind != "test-results" ||
		s.taskKind != task.KindTestRun ||
		!validGeneratedID(artifactID) {
		return ErrInvalidArtifact
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var encoded bytes.Buffer
	for _, raw := range values {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		var candidate testdomain.TestItemResult
		if err := decoder.Decode(&candidate); err != nil ||
			decoder.Decode(&struct{}{}) != io.EOF {
			return ErrInvalidArtifact
		}
		validated, err := testdomain.NewTestItemResult(candidate)
		if err != nil {
			return ErrInvalidArtifact
		}
		line, err := safeDomainJSON(validated)
		if err != nil ||
			encoded.Len()+len(line) > maxTestResultArtifactBytes {
			return ErrInvalidArtifact
		}
		_, _ = encoded.Write(line)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unavailable() {
		return ErrStoreUnavailable
	}
	if _, exists := s.json[kind]; exists {
		return ErrInvalidArtifact
	}
	for _, pending := range s.json {
		if pending.id == artifactID {
			return ErrInvalidArtifact
		}
	}
	s.json[kind] = pendingArtifact{
		id: artifactID, kind: kind,
		data: append([]byte(nil), encoded.Bytes()...),
	}
	return nil
}

func (s *taskSink) CommitBlob(
	ctx context.Context,
	artifactID string,
	kind string,
	data []byte,
) error {
	if ctx == nil || s.taskKind != task.KindCoverageRun ||
		!validGeneratedID(artifactID) || len(data) == 0 ||
		len(data) > maxCoverageArtifactBytes {
		return ErrInvalidArtifact
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	switch kind {
	case "coverage-json", "junit-xml", "coverage-html":
	default:
		return ErrInvalidArtifact
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unavailable() {
		return ErrStoreUnavailable
	}
	if _, exists := s.json[kind]; exists {
		return ErrInvalidArtifact
	}
	for _, pending := range s.json {
		if pending.id == artifactID {
			return ErrInvalidArtifact
		}
	}
	s.json[kind] = pendingArtifact{
		id: artifactID, kind: kind, data: append([]byte(nil), data...),
	}
	return nil
}

func (s *taskSink) Finalize(
	ctx context.Context,
	at time.Time,
) ([]task.Artifact, error) {
	if ctx == nil || at.IsZero() {
		return nil, ErrInvalidArtifact
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.unavailable() {
		s.mu.Unlock()
		return nil, ErrStoreUnavailable
	}
	pending, err := s.pendingArtifacts()
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.finished = true
	s.clear()
	s.mu.Unlock()

	artifacts := make([]task.Artifact, 0, len(pending))
	capabilities := make([]*finalizedArtifactCapability, 0, len(pending))
	for _, value := range pending {
		artifact, capability, err := s.store.commitArtifactDataRetained(
			ctx, s.taskID, value.id, value.kind, at, value.data,
		)
		if err != nil {
			rollbackErr := rollbackFinalizedCapabilities(artifacts, capabilities)
			return nil, errors.Join(err, rollbackErr)
		}
		artifacts = append(artifacts, artifact)
		capabilities = append(capabilities, capability)
	}
	s.mu.Lock()
	s.finalized = append([]task.Artifact{}, artifacts...)
	s.capabilities = capabilities
	s.mu.Unlock()
	return artifacts, nil
}

func (s *taskSink) RollbackFinalized(ctx context.Context, artifacts []task.Artifact) error {
	if s == nil || ctx == nil {
		return ErrInvalidArtifact
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil || !s.finished || s.aborted || s.finalized == nil ||
		len(s.finalized) != len(artifacts) {
		return ErrStoreUnavailable
	}
	for index := range artifacts {
		if artifacts[index] != s.finalized[index] {
			return ErrInvalidArtifact
		}
	}
	if s.rolledBack {
		return s.rollbackErr
	}
	if s.released {
		return ErrStoreUnavailable
	}
	s.rollbackErr = rollbackFinalizedCapabilities(s.finalized, s.capabilities)
	s.capabilities = nil
	s.rolledBack = true
	return s.rollbackErr
}

func (s *taskSink) ReleaseFinalized(ctx context.Context, artifacts []task.Artifact) error {
	if s == nil || ctx == nil {
		return ErrInvalidArtifact
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil || !s.finished || s.aborted || s.finalized == nil ||
		len(s.finalized) != len(artifacts) {
		return ErrStoreUnavailable
	}
	for index := range artifacts {
		if artifacts[index] != s.finalized[index] {
			return ErrInvalidArtifact
		}
	}
	if s.released {
		return s.releaseErr
	}
	if s.rolledBack {
		return ErrStoreUnavailable
	}
	s.releaseErr = closeFinalizedCapabilities(s.capabilities)
	s.capabilities = nil
	s.released = true
	return s.releaseErr
}

func (s *taskSink) Abort(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidArtifact
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return ErrStoreUnavailable
	}
	s.aborted = true
	s.clear()
	return nil
}

func (s *taskSink) pendingArtifacts() ([]pendingArtifact, error) {
	result := make([]pendingArtifact, 0, 5)
	switch s.taskKind {
	case task.KindSimulation:
		summary, ok := s.json["task-summary"]
		if !ok || len(s.json) != 1 {
			return nil, ErrInvalidArtifact
		}
		result = append(result, summary)
	case task.KindCMakeBuild:
		executionPlan, hasPlan := s.json["execution-plan"]
		buildSummary, hasSummary := s.json["build-summary"]
		if !hasPlan || !hasSummary || len(s.json) != 2 {
			return nil, ErrInvalidArtifact
		}
		stdoutID, err := newGeneratedID()
		if err != nil {
			return nil, err
		}
		stderrID, err := newGeneratedID()
		if err != nil {
			return nil, err
		}
		diagnosticsID, err := newGeneratedID()
		if err != nil {
			return nil, err
		}
		result = append(result,
			executionPlan,
			buildSummary,
			pendingArtifact{id: stdoutID, kind: "stdout", data: append([]byte(nil), s.stdout.Bytes()...)},
			pendingArtifact{id: stderrID, kind: "stderr", data: append([]byte(nil), s.stderr.Bytes()...)},
			pendingArtifact{id: diagnosticsID, kind: "diagnostics", data: append([]byte(nil), s.diagnostics.Bytes()...)},
		)
	case task.KindTestDiscovery, task.KindTestRun:
		executionPlan, hasPlan := s.json["execution-plan"]
		summary, hasSummary := s.json["task-summary"]
		expectedJSON := 2
		if s.taskKind == task.KindTestRun {
			expectedJSON = 5
		}
		if !hasPlan || !hasSummary || len(s.json) != expectedJSON {
			return nil, ErrInvalidArtifact
		}
		if s.taskKind == task.KindTestRun {
			selection, hasSelection := s.json["test-selection"]
			results, hasResults := s.json["test-results"]
			runSummary, hasRunSummary := s.json["test-run-summary"]
			if !hasSelection || !hasResults || !hasRunSummary {
				return nil, ErrInvalidArtifact
			}
			result = append(result, selection, results, runSummary)
		}
		stdoutID, err := newGeneratedID()
		if err != nil {
			return nil, err
		}
		stderrID, err := newGeneratedID()
		if err != nil {
			return nil, err
		}
		diagnosticsID, err := newGeneratedID()
		if err != nil {
			return nil, err
		}
		result = append(
			result,
			executionPlan,
			summary,
			pendingArtifact{
				id: stdoutID, kind: "stdout",
				data: append([]byte(nil), s.stdout.Bytes()...),
			},
			pendingArtifact{
				id: stderrID, kind: "stderr",
				data: append([]byte(nil), s.stderr.Bytes()...),
			},
			pendingArtifact{
				id: diagnosticsID, kind: "diagnostics",
				data: append([]byte(nil), s.diagnostics.Bytes()...),
			},
		)
	case task.KindCoverageRun:
		coverageJSON, hasCoverageJSON := s.json["coverage-json"]
		junitXML, hasJUnitXML := s.json["junit-xml"]
		coverageHTML, hasCoverageHTML := s.json["coverage-html"]
		hasReport := hasCoverageJSON || hasJUnitXML || hasCoverageHTML
		if hasReport && (!hasCoverageJSON || !hasJUnitXML || !hasCoverageHTML || len(s.json) != 3) {
			return nil, ErrInvalidArtifact
		}
		if !hasReport && len(s.json) != 0 {
			return nil, ErrInvalidArtifact
		}
		if hasReport {
			result = append(result, coverageJSON, junitXML, coverageHTML)
		}
	default:
		return nil, ErrInvalidArtifact
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].kind < result[right].kind
	})
	return result, nil
}

func supportedTaskKind(value task.Kind) bool {
	switch value {
	case task.KindSimulation, task.KindCMakeBuild,
		task.KindTestDiscovery, task.KindTestRun, task.KindCoverageRun:
		return true
	default:
		return false
	}
}

func (s *taskSink) unavailable() bool {
	return s == nil || s.store == nil || s.finished || s.aborted
}

func (s *taskSink) clear() {
	s.stdout.Reset()
	s.stderr.Reset()
	s.diagnostics.Reset()
	s.json = nil
}

func newGeneratedID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", ErrStoreUnavailable
	}
	return hex.EncodeToString(value[:]), nil
}

func safeExecutionPlanJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 {
		return nil, ErrInvalidArtifact
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, ErrInvalidArtifact
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF ||
		executionPlanContainsSecret(decoded) {
		return nil, ErrInvalidArtifact
	}
	return append(encoded, '\n'), nil
}

func safeDomainJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 {
		return nil, ErrInvalidArtifact
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil ||
		decoder.Decode(&struct{}{}) != io.EOF ||
		executionPlanContainsSecret(decoded) {
		return nil, ErrInvalidArtifact
	}
	return append(encoded, '\n'), nil
}

func executionPlanContainsSecret(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for name, child := range typed {
			switch strings.ToLower(name) {
			case "env", "environment", "token", "process", "processspec":
				return true
			}
			if executionPlanContainsSecret(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if executionPlanContainsSecret(child) {
				return true
			}
		}
	case string:
		upper := strings.ToUpper(typed)
		return strings.Contains(upper, "UNIT_TEST_SERVICE_TOKEN") ||
			strings.Contains(upper, "UNIT_TEST_IDE_TOKEN")
	}
	return false
}

type artifactDiagnostic struct {
	ID          string               `json:"id,omitempty"`
	TaskID      string               `json:"taskId"`
	StepID      string               `json:"stepId"`
	Source      string               `json:"source,omitempty"`
	ToolchainID string               `json:"toolchainId,omitempty"`
	Severity    string               `json:"severity"`
	Code        string               `json:"code"`
	Message     string               `json:"message,omitempty"`
	FileURI     string               `json:"fileUri,omitempty"`
	Range       *diagnostic.Range    `json:"range,omitempty"`
	Related     []diagnostic.Related `json:"related,omitempty"`
	External    bool                 `json:"external,omitempty"`
}

func projectArtifactDiagnostic(value diagnostic.Diagnostic) artifactDiagnostic {
	return artifactDiagnostic{
		ID: value.ID, TaskID: value.TaskID, StepID: value.StepID,
		Source: value.Source, ToolchainID: value.ToolchainID,
		Severity: value.Severity, Code: value.Code, Message: value.Message,
		FileURI: value.FileURI, Range: value.Range,
		Related:  append([]diagnostic.Related(nil), value.Related...),
		External: value.External,
	}
}

var _ task.ArtifactWriter = (*Store)(nil)
var _ task.ArtifactSink = (*taskSink)(nil)
var _ task.CoverageArtifactSink = (*taskSink)(nil)
var _ task.FinalizedArtifactRollback = (*taskSink)(nil)
var _ task.FinalizedArtifactReleaser = (*taskSink)(nil)
