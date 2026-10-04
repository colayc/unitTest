package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/coveragellvm"
	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
	"unit-test-ide.local/test-service/internal/coveragenormalize"
	coverageparserllvm "unit-test-ide.local/test-service/internal/coverageparser/llvm"
	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/processcontrol"
	"unit-test-ide.local/test-service/internal/testgenvalidate"
)

func productionValidationCoverageFactory(platform string) productionValidationCoveragePrepareFunc {
	return func(ctx context.Context, registration productionValidationPlanRegistration, build productionValidationBuildPreparation, roots testgenvalidate.Roots) (productionValidationCoveragePreparation, error) {
		return prepareProductionValidationCoverage(ctx, platform, registration, build, roots)
	}
}

func prepareProductionValidationCoverage(ctx context.Context, platform string, registration productionValidationPlanRegistration, prepared productionValidationBuildPreparation, roots testgenvalidate.Roots) (result productionValidationCoveragePreparation, err error) {
	if ctx == nil || ctx.Err() != nil || len(registration.baseline) == 0 {
		return result, errProductionValidationUnavailable
	}
	profiles := filepath.Join(roots.Artifacts, "profiles")
	profileFiles, err := productionValidationProfiles(profiles)
	if err != nil {
		return result, err
	}
	toolset, err := coveragellvm.PinToolset(registration.build.toolchain)
	if err != nil {
		return result, errProductionValidationUnavailable
	}
	defer func() { err = errors.Join(err, toolset.Close()) }()
	binary, err := newProductionValidationTrustedPath(prepared.testBinary, false)
	if err != nil {
		return result, err
	}
	profileRoot, err := newProductionValidationTrustedPath(profiles, true)
	if err != nil {
		return result, err
	}
	invocation, err := coveragerun.BuildLLVMInvocation(coveragerun.LLVMInputs{
		Profdata: toolset.Profdata(), Cov: toolset.Cov(), Binary: binary, ProfileDirectory: profileRoot,
		ProfileFiles: profileFiles, MergedProfile: "generated.profdata",
	})
	if err != nil {
		return result, errProductionValidationUnavailable
	}
	tools := make(map[string]string)
	for _, spec := range []processcontrol.Spec{invocation.Merge, invocation.Export} {
		if err := addProductionValidationSpecTools(tools, spec); err != nil {
			return result, err
		}
	}
	interpret := productionValidationCoverageInterpreter(platform, registration, roots)
	if interpret == nil {
		return result, errProductionValidationUnavailable
	}
	return productionValidationCoveragePreparation{
		specs: []processcontrol.Spec{invocation.Merge, invocation.Export}, tools: tools,
		environment: append([]string(nil), invocation.Export.Env...), unset: append([]string(nil), invocation.Export.EnvUnset...), interpret: interpret,
	}, nil
}

type productionValidationTrustedPath struct {
	path      string
	digest    string
	directory bool
}

func newProductionValidationTrustedPath(path string, directory bool) (*productionValidationTrustedPath, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errProductionValidationUnavailable
	}
	value := &productionValidationTrustedPath{path: path, directory: directory}
	if !directory {
		content, err := os.ReadFile(path)
		if err != nil || len(content) > 256<<20 {
			return nil, errProductionValidationUnavailable
		}
		value.digest = productionBytesDigest(content)
	}
	if value.Verify() != nil {
		return nil, errProductionValidationUnavailable
	}
	return value, nil
}

func (value *productionValidationTrustedPath) Path() string {
	if value == nil {
		return ""
	}
	return value.path
}

func (value *productionValidationTrustedPath) Verify() error {
	if value == nil || value.path == "" {
		return errProductionValidationUnavailable
	}
	info, err := os.Lstat(value.path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || value.directory != info.IsDir() {
		return errProductionValidationUnavailable
	}
	if value.directory {
		return nil
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > 256<<20 {
		return errProductionValidationUnavailable
	}
	content, err := os.ReadFile(value.path)
	if err != nil || productionBytesDigest(content) != value.digest {
		return errProductionValidationUnavailable
	}
	return nil
}

func productionValidationProfiles(root string) ([]string, error) {
	if !directProductionValidationDirectory(root) {
		return nil, errProductionValidationUnavailable
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) == 0 || len(entries) > 256 {
		return nil, errProductionValidationUnavailable
	}
	result := make([]string, 0, len(entries))
	candidate, suite := false, false
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".profraw") ||
			!strings.HasPrefix(entry.Name(), "candidate-") && !strings.HasPrefix(entry.Name(), "suite-") {
			return nil, errProductionValidationUnavailable
		}
		candidate = candidate || strings.HasPrefix(entry.Name(), "candidate-")
		suite = suite || strings.HasPrefix(entry.Name(), "suite-")
		result = append(result, entry.Name())
	}
	if !candidate || !suite {
		return nil, errProductionValidationUnavailable
	}
	sort.Strings(result)
	return result, nil
}

func productionValidationCoverageInterpreter(platform string, registration productionValidationPlanRegistration, roots testgenvalidate.Roots) testgenvalidate.StageInterpreter {
	baseline, err := coveragemodelv1.Decode(registration.baseline)
	if err != nil || baseline.Completeness.Outcome != coveragemodelv1.Available {
		return nil
	}
	includes := make([]string, 0, len(baseline.Files))
	for _, file := range baseline.Files {
		includes = append(includes, file.URI)
	}
	matcher, err := coveragenormalize.NewGlobMatcher(includes, nil)
	if err != nil {
		return nil
	}
	toolchainSnapshot, err := coverageToolchainSnapshot(registration.build.toolchain, platform)
	if err != nil {
		return nil
	}
	return func(stage testgenvalidate.Stage, gotRoots testgenvalidate.Roots, output []byte) (testgenvalidate.StageEvidence, error) {
		if stage != testgenvalidate.StageCoverage || gotRoots != roots || len(output) == 0 || len(output) > 32<<20 {
			return testgenvalidate.StageEvidence{}, errProductionValidationUnavailable
		}
		parsed, err := coverageparserllvm.Parse(bytes.NewReader(output), coverageparserllvm.Limits{
			MaxInputBytes: 32 << 20, MaxDepth: 128, MaxFiles: 10000, MaxFunctions: 1000000,
			MaxLines: 10000000, MaxBranches: 10000000, MaxStringBytes: 1 << 20,
		})
		if err != nil {
			return testgenvalidate.StageEvidence{}, errProductionValidationUnavailable
		}
		document, _, _, err := coveragenormalize.NormalizeLLVMWithDetail(coveragenormalize.LLVMInput{
			Export: parsed, WorkspaceRoot: roots.Source, Matcher: matcher, Toolchain: toolchainSnapshot,
			Completeness: coveragedomain.Completeness{Outcome: coveragedomain.OutcomeAvailable}, Limits: coveragenormalize.DefaultLimits(),
		})
		if err != nil || !reflect.DeepEqual(document.Provenance, baseline.Provenance) || len(document.Files) != len(baseline.Files) {
			return testgenvalidate.StageEvidence{}, errProductionValidationUnavailable
		}
		encoded, err := json.Marshal(document)
		if err != nil || len(encoded) == 0 || len(encoded) > 32<<20 {
			return testgenvalidate.StageEvidence{}, errProductionValidationUnavailable
		}
		return testgenvalidate.StageEvidence{CoverageJSON: encoded}, nil
	}
}

var _ coveragerun.TrustedPath = (*productionValidationTrustedPath)(nil)
