//go:build windows

package toolchain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/workspace"
)

const (
	maxCapturedEnvironmentBytes      = 64 * 1024
	maxCapturedEnvironmentEntries    = 256
	maxCapturedEnvironmentEntryBytes = 4096
)

var (
	msvcCompilerBannerPattern = regexp.MustCompile(
		`(?i)\bCompiler Version ([0-9]+\.[0-9]+(?:\.[0-9]+){0,2}) for (x86|x64|arm64)\b`,
	)
	msvcLinkerBannerPattern = regexp.MustCompile(
		`(?i)\bLinker Version ([0-9]+\.[0-9]+(?:\.[0-9]+){0,2})\b`,
	)
	windowsSDKVersionPattern = regexp.MustCompile(
		`^[0-9]+(?:\.[0-9]+){1,3}$`,
	)
)

type MSVCConfig struct {
	InstallationID     string
	ToolsetVersion     string
	HostArchitecture   string
	TargetArchitecture string
}

type msvcAdapter struct {
	options windowsAdapterOptions
}

type msvcContext struct {
	id               string
	manual           bool
	installation     visualStudioInstallation
	toolset          string
	toolsetIdentity  string
	config           MSVCConfig
	environment      []string
	sdk              string
	sdkIdentity      string
	sdkVersion       string
	cl               string
	link             string
	msbuild          string
	ninja            string
	vsDevCmd         string
	vsDevCmdIdentity string
}

func newMSVCAdapter(options windowsAdapterOptions) *msvcAdapter {
	options.config.BaseEnvironment = append([]string(nil), options.config.BaseEnvironment...)
	options.manual = append([]workspace.ToolchainConfig(nil), options.manual...)
	return &msvcAdapter{options: options}
}

func (adapter *msvcAdapter) Discover(ctx context.Context) ([]Instance, error) {
	if adapter == nil || ctx == nil {
		return nil, fmt.Errorf("%w: MSVC adapter or context is nil", ErrInvalidToolchain)
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
		manualMSVCConfigurations(adapter.options.manual),
	)
	if err != nil {
		return nil, err
	}
	instances := make([]Instance, 0, len(contexts))
	descriptors := make(map[string]struct{}, len(contexts))
	for _, candidate := range contexts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		instance, probeErr := adapter.probeContext(ctx, candidate)
		if probeErr != nil {
			if isContextError(probeErr) {
				return nil, probeErr
			}
			appendWindowsDiscoveryIssue(&issues, issueCodeFromProbeError(probeErr))
			continue
		}
		descriptor := descriptorKey(instance)
		if _, duplicate := descriptors[descriptor]; duplicate {
			continue
		}
		descriptors[descriptor] = struct{}{}
		instances = append(instances, instance)
		if len(instances) >= maxWindowsInstances {
			appendWindowsDiscoveryIssue(&issues, "TOOLCHAIN_LIMIT_EXCEEDED")
			break
		}
	}
	sortWindowsInstances(instances)
	return finishWindowsDiscovery(instances, issues)
}

func (adapter *msvcAdapter) Probe(ctx context.Context, candidate Candidate) (Instance, error) {
	if adapter == nil || ctx == nil {
		return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "MSVC adapter is not initialized")
	}
	if candidate.Family != FamilyMSVC {
		return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "candidate family does not match MSVC")
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
	return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "MSVC candidate was not discovered")
}

func manualMSVCConfigurations(manual []workspace.ToolchainConfig) []workspace.ToolchainConfig {
	result := make([]workspace.ToolchainConfig, 0, len(manual))
	for _, candidate := range manual {
		if Family(candidate.Family) == FamilyMSVC {
			result = append(result, candidate)
		}
	}
	return result
}

