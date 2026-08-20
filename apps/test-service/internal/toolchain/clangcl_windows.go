//go:build windows

package toolchain

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"

	"unit-test-ide.local/test-service/internal/workspace"
)

var (
	clangCLTargetPattern = regexp.MustCompile(
		`(?im)^Target:[ \t]*([A-Za-z0-9_+.]+(?:-[A-Za-z0-9_+.]+)+)[ \t\r]*$`,
	)
	lldVersionPattern = regexp.MustCompile(
		`(?i)\bLLD[ \t]+([0-9]+\.[0-9]+(?:\.[0-9]+)?)\b`,
	)
	llvmToolVersionPattern = regexp.MustCompile(
		`(?i)\bLLVM version[ \t]+([0-9]+\.[0-9]+(?:\.[0-9]+)?)\b`,
	)
)

type clangCLAdapter struct {
	options windowsAdapterOptions
}

type clangCLCandidate struct {
	id        string
	manual    bool
	cCompiler string
	cxx       string
	context   msvcContext
}

type clangGeneratorProbeResult struct {
	names        []string
	directories  []windowsDirectoryReference
	toolPath     string
	toolIdentity string
	toolFileID   string
}

func newClangCLAdapter(options windowsAdapterOptions) *clangCLAdapter {
	options.config.BaseEnvironment = append([]string(nil), options.config.BaseEnvironment...)
	options.manual = append([]workspace.ToolchainConfig(nil), options.manual...)
	return &clangCLAdapter{options: options}
}

func (adapter *clangCLAdapter) Discover(ctx context.Context) ([]Instance, error) {
	if adapter == nil || ctx == nil {
		return nil, fmt.Errorf("%w: clang-cl adapter or context is nil", ErrInvalidToolchain)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	installations, err := discoverVisualStudioInstallations(
		ctx,
		adapter.options.runner,
		adapter.options.config,
	)
	if err != nil {
		return nil, err
	}
	contexts, issues, err := discoverMSVCContexts(
		ctx,
		adapter.options,
		installations,
		nil,
	)
	if err != nil {
		return nil, err
	}
	if len(contexts) == 0 {
		if len(issues) == 0 {
			appendWindowsDiscoveryIssue(&issues, "TOOLCHAIN_ENVIRONMENT_INVALID")
		}
		return finishWindowsDiscovery(nil, issues)
	}
	candidates := adapter.clangCandidates()
	instances := make([]Instance, 0, len(candidates))
	descriptors := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, msvcEnvironment := range contexts {
			candidate.context = msvcEnvironment
			instance, probeErr := adapter.probeCandidate(ctx, candidate)
			if probeErr != nil {
				if isContextError(probeErr) {
					return nil, probeErr
				}
				appendWindowsDiscoveryIssue(&issues, issueCodeFromProbeError(probeErr))
				continue
			}
			descriptor := descriptorKey(instance)
			if _, duplicate := descriptors[descriptor]; !duplicate {
				descriptors[descriptor] = struct{}{}
				instances = append(instances, instance)
			}
			if len(instances) >= maxWindowsInstances {
				appendWindowsDiscoveryIssue(&issues, "TOOLCHAIN_LIMIT_EXCEEDED")
			}
			break
		}
		if len(instances) >= maxWindowsInstances {
			break
		}
	}
	sortWindowsInstances(instances)
	return finishWindowsDiscovery(instances, issues)
}

func (adapter *clangCLAdapter) Probe(ctx context.Context, candidate Candidate) (Instance, error) {
	if adapter == nil || ctx == nil {
		return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "clang-cl adapter is not initialized")
	}
	if candidate.Family != FamilyClangCL {
		return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "candidate family does not match clang-cl")
	}
	instances, err := adapter.Discover(ctx)
	for _, instance := range instances {
		if candidate.ID != "" && instance.ID == candidate.ID ||
			candidate.ID == "" &&
				identityPath(instance.CCompiler) == identityPath(candidate.CCompiler) &&
				identityPath(instance.CXXCompiler) == identityPath(candidate.CXXCompiler) {
			return cloneInstances([]Instance{instance})[0], nil
		}
	}
	if isContextError(err) {
		return Instance{}, err
	}
	return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "clang-cl candidate was not discovered")
}

