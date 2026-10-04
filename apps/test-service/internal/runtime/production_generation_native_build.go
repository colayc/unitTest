package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/coveragellvm"
	"unit-test-ide.local/test-service/internal/processcontrol"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgenvalidate"
	"unit-test-ide.local/test-service/internal/workspace"
)

func productionValidationBuildFactory(installation cmake.Installation) productionValidationBuildPrepareFunc {
	return func(ctx context.Context, registration productionValidationPlanRegistration, roots testgenvalidate.Roots) (productionValidationBuildPreparation, error) {
		return prepareProductionValidationBuild(ctx, installation, registration, roots)
	}
}

func prepareProductionValidationBuild(ctx context.Context, installation cmake.Installation, registration productionValidationPlanRegistration, roots testgenvalidate.Roots) (result productionValidationBuildPreparation, err error) {
	if ctx == nil || ctx.Err() != nil || roots.Source == "" || roots.Build == "" || roots.Artifacts == "" {
		return result, errProductionValidationUnavailable
	}
	isolated, err := workspace.OpenRoot(roots.Source)
	if err != nil {
		return result, errProductionValidationUnavailable
	}
	profile := registration.build.profile
	originalBuild := profile.BinaryDir
	profile.BinaryDir = roots.Build
	targets, err := rebaseProductionValidationTargets(registration.build.targets, registration.target.analysis.WorkspaceRoot, roots.Source, originalBuild, roots.Build)
	if err != nil {
		return result, err
	}
	testTarget, ok := productionTargetByID(targets, registration.target.testTargetID)
	if !ok || testTarget.Type != "EXECUTABLE" || testTarget.Name != registration.target.renderTarget.TestTarget {
		return result, errProductionValidationUnavailable
	}
	testBinary, ok := singleProductionTargetArtifact(testTarget)
	if !ok {
		return result, errProductionValidationUnavailable
	}
	instrumentationRoot := filepath.Join(roots.Artifacts, "instrumentation")
	guard, err := pinOwnerOnlyDirectory(instrumentationRoot)
	if err != nil {
		return result, errProductionValidationUnavailable
	}
	defer func() { err = errors.Join(err, guard.Close()) }()
	toolset, err := coveragellvm.PinToolset(registration.build.toolchain)
	if err != nil {
		return result, errProductionValidationUnavailable
	}
	defer func() { err = errors.Join(err, toolset.Close()) }()
	instrumentation, err := coveragellvm.PlanInstrumentation(toolset, coveragellvm.BuildRequest{TaskRoot: instrumentationRoot})
	if err != nil {
		return result, errProductionValidationUnavailable
	}
	plan, err := build.Plan(build.PlanInput{
		Installation: installation, WorkspaceRoot: isolated, Project: registration.build.project,
		Profile: profile, Toolchain: registration.build.toolchain, Targets: targets,
		TargetIDs: []string{registration.target.testTargetID}, Jobs: registration.target.concurrency, Configure: true,
		Coverage: &build.CoverageOptions{
			BinaryDir:                  roots.Build,
			TopLevelInclude:            cmake.FingerprintFile{Path: instrumentation.Instrumentation.IncludePath, Identity: instrumentation.Instrumentation.Fingerprint, SHA256: instrumentation.Instrumentation.SHA256},
			InstrumentationFingerprint: instrumentation.Instrumentation.Fingerprint, ToolsetIdentity: toolset.Identity(),
		},
	})
	if err != nil || len(plan.Steps) != 2 || plan.Steps[0].Kind != task.StepConfigure || plan.Steps[1].Kind != task.StepBuild {
		return result, errProductionValidationUnavailable
	}
	configure, compile := productionProcessSpec(plan.Steps[0].Process), productionProcessSpec(plan.Steps[1].Process)
	if !reflect.DeepEqual(configure.Env, compile.Env) {
		return result, errProductionValidationUnavailable
	}
	tools := make(map[string]string)
	for _, spec := range []processcontrol.Spec{configure, compile} {
		if err := addProductionValidationSpecTools(tools, spec); err != nil {
			return result, err
		}
	}
	return productionValidationBuildPreparation{
		configure: configure, compile: compile, profile: profile, testBinary: testBinary,
		environment: append([]string(nil), configure.Env...), tools: tools,
	}, nil
}

