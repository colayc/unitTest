package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unit-test-ide.local/test-service/internal/managedtest"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenpublish"
	"unit-test-ide.local/test-service/internal/testgenvalidate"
)

// Evidence is canonically sorted by case ID, while managed source is grouped
// by function. Recovery must bind identities without inventing a common order.
func TestProductionGenerationManagedEvidenceCaseOrder(t *testing.T) {
	_, _, _, source, target, pipeline := productionManagedFileFixture(t)
	cases, err := productionManagedCases(target, pipeline, source)
	if err != nil || len(cases) < 2 {
		t.Fatalf("real multi-function managed evidence unavailable: %v", err)
	}
	document, err := managedtest.ParseDocument(source, int64(len(source)), 200)
	if err != nil {
		t.Fatal(err)
	}
	evidenceIDs, sourceIDs := []string{}, []string{}
	for _, item := range cases {
		evidenceIDs = append(evidenceIDs, item.CaseID)
	}
	for _, block := range document.Blocks {
		sourceIDs = append(sourceIDs, block.CaseID)
	}
	if reflect.DeepEqual(evidenceIDs, sourceIDs) {
		t.Fatal("fixture did not exercise different valid case orders")
	}
	set := testgenpublish.CandidateSet{Files: pipeline.editSet.Files}
	if !validProductionManagedCases(cases, set, source) {
		t.Fatal("valid multi-function evidence rejected by source order")
	}
	for _, mutation := range []string{"duplicate", "missing", "function", "scenario", "unsorted"} {
		t.Run(mutation, func(t *testing.T) {
			changed := append([]productionManagedCaseEvidence(nil), cases...)
			switch mutation {
			case "duplicate":
				changed[1] = changed[0]
			case "missing":
				changed = changed[:len(changed)-1]
			case "function":
				changed[0].FunctionID = strings.Repeat("f", 32)
			case "scenario":
				changed[0].ScenarioID = "changed"
			case "unsorted":
				changed[0], changed[1] = changed[1], changed[0]
			}
			if validProductionManagedCases(changed, set, source) {
				t.Fatal("invalid managed identity or canonical evidence accepted")
			}
		})
	}
}

