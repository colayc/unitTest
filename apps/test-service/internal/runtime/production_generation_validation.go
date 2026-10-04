package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"unit-test-ide.local/test-service/internal/managedtest"
	"unit-test-ide.local/test-service/internal/task"
	assertion "unit-test-ide.local/test-service/internal/testgenassert"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenpublish"
	"unit-test-ide.local/test-service/internal/testgenrender"
	"unit-test-ide.local/test-service/internal/testgenvalidate"
)

var errProductionValidationUnavailable = errors.New("production test generation validation is unavailable")

// productionValidationBinding is produced by the service from exact rendered
// bytes and closed run identities. The authority uses it to register the
// candidate that testgenvalidate.Validator later resolves independently.
type productionValidationBinding struct {
	ValidationID, RunID, TaskDigest, SnapshotDigest  string
	EditDigest, AssertionDigest, TargetSymbol        string
	ProjectID, WorkspaceGeneration, CoverageReportID string
	BaselineReportDigest, SourceRelativePath         string
	SourceDigest                                     string
	TargetFunctionIDs, CoverageFunctionIDs           []string
	Kind                                             testgendomain.CandidateKind
}

type productionValidationInput struct {
	BaselineCoverage []byte
	Metrics          testgenvalidate.Metrics
}

type productionValidationAuthority interface {
	Ready() bool
	Prepare(context.Context, productionValidationBinding) (productionValidationInput, error)
	Verify(context.Context, productionValidationBinding, []testgenvalidate.StageReceipt) error
}

type productionGenerationArtifactStore interface {
	CommitGenerationSource(context.Context, string, string, time.Time, []byte) (task.Artifact, error)
	VerifyGenerationSource(context.Context, task.Artifact) error
	ReadGenerationSource(context.Context, task.Artifact) ([]byte, error)
	CommitGenerationEvidence(context.Context, string, string, time.Time, []byte) (task.Artifact, error)
	VerifyGenerationEvidence(context.Context, task.Artifact) error
	ReadGenerationEvidence(context.Context, task.Artifact) ([]byte, error)
	GetArtifact(context.Context, string) (task.Artifact, error)
}

type productionValidatedRecord struct {
	binding   productionValidationBinding
	receipts  []testgenvalidate.StageReceipt
	candidate testgendomain.Candidate
	artifact  task.Artifact
	evidence  task.Artifact
	set       testgenpublish.CandidateSet
	managed   []productionManagedCaseEvidence
}

type productionManagedCaseEvidence struct {
	CaseID, FunctionID, ScenarioID                string
	ProjectID, SourceFileID, SourceRelativePath   string
	TestRelativePath, GeneratorVersion, Framework string
	ToolchainID, SourceDigest                     string
}

type productionValidationEvidence struct {
	Version        int
	Binding        productionValidationBinding
	Receipts       []testgenvalidate.StageReceipt
	Candidate      testgendomain.Candidate
	SourceArtifact task.Artifact
	Set            testgenpublish.CandidateSet
	ManagedCases   []productionManagedCaseEvidence `json:",omitempty"`
}

type productionManagedValidationReceipt struct {
	Version        int
	Binding        productionValidationBinding
	Receipts       []testgenvalidate.StageReceipt
	SourceArtifact task.Artifact
}

func cloneProductionCandidateSet(value testgenpublish.CandidateSet) testgenpublish.CandidateSet {
	value.CaseIDs = append([]string(nil), value.CaseIDs...)
	value.CharacterizationIDs = append([]string(nil), value.CharacterizationIDs...)
	value.Files = append([]testgenrender.StagedFile(nil), value.Files...)
	for index := range value.Files {
		value.Files[index].Content = append([]byte(nil), value.Files[index].Content...)
	}
	return value
}

type productionGenerationValidation struct {
	validator testgenvalidate.Validator
	authority productionValidationAuthority
	artifacts productionGenerationArtifactStore
	mu        sync.Mutex
	records   map[string]productionValidatedRecord
}

func newProductionGenerationValidation(validator testgenvalidate.Validator, authority productionValidationAuthority, artifacts productionGenerationArtifactStore) *productionGenerationValidation {
	return &productionGenerationValidation{validator: validator, authority: authority, artifacts: artifacts, records: make(map[string]productionValidatedRecord)}
}

