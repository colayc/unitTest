package runtime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/artifactstore"
	generationv15 "unit-test-ide.local/test-service/internal/protocolmodel/v1_5/testgeneration"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenvalidate"
)

type stageCheckpointResolver struct {
	mu     sync.Mutex
	target generationTarget
	calls  int
}

func (r *stageCheckpointResolver) Targets(context.Context, generationv15.TestGenerationTargetListRequestV15) (generationv15.TestGenerationTargetListV15, error) {
	return generationv15.TestGenerationTargetListV15{}, nil
}
func (r *stageCheckpointResolver) ResolveStart(ctx context.Context, _ generationv15.TestGenerationStartRequestV15) (generationTarget, error) {
	return r.ResolveRequest(ctx, r.target.request)
}
func (r *stageCheckpointResolver) ResolveRequest(ctx context.Context, request testgendomain.Request) (generationTarget, error) {
	if err := ctx.Err(); err != nil {
		return generationTarget{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if !reflect.DeepEqual(request, r.target.request) {
		return generationTarget{}, testgendomain.ErrStaleSnapshot
	}
	return r.target, nil
}

type stageCheckpointAuthority struct {
	mu sync.Mutex
	*productionValidationAuthorityFixture
}

func (a *stageCheckpointAuthority) Prepare(ctx context.Context, b productionValidationBinding) (productionValidationInput, error) {
	if err := ctx.Err(); err != nil {
		return productionValidationInput{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.productionValidationAuthorityFixture.Prepare(ctx, b)
}
func (a *stageCheckpointAuthority) Verify(ctx context.Context, b productionValidationBinding, r []testgenvalidate.StageReceipt) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.productionValidationAuthorityFixture.Verify(ctx, b, r)
}

type stageCheckpointHarness struct {
	data      string
	meta      *taskstore.Store
	content   *artifactstore.Store
	adapter   *productionGenerationValidation
	driver    *productionGenerationDriver
	service   *generationService
	target    generationTarget
	pipeline  productionPipelineResult
	authority *stageCheckpointAuthority
	publisher *generationPublisherFixture
}

func newStageCheckpointHarness(t *testing.T) *stageCheckpointHarness {
	t.Helper()
	a, _, target, pipeline, authority, _ := productionValidationFixture(t)
	target.request.ManagedGapID = ""
	target.request.SessionOwnerDigest = strings.Repeat("a", 64)
	target.request.Budgets.WallTimeMS = 60000
	target.wallTime = time.Minute
	h := &stageCheckpointHarness{data: t.TempDir(), adapter: a, target: target,
		pipeline: pipeline, authority: &stageCheckpointAuthority{productionValidationAuthorityFixture: authority},
		publisher: &generationPublisherFixture{complete: true}}
	var err error
	h.meta, err = taskstore.Open(filepath.Join(h.data, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	h.content, err = artifactstore.New(filepath.Join(h.data, "artifacts"))
	if err != nil {
		_ = h.meta.Close()
		t.Fatal(err)
	}
	h.adapter.authority = h.authority
	h.adapter.artifacts = runtimeProductionGenerationArtifacts{metadata: h.meta, content: h.content}
	h.connect(t)
	t.Cleanup(func() {
		if h.service != nil {
			h.service.Close()
		}
		if h.content != nil {
			_ = h.content.Close()
		}
		if h.meta != nil {
			_ = h.meta.Close()
		}
	})
	return h
}
func (h *stageCheckpointHarness) connect(t *testing.T) {
	t.Helper()
	h.driver = newProductionGenerationDriver(&stageCheckpointResolver{target: h.target}, nil, h.adapter)
	var err error
	h.service, err = newGenerationService(GenerationServiceConfig{
		Store: h.meta, Driver: h.driver, Publisher: h.publisher, Trusted: true, CoverageReady: true,
		VerifySnapshot: func(ctx context.Context, r testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
			if err := ctx.Err(); err != nil {
				return testgendomain.SnapshotIdentity{}, err
			}
			if !reflect.DeepEqual(r, h.target.request) {
				return testgendomain.SnapshotIdentity{}, testgendomain.ErrStaleSnapshot
			}
			return r.SnapshotIdentity(), nil
		},
		VerifyArtifact: func(ctx context.Context, a task.Artifact) error {
			switch a.Kind {
			case "test-generation-source":
				return h.content.VerifyGenerationSource(ctx, a)
			case "test-generation-evidence":
				return h.content.VerifyGenerationEvidence(ctx, a)
			default:
				return task.ErrInvalidArgument
			}
		},
		VerifyProcess: func(ctx context.Context, _ string, _ string) error { return ctx.Err() },
	})
	if err != nil {
		t.Fatal(err)
	}
}
func (h *stageCheckpointHarness) checkpoint(t *testing.T, r testgendomain.Run, result GenerationStageResult, stage testgendomain.State) testgendomain.Run {
	t.Helper()
	amount := h.driver.StageBudget(r, stage)
	amount.Events = 1
	if stage == testgendomain.StateBaseline {
		amount.Events = 2
	}
	record := r.Record
	record.BudgetUsed.Candidates += amount.Candidates
	record.BudgetUsed.OutputBytes += amount.OutputBytes
	record.BudgetUsed.Artifacts += amount.Artifacts
	record.BudgetUsed.Events += amount.Events
	if result.MinimizedCaseIDs != nil {
		record.MinimizedCaseIDs = append([]string(nil), result.MinimizedCaseIDs...)
	}
	saved, err := h.service.coord.CheckpointWithRecord(context.Background(), r.ID, r.Revision, result.Next, result.Candidates, result.Artifacts, record)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}
func (h *stageCheckpointHarness) rendering(t *testing.T) testgendomain.Run {
	t.Helper()
	r, err := h.service.coord.Start(context.Background(), h.target.request)
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []testgendomain.State{
		testgendomain.StateBaseline, testgendomain.StateAnalyzing,
		testgendomain.StateSolving, testgendomain.StateRendering,
	} {
		r = h.checkpoint(t, r, GenerationStageResult{Next: stage}, stage)
	}
	h.driver.cache[r.ID] = cachedProductionPipeline{request: r.Request, result: h.pipeline}
	return r
}
func (h *stageCheckpointHarness) validate(t *testing.T, r testgendomain.Run) testgendomain.Run {
	t.Helper()
	result, err := h.driver.RunStage(context.Background(), r, testgendomain.StateValidating, func(ctx context.Context) error {
		return h.service.coord.AuthorizeProcessLaunch(ctx, r.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Next != testgendomain.StateValidating {
		t.Fatalf("validation skipped checkpoint: %s", result.Next)
	}
	return h.checkpoint(t, r, result, testgendomain.StateValidating)
}
func (h *stageCheckpointHarness) reopen(t *testing.T) {
	t.Helper()
	h.service.Close()
	if err := h.content.Close(); err != nil {
		t.Fatal(err)
	}
	if err := h.meta.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	h.meta, err = taskstore.Open(filepath.Join(h.data, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	h.content, err = artifactstore.New(filepath.Join(h.data, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	h.adapter = newProductionGenerationValidation(h.adapter.validator, h.authority,
		runtimeProductionGenerationArtifacts{metadata: h.meta, content: h.content})
	h.connect(t)
	if len(h.driver.cache) != 0 || len(h.adapter.records) != 0 || h.driver.pipeline != nil {
		t.Fatal("restart reused pipeline or adapter state")
	}
}
func statePath(t *testing.T, s *taskstore.Store, id string) []testgendomain.State {
	t.Helper()
	events, err := s.ReplayGenerationEvents(context.Background(), id, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	var path []testgendomain.State
	for _, e := range events {
		var value map[string]string
		if json.Unmarshal(e.Payload, &value) == nil && value["from"] != "" {
			from, to := testgendomain.State(value["from"]), testgendomain.State(value["to"])
			if !testgendomain.ValidTransition(from, to) {
				t.Fatal("invalid durable transition")
			}
			path = append(path, to)
		}
	}
	return path
}
func TestProductionGenerationValidatingCheckpoint(t *testing.T) {
	for _, retain := range []bool{true, false} {
		t.Run(map[bool]string{true: "retained", false: "not-retained"}[retain], func(t *testing.T) {
			h := newStageCheckpointHarness(t)
			if !retain {
				h.authority.planner.fail = testgenvalidate.StageCompile
			}
			r := h.validate(t, h.rendering(t))
			want := 0
			if retain {
				want = 1
			}
			if r.State != testgendomain.StateValidating || r.CandidateCount != want ||
				len(r.ArtifactDigests) != 2*want || r.Record.Preview != nil ||
				len(r.Record.MinimizedCaseIDs) != 0 {
				t.Fatal("validating checkpoint differs")
			}
			candidates, err := h.meta.ListGenerationCandidates(context.Background(), r.ID)
			if err != nil || len(candidates) != want {
				t.Fatal("candidate not durably bound")
			}
			row, err := h.meta.Get(context.Background(), r.TaskID)
			if err != nil || row.ErrorCode != "" {
				t.Fatal("validation became infrastructure failure")
			}
		})
	}
}
