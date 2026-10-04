package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
	"unit-test-ide.local/test-service/internal/managedtest"
	"unit-test-ide.local/test-service/internal/processcontrol"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgenpublish"
	"unit-test-ide.local/test-service/internal/testgenrender"
	"unit-test-ide.local/test-service/internal/testgenvalidate"
)

type productionSelectedBuildResolver interface {
	ResolveProductionBuild(context.Context, generationTarget) (productionBuildSnapshot, error)
}

type productionSelectedPlanRegistry interface {
	RegisterValidationPlan(productionValidationPlanRegistration) error
	ResolveValidationProcessPlan(context.Context, string, string, testgenvalidate.Stage, testgenvalidate.Roots) (productionValidationProcessPlan, error)
	ReleaseValidationPlan(string, string)
}

type productionSelectedStageRunner interface {
	Execute(context.Context, testgenvalidate.Stage, testgenvalidate.Roots) (testgenvalidate.StageEvidence, error)
}

type productionSelectedCoverageResolver func(productionManagedSelectionBinding, testgenvalidate.StageEvidence) (testgenvalidate.SelectedCoverage, error)

type productionManagedSelectionExecutorConfig struct {
	authority *productionManagedSelectionAuthority
	baseline  productionCoverageBaselineReader
	builds    productionSelectedBuildResolver
	plans     productionSelectedPlanRegistry
	stages    productionSelectedStageRunner
	coverage  productionSelectedCoverageResolver
}

type productionManagedSelectionSession struct {
	selectionDigest string
	binding         productionManagedSelectionBinding
	roots           testgenvalidate.Roots
	registration    productionValidationPlanRegistration
	next            int
	running         bool
	phaseDigests    map[testgenvalidate.SelectedPhase]string
}

type productionManagedSelectionAttestation struct {
	selectionDigest string
	request         testgendomainRequestIdentity
	context         testgenvalidate.SelectionContext
	phaseDigests    map[testgenvalidate.SelectedPhase]string
	planDigest      string
}

// Keep only the comparable request projection in post-validation attestations.
// generationTarget also contains slices and native analysis paths that are not
// needed once the exact request and current selected context are re-resolved.
type testgendomainRequestIdentity struct {
	RequestDigest string
}

type productionManagedSelectionExecutor struct {
	config productionManagedSelectionExecutorConfig
	mu     sync.Mutex
	active map[string]*productionManagedSelectionSession
	done   map[string]productionManagedSelectionAttestation
}

func newProductionManagedSelectionExecutor(config productionManagedSelectionExecutorConfig) (*productionManagedSelectionExecutor, error) {
	if config.authority == nil || config.baseline == nil || config.builds == nil || config.plans == nil || config.stages == nil {
		return nil, task.ErrStorageUnavailable
	}
	if config.coverage == nil {
		config.coverage = productionSelectedCoverageFromEvidence
	}
	return &productionManagedSelectionExecutor{config: config, active: make(map[string]*productionManagedSelectionSession), done: make(map[string]productionManagedSelectionAttestation)}, nil
}

func productionSelectedPhaseIndex(phase testgenvalidate.SelectedPhase) int {
	switch phase {
	case testgenvalidate.SelectedConfigure:
		return 0
	case testgenvalidate.SelectedBuild:
		return 1
	case testgenvalidate.SelectedDiscover:
		return 2
	case testgenvalidate.SelectedRun:
		return 3
	case testgenvalidate.SelectedCoveragePhase:
		return 4
	default:
		return -1
	}
}

func productionSelectionDigest(selection testgenpublish.ManagedSelection) (string, error) {
	return productionCanonicalDigest("managed-selection-execution-v1", selection)
}

