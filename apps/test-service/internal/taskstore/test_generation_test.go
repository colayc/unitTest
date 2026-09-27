package taskstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/eventbroker"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

func generationRequestFixture() testgendomain.Request {
	return testgendomain.Request{IdempotencyKey: strings.Repeat("1", 32), WorkspaceGeneration: strings.Repeat("a", 64), ProjectID: "project", Scope: testgendomain.ScopeWorkspace, Framework: testgendomain.FrameworkAuto,
		Goals: testgendomain.Goals{FunctionPercent: 90, LinePercent: 80, BranchPercent: 70}, Budgets: testgendomain.Budgets{WallTimeMS: 1000, CandidateCount: 4, MemoryMiB: 256, Concurrency: 1},
		CompileSnapshotDigest: strings.Repeat("b", 64), CoverageSnapshotDigest: strings.Repeat("c", 64),
		SourceDigest: strings.Repeat("1", 64), CMakeTargetDigest: strings.Repeat("2", 64), FrameworkBundleDigest: strings.Repeat("3", 64), AnalyzerBundleDigest: strings.Repeat("4", 64), BaselineReportDigest: strings.Repeat("5", 64), ProcessOwnerDigest: strings.Repeat("d", 64)}
}

func generationRunFixture() testgendomain.Run {
	return testgendomain.Run{ID: strings.Repeat("2", 32), TaskID: strings.Repeat("3", 32), Request: generationRequestFixture(), State: testgendomain.StateQueued, Revision: 1, CreatedAt: time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)}
}

func TestGenerationPersistenceAndRevisionCAS(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	original := generationRunFixture()
	if _, err := s.CreateGeneration(ctx, original); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetGeneration(ctx, original.ID)
	if err != nil || got.ID != original.ID || got.Revision != 1 {
		t.Fatalf("GetGeneration = %+v, %v", got, err)
	}
	storedTask, err := s.Get(ctx, original.TaskID)
	if err != nil || storedTask.Kind != task.KindTestGeneration {
		t.Fatalf("task = %+v, %v", storedTask, err)
	}
	next := got
	next.State = testgendomain.StateBaseline
	next.Revision = 2
	if _, err := s.CheckpointGeneration(ctx, 1, next, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CheckpointGeneration(ctx, 1, next, nil, nil); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("stale writer: %v", err)
	}
	reopened, err := s.GetGeneration(ctx, original.ID)
	if err != nil || reopened.State != next.State || reopened.Revision != 2 {
		t.Fatalf("checkpoint = %+v, %v", reopened, err)
	}
}

func TestGenerationEventsStayOutOfLegacyGlobalReplay(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	r := generationRunFixture()
	if _, err := s.CreateGeneration(ctx, r); err != nil {
		t.Fatal(err)
	}
	watermark, err := s.Watermark(ctx)
	if err != nil || watermark != 0 {
		t.Fatalf("global watermark = %d, %v", watermark, err)
	}
	global, err := s.EventsAfter(ctx, 0, 1, 200)
	if err != nil || len(global) != 0 {
		t.Fatalf("global generation events = %+v, %v", global, err)
	}
	owned, err := s.ReplayGenerationEvents(ctx, r.ID, 0, 200)
	if err != nil || len(owned) != 1 {
		t.Fatalf("owned generation events = %+v, %v", owned, err)
	}
}

func TestGenerationRejectsTaskMismatchAndImpossibleTransition(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	r := generationRunFixture()
	r.TaskID = r.ID
	if _, err := s.CreateGeneration(ctx, r); err == nil {
		t.Fatal("accepted matching task/run ids")
	}
	r = generationRunFixture()
	if _, err := s.CreateGeneration(ctx, r); err != nil {
		t.Fatal(err)
	}
	next := r
	next.State = testgendomain.StateValidating
	next.Revision = 2
	if _, err := s.CheckpointGeneration(ctx, 1, next, nil, nil); err == nil {
		t.Fatal("accepted impossible transition")
	}
	if _, err := s.ReplayGenerationEvents(ctx, r.ID, 0, 201); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("unbounded replay: %v", err)
	}
}

