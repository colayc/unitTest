package taskstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

// Generation rows are typed relations inside the existing task database. Task
// lifecycle, events, and artifact metadata remain their authoritative stores.
func (s *Store) CreateGeneration(ctx context.Context, run testgendomain.Run) (testgendomain.Run, error) {
	if s == nil || ctx == nil || testgendomain.ValidateRun(run) != nil || run.State != testgendomain.StateQueued || run.Revision != 1 || run.LastSequence != 0 || run.CandidateCount != 0 || len(run.ArtifactDigests) != 0 {
		return testgendomain.Run{}, task.ErrInvalidArgument
	}
	requestJSON, err := json.Marshal(run.Request)
	if err != nil || len(requestJSON) > 16384 {
		return testgendomain.Run{}, task.ErrInvalidArgument
	}
	hash := sha256.Sum256(requestJSON)
	requestHash := hex.EncodeToString(hash[:])
	recordJSON, recordHash, err := generationRecordBytes(run.Record)
	if err != nil {
		return testgendomain.Run{}, task.ErrInvalidArgument
	}
	now := run.CreatedAt.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return testgendomain.Run{}, storageError("begin generation create", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO tasks(task_id,idempotency_key,request_hash,kind,scenario,request_json,workspace_generation,plan_fingerprint,active_step,timeout_ms,status,outcome,created_at,started_at,finished_at,last_sequence,error_code,error_message)
		VALUES(?,?,?,?,NULL,?,?,?,?,?,'queued',NULL,?,NULL,NULL,0,'','')`, run.TaskID, run.Request.IdempotencyKey, requestHash, string(task.KindTestGeneration), string(requestJSON), run.Request.WorkspaceGeneration, requestHash, "", run.Request.Budgets.WallTimeMS, formatTime(now))
	if err != nil {
		existing, findErr := findTaskByIdempotencyKey(ctx, tx, run.Request.IdempotencyKey)
		if findErr == nil {
			if existing.Kind != task.KindTestGeneration || existing.RequestHash != requestHash {
				return testgendomain.Run{}, task.ErrIdempotencyConflict
			}
			persisted, readErr := getGeneration(ctx, tx, "", existing.ID)
			if readErr == nil {
				return persisted, nil
			}
			return testgendomain.Run{}, task.ErrConflict
		}
		return testgendomain.Run{}, storageError("create generation task", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO test_generation_runs(run_id,task_id,state,revision,artifact_digests_json,candidate_count,record_json,record_sha256) VALUES(?,?,?,1,'[]',0,?,?)`, run.ID, run.TaskID, string(run.State), string(recordJSON), recordHash)
	if err != nil {
		return testgendomain.Run{}, storageError("create generation relation", err)
	}
	events, err := insertEvents(ctx, tx, []task.EventDraft{{TaskID: run.TaskID, Type: task.EventTaskCreated, At: now, Payload: json.RawMessage(`{"status":"queued"}`)}}, s.newID)
	if err != nil {
		return testgendomain.Run{}, err
	}
	run.LastSequence = events[0].Sequence
	if _, err = tx.ExecContext(ctx, `UPDATE tasks SET last_sequence=? WHERE task_id=?`, run.LastSequence, run.TaskID); err != nil {
		return testgendomain.Run{}, storageError("generation sequence", err)
	}
	if err = tx.Commit(); err != nil {
		return testgendomain.Run{}, storageError("commit generation create", err)
	}
	return testgendomain.CloneRun(run), nil
}

func (s *Store) GetGeneration(ctx context.Context, runID string) (testgendomain.Run, error) {
	if s == nil || ctx == nil {
		return testgendomain.Run{}, task.ErrInvalidArgument
	}
	return getGeneration(ctx, s.db, runID, "")
}
func (s *Store) GetGenerationByTask(ctx context.Context, taskID string) (testgendomain.Run, error) {
	if s == nil || ctx == nil {
		return testgendomain.Run{}, task.ErrInvalidArgument
	}
	return getGeneration(ctx, s.db, "", taskID)
}

