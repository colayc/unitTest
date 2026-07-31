package cpputest

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

func TestFrameworkFixtureProcessIntegration(t *testing.T) {
	executable := buildFrameworkFixture(t)
	runner := probe.NewRunner()
	adapter, err := NewAdapter(runner)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("list and exact run", func(t *testing.T) {
		descriptor := frameworkFixtureDescriptor(t, executable, "normal")
		discovery, err := adapter.Discover(context.Background(), descriptor)
		if err != nil {
			t.Fatal(err)
		}
		if len(discovery.Items) != 3 ||
			discovery.Items[0].Kind != testdomain.ItemGroup ||
			discovery.Items[1].LogicalName != "passes" ||
			discovery.Items[2].LogicalName != "fails" {
			t.Fatalf("discovery = %#v", discovery)
		}

		selected := selectedCase("1", "Fixture", "passes")
		items := []testframework.RunItem{frameworkRunItem(selected)}
		plan, err := adapter.PlanRun(context.Background(), testframework.RunInput{
			Descriptor: descriptor,
			Mode:       testframework.RunSelectionCases,
			Items:      items,
		})
		if err != nil {
			t.Fatal(err)
		}
		result := executeFixtureInvocation(
			t,
			runner,
			descriptor,
			plan.Invocations[0],
			time.Second,
		)
		parsed := parseFixtureResult(
			t,
			adapter,
			descriptor,
			items,
			result,
			testframework.ProcessExited,
		)
		if !parsed.Complete ||
			len(parsed.Cases) != 1 ||
			parsed.Cases[0].Status != testframework.CasePassed {
			t.Fatalf("parsed = %#v", parsed)
		}
	})

	t.Run("crash prefix", func(t *testing.T) {
		descriptor := frameworkFixtureDescriptor(t, executable, "crash")
		pass := selectedCase("1", "Fixture", "passes")
		crash := selectedCase("2", "Fixture", "crashes")
		items := []testframework.RunItem{
			frameworkRunItem(pass),
			frameworkRunItem(crash),
		}
		plan, err := adapter.PlanRun(context.Background(), testframework.RunInput{
			Descriptor: descriptor,
			Mode:       testframework.RunSelectionAll,
			Items:      items,
		})
		if err != nil {
			t.Fatal(err)
		}
		result := executeFixtureInvocation(
			t,
			runner,
			descriptor,
			plan.Invocations[0],
			time.Second,
		)
		parsed := parseFixtureResult(
			t,
			adapter,
			descriptor,
			items,
			result,
			testframework.ProcessCrashed,
		)
		if parsed.Complete ||
			len(parsed.Cases) != 2 ||
			parsed.Cases[0].Status != testframework.CasePassed ||
			parsed.Cases[1].Status != testframework.CaseNotRun ||
			!hasDiagnosticCategory(parsed, "test_process_crash") {
			t.Fatalf("parsed = %#v", parsed)
		}
	})

	t.Run("timeout prefix", func(t *testing.T) {
		descriptor := frameworkFixtureDescriptor(t, executable, "timeout")
		pass := selectedCase("1", "Fixture", "passes")
		timeout := selectedCase("2", "Fixture", "timesOut")
		items := []testframework.RunItem{
			frameworkRunItem(pass),
			frameworkRunItem(timeout),
		}
		plan, err := adapter.PlanRun(context.Background(), testframework.RunInput{
			Descriptor: descriptor,
			Mode:       testframework.RunSelectionAll,
			Items:      items,
		})
		if err != nil {
			t.Fatal(err)
		}
		result, runErr := runFixtureInvocation(
			context.Background(),
			runner,
			descriptor,
			plan.Invocations[0],
			500*time.Millisecond,
		)
		if !errors.Is(runErr, probe.ErrTimeout) {
			t.Fatalf("fixture timeout error = %v", runErr)
		}
		parsed := parseFixtureResult(
			t,
			adapter,
			descriptor,
			items,
			result,
			testframework.ProcessTimedOut,
		)
		if parsed.Complete ||
			len(parsed.Cases) != 2 ||
			parsed.Cases[0].Status != testframework.CasePassed ||
			parsed.Cases[1].Status != testframework.CaseNotRun ||
			!hasDiagnosticCategory(parsed, "test_timeout") {
			t.Fatalf("parsed = %#v", parsed)
		}
	})
}

