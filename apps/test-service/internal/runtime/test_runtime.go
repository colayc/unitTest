package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testcontrol"
	"unit-test-ide.local/test-service/internal/testdiscovery"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
	"unit-test-ide.local/test-service/internal/testframework/cpputest"
	"unit-test-ide.local/test-service/internal/testframework/unity"
	"unit-test-ide.local/test-service/internal/testrun"
	"unit-test-ide.local/test-service/internal/workspace"
)

type testCoordinatorConfig struct {
	Build         runtimeCoordinator
	Tasks         runtimeManager
	Store         runtimeStore
	Artifacts     runtimeArtifacts
	Probe         probe.Runner
	Installation  cmake.Installation
	WorkspaceRoot workspace.Root
	BuildDataRoot string
	CoverageRoot  string
	ControlRoot   string
	Clock         task.Clock
	NewID         task.IDGenerator
}

type buildPlanPreparer interface {
	PreparePlan(
		context.Context,
		build.StartRequest,
	) (*build.PreparedPlan, error)
}

func newRuntimeTestCoordinator(
	config testCoordinatorConfig,
) (runtimeTestCoordinator, io.Closer, error) {
	preparer, ok := config.Build.(buildPlanPreparer)
	if !ok || config.Tasks == nil || config.Store == nil ||
		config.Artifacts == nil || config.Probe == nil ||
		config.WorkspaceRoot.ID == "" ||
		!absoluteCleanRuntimePath(config.BuildDataRoot) ||
		config.Installation.Identity == "" ||
		config.NewID == nil {
		return nil, nil, task.ErrInvalidArgument
	}
	ctestRunner, err := ctest.NewRunner(config.Installation)
	if err != nil {
		return nil, nil, err
	}
	controls, err := testcontrol.NewAllocator(config.ControlRoot)
	if err != nil {
		return nil, nil, err
	}
	fail := func(cause error) (
		runtimeTestCoordinator,
		io.Closer,
		error,
	) {
		return nil, nil, errors.Join(cause, controls.Close())
	}
	cpputestAdapter, err := cpputest.NewAdapter(config.Probe)
	if err != nil {
		return fail(err)
	}
	unityAdapter, err := unity.NewAdapter(
		config.Probe,
		controls,
	)
	if err != nil {
		return fail(err)
	}
	registry, err := testframework.NewRegistry(
		cpputestAdapter,
		unityAdapter,
	)
	if err != nil {
		return fail(err)
	}
	service, err := testdiscovery.NewService(
		testdiscovery.ServiceConfig{
			Runner:    ctestRunner,
			Executor:  ctestProbeExecutor{runner: config.Probe},
			Registry:  registry,
			Builder:   testdiscovery.NewBuilder(),
			Artifacts: config.Artifacts,
			Catalogs:  config.Store,
			Limits:    ctest.DefaultLimits(),
			Now: func() time.Time {
				return clockNow(config.Clock)
			},
		},
	)
	if err != nil {
		return fail(err)
	}
	refresher, err := testrun.NewTaskCatalogRefresher(
		service,
		newTaskDiscoveryInputFactory(config),
	)
	if err != nil {
		return fail(err)
	}
	prepare := func(
		ctx context.Context,
		request testrun.BuildRequest,
	) (testrun.PreparedBuild, error) {
		return preparer.PreparePlan(ctx, build.StartRequest{
			IdempotencyKey:      request.IdempotencyKey,
			WorkspaceGeneration: request.WorkspaceGeneration,
			ProjectID:           request.ProjectID,
			BuildProfileID:      request.BuildProfileID,
			TargetIDs: append(
				[]string(nil),
				request.TargetIDs...,
			),
			Jobs:    request.Jobs,
			Timeout: request.Timeout,
		})
	}
	coordinator, err := testrun.NewCoordinator(
		testrun.CoordinatorConfig{
			PrepareBuild: prepare,
			Catalogs:     config.Store,
			Refresher:    refresher,
			Tasks:        config.Tasks,
			Runs:         config.Store,
			Runner:       ctestRunner,
			Limits: testdomain.Limits{
				MaxSelectionSize: 100_000,
			},
		},
	)
	if err != nil {
		return fail(err)
	}
	return coordinator, controls, nil
}

type ctestProbeExecutor struct {
	runner probe.Runner
}