func discoverMSVCContexts(
	ctx context.Context,
	options windowsAdapterOptions,
	installations []visualStudioInstallation,
	manual []workspace.ToolchainConfig,
) ([]msvcContext, []Issue, error) {
	configurations := append([]workspace.ToolchainConfig(nil), manual...)
	if len(configurations) == 0 {
		for _, installation := range installations {
			version, err := latestMSVCToolset(installation.Path)
			if err != nil {
				continue
			}
			configurations = append(configurations, workspace.ToolchainConfig{
				Family:             string(FamilyMSVC),
				InstallationID:     installation.ID,
				ToolsetVersion:     version,
				HostArchitecture:   options.config.HostArchitecture,
				TargetArchitecture: options.config.HostArchitecture,
			})
		}
	}
	contexts := make([]msvcContext, 0, len(configurations))
	issues := make([]Issue, 0)
	for _, requested := range configurations {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		installation, found := findVisualStudioInstallation(installations, requested.InstallationID)
		if !found {
			appendWindowsDiscoveryIssue(&issues, "TOOLCHAIN_MANUAL_SELECTION_FAILED")
			continue
		}
		candidate, err := captureMSVCContext(ctx, options, installation, requested)
		if err != nil {
			if isContextError(err) {
				return nil, nil, err
			}
			code := issueCodeFromProbeError(err)
			if code == "TOOLCHAIN_PROBE_FAILED" && requested.ID != "" {
				code = "TOOLCHAIN_MANUAL_SELECTION_FAILED"
			}
			appendWindowsDiscoveryIssue(&issues, code)
			continue
		}
		contexts = append(contexts, candidate)
	}
	return contexts, issues, nil
}

func latestMSVCToolset(installation string) (string, error) {
	root := filepath.Join(installation, "VC", "Tools", "MSVC")
	file, err := os.Open(root)
	if err != nil {
		return "", err
	}
	defer file.Close()
	entries, err := file.ReadDir(maxWindowsToolsets + 1)
	if err != nil {
		return "", err
	}
	if len(entries) > maxWindowsToolsets {
		return "", errors.New("too many MSVC toolsets")
	}
	versions := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && versionPattern.MatchString(entry.Name()) &&
			len(entry.Name()) <= maxVSWhereVersionBytes {
			versions = append(versions, entry.Name())
		}
	}
	if len(versions) == 0 {
		return "", errors.New("MSVC toolset is unavailable")
	}
	sort.Slice(versions, func(left, right int) bool {
		comparison := compareNumericVersions(versions[left], versions[right])
		if comparison == 0 {
			return versions[left] < versions[right]
		}
		return comparison < 0
	})
	return versions[len(versions)-1], nil
}

func compareNumericVersions(left, right string) int {
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	count := max(len(leftParts), len(rightParts))
	for index := range count {
		leftPart := "0"
		if index < len(leftParts) {
			leftPart = strings.TrimLeft(leftParts[index], "0")
			if leftPart == "" {
				leftPart = "0"
			}
		}
		rightPart := "0"
		if index < len(rightParts) {
			rightPart = strings.TrimLeft(rightParts[index], "0")
			if rightPart == "" {
				rightPart = "0"
			}
		}
		switch {
		case len(leftPart) < len(rightPart):
			return -1
		case len(leftPart) > len(rightPart):
			return 1
		case leftPart < rightPart:
			return -1
		case leftPart > rightPart:
			return 1
		}
	}
	return 0
}

func findVisualStudioInstallation(
	installations []visualStudioInstallation,
	id string,
) (visualStudioInstallation, bool) {
	var result visualStudioInstallation
	found := false
	for _, installation := range installations {
		if !strings.EqualFold(installation.ID, id) {
			continue
		}
		if found {
			return visualStudioInstallation{}, false
		}
		result = installation
		found = true
	}
	return result, found
}

