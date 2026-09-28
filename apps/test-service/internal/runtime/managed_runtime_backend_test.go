package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/managedtest"
	generationv16 "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/testgeneration"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenpublish"
	"unit-test-ide.local/test-service/internal/testgenrender"
)

type managedBackendDriverFixture struct {
	ready  bool
	starts int
	start  func(context.Context, string, generationv16.TestGenerationStartRequestV16, testgendomain.ManagedTarget) (testgendomain.Request, error)
	set    func(context.Context, testgendomain.Run, managedtest.ReviewDraft) (testgenpublish.CandidateSet, error)
}

func (d *managedBackendDriverFixture) ManagedDriverReady() bool { return d.ready }
func (d *managedBackendDriverFixture) PrepareManaged(ctx context.Context, owner string, input generationv16.TestGenerationStartRequestV16, target testgendomain.ManagedTarget) (testgendomain.Request, error) {
	d.starts++
	return d.start(ctx, owner, input, target)
}
func (d *managedBackendDriverFixture) ManagedCandidateSet(ctx context.Context, run testgendomain.Run, draft managedtest.ReviewDraft) (testgenpublish.CandidateSet, error) {
	if d.set == nil {
		return testgenpublish.CandidateSet{}, task.ErrStorageUnavailable
	}
	return d.set(ctx, run, draft)
}

type managedBaselineFixture bool

func (b managedBaselineFixture) ManagedBaselineReady() bool { return bool(b) }
func (b managedBaselineFixture) ValidateManagedBaseline(context.Context, coveragedetail.Index, testgendomain.ManagedTarget) error {
	return nil
}

type managedReceiptFixture bool

func (b managedReceiptFixture) ManagedReceiptReady() bool { return bool(b) }
func (b managedReceiptFixture) ValidateManagedEvidence(context.Context, testgendomain.Run, managedtest.ReviewDraft) error {
	return nil
}

type managedPublisherFixture struct {
	ready                   bool
	preimages               map[string][]byte
	planCalls, publishCalls int
	decision                testgenpublish.ManagedDecision
	receipt                 testgenpublish.Receipt
	published               bool
	publishErr              error
	onPlan                  func()
	onPublish               func()
}

func (p *managedPublisherFixture) ManagedPublicationReady() bool { return p.ready }
func (p *managedPublisherFixture) Recover(context.Context) error { return nil }
func (p *managedPublisherFixture) ReadManagedPreimage(_ context.Context, path string) ([]byte, error) {
	return bytes.Clone(p.preimages[path]), nil
}
func (p *managedPublisherFixture) PlanManaged(_ context.Context, set testgenpublish.CandidateSet, decision testgenpublish.ManagedDecision) (testgenpublish.Plan, error) {
	p.planCalls++
	p.decision = decision
	if p.onPlan != nil {
		p.onPlan()
	}
	return testgenpublish.Plan{RunID: set.RunID, SnapshotDigest: set.SnapshotDigest, ManagedReviewID: decision.ReviewID, ManagedReviewDigest: decision.ReviewDigest,
		CandidateSetDigest: strings.Repeat("d", 64), DiffDigest: strings.Repeat("e", 64), ConfirmationDigest: strings.Repeat("f", 64)}, nil
}
func (p *managedPublisherFixture) PublishManaged(_ context.Context, plan testgenpublish.Plan) (testgenpublish.Receipt, error) {
	p.publishCalls++
	if p.onPublish != nil {
		p.onPublish()
	}
	if p.publishErr != nil {
		return testgenpublish.Receipt{}, p.publishErr
	}
	p.receipt = testgenpublish.Receipt{RunID: plan.RunID, SnapshotDigest: plan.SnapshotDigest, ManagedReviewID: plan.ManagedReviewID, ManagedReviewDigest: plan.ManagedReviewDigest,
		CandidateSetDigest: plan.CandidateSetDigest, DiffDigest: plan.DiffDigest, ConfirmationDigest: plan.ConfirmationDigest}
	p.published = true
	return p.receipt, nil
}
func (p *managedPublisherFixture) ManagedReceipt(_ context.Context, decision testgenpublish.ManagedDecision) (testgenpublish.Receipt, bool, error) {
	if p.published && p.decision.ReviewID == decision.ReviewID && p.decision.ReviewDigest == decision.ReviewDigest {
		return p.receipt, true, nil
	}
	return testgenpublish.Receipt{}, false, nil
}