func stageCheckpointCall(ctx context.Context, h *stageCheckpointHarness, r testgendomain.Run, next testgendomain.State) (GenerationStageResult, error) {
	h.driver.resolver = &stageCheckpointResolver{target: h.target}
	return h.driver.RunStage(ctx, r, next, nil)
}
func stageCheckpointInput(t *testing.T, h *stageCheckpointHarness, next testgendomain.State) testgendomain.Run {
	t.Helper()
	r := h.validate(t, h.rendering(t))
	if next == testgendomain.StateAwaitingConfirmation {
		result, err := stageCheckpointCall(context.Background(), h, r, testgendomain.StateMinimizing)
		if err != nil || result.Next != testgendomain.StateMinimizing ||
			result.MinimizedCaseIDs != nil || result.PreviewSet != nil {
			t.Fatalf("minimization skipped durable state: %+v %v", result, err)
		}
		r = h.checkpoint(t, r, result, testgendomain.StateMinimizing)
	}
	return r
}
func stageCheckpointArtifactFile(t *testing.T, h *stageCheckpointHarness, r testgendomain.Run, kind string) string {
	t.Helper()
	root := filepath.Join(h.data, "artifacts")
	for _, ref := range r.ArtifactDigests {
		artifact, err := h.meta.GetArtifact(context.Background(), ref.ID)
		if err != nil {
			t.Fatal(err)
		}
		if artifact.Kind != kind {
			continue
		}
		file := filepath.Join(root, filepath.FromSlash(artifact.RelativePath))
		relative, err := filepath.Rel(root, file)
		if err != nil || filepath.IsAbs(relative) || relative == ".." ||
			strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			t.Fatal("test mutation escaped owned artifacts")
		}
		info, err := os.Lstat(file)
		if err != nil || !info.Mode().IsRegular() {
			t.Fatal("owned regular artifact absent")
		}
		return file
	}
	t.Fatal("registered artifact kind absent")
	return ""
}
func TestProductionGenerationCheckpointEvidenceGate(t *testing.T) {
	// An in-memory run cannot manufacture ownership of an empty checkpoint.
	for _, mutation := range []string{"wrong-task", "uncommitted"} {
		t.Run("zero-persisted-ownership/"+mutation, func(t *testing.T) {
			h := newStageCheckpointHarness(t)
			h.authority.planner.fail = testgenvalidate.StageCompile
			r := h.rendering(t)
			if mutation == "wrong-task" {
				r = h.validate(t, r)
				r.TaskID = strings.Repeat("f", 32)
			} else {
				r.State = testgendomain.StateValidating
				r.Record.BudgetUsed.Candidates = r.Request.Budgets.CandidateCount
			}
			got, err := stageCheckpointCall(context.Background(), h, r, testgendomain.StateMinimizing)
			if err == nil || !reflect.DeepEqual(got, GenerationStageResult{}) {
				t.Fatal("unowned zero checkpoint became normal rejection")
			}
		})
	}
	mutations := map[string]func(*stageCheckpointHarness, *testgendomain.Run){
		"count":                  func(_ *stageCheckpointHarness, r *testgendomain.Run) { r.CandidateCount = 0 },
		"unowned":                func(_ *stageCheckpointHarness, r *testgendomain.Run) { r.ArtifactDigests = nil },
		"wrong-task":             func(_ *stageCheckpointHarness, r *testgendomain.Run) { r.TaskID = strings.Repeat("f", 32) },
		"wrong-run":              func(_ *stageCheckpointHarness, r *testgendomain.Run) { r.ID = strings.Repeat("f", 32) },
		"target":                 func(h *stageCheckpointHarness, _ *testgendomain.Run) { h.target.sourceRelativePath = "changed.c" },
		"wrong-state":            func(_ *stageCheckpointHarness, r *testgendomain.Run) { r.State = testgendomain.StateRendering },
		"uncommitted-validation": func(_ *stageCheckpointHarness, r *testgendomain.Run) { r.Record.BudgetUsed.Candidates = 0 },
		"stale-receipt": func(h *stageCheckpointHarness, _ *testgendomain.Run) {
			h.authority.mu.Lock()
			h.authority.binding.SourceDigest = strings.Repeat("f", 64)
			h.authority.mu.Unlock()
		},
	}
	for _, next := range []testgendomain.State{testgendomain.StateMinimizing, testgendomain.StateAwaitingConfirmation} {
		for _, cold := range []bool{false, true} {
			prefix := string(next) + "/" + map[bool]string{false: "warm", true: "cold"}[cold]
			for name, mutate := range mutations {
				t.Run(prefix+"/"+name, func(t *testing.T) {
					h := newStageCheckpointHarness(t)
					r := stageCheckpointInput(t, h, next)
					if cold {
						h.reopen(t)
					}
					mutate(h, &r)
					got, err := stageCheckpointCall(context.Background(), h, r, next)
					if err == nil || !reflect.DeepEqual(got, GenerationStageResult{}) {
						t.Fatal("invalid checkpoint accepted")
					}
				})
			}
			for _, kind := range []string{"test-generation-source", "test-generation-evidence"} {
				for _, missing := range []bool{false, true} {
					name := prefix + "/" + kind + "/" + map[bool]string{false: "corrupt", true: "missing"}[missing]
					t.Run(name, func(t *testing.T) {
						h := newStageCheckpointHarness(t)
						r := stageCheckpointInput(t, h, next)
						if cold {
							h.reopen(t)
						}
						file := stageCheckpointArtifactFile(t, h, r, kind)
						var err error
						if missing {
							err = os.Remove(file)
						} else {
							err = os.WriteFile(file, []byte("changed"), 0600)
						}
						if err != nil {
							t.Fatal(err)
						}
						got, err := stageCheckpointCall(context.Background(), h, r, next)
						if err == nil || !reflect.DeepEqual(got, GenerationStageResult{}) {
							t.Fatal("missing or changed positive evidence accepted")
						}
					})
				}
			}
		}
	}
	t.Run("zero-with-contradictory-artifact", func(t *testing.T) {
		h := newStageCheckpointHarness(t)
		h.authority.planner.fail = testgenvalidate.StageCompile
		r := h.validate(t, h.rendering(t))
		if r.CandidateCount != 0 || len(r.ArtifactDigests) != 0 {
			t.Fatal("not a clean zero checkpoint")
		}
		r.ArtifactDigests = []testgendomain.ArtifactRef{{ID: strings.Repeat("f", 32), Digest: strings.Repeat("f", 64)}}
		got, err := stageCheckpointCall(context.Background(), h, r, testgendomain.StateMinimizing)
		if err == nil || !reflect.DeepEqual(got, GenerationStageResult{}) {
			t.Fatal("contradiction became normal rejection")
		}
	})
}
func TestProductionGenerationCheckpointContextGate(t *testing.T) {
	for _, expired := range []bool{false, true} {
		h := newStageCheckpointHarness(t)
		r := h.validate(t, h.rendering(t))
		var ctx context.Context
		var cancel context.CancelFunc
		want := context.Canceled
		if expired {
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			want = context.DeadlineExceeded
		} else {
			ctx, cancel = context.WithCancel(context.Background())
			cancel()
		}
		got, err := stageCheckpointCall(ctx, h, r, testgendomain.StateMinimizing)
		cancel()
		if !errors.Is(err, want) || got.Next != "" {
			t.Fatal("context identity lost")
		}
		r.State = testgendomain.StateMinimizing
		got, err = stageCheckpointCall(ctx, h, r, testgendomain.StateAwaitingConfirmation)
		if !errors.Is(err, want) || got.Next != "" || got.PreviewSet != nil {
			t.Fatal("cancelled preview accepted")
		}
	}
}
func TestProductionGenerationCheckpointConcurrentCAS(t *testing.T) {
	h := newStageCheckpointHarness(t)
	r := h.validate(t, h.rendering(t))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			result, err := h.driver.RunStage(ctx, r, testgendomain.StateMinimizing, nil)
			ready <- struct{}{}
			<-release
			if err == nil {
				record := r.Record
				record.BudgetUsed.Events++
				_, err = h.service.coord.CheckpointWithRecord(ctx, r.ID, r.Revision, result.Next,
					result.Candidates, result.Artifacts, record)
			}
			results <- err
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-ready:
		case <-ctx.Done():
			close(release)
			t.Fatal("bounded worker rendezvous failed")
		}
	}
	close(release)
	successes, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			if err == nil {
				successes++
			} else if errors.Is(err, task.ErrConflict) {
				conflicts++
			} else {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("bounded CAS workers did not finish")
		}
	}
	after, err := h.meta.GetGeneration(ctx, r.ID)
	if err != nil || after.State != testgendomain.StateMinimizing ||
		after.Revision != r.Revision+1 || successes != 1 || conflicts != 1 ||
		len(after.Record.MinimizedCaseIDs) != 0 || h.publisher.plans != 0 ||
		len(h.authority.planner.stages) != 6 {
		t.Fatal("duplicate progress or process execution")
	}
}

type stageCancelAfterRead struct {
	runtimeProductionGenerationArtifacts
	cancel context.CancelFunc
}

func (s stageCancelAfterRead) ListGenerationCandidates(ctx context.Context, id string) ([]testgendomain.Candidate, error) {
	result, err := s.metadata.ListGenerationCandidates(ctx, id)
	s.cancel()
	return result, err
}
func TestProductionGenerationCheckpointCancelDuringRead(t *testing.T) {
	h := newStageCheckpointHarness(t)
	r := h.validate(t, h.rendering(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.adapter.artifacts = stageCancelAfterRead{
		runtimeProductionGenerationArtifacts: runtimeProductionGenerationArtifacts{metadata: h.meta, content: h.content},
		cancel:                               cancel,
	}
	value, err := h.driver.RunStage(ctx, r, testgendomain.StateMinimizing, nil)
	if !errors.Is(err, context.Canceled) || value.Next != "" {
		t.Fatal("read cancellation became success")
	}
	after, getErr := h.meta.GetGeneration(context.Background(), r.ID)
	if getErr != nil || after.Revision != r.Revision || h.publisher.plans != 0 {
		t.Fatal("cancelled read changed checkpoint")
	}
}