func getGeneration(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, runID, taskID string) (testgendomain.Run, error) {
	if runID == "" && taskID == "" || runID != "" && taskID != "" {
		return testgendomain.Run{}, task.ErrInvalidArgument
	}
	filter := "g.run_id=?"
	value := runID
	if taskID != "" {
		filter = "g.task_id=?"
		value = taskID
	}
	var r testgendomain.Run
	var request, artifacts, recordJSON []byte
	var recordHash string
	var state, created, requestHash, workspace, status string
	var finished sql.NullString
	var kind string
	err := q.QueryRowContext(ctx, `SELECT g.run_id,g.task_id,g.state,g.revision,g.artifact_digests_json,g.candidate_count,g.record_json,g.record_sha256,t.request_json,t.created_at,t.finished_at,t.last_sequence,t.kind,t.request_hash,t.workspace_generation,t.status FROM test_generation_runs g JOIN tasks t ON t.task_id=g.task_id WHERE `+filter, value).Scan(&r.ID, &r.TaskID, &state, &r.Revision, &artifacts, &r.CandidateCount, &recordJSON, &recordHash, &request, &created, &finished, &r.LastSequence, &kind, &requestHash, &workspace, &status)
	if isNoRows(err) {
		return testgendomain.Run{}, task.ErrNotFound
	}
	if err != nil {
		return testgendomain.Run{}, storageError("get generation", err)
	}
	if kind != string(task.KindTestGeneration) {
		return testgendomain.Run{}, task.ErrConflict
	}
	r.State = testgendomain.State(state)
	if err := strictGenerationJSON(request, &r.Request); err != nil {
		return testgendomain.Run{}, task.ErrConflict
	}
	if err := strictGenerationJSON(artifacts, &r.ArtifactDigests); err != nil {
		return testgendomain.Run{}, task.ErrConflict
	}
	if err := strictGenerationJSON(recordJSON, &r.Record); err != nil {
		return testgendomain.Run{}, task.ErrConflict
	}
	canonicalRecord, expectedRecordHash, err := generationRecordBytes(r.Record)
	if err != nil || !bytes.Equal(recordJSON, canonicalRecord) || recordHash != expectedRecordHash {
		return testgendomain.Run{}, task.ErrConflict
	}
	r.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return testgendomain.Run{}, task.ErrConflict
	}
	if finished.Valid {
		t, e := time.Parse(time.RFC3339Nano, finished.String)
		if e != nil {
			return testgendomain.Run{}, task.ErrConflict
		}
		r.FinishedAt = &t
	}
	if testgendomain.ValidateRun(r) != nil || r.Request.WorkspaceGeneration != workspace || r.LastSequence < 1 {
		return testgendomain.Run{}, task.ErrConflict
	}
	canonical, e := json.Marshal(r.Request)
	digest := sha256.Sum256(request)
	if e != nil || !bytes.Equal(request, canonical) && !r.Request.IsLegacySnapshot() || requestHash != hex.EncodeToString(digest[:]) {
		return testgendomain.Run{}, task.ErrConflict
	}
	wantStatus := "running"
	if r.State == testgendomain.StateQueued {
		wantStatus = "queued"
	} else if testgendomain.IsTerminal(r.State) {
		wantStatus = "finished"
	}
	if status != wantStatus {
		return testgendomain.Run{}, task.ErrConflict
	}
	var actual int
	if e := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM test_generation_candidates WHERE run_id=?`, r.ID).Scan(&actual); e != nil || actual != r.CandidateCount {
		return testgendomain.Run{}, task.ErrConflict
	}
	return testgendomain.CloneRun(r), nil
}

func strictGenerationJSON(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	var tail any
	if err := d.Decode(&tail); !errors.Is(err, io.EOF) {
		if err == nil {
			return task.ErrInvalidArgument
		}
		return err
	}
	return nil
}

func generationRecordBytes(r testgendomain.GenerationRecord) ([]byte, string, error) {
	var raw []byte
	var err error
	if r.IsZero() {
		raw = []byte(`{}`)
	} else {
		raw, err = json.Marshal(r)
	}
	if err != nil || len(raw) > 65536 {
		return nil, "", task.ErrInvalidArgument
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}

func (s *Store) CheckpointGeneration(ctx context.Context, expected int64, next testgendomain.Run, candidates []testgendomain.Candidate, artifacts []task.Artifact) (testgendomain.Run, error) {
	if s == nil || ctx == nil || expected < 1 || testgendomain.ValidateRun(next) != nil || next.Revision != expected+1 || testgendomain.ValidateCandidates(candidates) != nil || len(artifacts) > 1000 {
		return testgendomain.Run{}, task.ErrInvalidArgument
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return testgendomain.Run{}, storageError("begin generation checkpoint", err)
	}
	defer tx.Rollback()
	current, err := getGeneration(ctx, tx, next.ID, "")
	if err != nil {
		return testgendomain.Run{}, err
	}
	if current.Revision != expected || current.TaskID != next.TaskID || current.Request != next.Request || !current.CreatedAt.Equal(next.CreatedAt) || !testgendomain.ValidTransition(current.State, next.State) {
		return testgendomain.Run{}, task.ErrConflict
	}
	if next.CandidateCount != current.CandidateCount+len(candidates) || next.LastSequence != current.LastSequence || len(next.ArtifactDigests) < len(current.ArtifactDigests) {
		return testgendomain.Run{}, task.ErrInvalidArgument
	}
	if !current.Record.IsZero() && (!next.Record.ValidFor(next.Request, next.CandidateCount) || !next.Record.MonotonicAfter(current.Record)) {
		return testgendomain.Run{}, task.ErrInvalidArgument
	}
	if current.Record.IsZero() && !next.Record.IsZero() && !next.Record.ValidFor(next.Request, next.CandidateCount) {
		return testgendomain.Run{}, task.ErrInvalidArgument
	}
	if len(next.Record.MinimizedCaseIDs) > 0 && next.State != testgendomain.StateAwaitingConfirmation && next.State != testgendomain.StateAccepted && next.State != testgendomain.StateRejected {
		return testgendomain.Run{}, task.ErrInvalidArgument
	}
	for i, a := range current.ArtifactDigests {
		if next.ArtifactDigests[i] != a {
			return testgendomain.Run{}, task.ErrConflict
		}
	}
	for _, a := range artifacts {
		if a.TaskID != next.TaskID || !validGenerationArtifact(a) {
			return testgendomain.Run{}, task.ErrInvalidArgument
		}
		if err := insertArtifact(ctx, tx, a); err != nil {
			return testgendomain.Run{}, err
		}
	}
	for _, ref := range next.ArtifactDigests {
		artifact, err := scanArtifact(tx.QueryRowContext(ctx, artifactSelect+` WHERE artifact_id=? AND task_id=?`, ref.ID, next.TaskID))
		if err != nil || !validGenerationArtifact(artifact) || artifact.SHA256 != ref.Digest {
			return testgendomain.Run{}, task.ErrConflict
		}
	}
	for _, c := range candidates {
		found := false
		for _, ref := range next.ArtifactDigests {
			if c.StagedSourceArtifact == ref {
				found = true
				break
			}
		}
		if !found {
			return testgendomain.Run{}, task.ErrInvalidArgument
		}
		raw, e := json.Marshal(c)
		if e != nil || len(raw) > 65536 {
			return testgendomain.Run{}, task.ErrInvalidArgument
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO test_generation_candidates(run_id,case_id,candidate_json) VALUES(?,?,?)`, next.ID, c.CaseID, string(raw)); e != nil {
			return testgendomain.Run{}, task.ErrConflict
		}
	}
	for _, caseID := range next.Record.MinimizedCaseIDs {
		var found int
		if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM test_generation_candidates WHERE run_id=? AND case_id=?`, next.ID, caseID).Scan(&found); e != nil || found != 1 {
			return testgendomain.Run{}, task.ErrInvalidArgument
		}
	}
	refs, e := json.Marshal(next.ArtifactDigests)
	if e != nil || len(refs) > 128000 {
		return testgendomain.Run{}, task.ErrInvalidArgument
	}
	recordJSON, recordHash, e := generationRecordBytes(next.Record)
	if e != nil {
		return testgendomain.Run{}, task.ErrInvalidArgument
	}
	result, e := tx.ExecContext(ctx, `UPDATE test_generation_runs SET state=?,revision=?,artifact_digests_json=?,candidate_count=?,record_json=?,record_sha256=? WHERE run_id=? AND revision=?`, string(next.State), next.Revision, string(refs), next.CandidateCount, string(recordJSON), recordHash, next.ID, expected)
	if e != nil {
		return testgendomain.Run{}, storageError("generation checkpoint", e)
	}
	n, e := result.RowsAffected()
	if e != nil || n != 1 {
		return testgendomain.Run{}, task.ErrConflict
	}
	at := time.Now().UTC()
	status := "running"
	var outcome any
	var finished any
	var started any
	if current.State == testgendomain.StateQueued {
		started = formatTime(at)
	}
	if testgendomain.IsTerminal(next.State) {
		status = "finished"
		finished = formatTime(*next.FinishedAt)
		switch next.State {
		case testgendomain.StateCancelled:
			outcome = string(task.OutcomeCancelled)
		case testgendomain.StateFailed:
			outcome = string(task.OutcomeInfrastructureFailed)
		default:
			outcome = string(task.OutcomeSucceeded)
		}
	}
	result, e = tx.ExecContext(ctx, `UPDATE tasks SET status=?,outcome=?,started_at=COALESCE(started_at,?),finished_at=?,active_step=? WHERE task_id=? AND last_sequence=?`, status, outcome, started, finished, string(next.State), next.TaskID, current.LastSequence)
	if e != nil {
		return testgendomain.Run{}, storageError("generation task checkpoint", e)
	}
	n, e = result.RowsAffected()
	if e != nil || n != 1 {
		return testgendomain.Run{}, task.ErrConflict
	}
	drafts := make([]task.EventDraft, 0, 3)
	if next.State == testgendomain.StateBaseline {
		drafts = append(drafts, task.EventDraft{TaskID: next.TaskID, Type: task.EventTaskStarted, At: at, Payload: json.RawMessage(`{"status":"running"}`)})
	}
	payload, _ := json.Marshal(struct {
		RunID string              `json:"runId"`
		From  testgendomain.State `json:"from"`
		To    testgendomain.State `json:"to"`
	}{next.ID, current.State, next.State})
	drafts = append(drafts, task.EventDraft{TaskID: next.TaskID, Type: task.EventTestGenerationStateChanged, At: at, Payload: payload})
	if testgendomain.IsTerminal(next.State) {
		terminalPayload, _ := json.Marshal(struct {
			Outcome string `json:"outcome"`
		}{outcome.(string)})
		drafts = append(drafts, task.EventDraft{TaskID: next.TaskID, Type: task.EventTaskFinished, At: at, Payload: terminalPayload})
	}
	events, e := insertEvents(ctx, tx, drafts, s.newID)
	if e != nil {
		return testgendomain.Run{}, e
	}
	next.LastSequence = events[len(events)-1].Sequence
	if _, e = tx.ExecContext(ctx, `UPDATE tasks SET last_sequence=? WHERE task_id=?`, next.LastSequence, next.TaskID); e != nil {
		return testgendomain.Run{}, storageError("generation task sequence", e)
	}
	if e = tx.Commit(); e != nil {
		return testgendomain.Run{}, storageError("commit generation checkpoint", e)
	}
	return testgendomain.CloneRun(next), nil
}

func validGenerationArtifact(a task.Artifact) bool {
	return validArtifact(a) && len(a.ID) == 32 && len(a.TaskID) == 32 &&
		a.Kind == "test-generation-source" && a.MIMEType == "application/octet-stream" &&
		a.Size >= 1 && a.Size <= 4*1024*1024 &&
		a.RelativePath == path.Join("tasks", a.TaskID, a.ID+".source") &&
		a.SHA256 == strings.ToLower(a.SHA256)
}

func (s *Store) ListGenerationCandidates(ctx context.Context, runID string) ([]testgendomain.Candidate, error) {
	if s == nil || ctx == nil {
		return nil, task.ErrInvalidArgument
	}
	if _, err := s.GetGeneration(ctx, runID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT candidate_json FROM test_generation_candidates WHERE run_id=? ORDER BY case_id LIMIT 1001`, runID)
	if err != nil {
		return nil, storageError("list generation candidates", err)
	}
	defer rows.Close()
	result := make([]testgendomain.Candidate, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, storageError("read generation candidate", err)
		}
		var c testgendomain.Candidate
		if err := strictGenerationJSON(raw, &c); err != nil || testgendomain.ValidateCandidate(c) != nil {
			return nil, task.ErrConflict
		}
		result = append(result, testgendomain.CloneCandidate(c))
		if len(result) > 1000 {
			return nil, task.ErrConflict
		}
	}
	if err := rows.Err(); err != nil {
		return nil, storageError("list generation candidates", err)
	}
	return result, nil
}