func (adapter *clangCLAdapter) clangCandidates() []clangCLCandidate {
	result := make([]clangCLCandidate, 0)
	for _, manual := range adapter.options.manual {
		if Family(manual.Family) != FamilyClangCL {
			continue
		}
		result = append(result, clangCLCandidate{
			id:        manual.ID,
			manual:    true,
			cCompiler: filepath.Clean(manual.CCompiler),
			cxx:       filepath.Clean(manual.CPPCompiler),
		})
	}
	if len(result) == 0 && adapter.options.config.LLVMRoot != "" {
		compiler := filepath.Join(adapter.options.config.LLVMRoot, "clang-cl.exe")
		result = append(result, clangCLCandidate{
			cCompiler: compiler,
			cxx:       compiler,
		})
	}
	sort.Slice(result, func(left, right int) bool {
		return lessStrings(
			[]string{result[left].id, identityPath(result[left].cCompiler), identityPath(result[left].cxx)},
			[]string{result[right].id, identityPath(result[right].cCompiler), identityPath(result[right].cxx)},
		)
	})
	return result
}

func (adapter *clangCLAdapter) probeCandidate(
	ctx context.Context,
	candidate clangCLCandidate,
) (Instance, error) {
	if err := adapter.verifyAutomaticLLVMRoot(ctx, candidate); err != nil {
		return Instance{}, contextualWindowsProbeError(err, "configured LLVM root changed")
	}
	cCompiler, err := openWindowsToolSnapshot(ctx, candidate.cCompiler)
	if err != nil {
		return Instance{}, contextualWindowsProbeError(err, "clang-cl C compiler is invalid")
	}
	defer cCompiler.Close()
	cxxCompiler, err := openWindowsToolSnapshot(ctx, candidate.cxx)
	if err != nil {
		return Instance{}, contextualWindowsProbeError(err, "clang-cl C++ compiler is invalid")
	}
	defer cxxCompiler.Close()
	if !candidate.manual &&
		(!pathWithinWindowsRoot(adapter.options.config.LLVMRoot, cCompiler.path) ||
			!pathWithinWindowsRoot(adapter.options.config.LLVMRoot, cxxCompiler.path)) {
		return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "automatic clang-cl leaves configured LLVM root")
	}
	if identityPath(filepath.Dir(cCompiler.path)) != identityPath(filepath.Dir(cxxCompiler.path)) {
		return Instance{}, invalidProbe("TOOLCHAIN_PAIR_MISMATCH", "clang-cl compiler roots differ")
	}
	lldPath := filepath.Join(filepath.Dir(cCompiler.path), "lld-link.exe")
	lld, err := openWindowsToolSnapshot(ctx, lldPath)
	if err != nil {
		return Instance{}, contextualWindowsProbeError(err, "lld-link is invalid")
	}
	defer lld.Close()
	if !candidate.manual &&
		!pathWithinWindowsRoot(adapter.options.config.LLVMRoot, lld.path) {
		return Instance{}, invalidProbe("TOOLCHAIN_PAIR_MISMATCH", "automatic lld-link leaves configured LLVM root")
	}
	if identityPath(filepath.Dir(lld.path)) != identityPath(filepath.Dir(cCompiler.path)) {
		return Instance{}, invalidProbe("TOOLCHAIN_PAIR_MISMATCH", "clang-cl and lld-link roots differ")
	}
	verify := func() error {
		if err := adapter.verifyAutomaticLLVMRoot(ctx, candidate); err != nil {
			return err
		}
		if err := candidate.context.verify(ctx); err != nil {
			return err
		}
		if err := cCompiler.Verify(ctx); err != nil {
			return err
		}
		if err := cxxCompiler.Verify(ctx); err != nil {
			return err
		}
		return lld.Verify(ctx)
	}
	cOutput, err := runWindowsProbe(
		ctx,
		adapter.options.runner,
		cCompiler,
		[]string{"--version"},
		candidate.context.environment,
		maxWindowsProbeOutput,
		true,
		verify,
	)
	if err != nil {
		return Instance{}, err
	}
	cVersion, cTriple, err := parseClangCLBanner(cOutput)
	if err != nil {
		return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "clang-cl C compiler output is malformed")
	}
	cxxOutput, err := runWindowsProbe(
		ctx,
		adapter.options.runner,
		cxxCompiler,
		[]string{"--version"},
		candidate.context.environment,
		maxWindowsProbeOutput,
		true,
		verify,
	)
	if err != nil {
		return Instance{}, err
	}
	cxxVersion, cxxTriple, err := parseClangCLBanner(cxxOutput)
	if err != nil || cVersion != cxxVersion || cTriple != cxxTriple {
		return Instance{}, invalidProbe("TOOLCHAIN_PAIR_MISMATCH", "clang-cl compiler pair is incompatible")
	}
	targetArchitecture, err := architectureFromTriple(cTriple)
	if err != nil || targetArchitecture != candidate.context.config.TargetArchitecture {
		return Instance{}, invalidProbe("TOOLCHAIN_PAIR_MISMATCH", "clang-cl target does not match MSVC environment")
	}
	lldOutput, err := runWindowsProbe(
		ctx,
		adapter.options.runner,
		lld,
		[]string{"--version"},
		candidate.context.environment,
		maxWindowsProbeOutput,
		true,
		verify,
	)
	if err != nil {
		return Instance{}, err
	}
	lldVersion, err := parseLLDVersion(lldOutput)
	if err != nil || !sameVersionMajor(cVersion, lldVersion) {
		return Instance{}, invalidProbe("TOOLCHAIN_PAIR_MISMATCH", "clang-cl and lld-link versions are incompatible")
	}
	generators, err := adapter.probeClangGenerator(ctx, candidate, verify)
	if err != nil {
		return Instance{}, err
	}
	coverage, err := adapter.probeCoverage(ctx, candidate, cCompiler, cVersion, verify)
	if err != nil {
		return Instance{}, err
	}
	if err := verifyClangGenerator(ctx, generators); err != nil {
		return Instance{}, contextualWindowsProbeError(err, "clang-cl Ninja changed")
	}
	if err := verify(); err != nil {
		return Instance{}, contextualWindowsProbeError(err, "clang-cl identity changed")
	}
	instanceEnvironment, err := appendVerifiedGeneratorPaths(
		candidate.context.environment,
		generators.directories,
	)
	if err != nil {
		return Instance{}, err
	}
	instance := Instance{
		ID:                 candidate.id,
		Family:             FamilyClangCL,
		CCompiler:          cCompiler.path,
		CXXCompiler:        cxxCompiler.path,
		Version:            cVersion,
		TargetTriple:       cTriple,
		HostArchitecture:   candidate.context.config.HostArchitecture,
		TargetArchitecture: targetArchitecture,
		Sysroot:            candidate.context.sdk,
		Environment:        instanceEnvironment,
		Generators:         append([]string(nil), generators.names...),
		Coverage:           coverage,
	}
	if !candidate.manual {
		instance.ID, err = automaticToolchainID(
			instance,
			cCompiler.identity,
			cxxCompiler.identity,
			candidate.context.sdkIdentity+"\x00"+candidate.context.sdkVersion+
				"\x00"+candidate.context.environmentIdentity+
				"\x00"+candidate.context.installation.Identity+
				"\x00"+candidate.context.toolsetIdentity+
				"\x00"+lld.identity+
				"\x00"+windowsDirectoryDescriptorIdentity(generators.directories)+
				"\x00generator-ninja\x00"+identityPath(generators.toolPath)+
				"\x00"+generators.toolFileID+
				"\x00"+generators.toolIdentity,
		)
		if err != nil {
			return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "construct clang-cl toolchain id")
		}
	}
	return instance, nil
}

