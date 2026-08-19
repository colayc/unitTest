package runtime

import (
	"context"
	"errors"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragecoord"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/toolchain"
)

type fakeCoverageQueue struct {
	called int
	input  coveragecoord.QueuedStartInput
	result coveragecoord.QueuedStartResult
	err    error
}

func (queue *fakeCoverageQueue) Start(_ context.Context, input coveragecoord.QueuedStartInput) (coveragecoord.QueuedStartResult, error) {
	queue.called++
	queue.input = input
	return queue.result, queue.err
}

type fakeCoverageRepository struct {
	run    coveragedomain.Run
	page   coveragedomain.RunPage
	report coveragedomain.Report
}

func (repository *fakeCoverageRepository) GetCoverageRun(context.Context, string) (coveragedomain.Run, error) {
	return repository.run, nil
}

func (repository *fakeCoverageRepository) ListCoverageRuns(context.Context, coveragedomain.RunPageRequest) (coveragedomain.RunPage, error) {
	return repository.page, nil
}

func (repository *fakeCoverageRepository) GetCoverageReport(context.Context, string) (coveragedomain.Report, error) {
	return repository.report, nil
}

func TestRuntimeCoverageBackendQueuesResolvedInputAndKeepsRunQueued(t *testing.T) {
	queue := &fakeCoverageQueue{result: coveragecoord.QueuedStartResult{
		Task:    task.Task{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Status: task.StatusQueued},
		Run:     coveragedomain.Run{ID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Status: coveragedomain.StatusQueued},
		TestRun: testdomain.TestRun{RunID: "cccccccccccccccccccccccccccccccc", Status: testdomain.RunQueued},
	}}
	resolver := func(context.Context, session.CoverageRunStart) (coveragecoord.QueuedStartInput, error) {
		return coveragecoord.QueuedStartInput{BuildProfileID: "profile", ToolchainID: "toolchain"}, nil
	}
	backend, err := newRuntimeCoverageBackend(queue, &fakeCoverageRepository{}, resolver)
	if err != nil {
		t.Fatalf("newRuntimeCoverageBackend() error = %v", err)
	}
	taskValue, run, testRun, err := backend.StartCoverageRun(context.Background(), session.CoverageRunStart{})
	if err != nil {
		t.Fatalf("StartCoverageRun() error = %v", err)
	}
	if queue.called != 1 || taskValue.Status != task.StatusQueued || run.Status != coveragedomain.StatusQueued || testRun.Status != testdomain.RunQueued {
		t.Fatalf("queued result = task=%#v run=%#v testRun=%#v calls=%d", taskValue, run, testRun, queue.called)
	}
}

func TestRuntimeCoverageBackendRejectsResolverFailureBeforeQueue(t *testing.T) {
	queue := &fakeCoverageQueue{}
	want := errors.New("coverage identity is stale")
	backend, err := newRuntimeCoverageBackend(queue, &fakeCoverageRepository{}, func(context.Context, session.CoverageRunStart) (coveragecoord.QueuedStartInput, error) {
		return coveragecoord.QueuedStartInput{}, want
	})
	if err != nil {
		t.Fatalf("newRuntimeCoverageBackend() error = %v", err)
	}
	if _, _, _, err := backend.StartCoverageRun(context.Background(), session.CoverageRunStart{}); !errors.Is(err, want) {
		t.Fatalf("StartCoverageRun() error = %v, want %v", err, want)
	}
	if queue.called != 0 {
		t.Fatalf("queue calls = %d, want 0", queue.called)
	}
}

func TestRuntimeCoverageBackendDelegatesCanonicalReads(t *testing.T) {
	wantRun := coveragedomain.Run{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	wantPage := coveragedomain.RunPage{NextCursor: "next"}
	wantReport := coveragedomain.Report{ID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	backend, err := newRuntimeCoverageBackend(&fakeCoverageQueue{}, &fakeCoverageRepository{run: wantRun, page: wantPage, report: wantReport}, func(context.Context, session.CoverageRunStart) (coveragecoord.QueuedStartInput, error) {
		return coveragecoord.QueuedStartInput{}, nil
	})
	if err != nil {
		t.Fatalf("newRuntimeCoverageBackend() error = %v", err)
	}
	if got, _ := backend.GetCoverageRun(context.Background(), wantRun.ID); got.ID != wantRun.ID {
		t.Fatalf("GetCoverageRun() = %#v", got)
	}
	if got, _ := backend.ListCoverageRuns(context.Background(), coveragedomain.RunPageRequest{}); got.NextCursor != wantPage.NextCursor {
		t.Fatalf("ListCoverageRuns() = %#v", got)
	}
	if got, _ := backend.GetCoverageReport(context.Background(), wantReport.ID); got.ID != wantReport.ID {
		t.Fatalf("GetCoverageReport() = %#v", got)
	}
}

func TestCoverageToolchainSnapshotAcceptsOnlySupportedPlatformFamilies(t *testing.T) {
	valid := []struct {
		name     string
		platform string
		family   toolchain.Family
		compiler coveragedomain.CompilerFamily
		driver   coveragedomain.DriverName
		collect  coveragedomain.CollectorName
	}{
		{"windows clang-cl", "windows", toolchain.FamilyClangCL, coveragedomain.CompilerFamilyClangCL, coveragedomain.DriverLLVMCov, coveragedomain.CollectorLLVMCov},
		{"linux gcc", "linux", toolchain.FamilyGCC, coveragedomain.CompilerFamilyGCC, coveragedomain.DriverGCov, coveragedomain.CollectorGCovr},
		{"linux clang", "linux", toolchain.FamilyClang, coveragedomain.CompilerFamilyClang, coveragedomain.DriverLLVMCov, coveragedomain.CollectorLLVMCov},
	}
	for _, test := range valid {
		t.Run(test.name, func(t *testing.T) {
			got, err := coverageToolchainSnapshot(toolchain.Instance{ID: "toolchain", Family: test.family, Version: "18.1.8", TargetArchitecture: "amd64"}, test.platform)
			if err != nil {
				t.Fatalf("coverageToolchainSnapshot() error = %v", err)
			}
			if got.Compiler.Family != test.compiler || got.Driver.Name != test.driver || got.Collector.Name != test.collect || len(got.InstrumentationFingerprint) != 64 {
				t.Fatalf("snapshot = %#v", got)
			}
		})
	}
	if _, err := coverageToolchainSnapshot(toolchain.Instance{ID: "toolchain", Family: toolchain.FamilyMSVC, Version: "19.0", TargetArchitecture: "amd64"}, "windows"); !errors.Is(err, coveragedomain.ErrInvalidToolchain) {
		t.Fatalf("unsupported family error = %v", err)
	}
}