func (adapter *productionGenerationValidation) Ready() bool {
	return adapter != nil && adapter.authority != nil && adapter.authority.Ready() && adapter.artifacts != nil && adapter.validator.Ready()
}

func productionValidationDigest(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func productionBytesDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func exactGeneratedSource(files []testgenrender.StagedFile) ([]byte, bool) {
	var source []byte
	for _, file := range files {
		if !strings.HasSuffix(file.Path, "_test.c") && !strings.HasSuffix(file.Path, "_test.cpp") {
			continue
		}
		if source != nil || len(file.Content) == 0 || productionBytesDigest(file.Content) != file.AfterDigest {
			return nil, false
		}
		source = append([]byte(nil), file.Content...)
	}
	return source, len(source) > 0
}

func productionManagedCases(target generationTarget, pipeline productionPipelineResult, source []byte) ([]productionManagedCaseEvidence, error) {
	if !target.managed {
		return nil, nil
	}
	document, err := managedtest.ParseDocument(source, int64(len(source)), 200)
	if err != nil || len(document.Blocks) != len(pipeline.vectors) || len(document.Blocks) == 0 || len(pipeline.functions) == 0 {
		return nil, errProductionValidationUnavailable
	}
	byID := make(map[string]productionManagedCaseEvidence, len(pipeline.vectors))
	for _, function := range pipeline.functions {
		for _, vector := range function.vectors {
			caseID, err := managedtest.StableCaseID(target.projectID, target.sourceRelativePath, function.functionID, vector.ID)
			if err != nil {
				return nil, errProductionValidationUnavailable
			}
			byID[caseID] = productionManagedCaseEvidence{
				CaseID: caseID, FunctionID: function.functionID, ScenarioID: vector.ID,
				ProjectID: target.projectID, SourceFileID: target.fileID, SourceRelativePath: target.sourceRelativePath,
				TestRelativePath: target.renderTarget.TestPath, GeneratorVersion: "unit-test-service-v1", Framework: target.framework,
				ToolchainID: target.toolchainID, SourceDigest: target.sourceDigest,
			}
		}
	}
	result := make([]productionManagedCaseEvidence, 0, len(document.Blocks))
	for _, block := range document.Blocks {
		item, ok := byID[block.CaseID]
		if !ok || block.FunctionID != item.FunctionID {
			return nil, errProductionValidationUnavailable
		}
		result = append(result, item)
		delete(byID, block.CaseID)
	}
	if len(byID) != 0 {
		return nil, errProductionValidationUnavailable
	}
	sort.Slice(result, func(left, right int) bool { return result[left].CaseID < result[right].CaseID })
	return result, nil
}

func validProductionManagedCases(cases []productionManagedCaseEvidence, set testgenpublish.CandidateSet, source []byte) bool {
	if len(cases) == 0 {
		return bytes.Index(source, []byte("unit-test-ide:managed-begin")) < 0
	}
	document, err := managedtest.ParseDocument(source, int64(len(source)), 200)
	if err != nil || len(document.Blocks) != len(cases) {
		return false
	}
	path, _, ok := productionManagedSource(set)
	if !ok {
		return false
	}
	seen := map[string]bool{}
	for index, item := range cases {
		if index > 0 && cases[index-1].CaseID >= item.CaseID || !managedtest.ValidTestPath(item.TestRelativePath) ||
			item.TestRelativePath != path || item.GeneratorVersion != "unit-test-service-v1" ||
			item.CaseID != document.Blocks[index].CaseID || item.FunctionID != document.Blocks[index].FunctionID ||
			item.ProjectID == "" || item.SourceFileID == "" || item.SourceRelativePath == "" || item.ScenarioID == "" ||
			item.Framework != "cpputest" && item.Framework != "unity" || !validProductionDigest(item.ToolchainID) || !validProductionDigest(item.SourceDigest) || seen[item.CaseID] {
			return false
		}
		stable, err := managedtest.StableCaseID(item.ProjectID, item.SourceRelativePath, item.FunctionID, item.ScenarioID)
		if err != nil || stable != item.CaseID {
			return false
		}
		seen[item.CaseID] = true
	}
	return true
}

func productionAssertionBinding(pipeline productionPipelineResult) (testgendomain.CandidateKind, string, error) {
	if len(pipeline.vectors) == 0 || len(pipeline.vectors) != len(pipeline.observations) {
		return "", "", errProductionValidationUnavailable
	}
	digests := make([]string, 0, len(pipeline.vectors))
	kind := testgendomain.KindVerified
	for index, vector := range pipeline.vectors {
		assertions, derivedKind, err := assertion.Derive(pipeline.program, vector, pipeline.observations[index])
		if err != nil || len(assertions) == 0 {
			return "", "", errProductionValidationUnavailable
		}
		if derivedKind == assertion.KindCharacterization {
			kind = testgendomain.KindCharacterization
		} else if derivedKind != assertion.KindVerified {
			return "", "", errProductionValidationUnavailable
		}
		for _, value := range assertions {
			digests = append(digests, value.EvidenceDigest)
		}
	}
	sort.Strings(digests)
	return kind, productionValidationDigest(struct {
		Program string
		Kind    testgendomain.CandidateKind
		Proofs  []string
	}{pipeline.program.Digest, kind, digests}), nil
}

func productionPlannedEdits(files []testgenrender.StagedFile) []testgendomain.PlannedEdit {
	result := make([]testgendomain.PlannedEdit, 0, len(files))
	for _, file := range files {
		operation := testgendomain.EditCreate
		if file.BeforeDigest != "" {
			operation = testgendomain.EditModify
		}
		result = append(result, testgendomain.PlannedEdit{Path: file.Path, Operation: operation, BeforeDigest: file.BeforeDigest, AfterDigest: file.AfterDigest})
	}
	return result
}

func productionValidationBindingFor(run testgendomain.Run, target generationTarget, pipeline productionPipelineResult) (productionValidationBinding, error) {
	kind, assertionDigest, err := productionAssertionBinding(pipeline)
	if err != nil {
		return productionValidationBinding{}, err
	}
	targetFunctions := make([]string, 0, len(pipeline.functions))
	coverageFunctions := make([]string, 0, len(pipeline.functions))
	seen := map[string]bool{}
	seenCoverage := map[string]bool{}
	for _, function := range pipeline.functions {
		if !validProductionDigest(function.symbolID) || !validProductionObjectID(function.functionID) ||
			seen[function.symbolID] || seenCoverage[function.functionID] {
			return productionValidationBinding{}, errProductionValidationUnavailable
		}
		seen[function.symbolID] = true
		seenCoverage[function.functionID] = true
		targetFunctions = append(targetFunctions, function.symbolID)
		coverageFunctions = append(coverageFunctions, function.functionID)
	}
	sort.Strings(targetFunctions)
	sort.Strings(coverageFunctions)
	if len(targetFunctions) == 0 || len(targetFunctions) != len(coverageFunctions) {
		return productionValidationBinding{}, errProductionValidationUnavailable
	}
	targetSymbol := "fn:" + pipeline.primarySymbolID()
	if target.request.Scope == testgendomain.ScopeFile {
		targetSymbol = "file:" + target.fileID
	}
	binding := productionValidationBinding{
		RunID: run.ID, TaskDigest: productionBytesDigest([]byte(run.TaskID)), SnapshotDigest: testgendomain.NewGenerationRecord(run.Request).SnapshotDigest,
		EditDigest: productionValidationDigest(pipeline.editSet.Files), AssertionDigest: assertionDigest,
		ProjectID: target.projectID, WorkspaceGeneration: target.workspaceGeneration, CoverageReportID: target.coverageReportID,
		BaselineReportDigest: target.request.BaselineReportDigest, SourceRelativePath: target.sourceRelativePath, SourceDigest: target.sourceDigest,
		TargetSymbol: targetSymbol, TargetFunctionIDs: targetFunctions, CoverageFunctionIDs: coverageFunctions, Kind: kind,
	}
	binding.ValidationID = productionValidationDigest(binding)
	return binding, nil
}

func assertionEvidence(kind testgendomain.CandidateKind, digest string) (testgenvalidate.AssertionEvidence, testgendomain.Assertion, error) {
	switch kind {
	case testgendomain.KindVerified:
		return testgenvalidate.AssertionEvidence{Kind: kind, IndependentProofDigest: digest}, testgendomain.Assertion{Kind: testgendomain.AssertionIndependentOracle, EvidenceDigest: digest}, nil
	case testgendomain.KindCharacterization:
		return testgenvalidate.AssertionEvidence{Kind: kind, ObservedOutputReceipt: digest}, testgendomain.Assertion{Kind: testgendomain.AssertionObservedOutput, EvidenceDigest: digest}, nil
	default:
		return testgenvalidate.AssertionEvidence{}, testgendomain.Assertion{}, errProductionValidationUnavailable
	}
}

func (adapter *productionGenerationValidation) Validate(ctx context.Context, run testgendomain.Run, target generationTarget, pipeline productionPipelineResult) (GenerationStageResult, error) {
	if ctx == nil || !adapter.Ready() || !target.valid() || run.ID == "" || run.TaskID == "" || !reflect.DeepEqual(run.Request, target.request) {
		return GenerationStageResult{}, errProductionValidationUnavailable
	}
	source, ok := exactGeneratedSource(pipeline.editSet.Files)
	if !ok {
		return GenerationStageResult{}, errProductionValidationUnavailable
	}
	managedCases, err := productionManagedCases(target, pipeline, source)
	if err != nil {
		return GenerationStageResult{}, err
	}
	binding, err := productionValidationBindingFor(run, target, pipeline)
	if err != nil {
		return GenerationStageResult{}, err
	}
	input, err := adapter.authority.Prepare(ctx, binding)
	if err != nil {
		return GenerationStageResult{}, err
	}
	evidence, projectedAssertion, err := assertionEvidence(binding.Kind, binding.AssertionDigest)
	if err != nil {
		return GenerationStageResult{}, err
	}
	validated, err := adapter.validator.Validate(ctx, testgenvalidate.ValidationRequest{
		TaskID: binding.TaskDigest, CandidateID: binding.ValidationID, Edits: pipeline.editSet,
		BaselineCoverage: input.BaselineCoverage, Metrics: input.Metrics, Assertion: evidence,
	})
	if err != nil {
		return GenerationStageResult{}, err
	}
	if !validated.Retained {
		adapter.mu.Lock()
		delete(adapter.records, run.ID)
		adapter.mu.Unlock()
		return GenerationStageResult{Next: testgendomain.StateRejected}, nil
	}
	caseDigest := productionBytesDigest([]byte("case:" + binding.ValidationID))
	caseID := caseDigest[:32]
	artifactDigest := productionBytesDigest([]byte("artifact:" + binding.ValidationID))
	artifact, err := adapter.artifacts.CommitGenerationSource(ctx, run.TaskID, artifactDigest[:32], run.CreatedAt, source)
	if err != nil {
		return GenerationStageResult{}, err
	}
	if err := adapter.artifacts.VerifyGenerationSource(ctx, artifact); err != nil {
		return GenerationStageResult{}, err
	}
	candidate := testgendomain.Candidate{
		CaseID: caseID, Kind: binding.Kind, TargetSymbol: binding.TargetSymbol, Assertions: []testgendomain.Assertion{projectedAssertion},
		StagedSourceArtifact: testgendomain.ArtifactRef{ID: artifact.ID, Digest: artifact.SHA256}, CodeDigest: productionBytesDigest(source),
		CoverageDelta: validated.Delta, BaselineCoverage: validated.BaselinePercent, DeltaCoveragePercent: validated.DeltaPercent,
		PlannedEdits: productionPlannedEdits(pipeline.editSet.Files),
	}
	if testgendomain.ValidateCandidate(candidate) != nil {
		return GenerationStageResult{}, errProductionValidationUnavailable
	}
	set := testgenpublish.CandidateSet{
		RunID: run.ID, SnapshotDigest: binding.SnapshotDigest, CaseIDs: []string{caseID},
		TestTarget: target.renderTarget.TestTarget, ProductionTarget: target.renderTarget.ProductionTarget,
		FrameworkTarget: target.renderTarget.FrameworkTarget, SymbolID: pipeline.primarySymbolID(),
		Files: append([]testgenrender.StagedFile(nil), pipeline.editSet.Files...), Diff: pipeline.editSet.Diff,
	}
	if binding.Kind == testgendomain.KindCharacterization {
		set.CharacterizationIDs = []string{caseID}
	}
	persisted := productionValidationEvidence{Version: 1, Binding: binding, Receipts: append([]testgenvalidate.StageReceipt(nil), validated.Receipts...), Candidate: testgendomain.CloneCandidate(candidate), SourceArtifact: artifact, Set: cloneProductionCandidateSet(set), ManagedCases: append([]productionManagedCaseEvidence(nil), managedCases...)}
	encoded, err := json.Marshal(persisted)
	if err != nil {
		return GenerationStageResult{}, errProductionValidationUnavailable
	}
	evidenceArtifact, err := adapter.artifacts.CommitGenerationEvidence(ctx, run.TaskID, caseID, run.CreatedAt, encoded)
	if err != nil {
		return GenerationStageResult{}, err
	}
	if err := adapter.artifacts.VerifyGenerationEvidence(ctx, evidenceArtifact); err != nil {
		return GenerationStageResult{}, err
	}
	record := productionValidatedRecord{binding: binding, receipts: persisted.Receipts, candidate: persisted.Candidate, artifact: artifact, evidence: evidenceArtifact, set: persisted.Set, managed: append([]productionManagedCaseEvidence(nil), persisted.ManagedCases...)}
	adapter.mu.Lock()
	adapter.records[run.ID] = record
	adapter.mu.Unlock()
	return GenerationStageResult{Next: testgendomain.StateMinimizing, Candidates: []testgendomain.Candidate{candidate}, Artifacts: []task.Artifact{artifact, evidenceArtifact}}, nil
}

func (adapter *productionGenerationValidation) cachedRecord(runID string) (productionValidatedRecord, bool) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	record, ok := adapter.records[runID]
	return record, ok
}

