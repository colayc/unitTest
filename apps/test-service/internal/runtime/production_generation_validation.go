package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

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
	ValidationID, RunID, TaskDigest, SnapshotDigest string
	EditDigest, AssertionDigest, TargetSymbol       string
	Kind                                            testgendomain.CandidateKind
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
}

type productionValidatedRecord struct {
	binding   productionValidationBinding
	receipts  []testgenvalidate.StageReceipt
	candidate testgendomain.Candidate
	artifact  task.Artifact
	set       testgenpublish.CandidateSet
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

func exactGeneratedSource(set testgenrender.StagedEditSet) ([]byte, bool) {
	var source []byte
	for _, file := range set.Files {
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
	source, ok := exactGeneratedSource(pipeline.editSet)
	if !ok {
		return GenerationStageResult{}, errProductionValidationUnavailable
	}
	kind, assertionDigest, err := productionAssertionBinding(pipeline)
	if err != nil {
		return GenerationStageResult{}, err
	}
	snapshotDigest := testgendomain.NewGenerationRecord(run.Request).SnapshotDigest
	editDigest := productionValidationDigest(pipeline.editSet.Files)
	targetSymbol := "fn:" + target.gap.SymbolID
	binding := productionValidationBinding{
		RunID: run.ID, TaskDigest: productionBytesDigest([]byte(run.TaskID)), SnapshotDigest: snapshotDigest,
		EditDigest: editDigest, AssertionDigest: assertionDigest, TargetSymbol: targetSymbol, Kind: kind,
	}
	binding.ValidationID = productionValidationDigest(binding)
	input, err := adapter.authority.Prepare(ctx, binding)
	if err != nil {
		return GenerationStageResult{}, err
	}
	evidence, projectedAssertion, err := assertionEvidence(kind, assertionDigest)
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
		CaseID: caseID, Kind: kind, TargetSymbol: targetSymbol, Assertions: []testgendomain.Assertion{projectedAssertion},
		StagedSourceArtifact: testgendomain.ArtifactRef{ID: artifact.ID, Digest: artifact.SHA256}, CodeDigest: productionBytesDigest(source),
		CoverageDelta: validated.Delta, BaselineCoverage: validated.BaselinePercent, DeltaCoveragePercent: validated.DeltaPercent,
		PlannedEdits: productionPlannedEdits(pipeline.editSet.Files),
	}
	if testgendomain.ValidateCandidate(candidate) != nil {
		return GenerationStageResult{}, errProductionValidationUnavailable
	}
	set := testgenpublish.CandidateSet{
		RunID: run.ID, SnapshotDigest: snapshotDigest, CaseIDs: []string{caseID},
		TestTarget: target.renderTarget.TestTarget, ProductionTarget: target.renderTarget.ProductionTarget,
		FrameworkTarget: target.renderTarget.FrameworkTarget, SymbolID: target.gap.SymbolID,
		Files: append([]testgenrender.StagedFile(nil), pipeline.editSet.Files...), Diff: pipeline.editSet.Diff,
	}
	if kind == testgendomain.KindCharacterization {
		set.CharacterizationIDs = []string{caseID}
	}
	record := productionValidatedRecord{binding: binding, receipts: append([]testgenvalidate.StageReceipt(nil), validated.Receipts...), candidate: testgendomain.CloneCandidate(candidate), artifact: artifact, set: cloneProductionCandidateSet(set)}
	adapter.mu.Lock()
	adapter.records[run.ID] = record
	adapter.mu.Unlock()
	return GenerationStageResult{Next: testgendomain.StateMinimizing, Candidates: []testgendomain.Candidate{candidate}, Artifacts: []task.Artifact{artifact}}, nil
}

func (adapter *productionGenerationValidation) record(runID string) (productionValidatedRecord, bool) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	record, ok := adapter.records[runID]
	return record, ok
}

func (adapter *productionGenerationValidation) ValidateCandidate(ctx context.Context, run testgendomain.Run, candidate testgendomain.Candidate) error {
	if ctx == nil || !adapter.Ready() || testgendomain.ValidateCandidate(candidate) != nil {
		return errProductionValidationUnavailable
	}
	record, ok := adapter.record(run.ID)
	if !ok || !reflect.DeepEqual(record.candidate, candidate) || record.binding.SnapshotDigest != testgendomain.NewGenerationRecord(run.Request).SnapshotDigest {
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
	record, ok := adapter.record(run.ID)
	if !ok {
		return testgenpublish.CandidateSet{}, errProductionValidationUnavailable
	}
	return cloneProductionCandidateSet(record.set), nil
}

func (adapter *productionGenerationValidation) Minimize(ctx context.Context, run testgendomain.Run, _ generationTarget, _ productionPipelineResult) (GenerationStageResult, error) {
	record, ok := adapter.record(run.ID)
	if !ok || adapter.ValidateCandidate(ctx, run, record.candidate) != nil {
		return GenerationStageResult{}, errProductionValidationUnavailable
	}
	set := cloneProductionCandidateSet(record.set)
	return GenerationStageResult{Next: testgendomain.StateAwaitingConfirmation, MinimizedCaseIDs: []string{record.candidate.CaseID}, PreviewSet: &set}, nil
}

var _ productionGenerationValidator = (*productionGenerationValidation)(nil)