func (executor ctestProbeExecutor) Execute(
	ctx context.Context,
	step task.ExecutionStep,
	maximum int,
) ([]byte, error) {
	if ctx == nil || executor.runner == nil ||
		maximum < 1 || step.Process.Executable == "" ||
		len(step.Process.Batch) != 0 ||
		len(step.Process.EnvUnset) != 0 {
		return nil, task.ErrInvalidArgument
	}
	result, err := executor.runner.Run(
		ctx,
		probe.Spec{
			Executable: step.Process.Executable,
			Args: append(
				[]string(nil),
				step.Process.Args...,
			),
			Env: append(
				[]string(nil),
				step.Process.Env...,
			),
			Dir:       step.Process.Dir,
			Timeout:   adapterTimeout,
			MaxOutput: maximum,
		},
	)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, task.ErrInvalidArgument
	}
	return append([]byte(nil), result.Stdout...), nil
}

func newTaskDiscoveryInputFactory(
	config testCoordinatorConfig,
) testrun.TaskDiscoveryInputFactory {
	const maxGenerationRebinds = 3
	return func(
		ctx context.Context,
		request testrun.RefreshRequest,
	) (testdiscovery.DiscoveryInput, error) {
		if ctx == nil || request.TaskID == "" ||
			request.WorkspaceGeneration == "" ||
			request.Project.ID == "" ||
			request.Profile.ID == "" ||
			request.Profile.ProjectID != request.Project.ID ||
			request.Toolchain.ID == "" {
			return testdiscovery.DiscoveryInput{},
				task.ErrInvalidArgument
		}
		configurationID := request.Profile.ID
		if request.ConfigurationID != "" {
			configurationID = request.ConfigurationID
		}
		buildConfiguration, err :=
			config.Store.GetBuildConfiguration(
				ctx,
				config.WorkspaceRoot.ID,
				request.Project.ID,
				configurationID,
			)
		if err != nil {
			return testdiscovery.DiscoveryInput{}, err
		}
		buildDirectory, err := runtimeBuildDirectoryIdentityWithCoverage(
			config.WorkspaceRoot.NativePath,
			config.BuildDataRoot,
			config.CoverageRoot,
			request.Profile.BinaryDir,
		)
		if err != nil {
			return testdiscovery.DiscoveryInput{}, err
		}
		if buildConfiguration.CMakeIdentity !=
			config.Installation.Identity ||
			buildConfiguration.BuildDirectory !=
				buildDirectory {
			return testdiscovery.DiscoveryInput{},
				task.ErrConflict
		}
		targets := cloneRuntimeTestTargets(request.Targets)
		var targetErr error
		if request.ConfigurationID == "" {
			targets, targetErr = config.Build.Targets(
				ctx,
				build.TargetsRequest{
					WorkspaceGeneration: request.
						WorkspaceGeneration,
					ProjectID:      request.Project.ID,
					BuildProfileID: request.Profile.ID,
				},
			)
		} else if len(targets) == 0 {
			return testdiscovery.DiscoveryInput{}, task.ErrConflict
		}
		err = targetErr
		generation := request.WorkspaceGeneration
		for rebinds := 0; errors.Is(err, build.ErrWorkspaceChanged) &&
			rebinds < maxGenerationRebinds; rebinds++ {
			// CMake configure/build may publish the File API between the
			// prepared build checkpoint and this refresh. Rebind to a fresh
			// snapshot, but only when the semantic project/profile identity
			// is unchanged; an actual workspace change remains fail-closed.
			current, inspectErr := config.Build.Inspect(ctx)
			if inspectErr != nil {
				return testdiscovery.DiscoveryInput{}, err
			}
			profileMatches := false
			for _, candidate := range current.Profiles {
				if candidate.ID == request.Profile.ID {
					profileMatches = candidate == request.Profile
					break
				}
			}
			projectMatches := false
			for _, candidate := range current.Projects {
				if candidate.ID == request.Project.ID {
					projectMatches = reflect.DeepEqual(candidate, request.Project)
					break
				}
			}
			if !profileMatches || !projectMatches || current.Generation == "" {
				return testdiscovery.DiscoveryInput{}, err
			}
			if request.Toolchain.ID != "" {
				toolchainMatches := false
				for _, candidate := range current.Toolchains {
					if candidate.ID == request.Toolchain.ID {
						toolchainMatches = reflect.DeepEqual(candidate, request.Toolchain)
						break
					}
				}
				if !toolchainMatches {
					return testdiscovery.DiscoveryInput{}, err
				}
			}
			targets, err = config.Build.Targets(
				ctx,
				build.TargetsRequest{
					WorkspaceGeneration: current.Generation,
					ProjectID:           request.Project.ID,
					BuildProfileID:      request.Profile.ID,
				},
			)
			generation = current.Generation
		}
		if err != nil {
			return testdiscovery.DiscoveryInput{}, err
		}
		testConfigJSON, err := json.Marshal(
			request.Project.Tests,
		)
		if err != nil {
			return testdiscovery.DiscoveryInput{},
				task.ErrInvalidArgument
		}
		testConfigHash := sha256.Sum256(testConfigJSON)
		emptySemanticHash := sha256.Sum256(nil)
		mappings := make(
			[]testframework.Mapping,
			len(request.Project.Tests.Containers),
		)
		for index, mapping := range request.Project.Tests.Containers {
			framework, ok := runtimeTestFramework(
				mapping.Framework,
			)
			if !ok {
				return testdiscovery.DiscoveryInput{},
					task.ErrInvalidArgument
			}
			mappings[index] = testframework.Mapping{
				CTestName: mapping.CTestName,
				Framework: framework,
			}
		}
		return testdiscovery.DiscoveryInput{
			TaskID:     request.TaskID,
			ArtifactID: config.NewID(),
			Profile:    request.Profile,
			Targets:    cloneRuntimeTestTargets(targets),
			Helpers:    map[string]testframework.Declaration{},
			Mappings:   mappings,
			Fingerprint: testdiscovery.Fingerprint{
				WorkspaceGeneration: generation,
				TestConfigurationSHA256: hex.EncodeToString(
					testConfigHash[:],
				),
				CMakeInstallationIdentity: config.Installation.Identity,
				BuildProfileIdentity:      request.Profile.ID,
				FileAPIReplyIdentity: buildConfiguration.
					FileAPIIdentity,
				CTestSemanticSHA256: hex.EncodeToString(
					emptySemanticHash[:],
				),
				Executables:      []cmake.FingerprintFile{},
				Manifests:        []cmake.FingerprintFile{},
				AdapterContracts: []testdiscovery.AdapterContract{},
			},
		}, nil
	}
}