func captureMSVCContext(
	ctx context.Context,
	options windowsAdapterOptions,
	installation visualStudioInstallation,
	requested workspace.ToolchainConfig,
) (msvcContext, error) {
	if err := verifyWindowsDirectory(installation.Path, installation.Identity); err != nil {
		return msvcContext{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "Visual Studio installation changed")
	}
	config := MSVCConfig{
		InstallationID:     requested.InstallationID,
		ToolsetVersion:     requested.ToolsetVersion,
		HostArchitecture:   requested.HostArchitecture,
		TargetArchitecture: requested.TargetArchitecture,
	}
	toolsetPath := filepath.Join(
		installation.Path,
		"VC",
		"Tools",
		"MSVC",
		config.ToolsetVersion,
	)
	toolset, toolsetIdentity, err := canonicalWindowsDirectoryIdentity(toolsetPath)
	if err != nil || !pathWithinWindowsRoot(installation.Path, toolset) {
		return msvcContext{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "MSVC toolset is invalid")
	}
	vsDevCmdPath := filepath.Join(installation.Path, "Common7", "Tools", "VsDevCmd.bat")
	vsDevCmd, err := openWindowsToolSnapshot(ctx, vsDevCmdPath)
	if err != nil {
		if isContextError(err) {
			return msvcContext{}, err
		}
		return msvcContext{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "VsDevCmd is invalid")
	}
	defer vsDevCmd.Close()
	if !validVsDevCmdPath(vsDevCmd.path) {
		return msvcContext{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "VsDevCmd path is unsafe")
	}
	cmd, err := openWindowsToolSnapshot(ctx, options.config.CmdPath)
	if err != nil {
		if isContextError(err) {
			return msvcContext{}, err
		}
		return msvcContext{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "cmd executable is invalid")
	}
	defer cmd.Close()
	args, err := buildVsDevCmdArguments(vsDevCmd.path, config)
	if err != nil {
		return msvcContext{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "VsDevCmd arguments are invalid")
	}
	verifyRoots := func() error {
		if err := vsDevCmd.Verify(ctx); err != nil {
			return err
		}
		if err := verifyWindowsDirectory(installation.Path, installation.Identity); err != nil {
			return err
		}
		return verifyWindowsDirectory(toolset, toolsetIdentity)
	}
	output, err := runWindowsProbe(
		ctx,
		options.runner,
		cmd,
		args,
		options.config.BaseEnvironment,
		maxCapturedEnvironmentBytes,
		false,
		verifyRoots,
	)
	if err != nil {
		return msvcContext{}, err
	}
	environment, err := parseCapturedEnvironment(output)
	if err != nil {
		return msvcContext{}, invalidProbe("TOOLCHAIN_ENVIRONMENT_INVALID", "MSVC environment is invalid")
	}
	values := windowsEnvironmentValues(environment)
	if values["VCTOOLSVERSION"] != config.ToolsetVersion ||
		!strings.EqualFold(values["VSCMD_ARG_HOST_ARCH"], config.HostArchitecture) ||
		!strings.EqualFold(values["VSCMD_ARG_TGT_ARCH"], config.TargetArchitecture) {
		return msvcContext{}, invalidProbe("TOOLCHAIN_ENVIRONMENT_INVALID", "MSVC environment does not match selection")
	}
	environmentToolset, environmentToolsetIdentity, err := canonicalWindowsDirectoryIdentity(
		strings.TrimRight(values["VCTOOLSINSTALLDIR"], `\/`),
	)
	if err != nil || identityPath(environmentToolset) != identityPath(toolset) ||
		environmentToolsetIdentity != toolsetIdentity {
		return msvcContext{}, invalidProbe("TOOLCHAIN_ENVIRONMENT_INVALID", "MSVC environment toolset is invalid")
	}
	sdkVersion := strings.TrimRight(values["WINDOWSSDKVERSION"], `\/`)
	if !windowsSDKVersionPattern.MatchString(sdkVersion) ||
		len(sdkVersion) > maxVSWhereVersionBytes {
		return msvcContext{}, invalidProbe("TOOLCHAIN_ENVIRONMENT_INVALID", "Windows SDK version is invalid")
	}
	sdk, sdkIdentity, err := canonicalWindowsDirectoryIdentity(
		strings.TrimRight(values["WINDOWSSDKDIR"], `\/`),
	)
	if err != nil {
		return msvcContext{}, invalidProbe("TOOLCHAIN_ENVIRONMENT_INVALID", "Windows SDK directory is invalid")
	}
	hostDirectory := "Host" + config.HostArchitecture
	binaryDirectory := filepath.Join(toolset, "bin", hostDirectory, config.TargetArchitecture)
	candidate := msvcContext{
		id:               requested.ID,
		manual:           requested.ID != "",
		installation:     installation,
		toolset:          toolset,
		toolsetIdentity:  toolsetIdentity,
		config:           config,
		environment:      append([]string(nil), environment...),
		sdk:              sdk,
		sdkIdentity:      sdkIdentity,
		sdkVersion:       sdkVersion,
		cl:               filepath.Join(binaryDirectory, "cl.exe"),
		link:             filepath.Join(binaryDirectory, "link.exe"),
		msbuild:          filepath.Join(installation.Path, "MSBuild", "Current", "Bin", "MSBuild.exe"),
		ninja:            options.config.NinjaPath,
		vsDevCmd:         vsDevCmd.path,
		vsDevCmdIdentity: vsDevCmd.identity,
	}
	if err := candidate.verify(ctx); err != nil {
		return msvcContext{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "MSVC context changed")
	}
	return candidate, nil
}