func (adapter *clangCLAdapter) verifyAutomaticLLVMRoot(
	ctx context.Context,
	candidate clangCLCandidate,
) error {
	if candidate.manual {
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if adapter.options.config.LLVMRoot == "" ||
		adapter.options.config.LLVMRootIdentity == "" {
		return errors.New("configured LLVM root is unavailable")
	}
	return verifyWindowsDirectory(
		adapter.options.config.LLVMRoot,
		adapter.options.config.LLVMRootIdentity,
	)
}

func (adapter *clangCLAdapter) probeClangGenerator(
	ctx context.Context,
	candidate clangCLCandidate,
	verify func() error,
) (clangGeneratorProbeResult, error) {
	path := candidate.context.ninja
	if path == "" {
		return clangGeneratorProbeResult{},
			invalidProbe("BUILD_TOOL_NOT_FOUND", "clang-cl Ninja is unavailable")
	}
	ninja, err := openWindowsToolSnapshot(ctx, path)
	if err != nil {
		if isContextError(err) {
			return clangGeneratorProbeResult{}, err
		}
		return clangGeneratorProbeResult{},
			invalidProbe("BUILD_TOOL_NOT_FOUND", "clang-cl Ninja is invalid")
	}
	defer ninja.Close()
	toolPath, toolFileID, err := canonicalWindowsFileSystemIdentity(ninja.path)
	if err != nil || identityPath(toolPath) != identityPath(ninja.path) {
		return clangGeneratorProbeResult{},
			invalidProbe("BUILD_TOOL_NOT_FOUND", "clang-cl Ninja identity is invalid")
	}
	if err := ninja.Verify(ctx); err != nil {
		return clangGeneratorProbeResult{},
			contextualWindowsProbeError(err, "clang-cl Ninja identity changed")
	}
	root, rootIdentity, err := canonicalWindowsDirectoryIdentity(filepath.Dir(ninja.path))
	if err != nil || !pathWithinWindowsRoot(root, ninja.path) {
		return clangGeneratorProbeResult{},
			invalidProbe("BUILD_TOOL_NOT_FOUND", "clang-cl Ninja root is invalid")
	}
	rootReference := windowsDirectoryReference{
		role: "path-generator-ninja", path: root, identity: rootIdentity,
	}
	verifyNinja := func() error {
		if err := verify(); err != nil {
			return err
		}
		return verifyWindowsDirectory(rootReference.path, rootReference.identity)
	}
	output, err := runWindowsProbe(
		ctx,
		adapter.options.runner,
		ninja,
		[]string{"--version"},
		candidate.context.environment,
		maxWindowsProbeOutput,
		false,
		verifyNinja,
	)
	if err != nil {
		if isContextError(err) {
			return clangGeneratorProbeResult{}, err
		}
		return clangGeneratorProbeResult{},
			invalidProbe("BUILD_TOOL_NOT_FOUND", "clang-cl Ninja probe failed")
	}
	version, err := parseSingleLine(output, 128)
	if err != nil || !versionPattern.MatchString(version) {
		return clangGeneratorProbeResult{},
			invalidProbe("BUILD_TOOL_NOT_FOUND", "clang-cl Ninja output is invalid")
	}
	return clangGeneratorProbeResult{
		names:        []string{"Ninja"},
		directories:  []windowsDirectoryReference{rootReference},
		toolPath:     ninja.path,
		toolIdentity: ninja.identity,
		toolFileID:   toolFileID,
	}, nil
}

func verifyClangGenerator(
	ctx context.Context,
	generator clangGeneratorProbeResult,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(generator.directories) != 1 ||
		generator.directories[0].role != "path-generator-ninja" {
		return errors.New("clang-cl Ninja descriptor is invalid")
	}
	root := generator.directories[0]
	if err := verifyWindowsDirectory(root.path, root.identity); err != nil {
		return err
	}
	ninja, err := openWindowsToolSnapshot(ctx, generator.toolPath)
	if err != nil {
		return err
	}
	defer ninja.Close()
	toolPath, toolFileID, err := canonicalWindowsFileSystemIdentity(ninja.path)
	if err != nil {
		return err
	}
	if err := ninja.Verify(ctx); err != nil {
		return err
	}
	if identityPath(ninja.path) != identityPath(generator.toolPath) ||
		identityPath(toolPath) != identityPath(generator.toolPath) ||
		toolFileID != generator.toolFileID ||
		ninja.identity != generator.toolIdentity ||
		!pathWithinWindowsRoot(root.path, ninja.path) {
		return errors.New("clang-cl Ninja identity changed")
	}
	return ctx.Err()
}

func (adapter *clangCLAdapter) probeCoverage(
	ctx context.Context,
	candidate clangCLCandidate,
	compiler *executableSnapshot,
	compilerVersion string,
	verify func() error,
) (CoverageCapability, error) {
	root := filepath.Dir(candidate.cCompiler)
	profdata, err := openWindowsToolSnapshot(ctx, filepath.Join(root, "llvm-profdata.exe"))
	if err != nil {
		if isContextError(err) {
			return CoverageCapability{}, err
		}
		return CoverageCapability{}, nil
	}
	defer profdata.Close()
	coverage, err := openWindowsToolSnapshot(ctx, filepath.Join(root, "llvm-cov.exe"))
	if err != nil {
		if isContextError(err) {
			return CoverageCapability{}, err
		}
		return CoverageCapability{}, nil
	}
	defer coverage.Close()
	if identityPath(filepath.Dir(profdata.path)) != identityPath(root) ||
		identityPath(filepath.Dir(coverage.path)) != identityPath(root) {
		return CoverageCapability{}, nil
	}
	if !candidate.manual &&
		(!pathWithinWindowsRoot(adapter.options.config.LLVMRoot, profdata.path) ||
			!pathWithinWindowsRoot(adapter.options.config.LLVMRoot, coverage.path)) {
		return CoverageCapability{}, nil
	}
	verifyAll := func() error {
		if err := verify(); err != nil {
			return err
		}
		if err := profdata.Verify(ctx); err != nil {
			return err
		}
		return coverage.Verify(ctx)
	}
	profdataOutput, err := runWindowsProbe(
		ctx,
		adapter.options.runner,
		profdata,
		[]string{"--version"},
		candidate.context.environment,
		maxWindowsProbeOutput,
		true,
		verifyAll,
	)
	if err != nil {
		if isContextError(err) {
			return CoverageCapability{}, err
		}
		return CoverageCapability{}, nil
	}
	coverageOutput, err := runWindowsProbe(
		ctx,
		adapter.options.runner,
		coverage,
		[]string{"--version"},
		candidate.context.environment,
		maxWindowsProbeOutput,
		true,
		verifyAll,
	)
	if err != nil {
		if isContextError(err) {
			return CoverageCapability{}, err
		}
		return CoverageCapability{}, nil
	}
	profdataVersion, profdataErr := parseLLVMToolVersion(profdataOutput)
	coverageVersion, coverageErr := parseLLVMToolVersion(coverageOutput)
	if profdataErr != nil || coverageErr != nil ||
		compilerVersion != profdataVersion ||
		compilerVersion != coverageVersion ||
		profdataVersion != coverageVersion {
		return CoverageCapability{}, nil
	}
	compilerPath, compilerFileID, compilerIdentityErr := canonicalWindowsFileSystemIdentity(compiler.path)
	profdataPath, profdataFileID, profdataIdentityErr := canonicalWindowsFileSystemIdentity(profdata.path)
	coveragePath, coverageFileID, coverageIdentityErr := canonicalWindowsFileSystemIdentity(coverage.path)
	if compilerIdentityErr != nil || profdataIdentityErr != nil || coverageIdentityErr != nil ||
		identityPath(compilerPath) != identityPath(compiler.path) ||
		identityPath(profdataPath) != identityPath(profdata.path) ||
		identityPath(coveragePath) != identityPath(coverage.path) {
		return CoverageCapability{}, nil
	}
	evidence := []ExecutableEvidence{
		{FileIdentity: compilerFileID, SHA256: compiler.digest},
		{FileIdentity: profdataFileID, SHA256: profdata.digest},
		{FileIdentity: coverageFileID, SHA256: coverage.digest},
	}
	paths := []string{compiler.path, profdata.path, coverage.path}
	identity := LLVMToolsetIdentity(compilerVersion, paths, evidence)
	if identity == "" {
		return CoverageCapability{}, nil
	}
	return CoverageCapability{
		LLVMProfdata:     profdata.path,
		LLVMCov:          coverage.path,
		CompilerEvidence: evidence[0], ProfdataEvidence: evidence[1], CovEvidence: evidence[2],
		ToolsetIdentity: identity,
	}, nil
}

func parseClangCLBanner(output []byte) (string, string, error) {
	version, err := parseCompilerVersion(FamilyClang, output)
	if err != nil {
		return "", "", err
	}
	target := clangCLTargetPattern.FindSubmatch(output)
	if len(target) != 2 || !triplePattern.Match(target[1]) {
		return "", "", errors.New("clang-cl target is malformed")
	}
	return version, string(target[1]), nil
}

func parseLLDVersion(output []byte) (string, error) {
	match := lldVersionPattern.FindSubmatch(output)
	if len(match) != 2 {
		return "", errors.New("lld-link version is malformed")
	}
	return string(match[1]), nil
}

func parseLLVMToolVersion(output []byte) (string, error) {
	match := llvmToolVersionPattern.FindSubmatch(output)
	if len(match) != 2 {
		return "", errors.New("LLVM tool version is malformed")
	}
	return string(match[1]), nil
}