func TestGenerationCannotBypassCoordinatorThroughGenericTaskAPIs(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	r := generationRunFixture()
	input := task.Task{ID: r.TaskID, IdempotencyKey: r.Request.IdempotencyKey, RequestHash: strings.Repeat("d", 64), Kind: task.KindTestGeneration, Request: []byte(`{}`), WorkspaceGeneration: r.Request.WorkspaceGeneration, Timeout: time.Second, Status: task.StatusQueued, CreatedAt: r.CreatedAt}
	_, _, err := s.Create(ctx, input, nil, task.EventDraft{TaskID: r.TaskID, Type: task.EventTaskCreated, At: r.CreatedAt, Payload: []byte(`{}`)})
	if !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("generic create: %v", err)
	}
	created, err := s.CreateGeneration(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	input, err = s.Get(ctx, r.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	input.Status = task.StatusRunning
	_, _, err = s.Apply(ctx, task.Mutation{Task: input, Expected: task.StatusQueued})
	if !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("generic mutation: %v", err)
	}
	_, err = s.AppendEvent(ctx, r.TaskID, task.EventDraft{TaskID: r.TaskID, Type: task.EventTaskDiagnostic, At: r.CreatedAt, Payload: []byte(`{}`)})
	if !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("generic event: %v", err)
	}
	if created.Revision != 1 {
		t.Fatal(created)
	}
}