func (candidate msvcContext) verify(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := verifyWindowsDirectory(
		candidate.installation.Path,
		candidate.installation.Identity,
	); err != nil {
		return err
	}
	if err := verifyWindowsDirectory(candidate.toolset, candidate.toolsetIdentity); err != nil {
		return err
	}
	return verifyWindowsDirectory(candidate.sdk, candidate.sdkIdentity)
}

func windowsEnvironmentValues(environment []string) map[string]string {
	result := make(map[string]string, len(environment))
	for _, entry := range environment {
		separator := strings.IndexByte(entry, '=')
		if separator <= 0 {
			continue
		}
		result[strings.ToUpper(entry[:separator])] = entry[separator+1:]
	}
	return result
}

func (adapter *msvcAdapter) probeContext(
	ctx context.Context,
	candidate msvcContext,
) (Instance, error) {
	cl, err := openWindowsToolSnapshot(ctx, candidate.cl)
	if err != nil {
		return Instance{}, contextualWindowsProbeError(err, "MSVC compiler is invalid")
	}
	defer cl.Close()
	link, err := openWindowsToolSnapshot(ctx, candidate.link)
	if err != nil {
		return Instance{}, contextualWindowsProbeError(err, "MSVC linker is invalid")
	}
	defer link.Close()
	verify := func() error {
		if err := candidate.verify(ctx); err != nil {
			return err
		}
		if err := cl.Verify(ctx); err != nil {
			return err
		}
		return link.Verify(ctx)
	}
	compilerOutput, err := runWindowsProbe(
		ctx,
		adapter.options.runner,
		cl,
		[]string{"/Bv"},
		candidate.environment,
		maxWindowsProbeOutput,
		true,
		verify,
	)
	if err != nil {
		return Instance{}, err
	}
	compilerVersion, compilerArchitecture, err := parseMSVCCompilerBanner(compilerOutput)
	if err != nil ||
		!strings.EqualFold(compilerArchitecture, candidate.config.TargetArchitecture) {
		return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "MSVC compiler output is incompatible")
	}
	linkerOutput, err := runWindowsProbe(
		ctx,
		adapter.options.runner,
		link,
		[]string{"/?"},
		candidate.environment,
		maxWindowsProbeOutput,
		true,
		verify,
	)
	if err != nil {
		return Instance{}, err
	}
	linkerVersion, err := parseMSVCLinkerBanner(linkerOutput)
	if err != nil || !compatibleMSVCVersions(
		compilerVersion,
		linkerVersion,
		candidate.config.ToolsetVersion,
	) {
		return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "MSVC compiler and linker versions are incompatible")
	}
	generators, err := adapter.probeMSVCGenerators(ctx, candidate, verify)
	if err != nil {
		return Instance{}, err
	}
	if err := verify(); err != nil {
		return Instance{}, contextualWindowsProbeError(err, "MSVC identity changed")
	}
	instance := Instance{
		ID:                 candidate.id,
		Family:             FamilyMSVC,
		CCompiler:          cl.path,
		CXXCompiler:        cl.path,
		Version:            compilerVersion,
		TargetTriple:       windowsTargetTriple(candidate.config.TargetArchitecture, "msvc"),
		HostArchitecture:   candidate.config.HostArchitecture,
		TargetArchitecture: candidate.config.TargetArchitecture,
		Sysroot:            candidate.sdk,
		Environment:        append([]string(nil), candidate.environment...),
		Generators:         generators,
	}
	if !candidate.manual {
		instance.ID, err = automaticToolchainID(
			instance,
			cl.identity,
			link.identity,
			candidate.sdkIdentity+"\x00"+candidate.sdkVersion+
				"\x00"+candidate.installation.Identity+
				"\x00"+candidate.toolsetIdentity,
		)
		if err != nil {
			return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "construct MSVC toolchain id")
		}
	}
	return instance, nil
}