func buildFrameworkFixture(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	name := "test-framework-fixture"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	output := filepath.Join(t.TempDir(), name)
	command := exec.Command(
		"go",
		"build",
		"-o",
		output,
		"./apps/test-service/cmd/test-framework-fixture",
	)
	command.Dir = root
	command.Env = os.Environ()
	if encoded, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, encoded)
	}
	return output
}

func frameworkFixtureDescriptor(
	t *testing.T,
	executable string,
	scenario string,
) ctest.ExecutionDescriptor {
	t.Helper()
	buildDir := filepath.Dir(executable)
	profile := cmake.BuildProfile{
		ID: strings.Repeat("a", 64), ProjectID: "fixture",
		BinaryDir: buildDir, Configuration: "Debug",
	}
	target := cmake.Target{
		ID: "fixture-target", Name: "test-framework-fixture", Type: "EXECUTABLE",
		ProjectID: "fixture", ProfileID: profile.ID, Configuration: "Debug",
		SourceDir: buildDir, BuildDir: buildDir,
		ProjectSourceDir: buildDir, ProjectBuildDir: buildDir,
		Artifacts: []string{executable},
	}
	state, err := cmake.SnapshotTargetArtifact(profile, target, executable)
	if err != nil {
		t.Fatal(err)
	}
	timeout := 2.0
	return ctest.ExecutionDescriptor{
		LogicalName:        "fixture." + scenario,
		TestDirectory:      buildDir,
		Configuration:      "Debug",
		TargetID:           target.ID,
		Executable:         state,
		Arguments:          []string{"--fixture-scenario", scenario},
		WorkingDirectory:   buildDir,
		Environment:        []ctest.EnvironmentEntry{},
		EnvironmentChanges: []ctest.EnvironmentModification{},
		TimeoutSeconds:     &timeout,
		Compatibility: ctest.Compatibility{
			CaseLevel: true,
			Reasons:   []ctest.Reason{},
		},
	}
}

func frameworkRunItem(value SelectedCase) testframework.RunItem {
	return testframework.RunItem{
		ItemID:            value.ItemID,
		ParentLogicalName: value.Group,
		LogicalName:       value.Name,
		Parameters:        []testdomain.Parameter{},
	}
}

func executeFixtureInvocation(
	t *testing.T,
	runner probe.Runner,
	descriptor ctest.ExecutionDescriptor,
	invocation testframework.RunInvocation,
	timeout time.Duration,
) probe.Result {
	t.Helper()
	result, err := runFixtureInvocation(
		context.Background(),
		runner,
		descriptor,
		invocation,
		timeout,
	)
	if err != nil && result.ExitCode == 0 {
		t.Fatalf("run fixture: %v", err)
	}
	return result
}

func runFixtureInvocation(
	ctx context.Context,
	runner probe.Runner,
	descriptor ctest.ExecutionDescriptor,
	invocation testframework.RunInvocation,
	timeout time.Duration,
) (probe.Result, error) {
	environment, err := discoveryEnvironment(descriptor)
	if err != nil {
		return probe.Result{ExitCode: -1}, err
	}
	return runner.Run(ctx, probe.Spec{
		Executable: descriptor.Executable.Path,
		Args:       append([]string(nil), invocation.Arguments...),
		Env:        environment,
		Dir:        descriptor.WorkingDirectory,
		Timeout:    timeout,
		MaxOutput:  DefaultResultLimits().MaxOutputBytes,
	})
}

func parseFixtureResult(
	t *testing.T,
	adapter *Adapter,
	descriptor ctest.ExecutionDescriptor,
	items []testframework.RunItem,
	result probe.Result,
	termination testframework.ProcessTermination,
) testframework.ParseResult {
	t.Helper()
	parser, err := adapter.NewParser(testframework.ParseInput{
		Descriptor: descriptor,
		Items:      items,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Feed(testframework.StreamStdout, result.Stdout); err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Feed(testframework.StreamStderr, result.Stderr); err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.Finish(testframework.ProcessResult{
		ExitCode:    result.ExitCode,
		Termination: termination,
	})
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