func TestGenerationRecoveryLeavesCompletedCheckpointIntact(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	r := generationRunFixture()
	created, err := s.CreateGeneration(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	next := created
	next.State = testgendomain.StateBaseline
	next.Revision++
	checkpoint, err := s.CheckpointGeneration(ctx, created.Revision, next, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecoverInterrupted(ctx, r.CreatedAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetGeneration(ctx, r.ID)
	if err != nil || got.State != testgendomain.StateBaseline || got.Revision != checkpoint.Revision || got.LastSequence != checkpoint.LastSequence {
		t.Fatalf("recovered checkpoint = %+v, %v", got, err)
	}
}

func TestGenerationCandidatesAreOwnedBoundedAndUnique(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	r, err := s.CreateGeneration(ctx, generationRunFixture())
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []testgendomain.State{testgendomain.StateBaseline, testgendomain.StateAnalyzing, testgendomain.StateSolving, testgendomain.StateRendering} {
		next := r
		next.State = state
		next.Revision++
		r, err = s.CheckpointGeneration(ctx, r.Revision, next, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	a := task.Artifact{ID: strings.Repeat("4", 32), TaskID: r.TaskID, Kind: "test-generation-source", RelativePath: "tasks/" + r.TaskID + "/" + strings.Repeat("4", 32) + ".source", MIMEType: "application/octet-stream", Size: 12, SHA256: strings.Repeat("e", 64), CreatedAt: r.CreatedAt}
	c := testgendomain.Candidate{CaseID: strings.Repeat("5", 32), Kind: testgendomain.KindVerified, TargetSymbol: "fn:classify", Assertions: []testgendomain.Assertion{{Kind: testgendomain.AssertionIndependentOracle, EvidenceDigest: strings.Repeat("6", 64)}}, StagedSourceArtifact: testgendomain.ArtifactRef{ID: a.ID, Digest: a.SHA256}, CodeDigest: strings.Repeat("7", 64), PlannedEdits: []testgendomain.PlannedEdit{{Path: "tests/classify_test.c", Operation: testgendomain.EditCreate, AfterDigest: strings.Repeat("8", 64)}}}
	next := r
	next.State = testgendomain.StateValidating
	next.Revision++
	next.CandidateCount = 1
	next.ArtifactDigests = []testgendomain.ArtifactRef{{ID: a.ID, Digest: a.SHA256}}
	r, err = s.CheckpointGeneration(ctx, r.Revision, next, []testgendomain.Candidate{c}, []task.Artifact{a})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ListGenerationCandidates(ctx, r.ID)
	if err != nil || len(got) != 1 || got[0].CaseID != c.CaseID {
		t.Fatalf("candidates = %+v, %v", got, err)
	}
	got[0].PlannedEdits[0].Path = "tampered"
	again, err := s.ListGenerationCandidates(ctx, r.ID)
	if err != nil || again[0].PlannedEdits[0].Path != "tests/classify_test.c" {
		t.Fatalf("candidate clone = %+v, %v", again, err)
	}
	next = r
	next.State = testgendomain.StateMinimizing
	next.Revision++
	next.CandidateCount++
	if _, err = s.CheckpointGeneration(ctx, r.Revision, next, []testgendomain.Candidate{c}, nil); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("duplicate case id: %v", err)
	}
	other := c
	other.CaseID = strings.Repeat("9", 32)
	other.StagedSourceArtifact.ID = strings.Repeat("a", 32)
	if _, err = s.CheckpointGeneration(ctx, r.Revision, next, []testgendomain.Candidate{other}, nil); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("foreign artifact: %v", err)
	}
	events, err := s.ReplayGenerationEvents(ctx, r.ID, 0, 200)
	if err != nil || len(events) != 7 {
		t.Fatalf("events = %d, %v", len(events), err)
	}
}

func TestGenerationRejectsPersistedRequestOrStatusDrift(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tamper func(*testing.T, *Store, testgendomain.Run)
	}{
		{"request", func(t *testing.T, s *Store, r testgendomain.Run) {
			changed := r.Request
			changed.CompileSnapshotDigest = strings.Repeat("f", 64)
			raw, _ := json.Marshal(changed)
			if _, err := s.db.Exec(`UPDATE tasks SET request_json=? WHERE task_id=?`, string(raw), r.TaskID); err != nil {
				t.Fatal(err)
			}
		}},
		{"status", func(t *testing.T, s *Store, r testgendomain.Run) {
			if _, err := s.db.Exec(`UPDATE tasks SET status='running' WHERE task_id=?`, r.TaskID); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			r, err := s.CreateGeneration(context.Background(), generationRunFixture())
			if err != nil {
				t.Fatal(err)
			}
			tc.tamper(t, s, r)
			if _, err := s.GetGeneration(context.Background(), r.ID); !errors.Is(err, task.ErrConflict) {
				t.Fatalf("tampered row: %v", err)
			}
		})
	}
}

func TestGenerationRejectsTamperedCheckpointRecord(t *testing.T) {
	s := openTestStore(t)
	r := generationRunFixture()
	r.Record = testgendomain.NewGenerationRecord(r.Request)
	created, err := s.CreateGeneration(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE test_generation_runs SET record_json='{}' WHERE run_id=?`, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetGeneration(context.Background(), created.ID); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("tampered record: %v", err)
	}
}

func TestGenerationV10UpgradeBackfillsOrTerminalizesWithoutAdoption(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "complete-identity"
		if legacy {
			name = "old-identity"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tasks.sqlite")
			seedGenerationV10(t, path, legacy)
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			r, err := s.GetGeneration(context.Background(), strings.Repeat("2", 32))
			if err != nil {
				t.Fatal(err)
			}
			privateEvents, err := s.ReplayGenerationEvents(context.Background(), r.ID, 0, 200)
			if err != nil || len(privateEvents) == 0 || privateEvents[0].Sequence != 1 {
				t.Fatalf("migrated generation replay = %+v, %v", privateEvents, err)
			}
			global, err := s.EventsAfter(context.Background(), 0, 100, 200)
			if err != nil || len(global) != 1 || global[0].Type != task.EventTaskOutput ||
				!bytes.Equal(global[0].Payload, []byte(`{"stepId":"cursor-redacted","stream":"combined","text":"","truncated":false}`)) {
				t.Fatalf("legacy cursor tombstone = %+v, %v", global, err)
			}
			watermark, err := s.Watermark(context.Background())
			if err != nil || watermark < 1 {
				t.Fatalf("saved legacy cursor invalidated: watermark=%d, %v", watermark, err)
			}
			broker, err := eventbroker.New(s, 8, 8)
			if err != nil {
				t.Fatal(err)
			}
			subscription, err := broker.Subscribe(context.Background(), 1)
			if err != nil {
				t.Fatalf("saved cursor rejected: %v", err)
			}
			subscription.Close()
			_ = broker.Close()
			if legacy {
				if r.State != testgendomain.StateFailed || !r.Record.IsZero() {
					t.Fatalf("unsafe old run resumed: %+v", r)
				}
				storedTask, err := s.Get(context.Background(), r.TaskID)
				if err != nil || storedTask.Status != task.StatusFinished {
					t.Fatalf("old task not terminal: %+v, %v", storedTask, err)
				}
			} else if r.State != testgendomain.StateQueued || !r.Record.ValidFor(r.Request, r.CandidateCount) {
				t.Fatalf("backfill lost resumable checkpoint: %+v", r)
			}
		})
	}
}

func TestGenerationMigrationPreservesInterleavedLegacyCursorReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	seedGenerationV10(t, path, false)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	legacyTask := strings.Repeat("4", 32)
	_, err = db.Exec(`INSERT INTO tasks(task_id,idempotency_key,request_hash,kind,scenario,request_json,workspace_generation,plan_fingerprint,active_step,timeout_ms,status,outcome,created_at,started_at,finished_at,last_sequence,error_code,error_message)
		VALUES(?,?,?,?,?,?,?,?,?,?,'queued',NULL,?,NULL,NULL,0,'','')`, legacyTask, strings.Repeat("9", 32), strings.Repeat("a", 64), "simulation", "success", `{}`, "", "", "", 1000, "2026-09-27T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO task_events(event_id,task_id,event_type,occurred_at,payload_json) VALUES
		(?,?,'task.created','2026-09-27T00:00:01Z','{"status":"queued"}'),
		(?,?,'testGeneration.stateChanged','2026-09-27T00:00:02Z','{"secret":"must-not-leak"}')`, strings.Repeat("b", 32), legacyTask, strings.Repeat("c", 32), strings.Repeat("3", 32))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	global, err := s.EventsAfter(context.Background(), 0, 3, 10)
	if err != nil || len(global) != 3 || global[0].Sequence != 1 || global[1].Sequence != 2 || global[2].Sequence != 3 ||
		global[1].Type != task.EventTaskCreated || global[2].Type != task.EventTaskOutput || bytes.Contains(global[2].Payload, []byte("must-not-leak")) {
		t.Fatalf("interleaved replay = %+v, %v", global, err)
	}
	broker, err := eventbroker.New(s, 8, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	subscription, err := broker.Subscribe(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	subscription.Activate()
	for _, want := range []int64{2, 3} {
		select {
		case event := <-subscription.Events:
			if event.Sequence != want {
				t.Fatalf("replay sequence=%d want=%d", event.Sequence, want)
			}
		case err := <-subscription.Errors:
			t.Fatalf("replay error: %v", err)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for interleaved replay")
		}
	}
}

func TestGenerationRecordCapacityMigrationPreservesCandidateReferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	store := &Store{db: db, newID: task.NewID}
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	applyMigrationsThrough(t, context.Background(), store, migrations[:13])
	run, err := store.CreateGeneration(context.Background(), generationRunFixture())
	if err != nil {
		t.Fatal(err)
	}
	candidate := testgendomain.Candidate{CaseID: strings.Repeat("5", 32), Kind: testgendomain.KindVerified, TargetSymbol: "fn:classify",
		Assertions:           []testgendomain.Assertion{{Kind: testgendomain.AssertionIndependentOracle, EvidenceDigest: strings.Repeat("6", 64)}},
		StagedSourceArtifact: testgendomain.ArtifactRef{ID: strings.Repeat("4", 32), Digest: strings.Repeat("e", 64)}, CodeDigest: strings.Repeat("7", 64),
		PlannedEdits: []testgendomain.PlannedEdit{{Path: "tests/classify_test.cpp", Operation: testgendomain.EditCreate, AfterDigest: strings.Repeat("8", 64)}},
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO test_generation_candidates(run_id,case_id,candidate_json) VALUES(?,?,?)`, run.ID, candidate.CaseID, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE test_generation_runs SET candidate_count=1 WHERE run_id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if _, err := upgraded.GetGeneration(context.Background(), run.ID); err != nil {
		t.Fatalf("run after capacity migration = %v", err)
	}
	candidates, err := upgraded.ListGenerationCandidates(context.Background(), run.ID)
	if err != nil || len(candidates) != 1 || candidates[0].CaseID != candidate.CaseID {
		t.Fatalf("candidate after capacity migration = %+v, %v", candidates, err)
	}
	var violations int
	rows, err := upgraded.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		violations++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if violations != 0 {
		t.Fatalf("capacity migration broke %d foreign keys", violations)
	}
}

func seedGenerationV10(t *testing.T, path string, legacy bool) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, newID: task.NewID}
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	applyMigrationsThrough(t, context.Background(), store, migrations[:10])
	rq := generationRequestFixture()
	raw, err := json.Marshal(rq)
	if err != nil {
		t.Fatal(err)
	}
	if legacy {
		var values map[string]any
		if err := json.Unmarshal(raw, &values); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"sourceDigest", "cmakeTargetDigest", "frameworkBundleDigest", "analyzerBundleDigest", "baselineReportDigest", "processOwnerDigest"} {
			delete(values, key)
		}
		raw, err = json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
	}
	sum := sha256.Sum256(raw)
	created := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO tasks(task_id,idempotency_key,request_hash,kind,scenario,request_json,workspace_generation,plan_fingerprint,active_step,timeout_ms,status,outcome,created_at,started_at,finished_at,last_sequence,error_code,error_message) VALUES(?,?,?,'test_generation',NULL,?,?,?,'',?,'queued',NULL,?,NULL,NULL,0,'','')`, strings.Repeat("3", 32), rq.IdempotencyKey, hex.EncodeToString(sum[:]), string(raw), rq.WorkspaceGeneration, hex.EncodeToString(sum[:]), rq.Budgets.WallTimeMS, formatTime(created))
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(`INSERT INTO test_generation_runs(run_id,task_id,state,revision,artifact_digests_json,candidate_count) VALUES(?,?,'queued',1,'[]',0)`, strings.Repeat("2", 32), strings.Repeat("3", 32))
	if err != nil {
		t.Fatal(err)
	}
	events, err := insertEvents(context.Background(), tx, []task.EventDraft{{TaskID: strings.Repeat("3", 32), Type: task.EventTaskCreated, At: created, Payload: json.RawMessage(`{"status":"queued"}`)}}, task.NewID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE tasks SET last_sequence=? WHERE task_id=?`, events[0].Sequence, strings.Repeat("3", 32)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGenerationRejectsUnownedArtifactKinds(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	r, err := s.CreateGeneration(ctx, generationRunFixture())
	if err != nil {
		t.Fatal(err)
	}
	a := task.Artifact{ID: strings.Repeat("4", 32), TaskID: r.TaskID, Kind: "stdout", RelativePath: "tasks/" + r.TaskID + "/" + strings.Repeat("4", 32) + ".source", MIMEType: "application/octet-stream", Size: 4, SHA256: strings.Repeat("e", 64), CreatedAt: r.CreatedAt}
	next := r
	next.State = testgendomain.StateBaseline
	next.Revision++
	next.ArtifactDigests = []testgendomain.ArtifactRef{{ID: a.ID, Digest: a.SHA256}}
	if _, err = s.CheckpointGeneration(ctx, r.Revision, next, nil, []task.Artifact{a}); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("arbitrary artifact kind: %v", err)
	}
}

func TestGenerationEventsUseClosedProtocolPayloads(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	r, err := s.CreateGeneration(ctx, generationRunFixture())
	if err != nil {
		t.Fatal(err)
	}
	next := r
	next.State = testgendomain.StateBaseline
	next.Revision++
	r, err = s.CheckpointGeneration(ctx, r.Revision, next, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	next = r
	next.State = testgendomain.StateCancelled
	next.Revision++
	finished := r.CreatedAt.Add(time.Minute)
	next.FinishedAt = &finished
	if _, err = s.CheckpointGeneration(ctx, r.Revision, next, nil, nil); err != nil {
		t.Fatal(err)
	}
	events, err := s.ReplayGenerationEvents(ctx, r.ID, 0, 200)
	if err != nil || len(events) != 5 {
		t.Fatalf("events = %+v, %v", events, err)
	}
	wantTypes := []task.EventType{task.EventTaskCreated, task.EventTaskStarted, task.EventTestGenerationStateChanged, task.EventTestGenerationStateChanged, task.EventTaskFinished}
	wantPayloads := [][]byte{[]byte(`{"status":"queued"}`), []byte(`{"status":"running"}`), []byte(`{"runId":"` + r.ID + `","from":"queued","to":"baseline"}`), []byte(`{"runId":"` + r.ID + `","from":"baseline","to":"cancelled"}`), []byte(`{"outcome":"cancelled"}`)}
	for i, e := range events {
		if e.Type != wantTypes[i] || !bytes.Equal(e.Payload, wantPayloads[i]) {
			t.Fatalf("event %d = %s %s", i, e.Type, e.Payload)
		}
	}
}