func (s *Store) ReplayGenerationEvents(ctx context.Context, runID string, after int64, limit int) ([]task.Event, error) {
	if s == nil || ctx == nil || after < 0 || limit < 1 || limit > 200 {
		return nil, task.ErrInvalidArgument
	}
	r, err := s.GetGeneration(ctx, runID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT sequence,event_id,event_type,occurred_at,payload_json FROM task_events WHERE task_id=? AND sequence>? AND sequence<=? ORDER BY sequence LIMIT ?`, r.TaskID, after, r.LastSequence, limit)
	if err != nil {
		return nil, storageError("replay generation events", err)
	}
	defer rows.Close()
	result := make([]task.Event, 0)
	for rows.Next() {
		var e task.Event
		var typ, at, payload string
		if err := rows.Scan(&e.Sequence, &e.ID, &typ, &at, &payload); err != nil {
			return nil, storageError("read generation event", err)
		}
		e.TaskID = r.TaskID
		e.Type = task.EventType(typ)
		e.At, err = time.Parse(time.RFC3339Nano, at)
		if err != nil || !task.ValidEventType(e.Type) {
			return nil, task.ErrConflict
		}
		e.Payload = json.RawMessage(payload)
		result = append(result, e)
	}
	if err := rows.Err(); err != nil {
		return nil, storageError("replay generation events", err)
	}
	return result, nil
}