func (adapter *msvcAdapter) probeMSVCGenerators(
	ctx context.Context,
	candidate msvcContext,
	verify func() error,
) ([]string, error) {
	generators := make([]string, 0, 2)
	if msbuild, err := openWindowsToolSnapshot(ctx, candidate.msbuild); err == nil {
		output, probeErr := runWindowsProbe(
			ctx,
			adapter.options.runner,
			msbuild,
			[]string{"-version", "-nologo"},
			candidate.environment,
			maxWindowsProbeOutput,
			false,
			verify,
		)
		_ = msbuild.Close()
		if probeErr == nil {
			version, parseErr := parseSingleLine(output, 128)
			if parseErr == nil && sameVersionMajor(version, candidate.installation.Version) {
				if generator := visualStudioGenerator(version); generator != "" {
					generators = append(generators, generator)
				}
			}
		} else if isContextError(probeErr) {
			return nil, probeErr
		}
	} else if isContextError(err) {
		return nil, err
	}
	if candidate.ninja != "" {
		if ninja, err := openWindowsToolSnapshot(ctx, candidate.ninja); err == nil {
			output, probeErr := runWindowsProbe(
				ctx,
				adapter.options.runner,
				ninja,
				[]string{"--version"},
				candidate.environment,
				maxWindowsProbeOutput,
				false,
				verify,
			)
			_ = ninja.Close()
			if probeErr == nil {
				version, parseErr := parseSingleLine(output, 128)
				if parseErr == nil && versionPattern.MatchString(version) {
					generators = append(generators, "Ninja")
				}
			} else if isContextError(probeErr) {
				return nil, probeErr
			}
		} else if isContextError(err) {
			return nil, err
		}
	}
	if len(generators) == 0 {
		return nil, invalidProbe("BUILD_TOOL_NOT_FOUND", "MSVC generator is unavailable")
	}
	sort.Strings(generators)
	return generators, nil
}

func parseMSVCCompilerBanner(output []byte) (string, string, error) {
	match := msvcCompilerBannerPattern.FindSubmatch(output)
	if len(match) != 3 {
		return "", "", errors.New("unrecognized MSVC compiler banner")
	}
	return string(match[1]), strings.ToLower(string(match[2])), nil
}

func parseMSVCLinkerBanner(output []byte) (string, error) {
	match := msvcLinkerBannerPattern.FindSubmatch(output)
	if len(match) != 2 {
		return "", errors.New("unrecognized MSVC linker banner")
	}
	return string(match[1]), nil
}

func compatibleMSVCVersions(compiler, linker, toolset string) bool {
	compilerParts := strings.Split(compiler, ".")
	linkerParts := strings.Split(linker, ".")
	toolsetParts := strings.Split(toolset, ".")
	return len(compilerParts) >= 2 && len(linkerParts) >= 2 && len(toolsetParts) >= 2 &&
		compilerParts[0] == "19" && linkerParts[0] == "14" &&
		toolsetParts[0] == "14" &&
		compilerParts[1] == linkerParts[1] &&
		compilerParts[1] == toolsetParts[1]
}

func sameVersionMajor(left, right string) bool {
	leftMajor, _, leftFound := strings.Cut(left, ".")
	rightMajor, _, rightFound := strings.Cut(right, ".")
	return leftFound && rightFound && leftMajor == rightMajor
}

func versionMajor(version string) string {
	major, _, _ := strings.Cut(version, ".")
	return major
}