func hasGenerationArtifact(run testgendomain.Run, artifact task.Artifact) bool {
	for _, reference := range run.ArtifactDigests {
		if reference.ID == artifact.ID && reference.Digest == artifact.SHA256 {
			return true
		}
	}
	return false
}

func validProductionReceipts(receipts []testgenvalidate.StageReceipt) bool {
	expected := []testgenvalidate.Stage{testgenvalidate.StageConfigure, testgenvalidate.StageCompile, testgenvalidate.StageDiscover, testgenvalidate.StageCandidate, testgenvalidate.StageSuite, testgenvalidate.StageCoverage}
	if len(receipts) != len(expected) {
		return false
	}
	for index, receipt := range receipts {
		if receipt.Stage != expected[index] || !validProductionDigest(receipt.Digest) || !validProductionDigest(receipt.OutputDigest) || !validProductionDigest(receipt.CoverageDigest) {
			return false
		}
	}
	return true
}

func (adapter *productionGenerationValidation) loadRecord(ctx context.Context, run testgendomain.Run, candidate testgendomain.Candidate) (productionValidatedRecord, error) {
	evidenceArtifact, err := adapter.artifacts.GetArtifact(ctx, candidate.CaseID)
	if err != nil || evidenceArtifact.TaskID != run.TaskID || evidenceArtifact.Kind != "test-generation-evidence" || !hasGenerationArtifact(run, evidenceArtifact) {
		return productionValidatedRecord{}, errProductionValidationUnavailable
	}
	if err := adapter.artifacts.VerifyGenerationEvidence(ctx, evidenceArtifact); err != nil {
		return productionValidatedRecord{}, err
	}
	encoded, err := adapter.artifacts.ReadGenerationEvidence(ctx, evidenceArtifact)
	if err != nil {
		return productionValidatedRecord{}, err
	}
	var persisted productionValidationEvidence
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&persisted) != nil || decoder.Decode(new(any)) != io.EOF {
		return productionValidatedRecord{}, errProductionValidationUnavailable
	}
	canonical, err := json.Marshal(persisted)
	if err != nil || !bytes.Equal(canonical, encoded) || persisted.Version != 1 || !reflect.DeepEqual(persisted.Candidate, candidate) ||
		persisted.Binding.RunID != run.ID || persisted.Binding.SnapshotDigest != testgendomain.NewGenerationRecord(run.Request).SnapshotDigest ||
		persisted.Binding.ValidationID != productionValidationDigest(productionValidationBinding{
			RunID: persisted.Binding.RunID, TaskDigest: persisted.Binding.TaskDigest, SnapshotDigest: persisted.Binding.SnapshotDigest,
			EditDigest: persisted.Binding.EditDigest, AssertionDigest: persisted.Binding.AssertionDigest, TargetSymbol: persisted.Binding.TargetSymbol,
			ProjectID: persisted.Binding.ProjectID, WorkspaceGeneration: persisted.Binding.WorkspaceGeneration, CoverageReportID: persisted.Binding.CoverageReportID,
			BaselineReportDigest: persisted.Binding.BaselineReportDigest, SourceRelativePath: persisted.Binding.SourceRelativePath, SourceDigest: persisted.Binding.SourceDigest,
			TargetFunctionIDs: append([]string(nil), persisted.Binding.TargetFunctionIDs...), CoverageFunctionIDs: append([]string(nil), persisted.Binding.CoverageFunctionIDs...), Kind: persisted.Binding.Kind,
		}) || persisted.Binding.TaskDigest != productionBytesDigest([]byte(run.TaskID)) || !validProductionReceipts(persisted.Receipts) ||
		persisted.Set.RunID != run.ID || persisted.Set.SnapshotDigest != persisted.Binding.SnapshotDigest || !reflect.DeepEqual(persisted.Set.CaseIDs, []string{candidate.CaseID}) ||
		persisted.Binding.EditDigest != productionValidationDigest(persisted.Set.Files) || !reflect.DeepEqual(candidate.PlannedEdits, productionPlannedEdits(persisted.Set.Files)) {
		return productionValidatedRecord{}, errProductionValidationUnavailable
	}
	sourceArtifact, err := adapter.artifacts.GetArtifact(ctx, candidate.StagedSourceArtifact.ID)
	if err != nil || sourceArtifact != persisted.SourceArtifact || sourceArtifact.TaskID != run.TaskID || !hasGenerationArtifact(run, sourceArtifact) ||
		sourceArtifact.SHA256 != candidate.StagedSourceArtifact.Digest {
		return productionValidatedRecord{}, errProductionValidationUnavailable
	}
	source, err := adapter.artifacts.ReadGenerationSource(ctx, sourceArtifact)
	if err != nil || productionBytesDigest(source) != candidate.CodeDigest {
		return productionValidatedRecord{}, errProductionValidationUnavailable
	}
	exact, ok := exactGeneratedSource(persisted.Set.Files)
	if !ok || !bytes.Equal(exact, source) || !validProductionManagedCases(persisted.ManagedCases, persisted.Set, source) {
		return productionValidatedRecord{}, errProductionValidationUnavailable
	}
	record := productionValidatedRecord{binding: persisted.Binding, receipts: append([]testgenvalidate.StageReceipt(nil), persisted.Receipts...), candidate: testgendomain.CloneCandidate(candidate), artifact: sourceArtifact, evidence: evidenceArtifact, set: cloneProductionCandidateSet(persisted.Set), managed: append([]productionManagedCaseEvidence(nil), persisted.ManagedCases...)}
	adapter.mu.Lock()
	adapter.records[run.ID] = record
	adapter.mu.Unlock()
	return record, nil
}

