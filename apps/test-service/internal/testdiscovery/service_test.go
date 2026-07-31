package testdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

func TestServiceDiscoversThenCommitsArtifactBeforePublication(t *testing.T) {
	fixture := newServiceFixture(t)
	catalog, err := fixture.service.DiscoverAfterBuild(
		context.Background(),
		fixture.input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Containers) != 1 || len(catalog.Items) != 1 ||
		fixture.executor.steps[0].Kind != task.StepTestDiscovery ||
		fixture.executor.maxOutput != ctest.DefaultLimits().MaxDocumentBytes {
		t.Fatalf("catalog/steps = %#v / %#v", catalog, fixture.executor.steps)
	}
	if strings.Join(*fixture.order, ",") != "execute,commit,publish" {
		t.Fatalf("operation order = %#v", *fixture.order)
	}
	if fixture.publisher.current.Revision != catalog.Revision ||
		fixture.writer.catalog.Revision != catalog.Revision {
		t.Fatalf("published/artifact revisions = %q/%q, want %q",
			fixture.publisher.current.Revision,
			fixture.writer.catalog.Revision,
			catalog.Revision,
		)
	}
}

func TestServiceReturnsRuntimeBindingsWithPublishedCatalog(
	t *testing.T,
) {
	fixture := newServiceFixture(t)
	snapshot, err := fixture.service.DiscoverSnapshotAfterBuild(
		context.Background(),
		fixture.input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Bindings) != 1 ||
		snapshot.Bindings[0].ContainerID !=
			snapshot.Catalog.Containers[0].ID ||
		snapshot.Bindings[0].Descriptor.LogicalName !=
			snapshot.Catalog.Containers[0].CTestLogicalName ||
		snapshot.Bindings[0].Adapter == nil ||
		snapshot.Bindings[0].Adapter.Framework() !=
			testdomain.FrameworkCppUTest {
		t.Fatalf("discovery snapshot = %#v", snapshot)
	}
	second := snapshot.Clone()
	snapshot.Bindings[0].Descriptor.Arguments = append(
		snapshot.Bindings[0].Descriptor.Arguments,
		"mutated",
	)
	if len(second.Bindings[0].Descriptor.Arguments) != 0 {
		t.Fatal("DiscoverySnapshot.Clone returned aliased descriptor")
	}
}