func (executor *productionManagedSelectionExecutor) begin(ctx context.Context, selection testgenpublish.ManagedSelection, roots testgenvalidate.Roots) (*productionManagedSelectionSession, error) {
	binding, err := executor.config.authority.resolveBinding(ctx, selection.RunID)
	if err != nil || binding.context.SnapshotDigest != selection.SnapshotDigest || binding.context.ToolchainID != selection.ToolchainID {
		return nil, errProductionManagedUnavailable
	}
	baseline, err := executor.config.baseline.ReadCoverageBaseline(ctx, binding.run.Request.CoverageReportID)
	if err != nil {
		return nil, err
	}
	build, err := executor.config.builds.ResolveProductionBuild(ctx, binding.target)
	if err != nil {
		return nil, err
	}
	selectionDigest, err := productionSelectionDigest(selection)
	if err != nil {
		return nil, errProductionManagedUnavailable
	}
	validationTaskID, err := productionCanonicalDigest("managed-selection-validation-task-v1", struct {
		RunID, SelectionDigest string
	}{selection.RunID, selectionDigest})
	if err != nil {
		return nil, errProductionManagedUnavailable
	}
	roots.TaskID, roots.ProcessTaskID, roots.CandidateID = validationTaskID, binding.run.TaskID, selection.SelectedOutputDigest
	registration := productionValidationPlanRegistration{
		candidateID: selection.SelectedOutputDigest, processTaskID: binding.run.TaskID,
		target: binding.target, build: build, baseline: baseline,
	}
	session := &productionManagedSelectionSession{
		selectionDigest: selectionDigest, binding: binding, roots: roots, registration: registration,
		phaseDigests: make(map[testgenvalidate.SelectedPhase]string),
	}
	executor.mu.Lock()
	if existing := executor.active[selection.SelectedOutputDigest]; existing != nil {
		executor.mu.Unlock()
		return nil, errProductionManagedUnavailable
	}
	executor.active[selection.SelectedOutputDigest] = session
	executor.mu.Unlock()
	if err := executor.config.plans.RegisterValidationPlan(registration); err != nil {
		executor.mu.Lock()
		if executor.active[selection.SelectedOutputDigest] == session {
			delete(executor.active, selection.SelectedOutputDigest)
		}
		executor.mu.Unlock()
		return nil, err
	}
	return session, nil
}

func (executor *productionManagedSelectionExecutor) Execute(ctx context.Context, selection testgenpublish.ManagedSelection, phase testgenvalidate.SelectedPhase, roots testgenvalidate.Roots) (testgenvalidate.SelectedStageResult, string, error) {
	if executor == nil || ctx == nil || ctx.Err() != nil || productionSelectedPhaseIndex(phase) < 0 {
		return testgenvalidate.SelectedStageResult{}, "", errProductionManagedUnavailable
	}
	var session *productionManagedSelectionSession
	if phase == testgenvalidate.SelectedConfigure {
		var err error
		session, err = executor.begin(ctx, selection, roots)
		if err != nil {
			return testgenvalidate.SelectedStageResult{}, "", err
		}
	} else {
		executor.mu.Lock()
		session = executor.active[selection.SelectedOutputDigest]
		executor.mu.Unlock()
	}
	selectionDigest, err := productionSelectionDigest(selection)
	if err != nil || session == nil || session.selectionDigest != selectionDigest ||
		session.roots.Source != roots.Source || session.roots.Build != roots.Build || session.roots.Artifacts != roots.Artifacts || session.roots.SnapshotDigest != roots.SnapshotDigest {
		return testgenvalidate.SelectedStageResult{}, "", errProductionManagedUnavailable
	}
	executor.mu.Lock()
	if executor.active[selection.SelectedOutputDigest] != session || session.next != productionSelectedPhaseIndex(phase) || session.running {
		executor.mu.Unlock()
		return testgenvalidate.SelectedStageResult{}, "", errProductionManagedUnavailable
	}
	session.running = true
	executor.mu.Unlock()
	result, digest, err := executor.executePhase(ctx, selection, phase, session)
	if err != nil || !validProductionDigest(digest) {
		executor.mu.Lock()
		if executor.active[selection.SelectedOutputDigest] == session {
			session.running = false
		}
		executor.mu.Unlock()
		return testgenvalidate.SelectedStageResult{}, "", errProductionManagedUnavailable
	}
	executor.mu.Lock()
	if executor.active[selection.SelectedOutputDigest] != session || session.next != productionSelectedPhaseIndex(phase) || !session.running {
		executor.mu.Unlock()
		return testgenvalidate.SelectedStageResult{}, "", errProductionManagedUnavailable
	}
	session.running = false
	session.phaseDigests[phase] = digest
	session.next++
	executor.mu.Unlock()
	return result, digest, nil
}