func (adapter *productionGenerationValidation) validatedRecord(ctx context.Context, run testgendomain.Run, candidate testgendomain.Candidate) (productionValidatedRecord, error) {
	if record, ok := adapter.cachedRecord(run.ID); ok {
		if reflect.DeepEqual(record.candidate, candidate) {
			return record, nil
		}
		return productionValidatedRecord{}, errProductionValidationUnavailable
	}
	return adapter.loadRecord(ctx, run, candidate)
}

func (adapter *productionGenerationValidation) ValidateCandidate(ctx context.Context, run testgendomain.Run, candidate testgendomain.Candidate) error {
	if ctx == nil || !adapter.Ready() || testgendomain.ValidateCandidate(candidate) != nil {
		return errProductionValidationUnavailable
	}
	record, err := adapter.validatedRecord(ctx, run, candidate)
	if err != nil || record.binding.SnapshotDigest != testgendomain.NewGenerationRecord(run.Request).SnapshotDigest {
		return errProductionValidationUnavailable
	}
	if err := adapter.authority.Verify(ctx, record.binding, append([]testgenvalidate.StageReceipt(nil), record.receipts...)); err != nil {
		return err
	}
	return adapter.artifacts.VerifyGenerationSource(ctx, record.artifact)
}

func (adapter *productionGenerationValidation) CandidateSet(ctx context.Context, run testgendomain.Run, candidates []testgendomain.Candidate) (testgenpublish.CandidateSet, error) {
	if len(candidates) != 1 || adapter.ValidateCandidate(ctx, run, candidates[0]) != nil {
		return testgenpublish.CandidateSet{}, errProductionValidationUnavailable
	}
	record, ok := adapter.cachedRecord(run.ID)
	if !ok {
		return testgenpublish.CandidateSet{}, errProductionValidationUnavailable
	}
	return cloneProductionCandidateSet(record.set), nil
}