func cloneRuntimeTestTargets(
	values []cmake.Target,
) []cmake.Target {
	result := make([]cmake.Target, len(values))
	for index, value := range values {
		result[index] = value
		result[index].Artifacts = append(
			[]string{},
			value.Artifacts...,
		)
	}
	return result
}

func runtimeBuildDirectoryIdentity(
	workspaceRoot string,
	serviceDataRoot string,
	path string,
) (string, error) {
	return runtimeBuildDirectoryIdentityWithCoverage(workspaceRoot, serviceDataRoot, "", path)
}

func runtimeBuildDirectoryIdentityWithCoverage(
	workspaceRoot string,
	serviceDataRoot string,
	coverageRoot string,
	path string,
) (string, error) {
	if !absoluteCleanRuntimePath(workspaceRoot) ||
		!absoluteCleanRuntimePath(serviceDataRoot) ||
		!absoluteCleanRuntimePath(path) {
		return "", task.ErrInvalidArgument
	}
	for _, root := range []struct {
		prefix string
		path   string
	}{
		{prefix: "service", path: serviceDataRoot},
		{prefix: "workspace", path: workspaceRoot},
	} {
		relative, err := filepath.Rel(root.path, path)
		if err == nil && relative != ".." &&
			!strings.HasPrefix(
				relative,
				".."+string(filepath.Separator),
			) {
			return root.prefix + "/" +
				filepath.ToSlash(relative), nil
		}
	}
	if absoluteCleanRuntimePath(coverageRoot) {
		relative, err := filepath.Rel(coverageRoot, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "coverage/" + filepath.ToSlash(relative), nil
		}
	}
	return "", task.ErrInvalidArgument
}

func absoluteCleanRuntimePath(value string) bool {
	return value != "" && filepath.IsAbs(value) &&
		filepath.Clean(value) == value &&
		!strings.ContainsRune(value, '\x00')
}

func runtimeTestFramework(
	value workspace.Framework,
) (testdomain.Framework, bool) {
	switch value {
	case workspace.FrameworkCppUTest:
		return testdomain.FrameworkCppUTest, true
	case workspace.FrameworkUnity:
		return testdomain.FrameworkUnity, true
	default:
		return "", false
	}
}

func runtimeTestJobs() int {
	jobs := runtime.NumCPU()
	if jobs < 1 {
		return 1
	}
	if jobs > 256 {
		return 256
	}
	return jobs
}

func runtimeTestConcurrency() int {
	value := runtime.GOMAXPROCS(0)
	if value < 1 {
		return 1
	}
	if value > maxTestWorkers {
		return maxTestWorkers
	}
	return value
}

var _ task.TestCatalogRepository = (runtimeStore)(nil)
var _ task.TestRunRepository = (runtimeStore)(nil)