func (executor *productionManagedSelectionExecutor) executePhase(ctx context.Context, selection testgenpublish.ManagedSelection, phase testgenvalidate.SelectedPhase, session *productionManagedSelectionSession) (testgenvalidate.SelectedStageResult, string, error) {
	stages := []testgenvalidate.Stage{}
	switch phase {
	case testgenvalidate.SelectedConfigure:
		stages = []testgenvalidate.Stage{testgenvalidate.StageConfigure}
	case testgenvalidate.SelectedBuild:
		stages = []testgenvalidate.Stage{testgenvalidate.StageCompile}
	case testgenvalidate.SelectedDiscover:
		stages = []testgenvalidate.Stage{testgenvalidate.StageDiscover}
	case testgenvalidate.SelectedRun:
		stages = []testgenvalidate.Stage{testgenvalidate.StageCandidate, testgenvalidate.StageSuite}
	case testgenvalidate.SelectedCoveragePhase:
		stages = []testgenvalidate.Stage{testgenvalidate.StageCoverage}
	}
	var (
		outputDigests []string
		evidence      testgenvalidate.StageEvidence
		digests       []string
	)
	for _, stage := range stages {
		plan, err := executor.config.plans.ResolveValidationProcessPlan(ctx, session.registration.candidateID, session.registration.processTaskID, stage, session.roots)
		if err != nil {
			return testgenvalidate.SelectedStageResult{}, "", err
		}
		digest, err := productionSelectedProcessPlanDigest(stage, plan)
		if err != nil {
			return testgenvalidate.SelectedStageResult{}, "", err
		}
		current, err := executor.config.stages.Execute(ctx, stage, session.roots)
		if err != nil || current.ExitCode != 0 {
			return testgenvalidate.SelectedStageResult{}, "", errProductionManagedUnavailable
		}
		outputDigests = append(outputDigests, productionBytesDigest(current.Output))
		evidence = current
		digests = append(digests, digest)
	}
	phaseDigest, err := productionCanonicalDigest("managed-selected-native-phase-v1", struct {
		Phase   testgenvalidate.SelectedPhase
		Digests []string
	}{phase, digests})
	if err != nil {
		return testgenvalidate.SelectedStageResult{}, "", errProductionManagedUnavailable
	}
	outputDigest, err := productionCanonicalDigest("managed-selected-native-output-v1", outputDigests)
	if err != nil {
		return testgenvalidate.SelectedStageResult{}, "", errProductionManagedUnavailable
	}
	result := testgenvalidate.SelectedStageResult{Output: []byte("native:" + outputDigest)}
	switch phase {
	case testgenvalidate.SelectedDiscover:
		if !reflect.DeepEqual(evidence.DiscoveredCaseIDs, []string{selection.SelectedOutputDigest}) {
			return testgenvalidate.SelectedStageResult{}, "", errProductionManagedUnavailable
		}
		result.DiscoveredCaseIDs, err = productionSelectedCaseIDs(selection.Files)
	case testgenvalidate.SelectedRun:
		result.ExecutedCaseIDs, err = productionSelectedCaseIDs(selection.Files)
		result.TestsRun, result.TestsPassed = len(result.ExecutedCaseIDs), len(result.ExecutedCaseIDs)
	case testgenvalidate.SelectedCoveragePhase:
		var coverage testgenvalidate.SelectedCoverage
		coverage, err = executor.config.coverage(session.binding, evidence)
		if err == nil {
			err = writeProductionSelectedCoverage(session.roots.Artifacts, coverage)
		}
		result.CollectorRelativePath = "selected-coverage.json"
	}
	if err != nil {
		return testgenvalidate.SelectedStageResult{}, "", err
	}
	return result, phaseDigest, nil
}

func productionSelectedCaseIDs(files []testgenrender.StagedFile) ([]string, error) {
	seen := map[string]bool{}
	result := []string{}
	for _, file := range files {
		if !strings.HasPrefix(file.Path, "tests/generated/") {
			continue
		}
		document, err := managedtest.ParseDocument(file.Content, managedtest.MaxReviewBytes, 10000)
		if err != nil {
			return nil, errProductionManagedUnavailable
		}
		for _, block := range document.Blocks {
			if seen[block.CaseID] {
				return nil, errProductionManagedUnavailable
			}
			seen[block.CaseID] = true
			result = append(result, block.CaseID)
		}
	}
	if len(result) == 0 || len(result) > 10000 {
		return nil, errProductionManagedUnavailable
	}
	sort.Strings(result)
	return result, nil
}