func (adapter *productionGenerationValidation) ManagedEvidence(ctx context.Context, run testgendomain.Run, candidates []testgendomain.Candidate) (testgenpublish.CandidateSet, []productionManagedCaseEvidence, []byte, string, error) {
	set, err := adapter.CandidateSet(ctx, run, candidates)
	if err != nil {
		return testgenpublish.CandidateSet{}, nil, nil, "", err
	}
	record, ok := adapter.cachedRecord(run.ID)
	source, sourceOK := exactGeneratedSource(record.set.Files)
	if !ok || !sourceOK || len(record.managed) == 0 || !validProductionManagedCases(record.managed, record.set, source) {
		return testgenpublish.CandidateSet{}, nil, nil, "", errProductionValidationUnavailable
	}
	receipt := productionManagedValidationReceipt{
		Version: 1, Binding: record.binding, Receipts: append([]testgenvalidate.StageReceipt(nil), record.receipts...), SourceArtifact: record.artifact,
	}
	encoded, err := json.Marshal(receipt)
	if err != nil || len(encoded) == 0 || len(encoded) > 1<<20 {
		return testgenpublish.CandidateSet{}, nil, nil, "", errProductionValidationUnavailable
	}
	return set, append([]productionManagedCaseEvidence(nil), record.managed...), encoded, productionBytesDigest(encoded), nil
}