func rebaseProductionValidationTargets(values []cmake.Target, sourceFrom, sourceTo, buildFrom, buildTo string) ([]cmake.Target, error) {
	if sourceFrom == "" || sourceTo == "" || buildFrom == "" || buildTo == "" || len(values) == 0 {
		return nil, errProductionValidationUnavailable
	}
	result := cmake.CloneTargets(values)
	for index := range result {
		target := &result[index]
		var ok bool
		if target.SourceDir, ok = rebaseProductionValidationPath(target.SourceDir, sourceFrom, sourceTo); !ok {
			return nil, errProductionValidationUnavailable
		}
		if target.ProjectSourceDir, ok = rebaseProductionValidationPath(target.ProjectSourceDir, sourceFrom, sourceTo); !ok {
			return nil, errProductionValidationUnavailable
		}
		if target.BuildDir, ok = rebaseProductionValidationPath(target.BuildDir, buildFrom, buildTo); !ok {
			return nil, errProductionValidationUnavailable
		}
		if target.ProjectBuildDir, ok = rebaseProductionValidationPath(target.ProjectBuildDir, buildFrom, buildTo); !ok {
			return nil, errProductionValidationUnavailable
		}
		for artifactIndex := range target.Artifacts {
			if target.Artifacts[artifactIndex], ok = rebaseProductionValidationPath(target.Artifacts[artifactIndex], buildFrom, buildTo); !ok {
				return nil, errProductionValidationUnavailable
			}
		}
		for sourceIndex := range target.Sources {
			from, to := sourceFrom, sourceTo
			if target.Sources[sourceIndex].Generated {
				from, to = buildFrom, buildTo
			}
			if target.Sources[sourceIndex].Path, ok = rebaseProductionValidationPath(target.Sources[sourceIndex].Path, from, to); !ok {
				return nil, errProductionValidationUnavailable
			}
		}
		for unitIndex := range target.CompileUnits {
			unit := &target.CompileUnits[unitIndex]
			from, to := sourceFrom, sourceTo
			if unit.Generated {
				from, to = buildFrom, buildTo
			}
			if unit.Source, ok = rebaseProductionValidationPath(unit.Source, from, to); !ok {
				return nil, errProductionValidationUnavailable
			}
			for includeIndex := range unit.Includes {
				if rebased, matched := rebaseProductionValidationPath(unit.Includes[includeIndex], sourceFrom, sourceTo); matched {
					unit.Includes[includeIndex] = rebased
				} else if rebased, matched = rebaseProductionValidationPath(unit.Includes[includeIndex], buildFrom, buildTo); matched {
					unit.Includes[includeIndex] = rebased
				}
			}
		}
	}
	return result, nil
}

func rebaseProductionValidationPath(path, from, to string) (string, bool) {
	if path == "" || !filepath.IsAbs(path) || !filepath.IsAbs(from) || !filepath.IsAbs(to) {
		return "", false
	}
	relative, err := filepath.Rel(from, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", false
	}
	return filepath.Clean(filepath.Join(to, relative)), true
}

func singleProductionTargetArtifact(target cmake.Target) (string, bool) {
	result := ""
	for _, artifact := range target.Artifacts {
		if artifact == "" || !filepath.IsAbs(artifact) || filepath.Clean(artifact) != artifact {
			return "", false
		}
		if result != "" && !sameProductionPath(result, artifact) {
			return "", false
		}
		result = artifact
	}
	return result, result != ""
}

func addProductionValidationSpecTools(tools map[string]string, spec processcontrol.Spec) error {
	paths := make([]string, 0, 1+len(spec.LaunchPlan)+len(spec.LaunchInputs))
	paths = append(paths, spec.Executable)
	paths = append(paths, spec.LaunchPlan...)
	for _, state := range spec.LaunchInputs {
		paths = append(paths, state.Path)
	}
	for _, path := range paths {
		if err := addProductionValidationTool(tools, path); err != nil {
			return err
		}
	}
	return nil
}

func directProductionValidationDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}
