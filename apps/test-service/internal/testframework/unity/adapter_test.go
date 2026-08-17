package unity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
	"unit-test-ide.local/test-service/internal/unityrunner"
)

func TestAdapterDiscoversManifestBoundCasesThroughOwnedControlFile(t *testing.T) {
	fixture := newAdapterFixture(t)
	allocator := &fakeControlAllocator{
		root: fixture.controlDir,
		data: [][]byte{readUnityTestdata(t, "list.jsonl")},
	}
	runner := &fakeRunner{result: probe.Result{ExitCode: 0}}
	adapter, err := NewAdapter(runner, allocator)
	if err != nil {
		t.Fatal(err)
	}

	capabilities, err := adapter.Verify(context.Background(), fixture.descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if !capabilities.CanRunContainer ||
		!capabilities.CanDiscoverCases ||
		!capabilities.CanRunCase ||
		!capabilities.CanReportSkipped ||
		!capabilities.CanReportSourceLocation ||
		!capabilities.CanReportMockDetails {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	discovery, err := adapter.Discover(context.Background(), fixture.descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if discovery.Partial || len(discovery.Diagnostics) != 0 ||
		len(discovery.Items) != 3 {
		t.Fatalf("discovery = %#v", discovery)
	}
	group, first, second := discovery.Items[0], discovery.Items[1], discovery.Items[2]
	if group.Kind != testdomain.ItemGroup ||
		group.LogicalName != "testdata/basic.c" ||
		first.Kind != testdomain.ItemCase ||
		first.ParentLogicalName != group.LogicalName ||
		first.LogicalName != "test_adds_numbers" ||
		first.DisplayName != "test_adds_numbers" ||
		first.SourceLocation == nil ||
		!strings.HasPrefix(first.SourceLocation.URI, "file:") ||
		first.SourceLocation.Line != 16 ||
		!first.SourceLocation.Navigable ||
		first.SourceLocation.Provenance != "framework-manifest" ||
		second.LogicalName != "test_handles_zero" {
		t.Fatalf("items = %#v", discovery.Items)
	}
	if len(runner.specs) != 1 || len(allocator.files) != 1 {
		t.Fatalf("runs=%d allocations=%d", len(runner.specs), len(allocator.files))
	}
	wantSuffix := []string{
		"--utide-protocol", ContractVersion,
		"--utide-mode", "list",
		"--utide-result", allocator.files[0].path,
	}
	gotArgs := runner.specs[0].Args
	if !reflect.DeepEqual(gotArgs, wantSuffix) {
		t.Fatalf("control argv = %#v", gotArgs)
	}
	if runner.specs[0].Executable != fixture.descriptor.Executable.Path ||
		runner.specs[0].Dir != fixture.descriptor.WorkingDirectory {
		t.Fatalf("probe spec = %#v", runner.specs[0])
	}
}

func TestAdapterPreparesTaskOwnedDiscovery(t *testing.T) {
	t.Setenv("UNIT_TEST_SERVICE_TOKEN", "must-not-reach-target")
	t.Setenv("UTIDE_PRIVATE_VALUE", "must-not-reach-target")
	fixture := newAdapterFixture(t)
	allocator := &fakeControlAllocator{
		root: fixture.controlDir,
		data: [][]byte{readUnityTestdata(t, "list.jsonl")},
	}
	runner := &fakeRunner{}
	adapter, err := NewAdapter(runner, allocator)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := adapter.PrepareDiscovery(
		context.Background(),
		fixture.descriptor,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.specs) != 0 ||
		execution.Process.Executable !=
			fixture.descriptor.Executable.Path ||
		execution.Process.Dir != fixture.descriptor.WorkingDirectory ||
		len(allocator.files) != 1 ||
		execution.Parser == nil {
		t.Fatalf(
			"task discovery execution = %#v, probe calls=%d",
			execution,
			len(runner.specs),
		)
	}
	if strings.Contains(
		strings.Join(execution.Public.Args, "\x00"),
		allocator.files[0].path,
	) {
		t.Fatalf(
			"public args exposed control path: %#v",
			execution.Public.Args,
		)
	}
	for _, entry := range execution.Process.Env {
		upper := strings.ToUpper(entry)
		if strings.HasPrefix(upper, "UTIDE_") ||
			strings.HasPrefix(upper, "UNIT_TEST_IDE_") ||
			strings.HasPrefix(
				upper,
				"UNIT_TEST_SERVICE_TOKEN=",
			) {
			t.Fatalf("service environment leaked: %q", entry)
		}
	}
	if err := execution.Parser.Feed(
		testframework.StreamStdout,
		[]byte("framework output"),
	); err != nil {
		t.Fatal(err)
	}
	discovery, err := execution.Parser.Finish(
		context.Background(),
		testframework.ProcessResult{
			ExitCode:    0,
			Termination: testframework.ProcessExited,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Items) != 3 ||
		discovery.Items[0].Kind != testdomain.ItemGroup ||
		discovery.Items[1].Kind != testdomain.ItemCase {
		t.Fatalf("task discovery = %#v", discovery)
	}
}

func TestAdapterRejectsListAndManifestMismatches(t *testing.T) {
	tests := map[string]func([]byte) []byte{
		"magic": func(data []byte) []byte {
			return []byte(strings.Replace(
				string(data),
				`"magic":"unit-test-ide"`,
				`"magic":"other"`,
				1,
			))
		},
		"protocol": func(data []byte) []byte {
			return []byte(strings.Replace(
				string(data),
				`"protocol":"utide.runner.v1"`,
				`"protocol":"utide.runner.v0"`,
				1,
			))
		},
		"identity": func(data []byte) []byte {
			return []byte(strings.Replace(
				string(data),
				`"identity":"test_adds_numbers"`,
				`"identity":"test_handles_zero"`,
				1,
			))
		},
		"runner fingerprint": func(data []byte) []byte {
			return []byte(strings.Replace(
				string(data),
				`"generatorVersion":"1.0.0"`,
				`"generatorVersion":"9.0.0"`,
				1,
			))
		},
		"manifest fingerprint": func(data []byte) []byte {
			return []byte(strings.Replace(
				string(data),
				fixtureManifestSHA256,
				strings.Repeat("0", 64),
				1,
			))
		},
		"missing flushed record": func(data []byte) []byte {
			newline := strings.IndexByte(string(data), '\n')
			return append([]byte(nil), data[:newline+1]...)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newAdapterFixture(t)
			data := mutate(readUnityTestdata(t, "list.jsonl"))
			allocator := &fakeControlAllocator{
				root: fixture.controlDir,
				data: [][]byte{data},
			}
			adapter, err := NewAdapter(
				&fakeRunner{result: probe.Result{ExitCode: 0}},
				allocator,
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Discover(
				context.Background(),
				fixture.descriptor,
			); !errors.Is(err, ErrDiscoveryFailed) {
				t.Fatalf("Discover() error = %v", err)
			}
		})
	}
}

func TestAdapterRejectsExecutableAndManifestMutationAcrossDiscovery(t *testing.T) {
	tests := map[string]func(adapterFixture){
		"executable": func(fixture adapterFixture) {
			if err := os.WriteFile(
				fixture.descriptor.Executable.Path,
				[]byte("mutated executable"),
				0o700,
			); err != nil {
				panic(err)
			}
		},
		"manifest": func(fixture adapterFixture) {
			if err := os.WriteFile(
				fixture.manifestPath,
				append(fixture.manifestJSON, ' '),
				0o600,
			); err != nil {
				panic(err)
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newAdapterFixture(t)
			runner := &fakeRunner{
				result: probe.Result{ExitCode: 0},
				after: func() {
					mutate(fixture)
				},
			}
			adapter, err := NewAdapter(
				runner,
				&fakeControlAllocator{
					root: fixture.controlDir,
					data: [][]byte{readUnityTestdata(t, "list.jsonl")},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Discover(
				context.Background(),
				fixture.descriptor,
			); err == nil {
				t.Fatal("Discover() error = nil")
			}
		})
	}
}

func TestAdapterRejectsReservedControlArgumentsAndUnownedPaths(t *testing.T) {
	fixture := newAdapterFixture(t)
	for _, argument := range []string{
		"--utide-protocol",
		"--utide-mode=run",
		"--utide-case",
		"--utide-result=C:\\client.jsonl",
	} {
		t.Run(argument, func(t *testing.T) {
			descriptor := fixture.descriptor
			descriptor.Arguments = append(
				append([]string(nil), descriptor.Arguments...),
				argument,
			)
			adapter, err := NewAdapter(
				&fakeRunner{},
				&fakeControlAllocator{root: fixture.controlDir},
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Verify(
				context.Background(),
				descriptor,
			); !errors.Is(err, ErrReservedArguments) {
				t.Fatalf("Verify() error = %v", err)
			}
		})
	}

	descriptor := fixture.descriptor
	descriptor.Arguments = []string{"--project-argument"}
	adapter, err := NewAdapter(
		&fakeRunner{},
		&fakeControlAllocator{root: fixture.controlDir},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Verify(
		context.Background(),
		descriptor,
	); !errors.Is(err, ErrIncompatibleDescriptor) {
		t.Fatalf("ordinary argument error = %v", err)
	}

	adapter, err = NewAdapter(
		&fakeRunner{},
		&fakeControlAllocator{relative: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	items := fixture.runItems()
	if _, err := adapter.PlanRun(context.Background(), testframework.RunInput{
		Descriptor: fixture.descriptor,
		Mode:       testframework.RunSelectionCases,
		Items:      items[:1],
	}); !errors.Is(err, ErrInvalidRunPlan) {
		t.Fatalf("PlanRun() error = %v", err)
	}
}

const fixtureManifestSHA256 = "8fc8771747f61531a21ec26fc7138506849ba4a7b4396557e39285ac832dfb81"

type adapterFixture struct {
	descriptor   ctest.ExecutionDescriptor
	manifest     unityrunner.Manifest
	manifestJSON []byte
	manifestPath string
	controlDir   string
}

func newAdapterFixture(t *testing.T) adapterFixture {
	t.Helper()
	root := t.TempDir()
	sourceDir := filepath.Join(root, "source")
	buildDir := filepath.Join(root, "build")
	controlDir := filepath.Join(root, "artifacts", "task")
	for _, directory := range []string{
		filepath.Join(sourceDir, "testdata"),
		filepath.Join(buildDir, "bin"),
		controlDir,
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	source := readUnityRunnerTestdata(t, "basic.c")
	if err := os.WriteFile(
		filepath.Join(sourceDir, "testdata", "basic.c"),
		source,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	manifestJSON := readUnityRunnerTestdata(t, "basic.manifest.golden.json")
	var manifest unityrunner.Manifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		t.Fatal(err)
	}
	logicalName := "core.unity"
	sum := sha256.Sum256([]byte(logicalName))
	manifestPath := filepath.Join(
		buildDir,
		".unit-test-ide",
		hex.EncodeToString(sum[:]),
		"manifest.json",
	)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifestJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(buildDir, "bin", "unity-tests.exe")
	if err := os.WriteFile(
		executable,
		[]byte("unity test executable"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	profile := cmake.BuildProfile{
		ID:            strings.Repeat("a", 64),
		ProjectID:     "core",
		BinaryDir:     buildDir,
		Configuration: "Debug",
	}
	target := cmake.Target{
		ID: "unity-target", Name: "unity-tests", Type: "EXECUTABLE",
		ProjectID: "core", ProfileID: profile.ID, Configuration: "Debug",
		SourceDir: sourceDir, BuildDir: buildDir,
		ProjectSourceDir: sourceDir, ProjectBuildDir: buildDir,
		Artifacts: []string{executable},
	}
	state, err := cmake.SnapshotTargetArtifact(profile, target, executable)
	if err != nil {
		t.Fatal(err)
	}
	timeout := 10.0
	return adapterFixture{
		descriptor: ctest.ExecutionDescriptor{
			LogicalName:      logicalName,
			TestDirectory:    buildDir,
			SourceDirectory:  sourceDir,
			Configuration:    "Debug",
			TargetID:         target.ID,
			Executable:       state,
			Arguments:        []string{},
			WorkingDirectory: buildDir,
			Environment: []ctest.EnvironmentEntry{{
				Name: "FIXTURE_MODE", Value: "fixed",
			}},
			EnvironmentChanges: []ctest.EnvironmentModification{{
				Name: "PATH", Operation: "path_list_prepend", Value: buildDir,
			}},
			TimeoutSeconds: &timeout,
			Labels: []string{
				"utide.framework.unity",
				ContractVersion,
			},
			Compatibility: ctest.Compatibility{
				CaseLevel: true,
				Reasons:   []ctest.Reason{},
			},
		},
		manifest:     manifest,
		manifestJSON: manifestJSON,
		manifestPath: manifestPath,
		controlDir:   controlDir,
	}
}

func (fixture adapterFixture) runItems() []testframework.RunItem {
	result := make([]testframework.RunItem, len(fixture.manifest.Cases))
	for index, testCase := range fixture.manifest.Cases {
		result[index] = testframework.RunItem{
			ItemID: testdomain.ID(
				"utid-v1-" + strings.Repeat(
					string(rune('1'+index)),
					64,
				),
			),
			ParentLogicalName: testCase.Location.Path,
			LogicalName:       testCase.Identity,
			Parameters:        caseParameters(testCase),
		}
	}
	return result
}

func readUnityTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func readUnityRunnerTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(
		filepath.Join("..", "..", "unityrunner", "testdata", name),
	)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type fakeRunner struct {
	result probe.Result
	err    error
	specs  []probe.Spec
	after  func()
}

func (runner *fakeRunner) Run(
	_ context.Context,
	spec probe.Spec,
) (probe.Result, error) {
	spec.Args = append([]string(nil), spec.Args...)
	spec.Env = append([]string(nil), spec.Env...)
	runner.specs = append(runner.specs, spec)
	if runner.after != nil {
		runner.after()
	}
	return runner.result, runner.err
}

type fakeControlFile struct {
	path string
	data []byte
	err  error
}

func (file *fakeControlFile) Path() string {
	return file.path
}

func (file *fakeControlFile) Read(
	ctx context.Context,
	limit int64,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if file.err != nil {
		return nil, file.err
	}
	if int64(len(file.data)) > limit {
		return nil, ErrProtocolLimit
	}
	return append([]byte(nil), file.data...), nil
}

type fakeControlAllocator struct {
	root     string
	data     [][]byte
	files    []*fakeControlFile
	relative bool
}

func (allocator *fakeControlAllocator) Allocate(
	ctx context.Context,
) (testframework.ControlFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	index := len(allocator.files)
	path := filepath.Join(
		allocator.root,
		"result-"+string(rune('0'+index))+".jsonl",
	)
	if allocator.relative {
		path = "client-result.jsonl"
	}
	file := &fakeControlFile{path: path}
	if index < len(allocator.data) {
		file.data = append([]byte(nil), allocator.data[index]...)
	}
	allocator.files = append(allocator.files, file)
	return file, nil
}