func productionSelectedProcessPlanDigest(stage testgenvalidate.Stage, plan productionValidationProcessPlan) (string, error) {
	specs := append([]processcontrol.Spec(nil), plan.sequences[stage]...)
	if len(specs) == 0 {
		if spec, ok := plan.specs[stage]; ok {
			specs = []processcontrol.Spec{spec}
		}
	}
	if plan.taskID == "" || plan.serviceInstanceID == "" || len(specs) == 0 || len(plan.tools) == 0 || plan.recordLease == nil || plan.releaseLease == nil {
		return "", errProductionManagedUnavailable
	}
	return productionCanonicalDigest("managed-selected-process-plan-v1", struct {
		Stage                 testgenvalidate.Stage
		TaskID, ServiceID     string
		Tools                 map[string]string
		Environment, EnvUnset []string
		Specs                 []processcontrol.Spec
	}{stage, plan.taskID, plan.serviceInstanceID, cloneProductionValidationTools(plan.tools), append([]string(nil), plan.allowedEnvironment...), append([]string(nil), plan.allowedEnvUnset...), specs})
}

func productionSelectedPlanDigest(phases []testgenvalidate.SelectedPhaseReceipt) (string, error) {
	type phasePlan struct {
		Phase  testgenvalidate.SelectedPhase `json:"phase"`
		Digest string                        `json:"digest"`
	}
	values := make([]phasePlan, len(phases))
	for index, phase := range phases {
		values[index] = phasePlan{Phase: phase.Phase, Digest: phase.CommandDigest}
	}
	return productionCanonicalDigest("managed-dynamic-plan-v1", values)
}

func (executor *productionManagedSelectionExecutor) VerifyPlan(ctx context.Context, selection testgenpublish.ManagedSelection, phases []testgenvalidate.SelectedPhaseReceipt, planDigest string) error {
	if executor == nil || ctx == nil || ctx.Err() != nil || len(phases) != 5 || !validProductionDigest(planDigest) {
		return errProductionManagedUnavailable
	}
	wantPlan, err := productionSelectedPlanDigest(phases)
	if err != nil || wantPlan != planDigest {
		return errProductionManagedUnavailable
	}
	selectionDigest, err := productionSelectionDigest(selection)
	if err != nil {
		return errProductionManagedUnavailable
	}
	executor.mu.Lock()
	session := executor.active[selection.SelectedOutputDigest]
	done, completed := executor.done[selection.SelectedOutputDigest]
	executor.mu.Unlock()
	phaseDigests := done.phaseDigests
	requestIdentity, selectedContext := done.request, done.context
	if session != nil {
		if session.next != 5 || session.running || session.selectionDigest != selectionDigest {
			return errProductionManagedUnavailable
		}
		phaseDigests = session.phaseDigests
		requestDigest, digestErr := productionCanonicalDigest("managed-selection-request-v1", session.binding.target.request)
		if digestErr != nil {
			return errProductionManagedUnavailable
		}
		requestIdentity = testgendomainRequestIdentity{RequestDigest: requestDigest}
		selectedContext = session.binding.context
	}
	if session == nil && (!completed || done.selectionDigest != selectionDigest || done.planDigest != planDigest) {
		return errProductionManagedUnavailable
	}
	for index, phase := range phases {
		if productionSelectedPhaseIndex(phase.Phase) != index || phaseDigests[phase.Phase] != phase.CommandDigest {
			return errProductionManagedUnavailable
		}
	}
	current, err := executor.config.authority.resolveBinding(ctx, selection.RunID)
	if err != nil || !reflect.DeepEqual(current.context, selectedContext) {
		return errProductionManagedUnavailable
	}
	requestDigest, err := productionCanonicalDigest("managed-selection-request-v1", current.target.request)
	if err != nil || requestIdentity.RequestDigest != requestDigest {
		return errProductionManagedUnavailable
	}
	if session != nil {
		executor.mu.Lock()
		if len(executor.done) >= 256 {
			for key := range executor.done {
				delete(executor.done, key)
				break
			}
		}
		executor.done[selection.SelectedOutputDigest] = productionManagedSelectionAttestation{
			selectionDigest: selectionDigest, request: requestIdentity, context: selectedContext,
			phaseDigests: cloneProductionSelectedPhaseDigests(phaseDigests), planDigest: planDigest,
		}
		executor.mu.Unlock()
	}
	return nil
}

func cloneProductionSelectedPhaseDigests(value map[testgenvalidate.SelectedPhase]string) map[testgenvalidate.SelectedPhase]string {
	result := make(map[testgenvalidate.SelectedPhase]string, len(value))
	for phase, digest := range value {
		result[phase] = digest
	}
	return result
}

