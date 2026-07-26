package task_test

import (
	"context"
	"reflect"
	"regexp"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

func TestApplyTransitionTable(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		from    task.Status
		to      task.Status
		outcome task.Outcome
		wantErr bool
	}{
		{"queued starts", task.StatusQueued, task.StatusRunning, "", false},
		{"queued cancels", task.StatusQueued, task.StatusFinished, task.OutcomeCancelled, false},
		{"running cancels", task.StatusRunning, task.StatusCancelling, "", false},
		{"running succeeds", task.StatusRunning, task.StatusFinished, task.OutcomeSucceeded, false},
		{"running times out", task.StatusRunning, task.StatusFinished, task.OutcomeTimedOut, false},
		{"cancelling finishes", task.StatusCancelling, task.StatusFinished, task.OutcomeCancelled, false},
		{"finished is immutable", task.StatusFinished, task.StatusRunning, "", true},
		{"nonterminal has no outcome", task.StatusQueued, task.StatusRunning, task.OutcomeSucceeded, true},
		{"finished requires outcome", task.StatusRunning, task.StatusFinished, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := task.Task{ID: "0123456789abcdef0123456789abcdef", Status: tt.from, CreatedAt: now}
			_, err := task.ApplyTransition(current, task.Transition{From: tt.from, To: tt.to, Outcome: tt.outcome, At: now.Add(time.Second)})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestApplyTransitionRejectsEveryOtherStatusEdge(t *testing.T) {
	statuses := []task.Status{task.StatusQueued, task.StatusRunning, task.StatusCancelling, task.StatusFinished}
	allowed := map[task.Status]map[task.Status]bool{
		task.StatusQueued:     {task.StatusRunning: true, task.StatusFinished: true},
		task.StatusRunning:    {task.StatusCancelling: true, task.StatusFinished: true},
		task.StatusCancelling: {task.StatusFinished: true},
		task.StatusFinished:   {},
	}
	now := time.Date(2026, 7, 22, 1, 0, 0, 0, time.UTC)
	for _, from := range statuses {
		for _, to := range statuses {
			if allowed[from][to] {
				continue
			}
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				current := task.Task{ID: "0123456789abcdef0123456789abcdef", Status: from, CreatedAt: now}
				_, err := task.ApplyTransition(current, task.Transition{From: from, To: to, At: now.Add(time.Second)})
				if err == nil {
					t.Fatal("expected invalid transition error")
				}
			})
		}
	}
}

func TestApplyTransitionRejectsStateConflictWithoutMutatingCurrent(t *testing.T) {
	now := time.Date(2026, 7, 22, 2, 0, 0, 0, time.UTC)
	current := task.Task{
		ID:           "0123456789abcdef0123456789abcdef",
		Status:       task.StatusQueued,
		CreatedAt:    now,
		ErrorCode:    "existing_code",
		ErrorMessage: "existing message",
	}

	got, err := task.ApplyTransition(current, task.Transition{
		From:         task.StatusRunning,
		To:           task.StatusFinished,
		Outcome:      task.OutcomeSucceeded,
		At:           now.Add(time.Second),
		ErrorCode:    "replacement_code",
		ErrorMessage: "replacement message",
	})
	if err == nil {
		t.Fatal("expected state conflict")
	}
	if !reflect.DeepEqual(got, task.Task{}) {
		t.Fatalf("failed transition returned task %#v, want zero value", got)
	}
	if current.Status != task.StatusQueued || current.ErrorCode != "existing_code" || current.ErrorMessage != "existing message" {
		t.Fatalf("current task was mutated: %#v", current)
	}
}

func TestApplyTransitionSetsStartedAtAndPreservesCreatedAt(t *testing.T) {
	created := time.Date(2026, 7, 22, 3, 0, 0, 0, time.UTC)
	started := created.Add(5 * time.Second)
	current := task.Task{ID: "0123456789abcdef0123456789abcdef", Status: task.StatusQueued, CreatedAt: created}

	got, err := task.ApplyTransition(current, task.Transition{
		From: task.StatusQueued,
		To:   task.StatusRunning,
		At:   started,
	})
	if err != nil {
		t.Fatalf("ApplyTransition() error = %v", err)
	}
	if got.StartedAt == nil || !got.StartedAt.Equal(started) {
		t.Fatalf("StartedAt = %v, want %v", got.StartedAt, started)
	}
	if got.FinishedAt != nil {
		t.Fatalf("FinishedAt = %v, want nil", got.FinishedAt)
	}
	if !got.CreatedAt.Equal(created) {
		t.Fatalf("CreatedAt = %v, want %v", got.CreatedAt, created)
	}
}

func TestApplyTransitionSetsFinishedAtOutcomeAndErrors(t *testing.T) {
	started := time.Date(2026, 7, 22, 4, 0, 0, 0, time.UTC)
	finished := started.Add(10 * time.Second)
	current := task.Task{
		ID:        "0123456789abcdef0123456789abcdef",
		Status:    task.StatusRunning,
		CreatedAt: started.Add(-time.Second),
		StartedAt: &started,
	}

	got, err := task.ApplyTransition(current, task.Transition{
		From:         task.StatusRunning,
		To:           task.StatusFinished,
		Outcome:      task.OutcomeCommandFailed,
		At:           finished,
		ErrorCode:    "exit_nonzero",
		ErrorMessage: "simulated command exited with status 7",
	})
	if err != nil {
		t.Fatalf("ApplyTransition() error = %v", err)
	}
	if got.FinishedAt == nil || !got.FinishedAt.Equal(finished) {
		t.Fatalf("FinishedAt = %v, want %v", got.FinishedAt, finished)
	}
	if got.StartedAt != current.StartedAt || got.Outcome != task.OutcomeCommandFailed {
		t.Fatalf("transition result = %#v", got)
	}
	if got.ErrorCode != "exit_nonzero" || got.ErrorMessage != "simulated command exited with status 7" {
		t.Fatalf("errors = (%q, %q)", got.ErrorCode, got.ErrorMessage)
	}
}

func TestApplyTransitionCopiesTransitionTime(t *testing.T) {
	at := time.Date(2026, 7, 22, 5, 0, 0, 0, time.UTC)
	got, err := task.ApplyTransition(
		task.Task{Status: task.StatusQueued},
		task.Transition{From: task.StatusQueued, To: task.StatusRunning, At: at},
	)
	if err != nil {
		t.Fatalf("ApplyTransition() error = %v", err)
	}
	if got.StartedAt == nil {
		t.Fatal("StartedAt is nil")
	}
	*got.StartedAt = got.StartedAt.Add(time.Hour)
	if !at.Equal(time.Date(2026, 7, 22, 5, 0, 0, 0, time.UTC)) {
		t.Fatalf("transition time was aliased: %v", at)
	}
}

func TestOutcomesAreExactAndDoNotContainTestFailed(t *testing.T) {
	outcomes := []task.Outcome{
		task.OutcomeSucceeded,
		task.OutcomeCommandFailed,
		task.OutcomeCancelled,
		task.OutcomeTimedOut,
		task.OutcomeInterrupted,
		task.OutcomeInfrastructureFailed,
	}
	want := []string{"succeeded", "command_failed", "cancelled", "timed_out", "interrupted", "infrastructure_failed"}
	if task.OutcomeCommandFailed == task.OutcomeInfrastructureFailed {
		t.Fatal("command_failed and infrastructure_failed must be distinct")
	}
	for i, outcome := range outcomes {
		if string(outcome) != want[i] {
			t.Fatalf("outcome[%d] = %q, want %q", i, outcome, want[i])
		}
		if string(outcome) == "test_failed" {
			t.Fatal("forbidden outcome test_failed is present")
		}
	}
}

func TestApplyTransitionAcceptsEveryTerminalOutcome(t *testing.T) {
	outcomes := []task.Outcome{
		task.OutcomeSucceeded,
		task.OutcomeCommandFailed,
		task.OutcomeCancelled,
		task.OutcomeTimedOut,
		task.OutcomeInterrupted,
		task.OutcomeInfrastructureFailed,
	}
	for _, outcome := range outcomes {
		t.Run(string(outcome), func(t *testing.T) {
			_, err := task.ApplyTransition(
				task.Task{Status: task.StatusRunning},
				task.Transition{From: task.StatusRunning, To: task.StatusFinished, Outcome: outcome},
			)
			if err != nil {
				t.Fatalf("ApplyTransition() error = %v", err)
			}
		})
	}
}

func TestValidScenarioAllowsOnlyBuiltInScenarios(t *testing.T) {
	valid := []task.Scenario{
		task.ScenarioSuccess,
		task.ScenarioExitNonzero,
		task.ScenarioHang,
		task.ScenarioSpawnChild,
		task.ScenarioEmitOutput,
	}
	for _, scenario := range valid {
		if !task.ValidScenario(scenario) {
			t.Errorf("ValidScenario(%q) = false", scenario)
		}
	}
	invalid := []task.Scenario{"", "test-failed", "C:/Windows/System32/cmd.exe", "sh -c whoami"}
	for _, scenario := range invalid {
		if task.ValidScenario(scenario) {
			t.Errorf("ValidScenario(%q) = true", scenario)
		}
	}
}

func TestNewIDReturnsUniqueLowercaseHex128BitValues(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9a-f]{32}$`)
	seen := make(map[string]struct{}, 64)
	for range 64 {
		id := task.NewID()
		if !pattern.MatchString(id) {
			t.Fatalf("NewID() = %q, want 32 lowercase hex characters", id)
		}
		if _, exists := seen[id]; exists {
			t.Fatalf("NewID() returned duplicate %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestPortContractsCompile(t *testing.T) {
	var _ task.Store = (*contractStore)(nil)
	var publisher task.Publisher = contractPublisher{}
	publisher.Publish(task.Event{})
}

type contractStore struct{}

func (*contractStore) Create(context.Context, task.Task, []task.StepSnapshot, task.EventDraft) (task.Task, []task.Event, error) {
	return task.Task{}, nil, nil
}
func (*contractStore) FindByIdempotencyKey(context.Context, string) (task.Task, error) {
	return task.Task{}, nil
}
func (*contractStore) Get(context.Context, string) (task.Task, error) { return task.Task{}, nil }
func (*contractStore) List(context.Context, string, int) (task.Page[task.Task], error) {
	return task.Page[task.Task]{}, nil
}
func (*contractStore) Apply(context.Context, task.Mutation) (task.Task, []task.Event, error) {
	return task.Task{}, nil, nil
}
func (*contractStore) AppendEvent(context.Context, string, task.EventDraft) (task.Event, error) {
	return task.Event{}, nil
}
func (*contractStore) UpdateLease(context.Context, task.ProcessLease) error { return nil }
func (*contractStore) Watermark(context.Context) (int64, error)             { return 0, nil }
func (*contractStore) EventsAfter(context.Context, int64, int64, int) ([]task.Event, error) {
	return nil, nil
}
func (*contractStore) ListArtifacts(context.Context, string, string, int) (task.Page[task.Artifact], error) {
	return task.Page[task.Artifact]{}, nil
}
func (*contractStore) GetArtifact(context.Context, string) (task.Artifact, error) {
	return task.Artifact{}, nil
}
func (*contractStore) ActiveLeases(context.Context) ([]task.ProcessLease, error) { return nil, nil }
func (*contractStore) RecoverInterrupted(context.Context, time.Time) ([]task.Event, error) {
	return nil, nil
}
func (*contractStore) ReferencedArtifactPaths(context.Context) (map[string]struct{}, error) {
	return nil, nil
}
func (*contractStore) Close() error { return nil }

type contractPublisher struct{}

func (contractPublisher) Publish(task.Event) {}