func visualStudioGenerator(version string) string {
	switch versionMajor(version) {
	case "17":
		return "Visual Studio 17 2022"
	case "18":
		return "Visual Studio 18 2026"
	default:
		return ""
	}
}

func windowsTargetTriple(architecture, suffix string) string {
	var prefix string
	switch architecture {
	case "x86":
		prefix = "i686"
	case "x64":
		prefix = "x86_64"
	case "arm64":
		prefix = "aarch64"
	default:
		return ""
	}
	return prefix + "-pc-windows-" + suffix
}

func contextualWindowsProbeError(err error, message string) error {
	if isContextError(err) {
		return err
	}
	return invalidProbe("TOOLCHAIN_PROBE_FAILED", message)
}

func issueCodeFromProbeError(err error) string {
	var typed *toolchainProbeError
	if errors.As(err, &typed) {
		switch typed.code {
		case "BUILD_TOOL_NOT_FOUND",
			"TOOLCHAIN_ENVIRONMENT_INVALID",
			"TOOLCHAIN_MANUAL_SELECTION_FAILED",
			"TOOLCHAIN_LIMIT_EXCEEDED":
			return typed.code
		}
	}
	return "TOOLCHAIN_PROBE_FAILED"
}

func appendWindowsDiscoveryIssue(issues *[]Issue, code string) {
	if len(*issues) >= maxWindowsDiscoveryIssues {
		return
	}
	switch code {
	case "BUILD_TOOL_NOT_FOUND":
		*issues = append(*issues, Issue{
			Code:    "WINDOWS_BUILD_TOOL_NOT_FOUND",
			Message: "Windows toolchain has no verified build generator",
		})
	case "TOOLCHAIN_ENVIRONMENT_INVALID":
		*issues = append(*issues, Issue{
			Code: code, Message: "Windows toolchain environment is invalid",
		})
	case "TOOLCHAIN_MANUAL_SELECTION_FAILED":
		*issues = append(*issues, Issue{
			Code: code, Message: "manual Windows toolchain selection was not found",
		})
	case "TOOLCHAIN_LIMIT_EXCEEDED":
		*issues = append(*issues, Issue{
			Code: code, Message: "Windows toolchain discovery limit exceeded", Blocking: true,
		})
	default:
		*issues = append(*issues, Issue{
			Code: "TOOLCHAIN_PROBE_FAILED", Message: "Windows toolchain candidate probe failed",
		})
	}
}

func sortWindowsInstances(instances []Instance) {
	sort.Slice(instances, func(left, right int) bool {
		a, b := instances[left], instances[right]
		return lessStrings(
			[]string{
				string(a.Family), a.TargetTriple, a.Version,
				identityPath(a.CCompiler), a.ID,
			},
			[]string{
				string(b.Family), b.TargetTriple, b.Version,
				identityPath(b.CCompiler), b.ID,
			},
		)
	})
}

func finishWindowsDiscovery(instances []Instance, issues []Issue) ([]Instance, error) {
	sort.Slice(issues, func(left, right int) bool {
		return lessStrings(
			[]string{issues[left].Code, issues[left].Message},
			[]string{issues[right].Code, issues[right].Message},
		)
	})
	if len(issues) != 0 {
		return cloneInstances(instances), &discoveryIssuesError{
			issues: append([]Issue(nil), issues...),
		}
	}
	return cloneInstances(instances), nil
}

func buildVsDevCmdArguments(path string, config MSVCConfig) ([]string, error) {
	if !validVsDevCmdPath(path) ||
		!validArchitectureValue(config.HostArchitecture) ||
		!validArchitectureValue(config.TargetArchitecture) ||
		!versionPattern.MatchString(config.ToolsetVersion) ||
		len(config.ToolsetVersion) > maxVSWhereVersionBytes {
		return nil, fmt.Errorf("%w: invalid VsDevCmd invocation", ErrInvalidToolchain)
	}
	command := fmt.Sprintf(
		`"call \"%s\" -no_logo -host_arch=%s -arch=%s -vcvars_ver=%s && set"`,
		filepath.Clean(path),
		config.HostArchitecture,
		config.TargetArchitecture,
		config.ToolsetVersion,
	)
	return []string{"/d", "/s", "/c", command}, nil
}