func managedBackendFixture(t *testing.T) (*ManagedRuntimeProvider, *managedReadFixture, *managedBackendDriverFixture) {
	t.Helper()
	store, err := taskstore.Open(filepath.Join(t.TempDir(), "managed.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	base, err := newGenerationService(GenerationServiceConfig{Store: store, Driver: &generationDriverFixture{complete: true}, Publisher: &generationPublisherFixture{complete: true}, Trusted: true, CoverageReady: true,
		VerifySnapshot: func(_ context.Context, r testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
			return r.SnapshotIdentity(), nil
		},
		VerifyArtifact: func(context.Context, task.Artifact) error { return nil }, VerifyProcess: func(context.Context, string, string) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(base.Close)
	project, report, workspace := "core", strings.Repeat("a", 32), strings.Repeat("b", 64)
	fileID, err := coveragedetail.StableFileID(project, "src/a.c")
	if err != nil {
		t.Fatal(err)
	}
	functionID, err := coveragedetail.StableFunctionID(fileID, "qualified:1:f:signature:")
	if err != nil {
		t.Fatal(err)
	}
	location := coveragedomain.SourceLocation{Line: 4}
	gapID, err := coveragedetail.StableGapID(report, functionID, "line", location, 0)
	if err != nil {
		t.Fatal(err)
	}
	reads := &managedReadFixture{ready: true, index: coveragedetail.Index{ProjectID: project, ReportID: report, WorkspaceGeneration: workspace,
		Project: coveragedetail.Project{Status: coveragedetail.StatusCurrent},
		Files:   []coveragedetail.File{{ID: fileID, RelativePath: "src/a.c", SourceSHA256: strings.Repeat("c", 64), Status: coveragedetail.StatusCurrent, Functions: []coveragedetail.Function{{ID: functionID, Name: "f", Status: coveragedetail.StatusCurrent}}}},
		Gaps:    []coveragedetail.Gap{{ID: gapID, FileID: fileID, FunctionID: functionID, Kind: "line", Location: location}}}}
	driver := &managedBackendDriverFixture{ready: true}
	provider := newManagedRuntimeProvider(ManagedRuntimeConfig{Base: base, CurrentIndex: reads, Reviews: reads, Publisher: &managedPublisherFixture{ready: true}, Validator: healthyManagedValidator(true), Baseline: managedBaselineFixture(true), Receipts: managedReceiptFixture(true), Driver: driver})
	return provider, reads, driver
}

func TestManagedRuntimeProviderNeedsExplicitDependencies(t *testing.T) {
	var nilProvider *ManagedRuntimeProvider
	if nilProvider.ManagedTestsReady() {
		t.Fatal("nil provider advertised v1.6")
	}
	provider := newManagedRuntimeProvider(ManagedRuntimeConfig{})
	if provider.ManagedTestsReady() {
		t.Fatal("empty managed injection advertised v1.6")
	}
}

func TestManagedRuntimeProviderRequiresEveryHealthyDependency(t *testing.T) {
	provider, reads, driver := managedBackendFixture(t)
	if !provider.ManagedTestsReady() {
		t.Fatal("complete explicit test wiring did not advertise managed backend")
	}
	checks := []struct {
		name string
		off  func() func()
	}{
		{"index", func() func() {
			old := provider.config.CurrentIndex
			provider.config.CurrentIndex = nil
			return func() { provider.config.CurrentIndex = old }
		}},
		{"reviews", func() func() {
			old := provider.config.Reviews
			provider.config.Reviews = nil
			return func() { provider.config.Reviews = old }
		}},
		{"publisher", func() func() {
			old := provider.config.Publisher
			provider.config.Publisher = nil
			return func() { provider.config.Publisher = old }
		}},
		{"validator", func() func() {
			old := provider.config.Validator
			provider.config.Validator = nil
			return func() { provider.config.Validator = old }
		}},
		{"baseline", func() func() {
			old := provider.config.Baseline
			provider.config.Baseline = nil
			return func() { provider.config.Baseline = old }
		}},
		{"receipt", func() func() {
			old := provider.config.Receipts
			provider.config.Receipts = nil
			return func() { provider.config.Receipts = old }
		}},
		{"driver", func() func() {
			old := provider.config.Driver
			provider.config.Driver = nil
			return func() { provider.config.Driver = old }
		}},
		{"stale index", func() func() { reads.ready = false; return func() { reads.ready = true } }},
		{"stale driver", func() func() { driver.ready = false; return func() { driver.ready = true } }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			restore := check.off()
			defer restore()
			if provider.ManagedTestsReady() {
				t.Fatal("missing or unhealthy dependency advertised managed capability")
			}
		})
	}
}

func TestManagedRuntimeStartRequiresExactReportBoundGapAndDurableRun(t *testing.T) {
	provider, reads, driver := managedBackendFixture(t)
	owner := strings.Repeat("6", 64)
	gap, report := reads.index.Gaps[0].ID, reads.index.ReportID
	input := generationv16.TestGenerationStartRequestV16{IdempotencyKey: strings.Repeat("1", 32), ProjectID: "core", WorkspaceGeneration: reads.index.WorkspaceGeneration,
		Scope: generationv16.TestGenerationScopeV16CoverageGap, CoverageGapID: &gap, CoverageReportID: &report, Framework: generationv16.Auto,
		Budgets: generationv16.TestGenerationBudgetsV16{WallTimeMS: 60000, CandidateCount: 2, MemoryMiB: 64, Concurrency: 1}}
	driver.start = func(ctx context.Context, gotOwner string, got generationv16.TestGenerationStartRequestV16, target testgendomain.ManagedTarget) (testgendomain.Request, error) {
		if gotOwner != owner || target.GapID != gap || target.SourceDigest != reads.index.Files[0].SourceSHA256 {
			t.Fatal("driver did not receive attested target")
		}
		hash := strings.Repeat("d", 64)
		r := testgendomain.Request{SessionOwnerDigest: gotOwner, IdempotencyKey: got.IdempotencyKey, WorkspaceGeneration: got.WorkspaceGeneration, ProjectID: got.ProjectID,
			Scope: testgendomain.ScopeCoverageGap, CoverageReportID: report, ManagedGapID: gap, Framework: testgendomain.FrameworkAuto,
			Budgets:               testgendomain.Budgets{WallTimeMS: got.Budgets.WallTimeMS, CandidateCount: got.Budgets.CandidateCount, MemoryMiB: got.Budgets.MemoryMiB, Concurrency: got.Budgets.Concurrency},
			CompileSnapshotDigest: hash, CoverageSnapshotDigest: hash, SourceDigest: target.SourceDigest, CMakeTargetDigest: hash, FrameworkBundleDigest: hash,
			AnalyzerBundleDigest: hash, BaselineReportDigest: hash, ProcessOwnerDigest: hash}
		return r, nil
	}
	if _, err := provider.StartManaged(context.Background(), owner, input); err != nil {
		t.Fatal(err)
	}
	if driver.starts != 1 {
		t.Fatalf("starts=%d", driver.starts)
	}
	other := strings.Repeat("e", 32)
	input.CoverageGapID = &other
	if _, err := provider.StartManaged(context.Background(), owner, input); !errors.Is(err, testgendomain.ErrStaleSnapshot) || driver.starts != 1 {
		t.Fatalf("unknown gap reached driver: %v", err)
	}
	input.CoverageGapID, input.CoverageReportID = nil, nil
	input.Scope = generationv16.Symbol
	if _, err := provider.StartManaged(context.Background(), owner, input); !errors.Is(err, task.ErrStorageUnavailable) || driver.starts != 1 {
		t.Fatalf("unbound scope reached driver: %v", err)
	}
}

func TestManagedRuntimeStartRejectsDifferentSourceWithoutReservingIdempotency(t *testing.T) {
	provider, reads, driver := managedBackendFixture(t)
	owner := strings.Repeat("6", 64)
	gap, report := reads.index.Gaps[0].ID, reads.index.ReportID
	input := generationv16.TestGenerationStartRequestV16{IdempotencyKey: strings.Repeat("2", 32), ProjectID: "core", WorkspaceGeneration: reads.index.WorkspaceGeneration,
		Scope: generationv16.TestGenerationScopeV16CoverageGap, CoverageGapID: &gap, CoverageReportID: &report, Framework: generationv16.Auto,
		Budgets: generationv16.TestGenerationBudgetsV16{WallTimeMS: 60000, CandidateCount: 2, MemoryMiB: 64, Concurrency: 1}}
	source := strings.Repeat("d", 64) // Attested src/a.c is c..., not d....
	driver.start = func(ctx context.Context, gotOwner string, got generationv16.TestGenerationStartRequestV16, target testgendomain.ManagedTarget) (testgendomain.Request, error) {
		hash := strings.Repeat("d", 64)
		request := testgendomain.Request{SessionOwnerDigest: gotOwner, IdempotencyKey: got.IdempotencyKey, WorkspaceGeneration: got.WorkspaceGeneration, ProjectID: got.ProjectID,
			Scope: testgendomain.ScopeCoverageGap, CoverageReportID: report, ManagedGapID: gap, Framework: testgendomain.FrameworkAuto,
			Budgets:               testgendomain.Budgets{WallTimeMS: got.Budgets.WallTimeMS, CandidateCount: got.Budgets.CandidateCount, MemoryMiB: got.Budgets.MemoryMiB, Concurrency: got.Budgets.Concurrency},
			CompileSnapshotDigest: hash, CoverageSnapshotDigest: hash, SourceDigest: source, CMakeTargetDigest: hash, FrameworkBundleDigest: hash,
			AnalyzerBundleDigest: hash, BaselineReportDigest: hash, ProcessOwnerDigest: hash}
		return request, nil
	}
	if _, err := provider.StartManaged(context.Background(), owner, input); !errors.Is(err, testgendomain.ErrStaleSnapshot) {
		t.Fatalf("mismatched source start=%v", err)
	}
	source = reads.index.Files[0].SourceSHA256
	if _, err := provider.StartManaged(context.Background(), owner, input); err != nil {
		t.Fatalf("rejected source reserved the idempotency key or left a managed run: %v", err)
	}
}

func managedTestHash(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func awaitingManagedRun(t *testing.T, provider *ManagedRuntimeProvider, reads *managedReadFixture, owner string) testgendomain.Run {
	return awaitingManagedRunWithSource(t, provider, reads, owner, reads.index.Files[0].SourceSHA256)
}

func awaitingManagedRunWithSource(t *testing.T, provider *ManagedRuntimeProvider, reads *managedReadFixture, owner, sourceDigest string) testgendomain.Run {
	t.Helper()
	hash := strings.Repeat("d", 64)
	request := testgendomain.Request{SessionOwnerDigest: owner, IdempotencyKey: strings.Repeat("1", 32), WorkspaceGeneration: reads.index.WorkspaceGeneration,
		ProjectID: reads.index.ProjectID, Scope: testgendomain.ScopeCoverageGap, CoverageReportID: reads.index.ReportID, ManagedGapID: reads.index.Gaps[0].ID,
		Framework: testgendomain.FrameworkAuto, Budgets: testgendomain.Budgets{WallTimeMS: 60000, CandidateCount: 2, MemoryMiB: 64, Concurrency: 1},
		CompileSnapshotDigest: hash, CoverageSnapshotDigest: hash, SourceDigest: sourceDigest, CMakeTargetDigest: hash, FrameworkBundleDigest: hash,
		AnalyzerBundleDigest: hash, BaselineReportDigest: hash, ProcessOwnerDigest: hash}
	run, err := provider.coord.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []testgendomain.State{testgendomain.StateBaseline, testgendomain.StateAnalyzing, testgendomain.StateSolving,
		testgendomain.StateRendering, testgendomain.StateValidating, testgendomain.StateMinimizing, testgendomain.StateAwaitingConfirmation} {
		next := testgendomain.CloneRun(run)
		next.Revision++
		next.State = state
		run, err = provider.store.CheckpointGeneration(context.Background(), run.Revision, next, nil, nil)
		if err != nil {
			t.Fatalf("checkpoint %s: %v", state, err)
		}
	}
	return run
}

func bindManagedDraft(t *testing.T, reads *managedReadFixture, run testgendomain.Run) managedtest.ReviewDraft {
	t.Helper()
	current, generated := []byte("old exact bytes\n"), []byte("new exact bytes\n")
	id := "utc_" + strings.Repeat("9", 32)
	c := managedtest.ReviewCandidate{CandidateID: id, TestRelativePath: "tests/generated/src/a_test.cpp", Status: managedtest.StatusCurrent,
		CurrentBytes: current, GeneratedBytes: generated, CurrentDigest: managedTestHash(current), GeneratedDigest: managedTestHash(generated)}
	m := managedtest.ReviewManifest{ReviewID: strings.Repeat("8", 32), OwnerDigest: run.Request.SessionOwnerDigest, RunID: run.ID, RunRevision: run.Revision,
		ProjectID: run.Request.ProjectID, WorkspaceGeneration: run.Request.WorkspaceGeneration, ReportID: run.Request.CoverageReportID,
		ToolchainID: "trusted-toolchain", SourceDigest: run.Request.SourceDigest, ArtifactRef: "artifact:trusted-review", CreatedAt: time.Now().UTC()}
	m.CandidateSetDigest = managedtest.ReviewCandidateSetDigest([]managedtest.ReviewCandidate{c})
	m.CurrentPreimageDigest, m.GeneratedPreimageDigest = managedtest.ReviewPreimageSetDigests([]managedtest.ReviewCandidate{c})
	draft := managedtest.ReviewDraft{Manifest: m, Candidates: []managedtest.ReviewCandidate{c}}
	if !managedtest.ValidReviewDraft(draft) {
		t.Fatal("invalid managed review fixture")
	}
	reads.binding, reads.draft = m.Binding(), draft
	return draft
}

func managedApplyFixture(t *testing.T) (*ManagedRuntimeProvider, *managedReadFixture, *managedBackendDriverFixture, *managedPublisherFixture, managedtest.ReviewDraft, managedtest.ApplyRequest) {
	provider, reads, driver := managedBackendFixture(t)
	owner := strings.Repeat("6", 64)
	run := awaitingManagedRun(t, provider, reads, owner)
	draft := bindManagedDraft(t, reads, run)
	pub := provider.config.Publisher.(*managedPublisherFixture)
	pub.preimages = map[string][]byte{draft.Candidates[0].TestRelativePath: bytes.Clone(draft.Candidates[0].CurrentBytes)}
	driver.set = func(_ context.Context, current testgendomain.Run, source managedtest.ReviewDraft) (testgenpublish.CandidateSet, error) {
		if current.ID != run.ID || source.Manifest.Digest() != draft.Manifest.Digest() {
			t.Fatal("driver lost durable identity")
		}
		return testgenpublish.CandidateSet{RunID: run.ID, SnapshotDigest: run.Record.SnapshotDigest, CaseIDs: []string{strings.Repeat("9", 32)},
			Managed: &testgenpublish.ManagedCandidateSet{ReviewID: draft.Manifest.ReviewID, ReviewArtifactDigest: strings.Repeat("a", 64), ToolchainID: draft.Manifest.ToolchainID,
				Inputs: []managedtest.ReconcileInput{{Generated: managedtest.ManagedFile{Path: draft.Candidates[0].TestRelativePath, Content: bytes.Clone(draft.Candidates[0].GeneratedBytes)}}}}}, nil
	}
	request := managedtest.ApplyRequest{Owner: owner, ReviewID: draft.Manifest.ReviewID, ReviewDigest: draft.Manifest.Digest(), Resolutions: map[string]managedtest.ConflictChoice{}}
	return provider, reads, driver, pub, draft, request
}

func TestManagedRuntimeApplyChecksDurableBindingAndIsIdempotent(t *testing.T) {
	provider, _, _, pub, _, request := managedApplyFixture(t)
	accepted, err := provider.ApplyManagedReview(context.Background(), request)
	if err != nil || accepted.State != testgendomain.StateAccepted || pub.publishCalls != 1 {
		t.Fatalf("apply=%+v publish=%d err=%v", accepted, pub.publishCalls, err)
	}
	if repeated, err := provider.ApplyManagedReview(context.Background(), request); err != nil || repeated.State != testgendomain.StateAccepted || pub.publishCalls != 1 {
		t.Fatalf("repeat=%+v publish=%d err=%v", repeated, pub.publishCalls, err)
	}
	wrong := request
	wrong.Owner = strings.Repeat("7", 64)
	if _, err := provider.ApplyManagedReview(context.Background(), wrong); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("cross-owner apply=%v", err)
	}
}

func TestManagedRuntimeApplyRejectsRunSourceDifferentFromAttestedGap(t *testing.T) {
	provider, reads, _, pub, _, request := managedApplyFixture(t)
	reads.index.Files[0].SourceSHA256 = strings.Repeat("d", 64)
	if _, err := provider.ApplyManagedReview(context.Background(), request); !errors.Is(err, testgendomain.ErrStaleSnapshot) || pub.publishCalls != 0 {
		t.Fatalf("mismatched run source was published: err=%v publishes=%d", err, pub.publishCalls)
	}
}

func TestManagedRuntimeApplyRejectsStaleBytesAndIncompleteChoicesBeforePlanning(t *testing.T) {
	provider, reads, _, pub, draft, request := managedApplyFixture(t)
	pub.preimages[draft.Candidates[0].TestRelativePath] = []byte("edited after review\n")
	if _, err := provider.ApplyManagedReview(context.Background(), request); !errors.Is(err, testgenpublish.ErrConflict) || pub.planCalls != 0 {
		t.Fatalf("stale bytes planned: err=%v plans=%d", err, pub.planCalls)
	}
	pub.preimages[draft.Candidates[0].TestRelativePath] = bytes.Clone(draft.Candidates[0].CurrentBytes)
	reads.draft.Candidates[0].Status = managedtest.StatusConflicted
	reads.draft.Manifest.CandidateSetDigest = managedtest.ReviewCandidateSetDigest(reads.draft.Candidates)
	reads.draft.Manifest.CurrentPreimageDigest, reads.draft.Manifest.GeneratedPreimageDigest = managedtest.ReviewPreimageSetDigests(reads.draft.Candidates)
	request.ReviewDigest = reads.draft.Manifest.Digest()
	if _, err := provider.ApplyManagedReview(context.Background(), request); !errors.Is(err, task.ErrInvalidArgument) || pub.planCalls != 0 {
		t.Fatalf("unresolved conflict planned: err=%v plans=%d", err, pub.planCalls)
	}
}

func TestManagedRuntimeApplyCancellationAndUncertainPublication(t *testing.T) {
	provider, _, _, pub, _, request := managedApplyFixture(t)
	pre, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := provider.ApplyManagedReview(pre, request); !errors.Is(err, context.Canceled) || pub.publishCalls != 0 {
		t.Fatalf("pre-dispatch cancellation=%v publishes=%d", err, pub.publishCalls)
	}
	pub.publishErr = testgenpublish.ErrConflict
	if _, err := provider.ApplyManagedReview(context.Background(), request); !errors.Is(err, ErrManagedOutcomeUncertain) || pub.publishCalls != 1 {
		t.Fatalf("uncertain publication=%v publishes=%d", err, pub.publishCalls)
	}
	pub.publishErr = nil
	post, cancel := context.WithCancel(context.Background())
	pub.onPublish = cancel
	accepted, err := provider.ApplyManagedReview(post, request)
	if err != nil || accepted.State != testgendomain.StateAccepted || pub.publishCalls != 2 {
		t.Fatalf("post-dispatch cancellation=%+v err=%v publishes=%d", accepted, err, pub.publishCalls)
	}
}

func TestManagedRuntimeApplyKeepsCancelFromTerminalizingBeforePublish(t *testing.T) {
	provider, _, _, pub, _, request := managedApplyFixture(t)
	enteredPlan, releasePlan := make(chan struct{}), make(chan struct{})
	pub.onPlan = func() { close(enteredPlan); <-releasePlan }
	type outcome struct {
		run testgendomain.Run
		err error
	}
	applyDone := make(chan outcome, 1)
	go func() {
		run, err := provider.ApplyManagedReview(context.Background(), request)
		applyDone <- outcome{run, err}
	}()
	<-enteredPlan
	cancelEntered := make(chan struct{})
	cancelDone := make(chan error, 1)
	go func() {
		close(cancelEntered)
		_, err := provider.CancelTestGeneration(context.Background(), request.Owner, provider.config.Reviews.(*managedReadFixture).binding.RunID)
		cancelDone <- err
	}()
	<-cancelEntered
	select {
	case err := <-cancelDone:
		close(releasePlan)
		<-applyDone
		t.Fatalf("cancel terminalized while managed publication was pending: %v", err)
	case <-time.After(250 * time.Millisecond):
	}
	close(releasePlan)
	applied := <-applyDone
	if applied.err != nil || applied.run.State != testgendomain.StateAccepted || pub.publishCalls != 1 {
		t.Fatalf("managed apply lost the publication/cancel race: run=%+v err=%v publishes=%d", applied.run, applied.err, pub.publishCalls)
	}
	select {
	case err := <-cancelDone:
		if err != nil {
			t.Fatalf("cancel replay after accepted publication: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not resolve after managed publication")
	}
	current, err := provider.owned(context.Background(), request.Owner, applied.run.ID)
	if err != nil || current.State != testgendomain.StateAccepted {
		t.Fatalf("cancel replaced accepted run: %+v %v", current, err)
	}
}

func TestManagedRuntimeApplyNeverPublishesPreviouslyCancelledRun(t *testing.T) {
	provider, reads, _, pub, _, request := managedApplyFixture(t)
	cancelled, err := provider.CancelTestGeneration(context.Background(), request.Owner, reads.binding.RunID)
	if err != nil || string(cancelled.State) != string(testgendomain.StateCancelled) {
		t.Fatalf("cancel before apply: %+v %v", cancelled, err)
	}
	if _, err := provider.ApplyManagedReview(context.Background(), request); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("apply after durable cancellation=%v", err)
	}
	if pub.planCalls != 0 || pub.publishCalls != 0 || pub.published {
		t.Fatalf("cancelled run reached publication: plans=%d publishes=%d published=%t", pub.planCalls, pub.publishCalls, pub.published)
	}
}

func TestManagedRuntimeApplyReplayAfterProviderRestart(t *testing.T) {
	provider, _, _, pub, _, request := managedApplyFixture(t)
	if _, err := provider.ApplyManagedReview(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	restarted := newManagedRuntimeProvider(provider.config)
	got, err := restarted.ApplyManagedReview(context.Background(), request)
	if err != nil || got.State != testgendomain.StateAccepted || pub.publishCalls != 1 {
		t.Fatalf("restart replay=%+v err=%v publishes=%d", got, err, pub.publishCalls)
	}
}

type compilingManagedValidator struct{}

func (compilingManagedValidator) ManagedValidationReady() bool { return true }
func (compilingManagedValidator) ValidateManagedSelection(_ context.Context, selection testgenpublish.ManagedSelection) ([]byte, error) {
	if len(selection.Files) != 2 || selection.SelectedOutputDigest == "" {
		return nil, task.ErrConflict
	}
	return []byte("compiled-ran-measured"), nil
}

func TestManagedRuntimeApplyUsesRealJournalAndRegistry(t *testing.T) {
	provider, reads, driver := managedBackendFixture(t)
	root, journal := filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "journal")
	if err := os.MkdirAll(filepath.Join(root, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tests", "generated", "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(journal, 0700); err != nil {
		t.Fatal(err)
	}
	source := []byte("int a(void) { return 1; }\n")
	if err := os.WriteFile(filepath.Join(root, "src", "a.c"), source, 0600); err != nil {
		t.Fatal(err)
	}
	cmake := []byte("add_executable(unit_tests existing_test.cpp)\ntarget_link_libraries(unit_tests PRIVATE core CppUTest)\n")
	if err := os.WriteFile(filepath.Join(root, "tests", "CMakeLists.txt"), cmake, 0600); err != nil {
		t.Fatal(err)
	}
	reads.index.Files[0].SourceSHA256 = managedTestHash(source)
	owner := strings.Repeat("6", 64)
	run := awaitingManagedRunWithSource(t, provider, reads, owner, managedTestHash(source))
	caseID, err := managedtest.StableCaseID(reads.index.ProjectID, "src/a.c", reads.index.Files[0].Functions[0].ID, "zero")
	if err != nil {
		t.Fatal(err)
	}
	block, err := managedtest.RenderMarkers(caseID, reads.index.Files[0].Functions[0].ID, "test_a", []byte("TEST(Group, A) { CHECK_TRUE(1); }\n"), "\n")
	if err != nil {
		t.Fatal(err)
	}
	path := "tests/generated/src/a_test.cpp"
	candidate := managedtest.ReviewCandidate{CandidateID: caseID, TestRelativePath: path, Status: managedtest.StatusCurrent,
		CurrentBytes: []byte{}, GeneratedBytes: block, CurrentDigest: managedTestHash(nil), GeneratedDigest: managedTestHash(block)}
	m := managedtest.ReviewManifest{ReviewID: strings.Repeat("8", 32), OwnerDigest: owner, RunID: run.ID, RunRevision: run.Revision,
		ProjectID: run.Request.ProjectID, WorkspaceGeneration: run.Request.WorkspaceGeneration, ReportID: run.Request.CoverageReportID,
		ToolchainID: "trusted-toolchain", SourceDigest: run.Request.SourceDigest, ArtifactRef: "artifact:trusted-review", CreatedAt: time.Now().UTC()}
	m.CandidateSetDigest = managedtest.ReviewCandidateSetDigest([]managedtest.ReviewCandidate{candidate})
	m.CurrentPreimageDigest, m.GeneratedPreimageDigest = managedtest.ReviewPreimageSetDigests([]managedtest.ReviewCandidate{candidate})
	draft := managedtest.ReviewDraft{Manifest: m, Candidates: []managedtest.ReviewCandidate{candidate}}
	if !managedtest.ValidReviewDraft(draft) {
		t.Fatal("invalid real publication review")
	}
	reads.binding, reads.draft = m.Binding(), draft
	doc, err := managedtest.ParseDocument(block, 4096, 1)
	if err != nil {
		t.Fatal(err)
	}
	record := managedtest.Record{CaseID: caseID, ProjectID: m.ProjectID, SourceFileID: reads.index.Files[0].ID, FunctionID: reads.index.Files[0].Functions[0].ID,
		SourceRelativePath: "src/a.c", ScenarioID: "zero", TestRelativePath: path, AcceptedBlockDigest: doc.Blocks[0].Digest,
		GeneratorVersion: "test", Framework: "cpputest", ToolchainID: m.ToolchainID, SourceDigest: managedTestHash(source),
		ValidationReceiptDigest: managedTestHash([]byte("validated")), Status: managedtest.StatusCurrent, LastVerifiedAt: time.Now().UTC()}
	if !managedtest.ValidRecord(record) {
		t.Fatal("invalid real publication record")
	}
	currentDoc, err := managedtest.ParseDocument(nil, 4096, 1)
	if err != nil {
		t.Fatal(err)
	}
	input := managedtest.ReconcileInput{Current: currentDoc, Generated: managedtest.ManagedFile{Path: path, Content: block}}
	review, err := managedtest.Reconcile(input)
	if err != nil {
		t.Fatal(err)
	}
	cmakeAfter := append(bytes.Clone(cmake), []byte("target_sources(unit_tests PRIVATE \"generated/src/a_test.cpp\")\n")...)
	driver.set = func(_ context.Context, _ testgendomain.Run, _ managedtest.ReviewDraft) (testgenpublish.CandidateSet, error) {
		return testgenpublish.CandidateSet{RunID: run.ID, SnapshotDigest: run.Record.SnapshotDigest, CaseIDs: []string{strings.TrimPrefix(caseID, "utc_")},
			TestTarget: "unit_tests", ProductionTarget: "core", FrameworkTarget: "CppUTest",
			Files: []testgenrender.StagedFile{{Path: path, Content: bytes.Clone(block), AfterDigest: managedTestHash(block)},
				{Path: "tests/CMakeLists.txt", Content: bytes.Clone(cmakeAfter), BeforeDigest: managedTestHash(cmake), AfterDigest: managedTestHash(cmakeAfter)}},
			Managed: &testgenpublish.ManagedCandidateSet{ReviewID: m.ReviewID, ReviewArtifactDigest: review.Digest(), ToolchainID: m.ToolchainID,
				ValidationReceipt: []byte("validated"), ValidationReceiptDigest: record.ValidationReceiptDigest, CMakePath: "tests/CMakeLists.txt",
				Inputs: []managedtest.ReconcileInput{input}, Records: []managedtest.Record{record}}}, nil
	}
	publisher, err := testgenpublish.New(root, journal, func(context.Context, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = publisher.Close() })
	publisher.ManagedRegistry = provider.store.ManagedTestRegistry()
	validator := compilingManagedValidator{}
	publisher.ManagedSelectionValidator = validator.ValidateManagedSelection
	provider.config.Publisher, provider.config.Validator = publisher, validator
	request := managedtest.ApplyRequest{Owner: owner, ReviewID: m.ReviewID, ReviewDigest: m.Digest(), Resolutions: map[string]managedtest.ConflictChoice{}}
	accepted, err := provider.ApplyManagedReview(context.Background(), request)
	if err != nil || accepted.State != testgendomain.StateAccepted {
		t.Fatalf("real publish=%+v err=%v", accepted, err)
	}
	written, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil || !bytes.Equal(written, block) {
		t.Fatalf("published bytes=%q err=%v", written, err)
	}
	stored, err := provider.store.ManagedTestRegistry().Get(context.Background(), caseID)
	if err != nil || stored.Status != managedtest.StatusCurrent {
		t.Fatalf("registry=%+v err=%v", stored, err)
	}
	page, err := provider.ListManagedTestRecords(context.Background(), owner, generationv16.ManagedRecordsRequestV16{ProjectID: m.ProjectID,
		WorkspaceGeneration: m.WorkspaceGeneration, CoverageReportID: m.ReportID})
	if err != nil || len(page.Items) != 1 || page.Items[0].CaseID != caseID || page.Items[0].CurrentDigest != managedTestHash(block) {
		t.Fatalf("managed record page=%+v err=%v", page, err)
	}
	if repeated, err := provider.ApplyManagedReview(context.Background(), request); err != nil || repeated.State != testgendomain.StateAccepted {
		t.Fatalf("real journal replay=%+v err=%v", repeated, err)
	}
	reopened, err := testgenpublish.New(root, journal, func(context.Context, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reopened.ManagedRegistry = provider.store.ManagedTestRegistry()
	reopened.ManagedSelectionValidator = validator.ValidateManagedSelection
	restarted := newManagedRuntimeProvider(provider.config)
	restarted.config.Publisher = reopened
	if repeated, err := restarted.ApplyManagedReview(context.Background(), request); err != nil || repeated.State != testgendomain.StateAccepted {
		t.Fatalf("reopened journal replay=%+v err=%v", repeated, err)
	}
}