func TestServiceRunsFrameworkDiscoveryAsCancelableTaskSteps(
	t *testing.T,
) {
	fixture := newServiceFixture(t)
	execution, progress, err := fixture.service.PrepareTaskDiscovery(
		context.Background(),
		fixture.input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(progress.Steps) != 1 ||
		progress.Steps[0].Kind != task.StepTestDiscovery ||
		len(fixture.executor.steps) != 0 {
		t.Fatalf(
			"initial progress = %#v, synchronous executions=%d",
			progress,
			len(fixture.executor.steps),
		)
	}
	ctestStep := progress.Steps[0]
	if err := execution.ObserveOutput(
		context.Background(),
		ctestStep,
		task.ProcessOutput{
			Stream: "stdout",
			Data:   fixture.executor.output,
		},
	); err != nil {
		t.Fatal(err)
	}
	verdict, err := execution.Interpret(
		context.Background(),
		ctestStep,
		task.ProcessResult{ExitCode: 0},
	)
	if err != nil || verdict != task.StepVerdictSucceeded {
		t.Fatalf("CTest verdict = %q, %v", verdict, err)
	}
	progress, err = execution.AfterStep(
		context.Background(),
		ctestStep,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(progress.Steps) != 1 ||
		len(progress.Pins) != 1 ||
		progress.Snapshot != nil ||
		fixture.adapter.prepareCalls != 1 ||
		fixture.adapter.discoverCalls != 0 {
		t.Fatalf(
			"framework progress = %#v, prepare=%d discover=%d",
			progress,
			fixture.adapter.prepareCalls,
			fixture.adapter.discoverCalls,
		)
	}
	frameworkStep := progress.Steps[0]
	verdict, err = execution.Interpret(
		context.Background(),
		frameworkStep,
		task.ProcessResult{ExitCode: 0},
	)
	if err != nil || verdict != task.StepVerdictSucceeded {
		t.Fatalf("framework verdict = %q, %v", verdict, err)
	}
	progress, err = execution.AfterStep(
		context.Background(),
		frameworkStep,
	)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Snapshot == nil ||
		len(progress.Snapshot.Catalog.Items) != 1 ||
		len(progress.Snapshot.Bindings) != 1 ||
		strings.Join(*fixture.order, ",") != "commit,publish" {
		t.Fatalf(
			"terminal discovery progress = %#v, order=%#v",
			progress,
			*fixture.order,
		)
	}
}

func TestServiceKeepsPreviousCatalogWhenShowOnlyOrArtifactFails(t *testing.T) {
	failures := map[string]func(*serviceFixture){
		"show-only": func(fixture *serviceFixture) {
			fixture.executor.err = errors.New("ctest failed")
		},
		"artifact": func(fixture *serviceFixture) {
			fixture.writer.err = errors.New("artifact failed")
		},
	}
	for name, fail := range failures {
		t.Run(name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			old := validEmptyCatalog(strings.Repeat("9", 64))
			fixture.publisher.current = old
			fail(fixture)

			if _, err := fixture.service.DiscoverAfterBuild(
				context.Background(),
				fixture.input,
			); err == nil {
				t.Fatal("DiscoverAfterBuild() error = nil")
			}
			if fixture.publisher.current.Revision != old.Revision ||
				fixture.publisher.publishCalls != 0 {
				t.Fatalf("current Catalog changed = %#v", fixture.publisher.current)
			}
		})
	}
}

func TestServiceDoesNotReplacePreviousCatalogWhenPublicationFails(t *testing.T) {
	fixture := newServiceFixture(t)
	old := validEmptyCatalog(strings.Repeat("9", 64))
	fixture.publisher.current = old
	fixture.publisher.err = errors.New("transaction failed")

	if _, err := fixture.service.DiscoverAfterBuild(
		context.Background(),
		fixture.input,
	); err == nil {
		t.Fatal("DiscoverAfterBuild() error = nil")
	}
	if fixture.writer.commitCalls != 1 || fixture.publisher.publishCalls != 1 ||
		fixture.publisher.current.Revision != old.Revision {
		t.Fatalf("publication state = writer %d, publisher %#v",
			fixture.writer.commitCalls,
			fixture.publisher,
		)
	}
}

type serviceFixture struct {
	service   *Service
	input     DiscoveryInput
	executor  *fakeCTestExecutor
	writer    *fakeCatalogArtifactWriter
	publisher *fakeCatalogPublisher
	adapter   *taskServiceDiscoveryAdapter
	order     *[]string
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	root := t.TempDir()
	sourceDir := filepath.Join(root, "source")
	buildDir := filepath.Join(root, "build")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(buildDir, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(buildDir, "core-tests.exe")
	if err := os.WriteFile(executable, []byte("test executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	profileID := strings.Repeat("1", 64)
	profile := cmake.BuildProfile{
		ID: profileID, ProjectID: "core", BinaryDir: buildDir,
		Configuration: "Debug",
	}
	raw := map[string]any{
		"kind":    "ctestInfo",
		"version": map[string]any{"major": 1, "minor": 0},
		"backtraceGraph": map[string]any{
			"commands": []string{"add_test"},
			"files":    []string{filepath.Join(sourceDir, "CMakeLists.txt")},
			"nodes": []map[string]any{{
				"file": 0, "line": 1, "command": 0,
			}},
		},
		"tests": []map[string]any{{
			"name":      "core.tests",
			"config":    "Debug",
			"command":   []string{executable},
			"backtrace": 0,
			"properties": []map[string]any{{
				"name": "WORKING_DIRECTORY", "value": buildDir,
			}},
		}},
	}
	showOnly, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}

	order := []string{}
	executor := &fakeCTestExecutor{output: showOnly, order: &order}
	writer := &fakeCatalogArtifactWriter{order: &order}
	publisher := &fakeCatalogPublisher{order: &order}
	baseAdapter := &discoveryAdapter{
		framework: testdomain.FrameworkCppUTest,
		version:   "cpputest.v1",
		results: map[string]testframework.DiscoveryResult{
			"core.tests": {
				Items: []testframework.DiscoveredItem{{
					Kind: testdomain.ItemCase, LogicalName: "passes", DisplayName: "passes",
				}},
			},
		},
	}
	adapter := &taskServiceDiscoveryAdapter{
		discoveryAdapter: baseAdapter,
	}
	registry, err := testframework.NewRegistry(adapter)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := ctest.NewRunner(cmake.Installation{
		Executable:      filepath.Join(root, "cmake", "bin", "cmake.exe"),
		CTestExecutable: filepath.Join(root, "cmake", "bin", "ctest.exe"),
		Version:         "4.3.4",
		Identity:        strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ServiceConfig{
		Runner: runner, Executor: executor, Registry: registry,
		Builder: NewBuilder(), Artifacts: writer, Catalogs: publisher,
		Now: func() time.Time { return fixedGeneratedAt },
	})
	if err != nil {
		t.Fatal(err)
	}
	return &serviceFixture{
		service: service, executor: executor, writer: writer,
		publisher: publisher, adapter: adapter, order: &order,
		input: DiscoveryInput{
			TaskID: "11111111111111111111111111111111", ArtifactID: "22222222222222222222222222222222",
			Profile: profile,
			Targets: []cmake.Target{{
				ID: "target-1", Name: "core-tests", Type: "EXECUTABLE",
				ProjectID: "core", ProfileID: profileID, Configuration: "Debug",
				SourceDir: sourceDir, BuildDir: buildDir,
				ProjectSourceDir: sourceDir, ProjectBuildDir: buildDir,
				Artifacts: []string{executable},
			}},
			Mappings: []testframework.Mapping{{
				CTestName: "core.tests", Framework: testdomain.FrameworkCppUTest,
			}},
			Fingerprint: fingerprintFixture(profileID),
		},
	}
}

type taskServiceDiscoveryAdapter struct {
	*discoveryAdapter
	prepareCalls int
}

func (adapter *taskServiceDiscoveryAdapter) PrepareDiscovery(
	_ context.Context,
	descriptor ctest.ExecutionDescriptor,
) (testframework.DiscoveryExecution, error) {
	adapter.prepareCalls++
	result := adapter.results[descriptor.LogicalName]
	return testframework.DiscoveryExecution{
		Process: task.ProcessSpec{
			Executable: descriptor.Executable.Path,
			Dir:        descriptor.WorkingDirectory,
		},
		Public: task.CommandSummary{
			Executable: filepath.Base(
				descriptor.Executable.Path,
			),
			Args: []string{
				"<service-owned-discovery-invocation>",
			},
		},
		Parser: &serviceDiscoveryParser{result: result},
	}, nil
}

type serviceDiscoveryParser struct {
	result testframework.DiscoveryResult
}

func (*serviceDiscoveryParser) Feed(
	testframework.Stream,
	[]byte,
) error {
	return nil
}

func (parser *serviceDiscoveryParser) Finish(
	context.Context,
	testframework.ProcessResult,
) (testframework.DiscoveryResult, error) {
	return parser.result, nil
}

type fakeCTestExecutor struct {
	steps     []task.ExecutionStep
	output    []byte
	err       error
	order     *[]string
	maxOutput int
}

func (executor *fakeCTestExecutor) Execute(
	_ context.Context,
	step task.ExecutionStep,
	maxOutput int,
) ([]byte, error) {
	executor.steps = append(executor.steps, step)
	executor.maxOutput = maxOutput
	*executor.order = append(*executor.order, "execute")
	return append([]byte(nil), executor.output...), executor.err
}

type fakeCatalogArtifactWriter struct {
	catalog     testdomain.Catalog
	err         error
	commitCalls int
	order       *[]string
}

func (writer *fakeCatalogArtifactWriter) CommitTestCatalog(
	_ context.Context,
	taskID string,
	artifactID string,
	_ time.Time,
	catalog testdomain.Catalog,
) (task.Artifact, error) {
	writer.commitCalls++
	*writer.order = append(*writer.order, "commit")
	writer.catalog = catalog.Clone()
	if writer.err != nil {
		return task.Artifact{}, writer.err
	}
	return task.Artifact{
		ID: artifactID, TaskID: taskID, Kind: "test-catalog",
		RelativePath: taskID + "/test-catalog.json",
		Size:         1,
		SHA256:       strings.Repeat("a", 64),
		CreatedAt:    catalog.GeneratedAt,
	}, nil
}

type fakeCatalogPublisher struct {
	current      testdomain.Catalog
	err          error
	publishCalls int
	order        *[]string
}

func (publisher *fakeCatalogPublisher) PublishCatalog(
	_ context.Context,
	catalog testdomain.Catalog,
	_ task.Artifact,
) error {
	publisher.publishCalls++
	*publisher.order = append(*publisher.order, "publish")
	if publisher.err != nil {
		return publisher.err
	}
	publisher.current = catalog.Clone()
	return nil
}