func validVsDevCmdPath(path string) bool {
	return filepath.IsAbs(path) &&
		len(path) <= maxVSWherePathBytes &&
		!strings.ContainsAny(path, "\x00\r\n\"%!^&|<>") &&
		strings.EqualFold(filepath.Ext(path), ".bat")
}

func validArchitectureValue(value string) bool {
	return value == "x86" || value == "x64" || value == "arm64"
}

func parseCapturedEnvironment(output []byte) ([]string, error) {
	if len(output) > maxCapturedEnvironmentBytes || !utf8.Valid(output) || bytesContainNUL(output) {
		return nil, fmt.Errorf("%w: invalid captured environment", ErrInvalidToolchain)
	}
	lines := bytes.Split(output, []byte{'\n'})
	if len(lines) > maxCapturedEnvironmentEntries+1 {
		return nil, fmt.Errorf("%w: captured environment has too many entries", ErrInvalidToolchain)
	}
	type entry struct {
		key   string
		value string
	}
	byKey := make(map[string]entry, len(lines))
	inputCount := 0
	for _, raw := range lines {
		line := strings.TrimSuffix(string(raw), "\r")
		if line == "" {
			continue
		}
		inputCount++
		if inputCount > maxCapturedEnvironmentEntries ||
			len(line) > maxCapturedEnvironmentEntryBytes {
			return nil, fmt.Errorf("%w: captured environment limit exceeded", ErrInvalidToolchain)
		}
		if isDriveCurrentDirectoryEntry(line) {
			continue
		}
		separator := strings.IndexByte(line, '=')
		if separator <= 0 {
			return nil, fmt.Errorf("%w: malformed captured environment entry", ErrInvalidToolchain)
		}
		key, value := line[:separator], line[separator+1:]
		if !validEnvironmentKey(key) {
			return nil, fmt.Errorf("%w: malformed captured environment key", ErrInvalidToolchain)
		}
		identity := strings.ToUpper(key)
		if previous, duplicate := byKey[identity]; duplicate {
			if previous.value != value {
				return nil, fmt.Errorf("%w: conflicting captured environment key", ErrInvalidToolchain)
			}
			continue
		}
		if sensitiveEnvironmentKey(identity) {
			continue
		}
		byKey[identity] = entry{key: key, value: value}
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	total := 0
	for _, key := range keys {
		value := byKey[key]
		encoded := value.key + "=" + value.value
		total += len(encoded)
		if total > maxCapturedEnvironmentBytes {
			return nil, fmt.Errorf("%w: captured environment exceeds total limit", ErrInvalidToolchain)
		}
		result = append(result, encoded)
	}
	return result, nil
}

func isDriveCurrentDirectoryEntry(line string) bool {
	return len(line) >= 4 && line[0] == '=' &&
		(line[1] >= 'A' && line[1] <= 'Z' || line[1] >= 'a' && line[1] <= 'z') &&
		line[2] == ':' && line[3] == '='
}

func validEnvironmentKey(key string) bool {
	if key == "" || len(key) > 256 {
		return false
	}
	for index := range len(key) {
		character := key[index]
		if character >= 'A' && character <= 'Z' ||
			character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '(' || character == ')' {
			continue
		}
		return false
	}
	return true
}

func sensitiveEnvironmentKey(key string) bool {
	switch key {
	case "GH_TOKEN", "SYSTEM_ACCESSTOKEN", "DATABASE_URL":
		return true
	}
	if strings.HasPrefix(key, "UNIT_TEST_") ||
		strings.HasPrefix(key, "GITHUB_") ||
		strings.HasPrefix(key, "ACTIONS_") ||
		strings.HasPrefix(key, "SERVICE_CONTROL_") ||
		strings.Contains(key, "CONTROL") ||
		strings.HasSuffix(key, "_HANDLE") {
		return true
	}
	for _, marker := range []string{
		"TOKEN", "SECRET", "PASSWORD", "PASSWD", "PRIVATE_KEY",
		"ACCESS_KEY", "CLIENT_SECRET", "CONNECTION_STRING",
	} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}
