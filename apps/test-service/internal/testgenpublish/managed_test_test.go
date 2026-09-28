package testgenpublish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/managedtest"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
	"unit-test-ide.local/test-service/internal/testgenrender"
)

type managedRegistryFixture struct {
	records    map[string]managedtest.Record
	pending    map[string]managedtest.Acceptance
	failCommit bool
}

func (r *managedRegistryFixture) Get(_ context.Context, id string) (managedtest.Record, error) {
	if len(r.pending) > 0 {
		return managedtest.Record{}, task.ErrStorageUnavailable
	}
	if value, ok := r.records[id]; ok {
		return value, nil
	}
	return managedtest.Record{}, task.ErrNotFound
}

func TestManagedRecoveryCommitsPendingBeforeCheckingEarlierCommits(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	decision := addSecondManagedFile(t, &f)
	plan, err := f.p.PlanManaged(context.Background(), f.set, decision)
	if err != nil {
		t.Fatal(err)
	}
	prepared := f.p.plans[plan.ConfirmationDigest]
	if len(prepared.managed.acceptances) != 2 {
		t.Fatal("need two acceptances")
	}
	first, second := prepared.managed.acceptances[0], prepared.managed.acceptances[1]
	f.registry.records[first.Record.CaseID] = first.Record
	f.registry.pending[second.AcceptanceID] = second
	j := journalRecord{ManagedAcceptances: []managedtest.Acceptance{first, second}}
	if err := f.p.finalizeManaged(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if len(f.registry.pending) != 0 || len(f.registry.records) != 2 {
		t.Fatalf("partial replay: %d %d", len(f.registry.pending), len(f.registry.records))
	}
}

func TestManagedPublisherAndDurableRegistrySurviveRestart(t *testing.T) {
	f := newManagedFixture(t)
	storePath := filepath.Join(t.TempDir(), "managed.sqlite")
	store, err := taskstore.Open(storePath)
	if err != nil {
		t.Fatal(err)
	}
	f.p.ManagedRegistry = store.ManagedTestRegistry()
	plan, err := f.p.PlanManaged(context.Background(), f.set, f.decision(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.PublishManaged(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if !store.ManagedTestsReady() {
		t.Fatal("managed capability not ready")
	}
	f.close(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = taskstore.Open(storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	p, err := New(f.root, f.journal, func(context.Context, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.ManagedRegistry = store.ManagedTestRegistry()
	if err := p.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PublishManaged(context.Background(), plan); err != nil {
		t.Fatalf("repeat after restart: %v", err)
	}
	got, err := store.ManagedTestRegistry().Get(context.Background(), f.record.CaseID)
	if err != nil || got.AcceptedBlockDigest != f.record.AcceptedBlockDigest {
		t.Fatalf("durable registry: %+v %v", got, err)
	}
}

func TestManagedPublicationRetainsDurableManifestIdentity(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	decision := f.decision(t)
	artifactDigest := decision.ReviewDigest
	decision.ReviewDigest = strings.Repeat("a", 64)
	f.set.Managed.ReviewArtifactDigest = artifactDigest
	plan, err := f.p.PlanManaged(context.Background(), f.set, decision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.PublishManaged(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	receipt, found, err := f.p.ManagedReceipt(context.Background(), decision)
	if err != nil || !found || receipt.ManagedReviewDigest != decision.ReviewDigest || receipt.ManagedReviewID != decision.ReviewID {
		t.Fatalf("bound receipt=%+v found=%v err=%v", receipt, found, err)
	}
	wrong := decision
	wrong.Resolutions = map[string]managedtest.ConflictChoice{f.record.CaseID: managedtest.KeepCurrent}
	if _, found, err := f.p.ManagedReceipt(context.Background(), wrong); err != nil || found {
		t.Fatalf("different choices matched receipt: found=%v err=%v", found, err)
	}
}

func TestManagedTwoBlocksInOneFileAdvanceTogether(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	second := f.record
	second.ScenarioID = "other"
	var err error
	second.CaseID, err = managedtest.StableCaseID(second.ProjectID, second.SourceRelativePath, second.FunctionID, second.ScenarioID)
	if err != nil {
		t.Fatal(err)
	}
	block, err := managedtest.RenderMarkers(second.CaseID, second.FunctionID, "choose", []byte("TEST(Group, Other) { CHECK_TRUE(2); }\n"), "\n")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := managedtest.ParseDocument(block, maxEditBytes, 1)
	if err != nil {
		t.Fatal(err)
	}
	second.AcceptedBlockDigest = doc.Blocks[0].Digest
	together := append(bytes.Clone(f.block), block...)
	f.set.Managed.Records = append(f.set.Managed.Records, second)
	f.set.Managed.Inputs[0].Generated.Content = together
	f.set.Files[0].Content = together
	f.set.Files[0].AfterDigest = digest(together)
	f.set.CaseIDs = append(f.set.CaseIDs, second.CaseID[4:])
	plan, err := f.p.PlanManaged(context.Background(), f.set, f.decision(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.PublishManaged(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	for _, record := range f.set.Managed.Records {
		if _, err := f.registry.Get(context.Background(), record.CaseID); err != nil {
			t.Fatalf("missing case %s: %v", record.CaseID, err)
		}
	}
}

func TestManagedSourceOrCMakeDriftBeforeCommitRollsBack(t *testing.T) {
	for _, path := range []string{"src/a.cpp", "tests/CMakeLists.txt"} {
		t.Run(path, func(t *testing.T) {
			f := newManagedFixture(t)
			defer f.close(t)
			plan, err := f.p.PlanManaged(context.Background(), f.set, f.decision(t))
			if err != nil {
				t.Fatal(err)
			}
			f.p.hooks.fail = func(stage string) error {
				if stage == "commit-first" {
					return os.WriteFile(filepath.Join(f.root, filepath.FromSlash(path)), []byte("user edit\n"), 0600)
				}
				return nil
			}
			if _, err := f.p.PublishManaged(context.Background(), plan); !errors.Is(err, ErrConflict) {
				t.Fatalf("drift accepted: %v", err)
			}
			if len(f.registry.records) != 0 {
				t.Fatal("registry accepted on drift")
			}
			if path == "src/a.cpp" && len(f.registry.pending) != 0 {
				t.Fatal("source drift left pending acceptance")
			}
			if path == "tests/CMakeLists.txt" && len(f.registry.pending) == 0 {
				t.Fatal("unsafe CMake replacement should require recovery")
			}
			data, readErr := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(path)))
			if readErr != nil || string(data) != "user edit\n" {
				t.Fatalf("user edit lost: %q %v", data, readErr)
			}
			if _, err := os.Stat(filepath.Join(f.root, filepath.FromSlash(f.record.TestRelativePath))); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("test file published on drift: %v", err)
			}
		})
	}
}

func TestManagedCancellationDuringJournalRollsBack(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	ctx, cancel := context.WithCancel(context.Background())
	plan, err := f.p.PlanManaged(ctx, f.set, f.decision(t))
	if err != nil {
		t.Fatal(err)
	}
	f.p.hooks.fail = func(stage string) error {
		if stage == "commit-last" {
			cancel()
		}
		return nil
	}
	if _, err := f.p.PublishManaged(ctx, plan); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	if len(f.registry.records) != 0 || len(f.registry.pending) != 0 {
		t.Fatal("registry advanced on cancellation")
	}
}
func (r *managedRegistryFixture) List(context.Context, managedtest.Query) (managedtest.Page, error) {
	return managedtest.Page{}, nil
}
func (r *managedRegistryFixture) ReconcileWorkspace(context.Context, managedtest.WorkspaceSnapshot) error {
	return nil
}
func (r *managedRegistryFixture) CommitAccepted(_ context.Context, a managedtest.Acceptance) error {
	if r.failCommit {
		r.failCommit = false
		return errors.New("injected registry commit failure")
	}
	if _, ok := r.pending[a.AcceptanceID]; !ok {
		return task.ErrConflict
	}
	r.records[a.Record.CaseID] = a.Record
	delete(r.pending, a.AcceptanceID)
	return nil
}

func TestManagedJournalRollbackClearsPendingAtEachFault(t *testing.T) {
	for _, stage := range []string{"journal", "stage-first", "stage-last", "commit-first", "commit-last", "readback", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			f := newManagedFixture(t)
			defer f.close(t)
			plan, err := f.p.PlanManaged(context.Background(), f.set, f.decision(t))
			if err != nil {
				t.Fatal(err)
			}
			f.p.hooks.fail = func(at string) error {
				if at == stage {
					return errors.New("injected")
				}
				return nil
			}
			if _, err := f.p.PublishManaged(context.Background(), plan); err == nil {
				t.Fatal("fault was ignored")
			}
			if len(f.registry.pending) != 0 || len(f.registry.records) != 0 {
				t.Fatalf("partial registry at %s: pending=%d records=%d", stage, len(f.registry.pending), len(f.registry.records))
			}
			if _, err := os.Stat(filepath.Join(f.root, filepath.FromSlash(f.record.TestRelativePath))); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("partial file at %s: %v", stage, err)
			}
			cmake, err := os.ReadFile(filepath.Join(f.root, "tests", "CMakeLists.txt"))
			if err != nil || string(cmake) != f.before {
				t.Fatalf("CMake partial at %s: %q %v", stage, cmake, err)
			}
		})
	}
}

func TestManagedReceiptAfterRegistryFailureRecoversWithoutRewrite(t *testing.T) {
	f := newManagedFixture(t)
	plan, err := f.p.PlanManaged(context.Background(), f.set, f.decision(t))
	if err != nil {
		t.Fatal(err)
	}
	f.registry.failCommit = true
	receipt, err := f.p.PublishManaged(context.Background(), plan)
	if !errors.Is(err, ErrRecoveryRequired) || receipt.ConfirmationDigest != plan.ConfirmationDigest {
		t.Fatalf("missing durable receipt: %+v %v", receipt, err)
	}
	path := filepath.Join(f.root, filepath.FromSlash(f.record.TestRelativePath))
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	f.close(t)
	p, err := New(f.root, f.journal, func(context.Context, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.ManagedRegistry = f.registry
	if err := p.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.Recover(context.Background()); err != nil {
		t.Fatalf("repeat recovery: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("recovery rewrote file: %v", err)
	}
	if _, err := f.registry.Get(context.Background(), f.record.CaseID); err != nil {
		t.Fatalf("registry not recovered: %v", err)
	}
	if _, err := p.PublishManaged(context.Background(), plan); err != nil {
		t.Fatalf("same review retry: %v", err)
	}
}
func (r *managedRegistryFixture) BeginManagedAcceptance(_ context.Context, a managedtest.Acceptance) error {
	if _, ok := r.pending[a.AcceptanceID]; ok {
		return task.ErrConflict
	}
	r.pending[a.AcceptanceID] = a
	return nil
}
func (r *managedRegistryFixture) MarkManagedFileWritten(context.Context, string, string) error {
	return nil
}
func (r *managedRegistryFixture) ListPendingManagedAcceptances(context.Context) ([]managedtest.PendingAcceptance, error) {
	items := make([]managedtest.PendingAcceptance, 0, len(r.pending))
	for _, a := range r.pending {
		items = append(items, managedtest.PendingAcceptance{Acceptance: a, Phase: "prepared"})
	}
	return items, nil
}
func (r *managedRegistryFixture) ResolvePendingManagedAcceptance(_ context.Context, id, _ string) error {
	delete(r.pending, id)
	return nil
}
func (r *managedRegistryFixture) AuthorizeLegacyManagedRecovery(context.Context, managedtest.LegacyRecoveryEvidence) error {
	return task.ErrConflict
}
func (r *managedRegistryFixture) RetireAccepted(_ context.Context, retirement managedtest.Retirement) error {
	v, ok := r.records[retirement.CaseID]
	if !ok {
		return task.ErrNotFound
	}
	v.Status = managedtest.StatusInvalid
	r.records[retirement.CaseID] = v
	return nil
}

type managedFixture struct {
	fixture
	registry *managedRegistryFixture
	record   managedtest.Record
	block    []byte
}

func newManagedFixture(t *testing.T) managedFixture {
	t.Helper()
	f := newFixture(t)
	registry := &managedRegistryFixture{records: map[string]managedtest.Record{}, pending: map[string]managedtest.Acceptance{}}
	f.p.ManagedRegistry = registry
	f.p.ManagedSelectionValidator = func(_ context.Context, selection ManagedSelection) ([]byte, error) {
		if len(selection.Files) == 0 || !validHex(selection.SelectedOutputDigest, 64) {
			return nil, ErrConflict
		}
		return []byte("validated-selected:" + selection.SelectedOutputDigest), nil
	}
	functionID := strings.Repeat("a", 32)
	caseID, err := managedtest.StableCaseID("project", "src/a.cpp", functionID, "zero")
	if err != nil {
		t.Fatal(err)
	}
	fileID, err := coveragedetail.StableFileID("project", "src/a.cpp")
	if err != nil {
		t.Fatal(err)
	}
	block, err := managedtest.RenderMarkers(caseID, functionID, "choose", []byte("TEST(Group, Zero) { CHECK_TRUE(1); }\n"), "\n")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := managedtest.ParseDocument(block, 4096, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(f.root, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	source := []byte("int choose(int x) { return x; }\n")
	if err := os.WriteFile(filepath.Join(f.root, "src", "a.cpp"), source, 0600); err != nil {
		t.Fatal(err)
	}
	record := managedtest.Record{CaseID: caseID, ProjectID: "project", SourceFileID: fileID, FunctionID: functionID, SourceRelativePath: "src/a.cpp", ScenarioID: "zero", TestRelativePath: "tests/generated/src/a.cpp_test.cpp", AcceptedBlockDigest: doc.Blocks[0].Digest, GeneratorVersion: "1", Framework: "cpputest", ToolchainID: "toolchain", SourceDigest: digest(source), ValidationReceiptDigest: digest([]byte("validated")), Status: managedtest.StatusCurrent, LastVerifiedAt: time.Now().UTC()}
	f.set.CaseIDs = []string{caseID[4:]}
	f.set.Files[0] = testgenrender.StagedFile{Path: record.TestRelativePath, Content: bytes.Clone(block), AfterDigest: digest(block)}
	f.set.Files[1].Content = []byte(f.before + "target_sources(unit_tests PRIVATE \"generated/src/a.cpp_test.cpp\")\n")
	f.set.Files[1].AfterDigest = digest(f.set.Files[1].Content)
	f.set.Managed = &ManagedCandidateSet{ReviewID: strings.Repeat("e", 32), Inputs: []managedtest.ReconcileInput{{Generated: managedtest.ManagedFile{Path: record.TestRelativePath, Content: bytes.Clone(block)}}}, Records: []managedtest.Record{record}, ToolchainID: "toolchain", ValidationReceiptDigest: record.ValidationReceiptDigest, ValidationReceipt: []byte("validated"), CMakePath: "tests/CMakeLists.txt"}
	return managedFixture{fixture: f, registry: registry, record: record, block: block}
}

func (f managedFixture) decision(t *testing.T) ManagedDecision {
	t.Helper()
	currentBytes, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(f.record.TestRelativePath)))
	if errors.Is(err, os.ErrNotExist) {
		currentBytes = nil
	} else if err != nil {
		t.Fatal(err)
	}
	current, err := managedtest.ParseDocument(currentBytes, maxEditBytes, 4096)
	if err != nil {
		t.Fatal(err)
	}
	input := f.set.Managed.Inputs[0]
	input.Current = current
	review, err := managedtest.Reconcile(input)
	if err != nil {
		t.Fatal(err)
	}
	return ManagedDecision{ReviewID: f.set.Managed.ReviewID, ReviewDigest: review.Digest(), Resolutions: map[string]managedtest.ConflictChoice{}}
}

func (f *managedFixture) seedAccepted(t *testing.T, current []byte) {
	t.Helper()
	path := filepath.Join(f.root, filepath.FromSlash(f.record.TestRelativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, current, 0600); err != nil {
		t.Fatal(err)
	}
	f.registry.records[f.record.CaseID] = f.record
	f.set.Managed.Inputs[0].Accepted = []managedtest.Record{f.record}
	f.set.Managed.Inputs[0].AcceptedBlocks = map[string][]byte{f.record.CaseID: bytes.Clone(f.block)}
	f.set.Managed.Records[0].LastVerifiedAt = f.record.LastVerifiedAt.Add(time.Second)
	linked := f.before + "target_sources(unit_tests PRIVATE \"generated/src/a.cpp_test.cpp\")\n"
	if err := os.WriteFile(filepath.Join(f.root, "tests", "CMakeLists.txt"), []byte(linked), 0600); err != nil {
		t.Fatal(err)
	}
	f.set.Files = f.set.Files[:1] // the source is already linked; no CMake edit is needed.
}

func TestManagedUpdateWithoutCMakeEditPreservesHandwrittenBytes(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	current := append([]byte("// handwritten header\n"), f.block...)
	f.seedAccepted(t, current)
	next := bytes.Replace(f.block, []byte("CHECK_TRUE(1)"), []byte("CHECK_TRUE(2)"), 1)
	f.set.Managed.Inputs[0].Generated.Content = next
	f.set.Files[0].Content = next
	f.set.Files[0].AfterDigest = digest(next)
	nextDoc, err := managedtest.ParseDocument(next, 4096, 1)
	if err != nil {
		t.Fatal(err)
	}
	f.set.Managed.Records[0].AcceptedBlockDigest = nextDoc.Blocks[0].Digest
	decision := f.decision(t)
	decision.Resolutions["scaffold:"+f.record.TestRelativePath] = managedtest.KeepCurrent
	plan, err := f.p.PlanManaged(context.Background(), f.set, decision)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Edits) != 1 || plan.Edits[0].Path != f.record.TestRelativePath {
		t.Fatalf("unexpected edits: %+v", plan.Edits)
	}
	if _, err := f.p.PublishManaged(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(f.record.TestRelativePath)))
	if err != nil || !bytes.Equal(got, append([]byte("// handwritten header\n"), next...)) {
		t.Fatalf("handwritten bytes lost: %q %v", got, err)
	}
}

func TestManagedConflictNeedsExactChoiceAndConvertRetires(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	current := bytes.Replace(f.block, []byte("CHECK_TRUE(1)"), []byte("CHECK_TRUE(3)"), 1)
	f.seedAccepted(t, current)
	next := bytes.Replace(f.block, []byte("CHECK_TRUE(1)"), []byte("CHECK_TRUE(2)"), 1)
	f.set.Managed.Inputs[0].Generated.Content = next
	f.set.Files[0].Content = next
	f.set.Files[0].AfterDigest = digest(next)
	decision := f.decision(t)
	if _, err := f.p.PlanManaged(context.Background(), f.set, decision); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("missing choice accepted: %v", err)
	}
	decision.Resolutions[f.record.CaseID] = managedtest.ConvertToManual
	plan, err := f.p.PlanManaged(context.Background(), f.set, decision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.PublishManaged(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(f.record.TestRelativePath)))
	if err != nil || string(got) != "TEST(Group, Zero) { CHECK_TRUE(3); }\n" {
		t.Fatalf("manual body changed: %q %v", got, err)
	}
	record, err := f.registry.Get(context.Background(), f.record.CaseID)
	if err != nil || record.Status != managedtest.StatusInvalid {
		t.Fatalf("record not retired: %+v %v", record, err)
	}
}

func TestManagedScaffoldReplacementRequiresExplicitChoice(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	f.seedAccepted(t, f.block)
	next := append([]byte("#include \"new-header.h\"\n"), bytes.Replace(f.block, []byte("CHECK_TRUE(1)"), []byte("CHECK_TRUE(2)"), 1)...)
	f.set.Managed.Inputs[0].Generated.Content = next
	f.set.Files[0].Content = next
	f.set.Files[0].AfterDigest = digest(next)
	doc, err := managedtest.ParseDocument(next, maxEditBytes, 4)
	if err != nil {
		t.Fatal(err)
	}
	f.set.Managed.Records[0].AcceptedBlockDigest = doc.Blocks[0].Digest
	decision := f.decision(t)
	if _, err := f.p.PlanManaged(context.Background(), f.set, decision); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("implicit scaffold replacement: %v", err)
	}
	decision.Resolutions["scaffold:"+f.record.TestRelativePath] = managedtest.UseGenerated
	plan, err := f.p.PlanManaged(context.Background(), f.set, decision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.PublishManaged(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(f.record.TestRelativePath)))
	if err != nil || !bytes.Equal(got, next) {
		t.Fatalf("generated scaffold not applied: %q %v", got, err)
	}
}

func TestManagedPublishRejectsMutatedPlanBeforeWriting(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	plan, err := f.p.PlanManaged(context.Background(), f.set, f.decision(t))
	if err != nil {
		t.Fatal(err)
	}
	plan.ManagedReviewDigest = strings.Repeat("0", 64)
	if _, err := f.p.PublishManaged(context.Background(), plan); !errors.Is(err, ErrConflict) {
		t.Fatalf("tampered plan accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, filepath.FromSlash(f.record.TestRelativePath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tampered plan wrote file: %v", err)
	}
}

func TestManagedPlanIdentityBindsValidationAndGeneratorMetadata(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	decision := f.decision(t)
	first, err := f.p.PlanManaged(context.Background(), f.set, decision)
	if err != nil {
		t.Fatal(err)
	}
	f.set.Managed.Records[0].GeneratorVersion = "2"
	second, err := f.p.PlanManaged(context.Background(), f.set, decision)
	if err != nil {
		t.Fatal(err)
	}
	if first.ConfirmationDigest == second.ConfirmationDigest || first.CandidateSetDigest == second.CandidateSetDigest {
		t.Fatal("different accepted metadata aliased the same publication")
	}
}

func TestManagedKeepCurrentAdvancesAcceptedDigestWithoutRewritingFile(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	current := bytes.Replace(f.block, []byte("CHECK_TRUE(1)"), []byte("CHECK_TRUE(3)"), 1)
	f.seedAccepted(t, current)
	currentDoc, err := managedtest.ParseDocument(current, maxEditBytes, 1)
	if err != nil {
		t.Fatal(err)
	}
	f.set.Managed.Records[0].AcceptedBlockDigest = currentDoc.Blocks[0].Digest
	decision := f.decision(t)
	decision.Resolutions[f.record.CaseID] = managedtest.KeepCurrent
	plan, err := f.p.PlanManaged(context.Background(), f.set, decision)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Edits) != 0 {
		t.Fatalf("keep-current should not write: %+v", plan.Edits)
	}
	path := filepath.Join(f.root, filepath.FromSlash(f.record.TestRelativePath))
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.PublishManaged(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("keep-current rewrote file: %v", err)
	}
	record, err := f.registry.Get(context.Background(), f.record.CaseID)
	if err != nil || record.AcceptedBlockDigest != currentDoc.Blocks[0].Digest {
		t.Fatalf("digest not advanced: %+v %v", record, err)
	}
}

func TestManagedSelectedBytesBindValidationReceiptAndRegistry(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	current := bytes.Replace(f.block, []byte("CHECK_TRUE(1)"), []byte("CHECK_TRUE(3)"), 1)
	f.seedAccepted(t, current)
	currentDoc, err := managedtest.ParseDocument(current, maxEditBytes, 1)
	if err != nil {
		t.Fatal(err)
	}
	f.set.Managed.Records[0].AcceptedBlockDigest = currentDoc.Blocks[0].Digest
	decision := f.decision(t)
	decision.Resolutions[f.record.CaseID] = managedtest.KeepCurrent
	var observed ManagedSelection
	f.p.ManagedSelectionValidator = func(_ context.Context, selection ManagedSelection) ([]byte, error) {
		observed = selection
		return []byte("validated-current:" + selection.SelectedOutputDigest), nil
	}
	plan, err := f.p.PlanManaged(context.Background(), f.set, decision)
	if err != nil {
		t.Fatal(err)
	}
	if len(observed.Files) != 2 || !bytes.Equal(observed.Files[0].Content, current) || plan.ManagedSelectedOutputDigest != observed.SelectedOutputDigest || plan.ManagedValidationReceiptDigest == f.set.Managed.ValidationReceiptDigest {
		t.Fatalf("resolved bytes were not validated and bound: %+v %+v", observed, plan)
	}
	receipt, err := f.p.PublishManaged(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := f.registry.Get(context.Background(), f.record.CaseID)
	if err != nil || receipt.ManagedSelectedOutputDigest != plan.ManagedSelectedOutputDigest || receipt.ManagedValidationReceiptDigest != plan.ManagedValidationReceiptDigest || stored.ValidationReceiptDigest != plan.ManagedValidationReceiptDigest {
		t.Fatalf("final validation lineage lost: receipt=%+v record=%+v err=%v", receipt, stored, err)
	}
}

func TestManagedSelectionRequiresValidatorAndRejectsEmptyProof(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	f.p.ManagedSelectionValidator = nil
	if _, err := f.p.PlanManaged(context.Background(), f.set, f.decision(t)); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("missing validator accepted: %v", err)
	}
	f.p.ManagedSelectionValidator = func(context.Context, ManagedSelection) ([]byte, error) { return nil, nil }
	if _, err := f.p.PlanManaged(context.Background(), f.set, f.decision(t)); !errors.Is(err, ErrConflict) {
		t.Fatalf("empty validation proof accepted: %v", err)
	}
}

func TestManagedReadOnlyReceiptRejectsCMakeDriftAfterPublication(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	f.seedAccepted(t, f.block)
	plan, err := f.p.PlanManaged(context.Background(), f.set, f.decision(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Edits) != 0 {
		t.Fatal("expected read-only plan")
	}
	if _, err := f.p.PublishManaged(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "tests", "CMakeLists.txt"), []byte(f.before), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.PublishManaged(context.Background(), plan); !errors.Is(err, ErrConflict) {
		t.Fatalf("CMake drift accepted on retry: %v", err)
	}
}

func TestManagedRecoveryRejectsTamperedFinalValidationLineage(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	plan, err := f.p.PlanManaged(context.Background(), f.set, f.decision(t))
	if err != nil {
		t.Fatal(err)
	}
	prepared := f.p.plans[plan.ConfirmationDigest]
	a := prepared.managed.acceptances[0]
	j := journalRecord{Version: 1, ConfirmationDigest: plan.ConfirmationDigest, Receipt: receiptFor(plan), ManagedAcceptances: []managedtest.Acceptance{a}}
	for _, file := range prepared.files {
		j.Files = append(j.Files, journalFile{Path: file.edit.Path, BeforeDigest: file.edit.BeforeDigest, AfterDigest: file.edit.AfterDigest, Before: file.before, Existed: file.existed, StageName: ".testgen-a.stage", BackupName: ".testgen-a.backup", HoldName: ".testgen-a.hold"})
	}
	if !validJournal(j) {
		t.Fatal("valid managed journal rejected")
	}
	j.Receipt.ManagedSelectedOutputDigest = strings.Repeat("f", 64)
	if validJournal(j) {
		t.Fatal("tampered selected-output digest accepted")
	}
	j.Receipt = receiptFor(plan)
	j.ManagedAcceptances[0].Record.ValidationReceiptDigest = strings.Repeat("f", 64)
	if validJournal(j) {
		t.Fatal("tampered final validation lineage accepted")
	}
}

func TestManagedStagedBytesRereadImmediatelyBeforeCommit(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	plan, err := f.p.PlanManaged(context.Background(), f.set, f.decision(t))
	if err != nil {
		t.Fatal(err)
	}
	f.p.hooks.beforeRename = func(path string) {
		if path != f.record.TestRelativePath {
			return
		}
		stage := ".testgen-" + plan.ConfirmationDigest[:16] + "-a.stage"
		if err := os.WriteFile(filepath.Join(f.root, "tests", "generated", "src", stage), []byte("tampered stage\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.p.PublishManaged(context.Background(), plan); !errors.Is(err, ErrConflict) {
		t.Fatalf("tampered stage committed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, filepath.FromSlash(f.record.TestRelativePath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tampered stage published: %v", err)
	}
}

func addSecondManagedFile(t *testing.T, f *managedFixture) ManagedDecision {
	t.Helper()
	functionID := f.record.FunctionID
	caseID, err := managedtest.StableCaseID("project", "src/b.cpp", functionID, "zero")
	if err != nil {
		t.Fatal(err)
	}
	fileID, err := coveragedetail.StableFileID("project", "src/b.cpp")
	if err != nil {
		t.Fatal(err)
	}
	block, err := managedtest.RenderMarkers(caseID, functionID, "choose", []byte("TEST(Group, Other) { CHECK_TRUE(2); }\n"), "\n")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := managedtest.ParseDocument(block, maxEditBytes, 1)
	if err != nil {
		t.Fatal(err)
	}
	second := f.record
	second.CaseID = caseID
	second.SourceFileID = fileID
	second.SourceRelativePath = "src/b.cpp"
	second.TestRelativePath = "tests/generated/src/b.cpp_test.cpp"
	second.AcceptedBlockDigest = doc.Blocks[0].Digest
	if err := os.WriteFile(filepath.Join(f.root, "src", "b.cpp"), []byte("int choose(int x) { return x; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f.set.Managed.Records = append(f.set.Managed.Records, second)
	f.set.Managed.Inputs = append(f.set.Managed.Inputs, managedtest.ReconcileInput{Generated: managedtest.ManagedFile{Path: second.TestRelativePath, Content: block}})
	f.set.CaseIDs = append(f.set.CaseIDs, caseID[4:])
	cmake := f.set.Files[1]
	f.set.Files = append(f.set.Files[:1], testgenrender.StagedFile{Path: second.TestRelativePath, Content: block, AfterDigest: digest(block)}, cmake)
	newCMake := f.before + "target_sources(unit_tests PRIVATE \"generated/src/a.cpp_test.cpp\")\n" + "target_sources(unit_tests PRIVATE \"generated/src/b.cpp_test.cpp\")\n"
	f.set.Files[2].Content = []byte(newCMake)
	f.set.Files[2].AfterDigest = digest([]byte(newCMake))
	reviews := make([]managedtest.Review, 0, 2)
	for _, input := range f.set.Managed.Inputs {
		current, err := managedtest.ParseDocument(nil, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		input.Current = current
		review, err := managedtest.Reconcile(input)
		if err != nil {
			t.Fatal(err)
		}
		reviews = append(reviews, review)
	}
	sort.Slice(reviews, func(i, j int) bool { return reviews[i].Path < reviews[j].Path })
	encoded, err := json.Marshal(reviews)
	if err != nil {
		t.Fatal(err)
	}
	return ManagedDecision{ReviewID: f.set.Managed.ReviewID, ReviewDigest: digest(append([]byte("managed-review-set-v1\x00"), encoded...)), Resolutions: map[string]managedtest.ConflictChoice{}}
}

func decisionForAllInputs(t *testing.T, f managedFixture) ManagedDecision {
	t.Helper()
	reviews := make([]managedtest.Review, 0, len(f.set.Managed.Inputs))
	for _, input := range f.set.Managed.Inputs {
		data, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(input.Generated.Path)))
		if errors.Is(err, os.ErrNotExist) {
			data = nil
		} else if err != nil {
			t.Fatal(err)
		}
		current, err := managedtest.ParseDocument(data, maxEditBytes, 4096)
		if err != nil {
			t.Fatal(err)
		}
		input.Current = current
		review, err := managedtest.Reconcile(input)
		if err != nil {
			t.Fatal(err)
		}
		reviews = append(reviews, review)
	}
	sort.Slice(reviews, func(i, j int) bool { return reviews[i].Path < reviews[j].Path })
	encoded, err := json.Marshal(reviews)
	if err != nil {
		t.Fatal(err)
	}
	return ManagedDecision{ReviewID: f.set.Managed.ReviewID, ReviewDigest: digest(append([]byte("managed-review-set-v1\x00"), encoded...)), Resolutions: map[string]managedtest.ConflictChoice{}}
}

func TestManagedMixedReadOnlyAndNewFileBindsExistingCMake(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	_ = addSecondManagedFile(t, &f)
	first := f.set.Managed.Records[0]
	first.LastVerifiedAt = first.LastVerifiedAt.Add(time.Second)
	f.set.Managed.Records[0] = first
	f.set.Managed.Inputs[0].Accepted = []managedtest.Record{f.record}
	f.registry.records[f.record.CaseID] = f.record
	path := filepath.Join(f.root, filepath.FromSlash(f.record.TestRelativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, f.block, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "tests", "CMakeLists.txt"), f.set.Files[2].Content, 0600); err != nil {
		t.Fatal(err)
	}
	f.set.Files = f.set.Files[:2]
	plan, err := f.p.PlanManaged(context.Background(), f.set, decisionForAllInputs(t, f))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Edits) != 1 || len(plan.ManagedReadOnly) != 1 {
		t.Fatalf("mixed edits: %+v", plan)
	}
	if err := os.WriteFile(filepath.Join(f.root, "tests", "CMakeLists.txt"), []byte("user changed CMake\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.PublishManaged(context.Background(), plan); !errors.Is(err, ErrConflict) {
		t.Fatalf("CMake drift accepted: %v", err)
	}
}

func TestManagedJournalRejectsReadOnlyPathsWithoutBoundAcceptance(t *testing.T) {
	d := strings.Repeat("a", 64)
	j := journalRecord{Version: 1, ConfirmationDigest: d, Receipt: Receipt{ConfirmationDigest: d, ManagedReadOnly: []PlannedEdit{{Path: "tests/generated/orphan_test.cpp", BeforeDigest: d, AfterDigest: d}}}}
	if validJournal(j) {
		t.Fatal("unbound read-only journal accepted")
	}
}

func TestManagedPlanRejectsUnreviewedExtraRecord(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	extra := f.record
	extra.ScenarioID = "unreviewed"
	var err error
	extra.CaseID, err = managedtest.StableCaseID(extra.ProjectID, extra.SourceRelativePath, extra.FunctionID, extra.ScenarioID)
	if err != nil {
		t.Fatal(err)
	}
	f.set.Managed.Records = append(f.set.Managed.Records, extra)
	if _, err := f.p.PlanManaged(context.Background(), f.set, f.decision(t)); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("extra record accepted: %v", err)
	}
}

func TestManagedPlanRejectsWrongSourceFileIdentityBeforeReceipt(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	f.set.Managed.Records[0].SourceFileID = strings.Repeat("f", 32)
	if _, err := f.p.PlanManaged(context.Background(), f.set, f.decision(t)); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("invalid source file binding accepted: %v", err)
	}
}

func TestManagedNoCMakeEditRejectsCommentOnlySourceReference(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	f.seedAccepted(t, f.block)
	f.set.Files[0].Content = bytes.Replace(f.block, []byte("CHECK_TRUE(1)"), []byte("CHECK_TRUE(2)"), 1)
	f.set.Files[0].AfterDigest = digest(f.set.Files[0].Content)
	f.set.Managed.Inputs[0].Generated.Content = f.set.Files[0].Content
	doc, err := managedtest.ParseDocument(f.set.Files[0].Content, maxEditBytes, 1)
	if err != nil {
		t.Fatal(err)
	}
	f.set.Managed.Records[0].AcceptedBlockDigest = doc.Blocks[0].Digest
	if err := os.WriteFile(filepath.Join(f.root, "tests", "CMakeLists.txt"), []byte(f.before+"# \"generated/src/a.cpp_test.cpp\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.PlanManaged(context.Background(), f.set, f.decision(t)); !errors.Is(err, ErrConflict) {
		t.Fatalf("comment-only CMake ref accepted: %v", err)
	}
}

func TestManagedNoCMakeEditRejectsCommentedOrQuotedCommand(t *testing.T) {
	for _, fake := range []string{
		"# target_sources(unit_tests PRIVATE \"generated/src/a.cpp_test.cpp\")\n",
		"set(note \"target_sources(unit_tests PRIVATE \\\"generated/src/a.cpp_test.cpp\\\")\")\n",
		"if(FALSE)\ntarget_sources(unit_tests PRIVATE \"generated/src/a.cpp_test.cpp\")\nendif()\n",
	} {
		t.Run(fake, func(t *testing.T) {
			f := newManagedFixture(t)
			defer f.close(t)
			f.seedAccepted(t, f.block)
			if err := os.WriteFile(filepath.Join(f.root, "tests", "CMakeLists.txt"), []byte(f.before+fake), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := f.p.PlanManaged(context.Background(), f.set, f.decision(t)); !errors.Is(err, ErrConflict) {
				t.Fatalf("fake CMake linkage accepted: %v", err)
			}
		})
	}
}

func TestManagedConvertToManualRequiresCurrentMarker(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	current := []byte("// user removed the managed marker and body\n")
	f.seedAccepted(t, current)
	decision := f.decision(t)
	decision.Resolutions[f.record.CaseID] = managedtest.ConvertToManual
	decision.Resolutions["scaffold:"+f.record.TestRelativePath] = managedtest.KeepCurrent
	if _, err := f.p.PlanManaged(context.Background(), f.set, decision); !errors.Is(err, ErrConflict) {
		t.Fatalf("orphan conversion accepted: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(f.record.TestRelativePath)))
	if err != nil || !bytes.Equal(got, current) {
		t.Fatalf("orphan conversion changed current bytes: %q %v", got, err)
	}
}

func TestManagedReadOnlyIdentityBindsCharacterizationAndTargets(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	f.seedAccepted(t, f.block)
	first, err := f.p.PlanManaged(context.Background(), f.set, f.decision(t))
	if err != nil {
		t.Fatal(err)
	}
	f.set.CharacterizationIDs = []string{f.set.CaseIDs[0]}
	second, err := f.p.PlanManaged(context.Background(), f.set, f.decision(t))
	if err != nil {
		t.Fatal(err)
	}
	if first.CandidateSetDigest == second.CandidateSetDigest || first.ConfirmationDigest == second.ConfirmationDigest || second.CharacterizationDigest == "" {
		t.Fatalf("characterization replay: first=%+v second=%+v", first, second)
	}
	f.set.TestTarget = "bad target"
	if _, err := f.p.PlanManaged(context.Background(), f.set, f.decision(t)); err == nil {
		t.Fatalf("invalid zero-write target accepted: %v", err)
	}
}

func TestManagedMultipleFilesAndCMakePublishAtomically(t *testing.T) {
	for _, fault := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rollback"}[fault], func(t *testing.T) {
			f := newManagedFixture(t)
			defer f.close(t)
			decision := addSecondManagedFile(t, &f)
			plan, err := f.p.PlanManaged(context.Background(), f.set, decision)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Edits) != 3 {
				t.Fatalf("missing edits: %+v", plan.Edits)
			}
			if fault {
				f.p.hooks.fail = func(stage string) error {
					if stage == "commit-last" {
						return errors.New("injected")
					}
					return nil
				}
			}
			_, err = f.p.PublishManaged(context.Background(), plan)
			if fault && err == nil || !fault && err != nil {
				t.Fatalf("publish: %v", err)
			}
			for _, record := range f.set.Managed.Records {
				_, fileErr := os.Stat(filepath.Join(f.root, filepath.FromSlash(record.TestRelativePath)))
				_, recordErr := f.registry.Get(context.Background(), record.CaseID)
				if fault && (!errors.Is(fileErr, os.ErrNotExist) || !errors.Is(recordErr, task.ErrNotFound)) {
					t.Fatalf("partial rollback: %v %v", fileErr, recordErr)
				}
				if !fault && (fileErr != nil || recordErr != nil) {
					t.Fatalf("missing publication: %v %v", fileErr, recordErr)
				}
			}
		})
	}
}

func TestManagedPlanBindsFreshReviewAndSource(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	decision := f.decision(t)
	plan, err := f.p.PlanManaged(context.Background(), f.set, decision)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ManagedReviewDigest != decision.ReviewDigest || len(plan.Edits) != 2 {
		t.Fatalf("unbound plan: %+v", plan)
	}
	bad := decision
	bad.ReviewDigest = strings.Repeat("0", 64)
	if _, err := f.p.PlanManaged(context.Background(), f.set, bad); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale review accepted: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "src", "a.cpp"), []byte("user edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.PlanManaged(context.Background(), f.set, decision); !errors.Is(err, ErrConflict) {
		t.Fatalf("source drift accepted: %v", err)
	}
}

func TestManagedPublishCommitsReceiptAndRegistryAndIdempotentRetry(t *testing.T) {
	f := newManagedFixture(t)
	defer f.close(t)
	plan, err := f.p.PlanManaged(context.Background(), f.set, f.decision(t))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := f.p.PublishManaged(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ManagedReviewDigest != plan.ManagedReviewDigest {
		t.Fatalf("unbound receipt: %+v", receipt)
	}
	byRun, found, err := f.p.ManagedRunReceipt(context.Background(), plan.RunID)
	if err != nil || !found || byRun.ConfirmationDigest != receipt.ConfirmationDigest {
		t.Fatalf("cancel recovery could not find committed run receipt: %+v found=%t err=%v", byRun, found, err)
	}
	got, err := f.registry.Get(context.Background(), f.record.CaseID)
	if err != nil || got.AcceptedBlockDigest != f.record.AcceptedBlockDigest {
		t.Fatalf("registry not committed: %+v %v", got, err)
	}
	first, err := os.Stat(filepath.Join(f.root, filepath.FromSlash(f.record.TestRelativePath)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.PublishManaged(context.Background(), plan); err != nil {
		t.Fatalf("retry: %v", err)
	}
	second, err := os.Stat(filepath.Join(f.root, filepath.FromSlash(f.record.TestRelativePath)))
	if err != nil || !os.SameFile(first, second) {
		t.Fatalf("retry rewrote test: %v", err)
	}
}