func (adapter *productionGenerationValidation) Minimize(ctx context.Context, run testgendomain.Run, target generationTarget, pipeline productionPipelineResult) (GenerationStageResult, error) {
	record, ok := adapter.cachedRecord(run.ID)
	if !ok {
		binding, err := productionValidationBindingFor(run, target, pipeline)
		if err != nil {
			return GenerationStageResult{}, err
		}
		caseID := productionBytesDigest([]byte("case:" + binding.ValidationID))[:32]
		evidenceArtifact, err := adapter.artifacts.GetArtifact(ctx, caseID)
		if err != nil {
			return GenerationStageResult{}, errProductionValidationUnavailable
		}
		encoded, err := adapter.artifacts.ReadGenerationEvidence(ctx, evidenceArtifact)
		if err != nil {
			return GenerationStageResult{}, err
		}
		var persisted productionValidationEvidence
		if json.Unmarshal(encoded, &persisted) != nil || !reflect.DeepEqual(persisted.Binding, binding) {
			return GenerationStageResult{}, errProductionValidationUnavailable
		}
		record, err = adapter.loadRecord(ctx, run, persisted.Candidate)
		if err != nil {
			return GenerationStageResult{}, err
		}
	}
	if adapter.ValidateCandidate(ctx, run, record.candidate) != nil {
		return GenerationStageResult{}, errProductionValidationUnavailable
	}
	set := cloneProductionCandidateSet(record.set)
	return GenerationStageResult{Next: testgendomain.StateAwaitingConfirmation, MinimizedCaseIDs: []string{record.candidate.CaseID}, PreviewSet: &set}, nil
}

var _ productionGenerationValidator = (*productionGenerationValidation)(nil)
