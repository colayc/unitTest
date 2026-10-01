package coverageexec

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
)

type detailCommitStore struct {
	persisted bool
	index     coveragedetail.Index
	writes    int
}

func (s *detailCommitStore) PutCoverageDetail(_ context.Context, index coveragedetail.Index) error {
	if !s.persisted {
		return errors.New("report not durable")
	}
	s.writes++
	s.index = index
	return nil
}

func TestDetailIndexWritesOnlyAfterCompletionCommitAndFailureLeavesV1Graph(t *testing.T) {
	store := &detailCommitStore{}
	failures := 0
	report := coveragedomain.Report{ID: strings.Repeat("a", 32), RunID: strings.Repeat("b", 32), TestRunID: strings.Repeat("c", 32), SchemaVersion: coveragedomain.SchemaVersion10, CreatedAt: time.Now(), Completeness: coveragedomain.Completeness{Outcome: coveragedomain.OutcomeAvailable}, Toolchain: coveragedomain.ToolchainSnapshot{Platform: coveragedomain.PlatformLinux, Architecture: coveragedomain.ArchitectureX64, Compiler: coveragedomain.CompilerSnapshot{Family: coveragedomain.CompilerFamilyGCC, Version: "15"}, Driver: coveragedomain.DriverSnapshot{Name: coveragedomain.DriverGCov, Version: "15"}, Collector: coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorGCovr, Version: "8.6"}, NormalizerVersion: "1", InstrumentationFingerprint: strings.Repeat("d", 64)}, ArtifactID: strings.Repeat("e", 32), Sources: []coveragedomain.SourceSnapshot{{URI: "src/a.c", SHA256: strings.Repeat("1", 64)}}, Summary: coveragedomain.Summary{Functions: coveragedomain.Metric{Covered: 1, Total: 1}, Lines: coveragedomain.Metric{Covered: 1, Total: 1}}}
	e := &execution{config: Config{DetailStore: store, DetailFailure: func(error) { failures++ }}, run: coveragedomain.Run{Request: coveragedomain.Request{WorkspaceGeneration: strings.Repeat("f", 64), ProjectID: "demo"}}, observations: []coveragedomain.FunctionObservation{{QualifiedName: "foo", File: "src/a.c", ExecutionCount: 1, Lines: []coveragedomain.LineObservation{{Line: 1, Count: 1}}}}, normalized: true, detailNormalized: true}
	completion := task.DomainCompletion{Coverage: &task.CoverageCompletion{Report: &report}}
	if err := e.CompletionCommitted(context.Background(), completion); err != nil {
		t.Fatalf("optional detail failure escaped: %v", err)
	}
	if store.writes != 0 || failures != 1 {
		t.Fatalf("pre-persistence write=%d failures=%d", store.writes, failures)
	}
	store.persisted = true
	if err := e.CompletionCommitted(context.Background(), completion); err != nil {
		t.Fatal(err)
	}
	if store.writes != 1 || store.index.Project.Status != coveragedetail.StatusCurrent || store.index.ReportID != report.ID {
		t.Fatalf("post-persistence index=%#v writes=%d", store.index, store.writes)
	}
}
