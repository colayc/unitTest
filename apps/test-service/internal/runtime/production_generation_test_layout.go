package runtime

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenrender"
	"unit-test-ide.local/test-service/internal/workspace"
)

const maxProductionTestCMakeBytes = 1 << 20

type productionCTestSnapshotReader interface {
	ReadProductionCTest(context.Context, cmake.BuildProfile) (ctest.Snapshot, error)
}

type productionCTestExecutor interface {
	Execute(context.Context, task.ExecutionStep, int) ([]byte, error)
}

type productionCTestReader struct {
	runner   *ctest.Runner
	executor productionCTestExecutor
	limits   ctest.Limits
}

func (reader productionCTestReader) ReadProductionCTest(ctx context.Context, profile cmake.BuildProfile) (ctest.Snapshot, error) {
	if reader.runner == nil || reader.executor == nil || ctx == nil {
		return ctest.Snapshot{}, errProductionGenerationUnavailable
	}
	limits := reader.limits
	if limits == (ctest.Limits{}) {
		limits = ctest.DefaultLimits()
	}
	if !limits.Valid() || limits.MaxDocumentBytes < 1 {
		return ctest.Snapshot{}, errProductionGenerationUnavailable
	}
	step, err := reader.runner.ShowOnlyPlan(profile)
	if err != nil {
		return ctest.Snapshot{}, err
	}
	encoded, err := reader.executor.Execute(ctx, step, limits.MaxDocumentBytes)
	if err != nil {
		return ctest.Snapshot{}, err
	}
	return ctest.ParseShowOnlyJSON(encoded, limits)
}

type productionTestLayoutConfig struct {
	root             workspace.Root
	ctest            productionCTestSnapshotReader
	frameworkDigests map[testgendomain.Framework]string
}

type ctestProductionTestLayoutAuthority struct {
	config productionTestLayoutConfig
}

func newCTestProductionTestLayoutAuthority(config productionTestLayoutConfig) (*ctestProductionTestLayoutAuthority, error) {
	if config.root.NativePath == "" || config.root.ID == "" || config.ctest == nil ||
		!validProductionDigest(config.frameworkDigests[testgendomain.FrameworkUnity]) ||
		!validProductionDigest(config.frameworkDigests[testgendomain.FrameworkCppUTest]) {
		return nil, task.ErrStorageUnavailable
	}
	return &ctestProductionTestLayoutAuthority{config: config}, nil
}

func (authority *ctestProductionTestLayoutAuthority) ResolveProductionTestLayout(ctx context.Context, resolved productionResolverContext, binding productionCompileBinding, requested testgendomain.Framework) (productionTestLayout, error) {
	if authority == nil || ctx == nil || binding.target.ProjectID != resolved.project.ID || binding.target.ProfileID != resolved.profile.ID {
		return productionTestLayout{}, errProductionGenerationUnavailable
	}
	wantFramework := testgendomain.FrameworkUnity
	frameworkTarget := "unity"
	extension := ".c"
	if binding.language == testgenrender.LanguageCPP {
		wantFramework, frameworkTarget, extension = testgendomain.FrameworkCppUTest, "CppUTest", ".cpp"
	} else if binding.language != testgenrender.LanguageC {
		return productionTestLayout{}, errProductionGenerationUnavailable
	}
	if requested != testgendomain.FrameworkAuto && requested != wantFramework {
		return productionTestLayout{}, errProductionGenerationUnavailable
	}
	snapshot, err := authority.config.ctest.ReadProductionCTest(ctx, resolved.profile)
	if err != nil {
		return productionTestLayout{}, err
	}
	var descriptor *ctest.ExecutionDescriptor
	ctestName := ""
	for _, mapping := range resolved.project.Tests.Containers {
		if testgendomain.Framework(mapping.Framework) != wantFramework {
			continue
		}
		for _, raw := range snapshot.Tests {
			if raw.Name != mapping.CTestName {
				continue
			}
			candidate, err := ctest.BuildDescriptor(raw, resolved.profile, resolved.targets)
			if err != nil || candidate.Blocked || candidate.TargetID == "" {
				return productionTestLayout{}, errProductionGenerationUnavailable
			}
			if descriptor != nil {
				return productionTestLayout{}, errProductionGenerationUnavailable
			}
			descriptor = &candidate
			ctestName = mapping.CTestName
		}
	}
	if descriptor == nil {
		return productionTestLayout{}, errProductionGenerationUnavailable
	}
	testTarget, ok := productionTargetByID(resolved.targets, descriptor.TargetID)
	if !ok || testTarget.ID == binding.target.ID || !productionCMakeIdentifierPattern.MatchString(testTarget.Name) {
		return productionTestLayout{}, errProductionGenerationUnavailable
	}
	frameworkCount := 0
	for _, target := range resolved.targets {
		if target.Name == frameworkTarget {
			frameworkCount++
		}
	}
	if frameworkCount != 1 {
		return productionTestLayout{}, errProductionGenerationUnavailable
	}
	cmakeAbsolute := filepath.Join(testTarget.SourceDir, "CMakeLists.txt")
	cmakeRelative, ok := productionWorkspaceRelative(authority.config.root, cmakeAbsolute, false)
	if !ok || !strings.HasPrefix(cmakeRelative, "tests/") || !strings.HasSuffix(cmakeRelative, "CMakeLists.txt") {
		return productionTestLayout{}, errProductionGenerationUnavailable
	}
	existing, err := readProductionTestCMake(cmakeAbsolute)
	if err != nil {
		return productionTestLayout{}, err
	}
	testPath := "tests/generated/" + bindingSourceRelative(authority.config.root, binding) + "_test" + extension
	if !validProductionRelative(testPath) || len(testPath) > 240 {
		return productionTestLayout{}, errProductionGenerationUnavailable
	}
	return productionTestLayout{
		framework: wantFramework, frameworkDigest: authority.config.frameworkDigests[wantFramework], ctestName: ctestName,
		renderTarget: testgenrender.TargetMetadata{
			TestTarget: testTarget.Name, ProductionTarget: binding.target.Name, FrameworkTarget: frameworkTarget,
			CMakePath: cmakeRelative, TestPath: testPath, ExistingCMake: string(existing),
		},
	}, nil
}

func productionTargetByID(targets []cmake.Target, id string) (cmake.Target, bool) {
	var result cmake.Target
	found := false
	for _, target := range targets {
		if target.ID != id {
			continue
		}
		if found {
			return cmake.Target{}, false
		}
		result, found = target, true
	}
	return result, found
}

func bindingSourceRelative(root workspace.Root, binding productionCompileBinding) string {
	relative, ok := productionWorkspaceRelative(root, binding.unit.Source, false)
	if !ok {
		return ""
	}
	return relative
}

func readProductionTestCMake(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errProductionGenerationUnavailable
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() > maxProductionTestCMakeBytes {
		return nil, errProductionGenerationUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(file, maxProductionTestCMakeBytes+1))
	if err != nil || len(data) > maxProductionTestCMakeBytes || strings.ContainsRune(string(data), 0) {
		return nil, errProductionGenerationUnavailable
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() {
		return nil, errProductionGenerationUnavailable
	}
	return data, nil
}

var _ productionCTestSnapshotReader = productionCTestReader{}
var _ productionTestLayoutAuthority = (*ctestProductionTestLayoutAuthority)(nil)