func (executor *productionManagedSelectionExecutor) Release(selection testgenpublish.ManagedSelection, roots testgenvalidate.Roots) {
	if executor == nil {
		return
	}
	executor.mu.Lock()
	session := executor.active[selection.SelectedOutputDigest]
	if session != nil && session.roots.Source == roots.Source && session.roots.Build == roots.Build && session.roots.Artifacts == roots.Artifacts && session.roots.SnapshotDigest == roots.SnapshotDigest {
		delete(executor.active, selection.SelectedOutputDigest)
	} else {
		session = nil
	}
	executor.mu.Unlock()
	if session != nil {
		executor.config.plans.ReleaseValidationPlan(session.registration.candidateID, session.registration.processTaskID)
	}
}

func productionSelectedCoverageFromEvidence(binding productionManagedSelectionBinding, evidence testgenvalidate.StageEvidence) (testgenvalidate.SelectedCoverage, error) {
	document, err := coveragemodelv1.Decode(evidence.CoverageJSON)
	if err != nil || document.Completeness.Outcome != coveragemodelv1.Available || len(evidence.CoverageDetailJSON) == 0 || len(evidence.CoverageDetailJSON) > 16<<20 {
		return testgenvalidate.SelectedCoverage{}, errProductionManagedUnavailable
	}
	var detail productionCoverageDetailEvidence
	decoder := json.NewDecoder(bytes.NewReader(evidence.CoverageDetailJSON))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&detail) != nil || decoder.Decode(new(any)) != io.EOF || detail.Version != 1 || len(detail.Observations) == 0 || len(detail.Observations) > 1000000 {
		return testgenvalidate.SelectedCoverage{}, errProductionManagedUnavailable
	}
	canonical, _ := json.Marshal(detail)
	if !bytes.Equal(canonical, evidence.CoverageDetailJSON) {
		return testgenvalidate.SelectedCoverage{}, errProductionManagedUnavailable
	}
	sources := make([]coveragedomain.SourceSnapshot, len(document.Files))
	for index, file := range document.Files {
		sources[index] = coveragedomain.SourceSnapshot{URI: file.URI, SHA256: file.Sha256}
	}
	report, err := coveragedomain.NewReport(coveragedomain.Report{
		ID: binding.index.ReportID, RunID: binding.index.RunID, TestRunID: binding.index.RunID,
		SchemaVersion: coveragedomain.SchemaVersion10, CreatedAt: binding.run.CreatedAt,
		Completeness: coveragedomain.Completeness{Outcome: coveragedomain.OutcomeAvailable},
		Summary:      productionDomainCoverageSummary(document.Summary), Toolchain: binding.index.Toolchain,
		ArtifactID: binding.run.ID, Sources: sources,
	})
	if err != nil {
		return testgenvalidate.SelectedCoverage{}, errProductionManagedUnavailable
	}
	index, err := coveragedetail.Build(coveragedetail.BuildInput{
		WorkspaceGeneration: binding.index.WorkspaceGeneration, ProjectID: binding.index.ProjectID,
		Report: report, Sources: sources, Functions: detail.Observations,
	})
	if err != nil {
		return testgenvalidate.SelectedCoverage{}, errProductionManagedUnavailable
	}
	index.ToolchainID = binding.index.ToolchainID
	return productionSelectedCoverage(index)
}

func productionDomainCoverageSummary(value coveragemodelv1.CoverageSummaryV1) coveragedomain.Summary {
	metric := func(input coveragemodelv1.CoverageMetricV1) coveragedomain.Metric {
		return coveragedomain.Metric{Covered: input.Covered, Total: input.Total}
	}
	return coveragedomain.Summary{Functions: metric(value.Functions), Lines: metric(value.Lines), Branches: metric(value.Branches)}
}

func writeProductionSelectedCoverage(rootPath string, coverage testgenvalidate.SelectedCoverage) error {
	encoded, err := json.Marshal(coverage)
	if err != nil || len(encoded) == 0 || len(encoded) > 8<<20 {
		return errProductionManagedUnavailable
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return errProductionManagedUnavailable
	}
	defer root.Close()
	file, err := root.OpenFile("selected-coverage.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errProductionManagedUnavailable
	}
	if _, err = file.Write(encoded); err == nil {
		err = file.Sync()
	}
	return errors.Join(err, file.Close())
}

var _ testgenvalidate.SelectedStageExecutor = (*productionManagedSelectionExecutor)(nil)
