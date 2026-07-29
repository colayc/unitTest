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
	maxCapturedEnvironmentBytes       = 64 * 1024
	maxCapturedEnvironmentEntries     = 256
	maxCapturedEnvironmentEntryBytes  = 4096
	maxCapturedEnvironmentPathEntries = 64
)

var (
	msvcCompilerBannerPattern = regexp.MustCompile(
		`(?i)\bCompiler Version ([0-9]+\.[0-9]+(?:\.[0-9]+){0,2}) for (x86|x64|arm64)\b`,
	)
	msvcCompilerPathMarkerPattern = regexp.MustCompile(`(?i)\\cl\.exe:`)
	msvcFileVersionPattern        = regexp.MustCompile(
		`\b([0-9]+\.[0-9]+(?:\.[0-9]+){0,2})\b`,
	)
	msvcArchitecturePattern = regexp.MustCompile(`(?i)\b(x86|x64|arm64)\b`)
	msvcLinkerBannerPattern = regexp.MustCompile(
		`(?i)\bLinker Version ([0-9]+\.[0-9]+(?:\.[0-9]+){0,2})\b`,
	)
	msbuildVersionPattern = regexp.MustCompile(
		`(?:^|[ \t\r\n])([0-9]+(?:\.[0-9]+){1,3})(?:\+[A-Za-z0-9._-]+)?(?:$|[ \t\r\n])`,
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

type windowsDirectoryReference struct {
	role     string
	path     string
	identity string
}

type windowsDirectoryRule struct {
	role  string
	root  string
	exact bool
}

type windowsGeneratorProbeResult struct {
	names          []string
	directories    []windowsDirectoryReference
	toolIdentities []string
}

type msvcContext struct {
	id                  string
	manual              bool
	installation        visualStudioInstallation
	toolset             string
	toolsetIdentity     string
	config              MSVCConfig
	environment         []string
	verifiedDirectories []windowsDirectoryReference
	sdk                 string
	sdkIdentity         string
	sdkVersion          string
	environmentIdentity string
	cl                  string
	link                string
	msbuild             string
	ninja               string
	vsDevCmd            string
	vsDevCmdIdentity    string
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
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			version, err := latestMSVCToolset(ctx, installation.Path)
			if err != nil {
				if isContextError(err) {
					return nil, nil, err
				}
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

func latestMSVCToolset(ctx context.Context, installation string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root := filepath.Join(installation, "VC", "Tools", "MSVC")
	file, err := os.Open(root)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
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
		if err := ctx.Err(); err != nil {
			return "", err
		}
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
	if err := ctx.Err(); err != nil {
		return "", err
	}
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
	if !validVsDevCmdPath(vsDevCmd.path) ||
		!pathWithinWindowsRoot(installation.Path, vsDevCmd.path) {
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
		if err := options.config.verifyBaseDirectories(ctx); err != nil {
			return err
		}
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
	binaryDirectory, binaryIdentity, err := canonicalWindowsDirectoryIdentity(
		filepath.Join(toolset, "bin", hostDirectory, config.TargetArchitecture),
	)
	if err != nil || !pathWithinWindowsRoot(toolset, binaryDirectory) {
		return msvcContext{}, invalidProbe("TOOLCHAIN_ENVIRONMENT_INVALID", "MSVC binary directory is invalid")
	}
	binaryReference := windowsDirectoryReference{
		role: "path-msvc-bin", path: binaryDirectory, identity: binaryIdentity,
	}
	toolsetInclude, err := captureWindowsDirectoryWithin(
		filepath.Join(toolset, "include"),
		toolset,
	)
	if err != nil {
		return msvcContext{}, invalidProbe("TOOLCHAIN_ENVIRONMENT_INVALID", "MSVC include directory is invalid")
	}
	toolsetInclude.role = "include-toolset"
	toolsetLibrary, err := captureWindowsDirectoryWithin(
		filepath.Join(toolset, "lib", config.TargetArchitecture),
		toolset,
	)
	if err != nil {
		return msvcContext{}, invalidProbe("TOOLCHAIN_ENVIRONMENT_INVALID", "MSVC library directory is invalid")
	}
	toolsetLibrary.role = "lib-toolset-" + config.TargetArchitecture
	sdkIncludeRoot := filepath.Join(sdk, "Include", sdkVersion)
	sdkLibraryRoot := filepath.Join(sdk, "Lib", sdkVersion)
	sdkIncludes := make([]windowsDirectoryReference, 0, 3)
	for _, name := range []string{"ucrt", "um", "shared"} {
		reference, referenceErr := captureWindowsDirectoryWithin(
			filepath.Join(sdkIncludeRoot, name),
			sdk,
		)
		if referenceErr != nil {
			return msvcContext{}, invalidProbe(
				"TOOLCHAIN_ENVIRONMENT_INVALID",
				"Windows SDK include tree is invalid",
			)
		}
		reference.role = "include-sdk-" + name
		sdkIncludes = append(sdkIncludes, reference)
	}
	sdkLibraries := make([]windowsDirectoryReference, 0, 2)
	for _, name := range []string{"ucrt", "um"} {
		reference, referenceErr := captureWindowsDirectoryWithin(
			filepath.Join(sdkLibraryRoot, name, config.TargetArchitecture),
			sdk,
		)
		if referenceErr != nil {
			return msvcContext{}, invalidProbe(
				"TOOLCHAIN_ENVIRONMENT_INVALID",
				"Windows SDK library tree is invalid",
			)
		}
		reference.role = "lib-sdk-" + name + "-" + config.TargetArchitecture
		sdkLibraries = append(sdkLibraries, reference)
	}
	requiredIncludes := append(
		[]windowsDirectoryReference{toolsetInclude},
		sdkIncludes...,
	)
	allowedIncludes := append(
		[]windowsDirectoryReference(nil),
		requiredIncludes...,
	)
	if auxiliary, ok := captureOptionalWindowsDirectoryWithin(
		filepath.Join(installation.Path, "VC", "Auxiliary", "VS", "include"),
		installation.Path,
		"include-vs-auxiliary",
	); ok {
		allowedIncludes = append(allowedIncludes, auxiliary)
	}
	for _, name := range []string{"winrt", "cppwinrt"} {
		if optional, ok := captureOptionalWindowsDirectoryWithin(
			filepath.Join(sdkIncludeRoot, name),
			sdk,
			"include-sdk-"+name,
		); ok {
			allowedIncludes = append(allowedIncludes, optional)
		}
	}
	requiredLibraries := append(
		[]windowsDirectoryReference{toolsetLibrary},
		sdkLibraries...,
	)
	includeValue, includeReferences, err := filterWindowsEnvironmentDirectoryList(
		ctx,
		values["INCLUDE"],
		requiredIncludes,
		exactWindowsDirectoryRules(allowedIncludes),
	)
	if err != nil {
		return msvcContext{}, contextualWindowsProbeError(
			err,
			"MSVC INCLUDE environment is invalid",
		)
	}
	libraryValue, libraryReferences, err := filterWindowsEnvironmentDirectoryList(
		ctx,
		values["LIB"],
		requiredLibraries,
		exactWindowsDirectoryRules(requiredLibraries),
	)
	if err != nil {
		return msvcContext{}, contextualWindowsProbeError(
			err,
			"MSVC LIB environment is invalid",
		)
	}
	baseValues := windowsEnvironmentValues(options.config.BaseEnvironment)
	systemRoot, _, rootErr := canonicalWindowsDirectoryIdentity(
		strings.TrimRight(baseValues["SYSTEMROOT"], `\/`),
	)
	if rootErr != nil {
		return msvcContext{}, invalidProbe(
			"TOOLCHAIN_ENVIRONMENT_INVALID",
			"SystemRoot directory is invalid",
		)
	}
	toolsetLibRoot, _, toolsetLibErr := canonicalWindowsDirectoryIdentity(
		filepath.Join(toolset, "lib"),
	)
	if toolsetLibErr != nil || !pathWithinWindowsRoot(toolset, toolsetLibRoot) {
		return msvcContext{}, invalidProbe(
			"TOOLCHAIN_ENVIRONMENT_INVALID",
			"MSVC library root is invalid",
		)
	}
	libPathRules := []windowsDirectoryRule{{
		role: "libpath-toolset", root: toolsetLibRoot,
	}}
	for _, optional := range []struct {
		path string
		role string
	}{
		{
			path: filepath.Join(sdk, "UnionMetadata", sdkVersion),
			role: "libpath-sdk-union-metadata",
		},
		{
			path: filepath.Join(sdk, "References", sdkVersion),
			role: "libpath-sdk-references",
		},
	} {
		if reference, ok := captureOptionalWindowsDirectoryWithin(
			optional.path,
			sdk,
			optional.role,
		); ok {
			libPathRules = append(libPathRules, windowsDirectoryRule{
				role: reference.role, root: reference.path,
			})
		}
	}
	if dotNet, ok := captureOptionalWindowsDirectoryWithin(
		filepath.Join(systemRoot, "Microsoft.NET"),
		systemRoot,
		"libpath-system-dotnet",
	); ok {
		libPathRules = append(libPathRules, windowsDirectoryRule{
			role: dotNet.role, root: dotNet.path,
		})
	}
	var netFXReference windowsDirectoryReference
	var netFXRoot string
	if value := strings.TrimRight(values["NETFXSDKDIR"], `\/`); value != "" {
		netFXParent := filepath.Join(filepath.Dir(sdk), "NETFXSDK")
		if reference, ok := captureOptionalWindowsDirectoryWithin(
			value,
			netFXParent,
			"environment-netfx-sdk-root",
		); ok {
			netFXReference = reference
			netFXRoot = reference.path
			libPathRules = append(libPathRules, windowsDirectoryRule{
				role: "libpath-netfx-sdk", root: reference.path,
			})
		}
	}
	libPathValue, libPathReferences, err := filterWindowsEnvironmentDirectoryList(
		ctx,
		values["LIBPATH"],
		[]windowsDirectoryReference{toolsetLibrary},
		libPathRules,
	)
	if err != nil {
		return msvcContext{}, contextualWindowsProbeError(
			err,
			"MSVC LIBPATH environment is invalid",
		)
	}
	pathRules := []windowsDirectoryRule{
		{role: "path-toolset", root: toolset},
		{role: "path-installation", root: installation.Path},
		{role: "path-sdk", root: sdk},
		{role: "path-system", root: systemRoot},
	}
	if netFXRoot != "" {
		pathRules = append(pathRules, windowsDirectoryRule{
			role: "path-netfx-sdk", root: netFXRoot,
		})
	}
	pathValue, pathReferences, err := filterWindowsEnvironmentDirectoryList(
		ctx,
		values["PATH"],
		[]windowsDirectoryReference{binaryReference},
		pathRules,
	)
	if err != nil {
		return msvcContext{}, contextualWindowsProbeError(
			err,
			"MSVC PATH environment is invalid",
		)
	}
	replacements := map[string]string{
		"INCLUDE": includeValue,
		"LIB":     libraryValue,
		"LIBPATH": libPathValue,
		"PATH":    pathValue,
	}
	removals := map[string]struct{}{"NETFXSDKDIR": {}}
	if netFXRoot != "" {
		replacements["NETFXSDKDIR"] = netFXRoot + string(filepath.Separator)
		delete(removals, "NETFXSDKDIR")
	}
	environment = replaceWindowsEnvironmentValues(
		environment,
		replacements,
		removals,
	)
	verifiedDirectories := make([]windowsDirectoryReference, 0,
		len(includeReferences)+len(libraryReferences)+
			len(libPathReferences)+len(pathReferences)+1)
	verifiedDirectories = append(verifiedDirectories, includeReferences...)
	verifiedDirectories = append(verifiedDirectories, libraryReferences...)
	verifiedDirectories = append(verifiedDirectories, libPathReferences...)
	verifiedDirectories = append(verifiedDirectories, pathReferences...)
	if netFXRoot != "" {
		verifiedDirectories = append(verifiedDirectories, netFXReference)
	}
	environmentIdentity := windowsDirectoryDescriptorIdentity(verifiedDirectories)
	ninja, err := discoverWindowsNinjaPath(
		ctx,
		options.config.NinjaPath,
		installation,
	)
	if err != nil {
		return msvcContext{}, err
	}
	candidate := msvcContext{
		id:                  requested.ID,
		manual:              requested.ID != "",
		installation:        installation,
		toolset:             toolset,
		toolsetIdentity:     toolsetIdentity,
		config:              config,
		environment:         append([]string(nil), environment...),
		verifiedDirectories: verifiedDirectories,
		sdk:                 sdk,
		sdkIdentity:         sdkIdentity,
		sdkVersion:          sdkVersion,
		environmentIdentity: environmentIdentity,
		cl:                  filepath.Join(binaryDirectory, "cl.exe"),
		link:                filepath.Join(binaryDirectory, "link.exe"),
		msbuild:             filepath.Join(installation.Path, "MSBuild", "Current", "Bin", "MSBuild.exe"),
		ninja:               ninja,
		vsDevCmd:            vsDevCmd.path,
		vsDevCmdIdentity:    vsDevCmd.identity,
	}
	if options.config.afterEnvironmentValidation != nil {
		options.config.afterEnvironmentValidation()
	}
	if err := candidate.verify(ctx); err != nil {
		if isContextError(err) {
			return msvcContext{}, err
		}
		return msvcContext{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "MSVC context changed")
	}
	return candidate, nil
}

func discoverWindowsNinjaPath(
	ctx context.Context,
	configured string,
	installation visualStudioInstallation,
) (string, error) {
	candidates := []struct {
		path string
		root string
	}{
		{path: configured},
		{
			path: filepath.Join(
				installation.Path,
				"Common7",
				"IDE",
				"CommonExtensions",
				"Microsoft",
				"CMake",
				"Ninja",
				"ninja.exe",
			),
			root: installation.Path,
		},
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if candidate.path == "" {
			continue
		}
		key := identityPath(candidate.path)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		ninja, err := openWindowsToolSnapshot(ctx, candidate.path)
		if err != nil {
			if isContextError(err) {
				return "", err
			}
			continue
		}
		toolPath, _, identityErr := canonicalWindowsFileSystemIdentity(ninja.path)
		verifyErr := ninja.Verify(ctx)
		_ = ninja.Close()
		if isContextError(identityErr) {
			return "", identityErr
		}
		if isContextError(verifyErr) {
			return "", verifyErr
		}
		if identityErr != nil || verifyErr != nil ||
			identityPath(toolPath) != identityPath(ninja.path) {
			continue
		}
		if candidate.root != "" &&
			(!pathWithinWindowsRoot(candidate.root, toolPath) ||
				verifyWindowsDirectory(
					installation.Path,
					installation.Identity,
				) != nil) {
			continue
		}
		return toolPath, nil
	}
	return "", nil
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
	if err := verifyWindowsDirectory(candidate.sdk, candidate.sdkIdentity); err != nil {
		return err
	}
	for _, reference := range candidate.verifiedDirectories {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := verifyWindowsDirectory(reference.path, reference.identity); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func captureWindowsDirectoryWithin(
	path string,
	root string,
) (windowsDirectoryReference, error) {
	canonical, identity, err := canonicalWindowsDirectoryIdentity(path)
	if err != nil || !pathWithinWindowsRoot(root, canonical) {
		return windowsDirectoryReference{}, errors.New("directory leaves verified root")
	}
	return windowsDirectoryReference{path: canonical, identity: identity}, nil
}

func filterWindowsEnvironmentDirectoryList(
	ctx context.Context,
	value string,
	required []windowsDirectoryReference,
	rules []windowsDirectoryRule,
) (string, []windowsDirectoryReference, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if len(value) > maxCapturedEnvironmentBytes {
		return "", nil, errors.New("environment directory list is oversized")
	}
	paths := filepath.SplitList(value)
	if len(paths) > maxCapturedEnvironmentPathEntries {
		return "", nil, errors.New("environment directory list count is invalid")
	}
	result := make([]windowsDirectoryReference, 0, len(paths))
	acceptedPaths := make([]string, 0, len(paths))
	found := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		path = strings.TrimSpace(path)
		if len(path) >= 2 && path[0] == '"' && path[len(path)-1] == '"' {
			path = strings.TrimSpace(path[1 : len(path)-1])
		}
		if path == "" || len(path) > maxWindowsDirectoryPathBytes || !filepath.IsAbs(path) {
			continue
		}
		canonical, identity, err := canonicalWindowsDirectoryIdentity(path)
		if err != nil {
			continue
		}
		role := ""
		for _, rule := range rules {
			if rule.exact && identityPath(canonical) == identityPath(rule.root) ||
				!rule.exact && pathWithinWindowsRoot(rule.root, canonical) {
				role = rule.role
				break
			}
		}
		if role == "" {
			continue
		}
		key := identityPath(canonical)
		if _, duplicate := found[key]; duplicate {
			continue
		}
		found[key] = struct{}{}
		acceptedPaths = append(acceptedPaths, canonical)
		result = append(result, windowsDirectoryReference{
			role: role, path: canonical, identity: identity,
		})
	}
	for _, reference := range required {
		if _, ok := found[identityPath(reference.path)]; !ok {
			return "", nil, errors.New("required environment directory is missing")
		}
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	return strings.Join(acceptedPaths, ";"), result, nil
}

func exactWindowsDirectoryRules(
	references []windowsDirectoryReference,
) []windowsDirectoryRule {
	result := make([]windowsDirectoryRule, 0, len(references))
	for _, reference := range references {
		result = append(result, windowsDirectoryRule{
			role: reference.role, root: reference.path, exact: true,
		})
	}
	return result
}

func captureOptionalWindowsDirectoryWithin(
	path string,
	root string,
	role string,
) (windowsDirectoryReference, bool) {
	reference, err := captureWindowsDirectoryWithin(path, root)
	if err != nil {
		return windowsDirectoryReference{}, false
	}
	reference.role = role
	return reference, true
}

func replaceWindowsEnvironmentPathLists(
	environment []string,
	replacements map[string]string,
) []string {
	return replaceWindowsEnvironmentValues(environment, replacements, nil)
}

func replaceWindowsEnvironmentValues(
	environment []string,
	replacements map[string]string,
	removals map[string]struct{},
) []string {
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		separator := strings.IndexByte(entry, '=')
		if separator <= 0 {
			continue
		}
		key := strings.ToUpper(entry[:separator])
		if _, remove := removals[key]; remove {
			continue
		}
		if value, ok := replacements[key]; ok {
			result = append(result, canonicalWindowsEnvironmentKey(key)+"="+value)
			continue
		}
		result = append(result, entry)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToUpper(result[left]) < strings.ToUpper(result[right])
	})
	return result
}

func canonicalWindowsEnvironmentKey(key string) string {
	if key == "PATH" {
		return "Path"
	}
	return key
}

func windowsDirectoryDescriptorIdentity(
	references []windowsDirectoryReference,
) string {
	owned := append([]windowsDirectoryReference(nil), references...)
	sort.Slice(owned, func(left, right int) bool {
		return lessStrings(
			[]string{
				owned[left].role,
				identityPath(owned[left].path),
				owned[left].identity,
			},
			[]string{
				owned[right].role,
				identityPath(owned[right].path),
				owned[right].identity,
			},
		)
	})
	parts := make([]string, 0, len(owned)*3)
	previous := ""
	for _, reference := range owned {
		descriptor := reference.role + "\x00" +
			identityPath(reference.path) + "\x00" +
			reference.identity
		if descriptor == previous {
			continue
		}
		previous = descriptor
		parts = append(parts, descriptor)
	}
	return strings.Join(parts, "\x00")
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
	if !pathWithinWindowsRoot(candidate.toolset, cl.path) ||
		!pathWithinWindowsRoot(candidate.toolset, link.path) {
		return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "MSVC tools leave the selected toolset")
	}
	verify := func() error {
		if err := candidate.verify(ctx); err != nil {
			return err
		}
		if err := cl.Verify(ctx); err != nil {
			return err
		}
		return link.Verify(ctx)
	}
	compilerOutput, err := runWindowsProbeWithPolicy(
		ctx,
		adapter.options.runner,
		cl,
		[]string{"/Bv"},
		candidate.environment,
		maxWindowsProbeOutput,
		true,
		verify,
		windowsProbeOutputPolicy{
			acceptedExitCodes: []int{0, 2},
			allowNonUTF8:      true,
		},
	)
	if err != nil {
		return Instance{}, err
	}
	compilerVersion, compilerArchitecture, err := parseMSVCCompilerBanner(compilerOutput)
	if err != nil ||
		!strings.EqualFold(compilerArchitecture, candidate.config.TargetArchitecture) {
		return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "MSVC compiler output is incompatible")
	}
	linkerOutput, err := runWindowsProbeWithPolicy(
		ctx,
		adapter.options.runner,
		link,
		[]string{"/?"},
		candidate.environment,
		maxWindowsProbeOutput,
		true,
		verify,
		windowsProbeOutputPolicy{
			acceptedExitCodes: []int{0, 1100},
			allowNonUTF8:      true,
		},
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
	for _, reference := range generators.directories {
		if err := verifyWindowsDirectory(reference.path, reference.identity); err != nil {
			return Instance{}, invalidProbe(
				"TOOLCHAIN_PROBE_FAILED",
				"MSVC generator directory changed",
			)
		}
	}
	if err := verify(); err != nil {
		return Instance{}, contextualWindowsProbeError(err, "MSVC identity changed")
	}
	instanceEnvironment, environmentErr := appendVerifiedGeneratorPaths(
		candidate.environment,
		generators.directories,
	)
	if environmentErr != nil {
		generators = withoutNinjaGenerator(generators)
		if len(generators.names) == 0 {
			return Instance{}, environmentErr
		}
		instanceEnvironment = append([]string(nil), candidate.environment...)
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
		Environment:        instanceEnvironment,
		Generators:         append([]string(nil), generators.names...),
	}
	if !candidate.manual {
		instance.ID, err = automaticToolchainID(
			instance,
			cl.identity,
			link.identity,
			candidate.sdkIdentity+"\x00"+candidate.sdkVersion+
				"\x00"+candidate.environmentIdentity+
				"\x00"+candidate.installation.Identity+
				"\x00"+candidate.toolsetIdentity+
				"\x00"+windowsDirectoryDescriptorIdentity(generators.directories)+
				"\x00"+strings.Join(generators.toolIdentities, "\x00"),
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
) (windowsGeneratorProbeResult, error) {
	result := windowsGeneratorProbeResult{
		names:          make([]string, 0, 2),
		directories:    make([]windowsDirectoryReference, 0, 2),
		toolIdentities: make([]string, 0, 2),
	}
	if msbuild, err := openWindowsToolSnapshot(ctx, candidate.msbuild); err == nil {
		root, rootIdentity, rootErr := canonicalWindowsDirectoryIdentity(
			filepath.Dir(msbuild.path),
		)
		verifyMSBuild := verify
		if rootErr == nil {
			verifyMSBuild = func() error {
				if err := verify(); err != nil {
					return err
				}
				return verifyWindowsDirectory(root, rootIdentity)
			}
		}
		var output []byte
		var probeErr error
		if rootErr == nil &&
			pathWithinWindowsRoot(candidate.installation.Path, root) &&
			pathWithinWindowsRoot(root, msbuild.path) {
			output, probeErr = runWindowsProbe(
				ctx,
				adapter.options.runner,
				msbuild,
				[]string{"-version", "-nologo"},
				candidate.environment,
				maxWindowsProbeOutput,
				false,
				verifyMSBuild,
			)
		}
		_ = msbuild.Close()
		if probeErr == nil && output != nil {
			version, parseErr := parseMSBuildVersion(output)
			if parseErr == nil && sameVersionMajor(version, candidate.installation.Version) {
				if generator := visualStudioGenerator(candidate.installation.Version); generator != "" {
					result.names = append(result.names, generator)
					result.directories = append(
						result.directories,
						windowsDirectoryReference{
							role: "generator-msbuild",
							path: root, identity: rootIdentity,
						},
					)
					result.toolIdentities = append(
						result.toolIdentities,
						"msbuild\x00"+msbuild.identity,
					)
				}
			}
		} else if isContextError(probeErr) {
			return windowsGeneratorProbeResult{}, probeErr
		}
	} else if isContextError(err) {
		return windowsGeneratorProbeResult{}, err
	}
	if candidate.ninja != "" {
		if ninja, err := openWindowsToolSnapshot(ctx, candidate.ninja); err == nil {
			ninjaRoot, ninjaRootIdentity, rootErr := canonicalWindowsDirectoryIdentity(
				filepath.Dir(ninja.path),
			)
			verifyNinja := verify
			if rootErr == nil {
				verifyNinja = func() error {
					if err := verify(); err != nil {
						return err
					}
					return verifyWindowsDirectory(ninjaRoot, ninjaRootIdentity)
				}
			}
			output, probeErr := runWindowsProbe(
				ctx,
				adapter.options.runner,
				ninja,
				[]string{"--version"},
				candidate.environment,
				maxWindowsProbeOutput,
				false,
				verifyNinja,
			)
			_ = ninja.Close()
			if probeErr == nil {
				version, parseErr := parseSingleLine(output, 128)
				if parseErr == nil && versionPattern.MatchString(version) &&
					rootErr == nil &&
					pathWithinWindowsRoot(ninjaRoot, ninja.path) {
					result.names = append(result.names, "Ninja")
					result.directories = append(
						result.directories,
						windowsDirectoryReference{
							role: "path-generator-ninja",
							path: ninjaRoot, identity: ninjaRootIdentity,
						},
					)
					result.toolIdentities = append(
						result.toolIdentities,
						"ninja\x00"+ninja.identity,
					)
				}
			} else if isContextError(probeErr) {
				return windowsGeneratorProbeResult{}, probeErr
			}
		} else if isContextError(err) {
			return windowsGeneratorProbeResult{}, err
		}
	}
	if len(result.names) == 0 {
		return windowsGeneratorProbeResult{},
			invalidProbe("BUILD_TOOL_NOT_FOUND", "MSVC generator is unavailable")
	}
	sort.Strings(result.names)
	sort.Strings(result.toolIdentities)
	return result, nil
}

func appendVerifiedGeneratorPaths(
	environment []string,
	references []windowsDirectoryReference,
) ([]string, error) {
	values := windowsEnvironmentValues(environment)
	if _, present := values["PATH"]; !present {
		return nil, invalidProbe(
			"TOOLCHAIN_ENVIRONMENT_INVALID",
			"Windows generator environment is invalid",
		)
	}
	paths := filepath.SplitList(values["PATH"])
	seen := make(map[string]struct{}, len(paths)+len(references))
	result := make([]string, 0, len(paths)+len(references))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		key := identityPath(path)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, path)
	}
	for _, reference := range references {
		if reference.role != "path-generator-ninja" {
			continue
		}
		key := identityPath(reference.path)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, reference.path)
	}
	if len(result) > maxCapturedEnvironmentPathEntries {
		return nil, invalidProbe(
			"TOOLCHAIN_ENVIRONMENT_INVALID",
			"Windows generator environment is invalid",
		)
	}
	merged := replaceWindowsEnvironmentPathLists(
		environment,
		map[string]string{"PATH": strings.Join(result, ";")},
	)
	if _, ok := registryEnvironmentBytes(merged); !ok {
		return nil, invalidProbe(
			"TOOLCHAIN_ENVIRONMENT_INVALID",
			"Windows generator environment is invalid",
		)
	}
	return merged, nil
}

func withoutNinjaGenerator(
	result windowsGeneratorProbeResult,
) windowsGeneratorProbeResult {
	filtered := windowsGeneratorProbeResult{
		names:          make([]string, 0, len(result.names)),
		directories:    make([]windowsDirectoryReference, 0, len(result.directories)),
		toolIdentities: make([]string, 0, len(result.toolIdentities)),
	}
	for _, name := range result.names {
		if name != "Ninja" {
			filtered.names = append(filtered.names, name)
		}
	}
	for _, reference := range result.directories {
		if reference.role != "path-generator-ninja" {
			filtered.directories = append(filtered.directories, reference)
		}
	}
	for _, identity := range result.toolIdentities {
		if !strings.HasPrefix(identity, "ninja\x00") {
			filtered.toolIdentities = append(filtered.toolIdentities, identity)
		}
	}
	return filtered
}

func parseMSVCCompilerBanner(output []byte) (string, string, error) {
	englishMatches := msvcCompilerBannerPattern.FindAllSubmatch(output, 2)
	if len(englishMatches) > 1 {
		return "", "", errors.New("duplicate MSVC compiler banner")
	}
	var englishVersion, englishArchitecture string
	if len(englishMatches) == 1 {
		englishVersion = string(englishMatches[0][1])
		englishArchitecture = strings.ToLower(string(englishMatches[0][2]))
	}

	pathMarkers := msvcCompilerPathMarkerPattern.FindAllIndex(output, 2)
	if len(pathMarkers) > 1 {
		return "", "", errors.New("duplicate MSVC compiler file version")
	}
	if len(pathMarkers) == 0 {
		if englishVersion == "" {
			return "", "", errors.New("unrecognized MSVC compiler banner")
		}
		return englishVersion, englishArchitecture, nil
	}
	versionLine := output[pathMarkers[0][1]:]
	if end := bytes.IndexAny(versionLine, "\r\n"); end >= 0 {
		versionLine = versionLine[:end]
	}
	versionMatches := msvcFileVersionPattern.FindAllSubmatch(versionLine, 2)
	if len(versionMatches) != 1 {
		return "", "", errors.New("malformed MSVC compiler file version")
	}
	fileVersion := string(versionMatches[0][1])

	architectureLine := output
	if end := bytes.IndexAny(architectureLine, "\r\n"); end >= 0 {
		architectureLine = architectureLine[:end]
	}
	architectureMatches := msvcArchitecturePattern.FindAllSubmatch(architectureLine, 2)
	if len(architectureMatches) != 1 {
		return "", "", errors.New("ambiguous MSVC compiler architecture")
	}
	fileArchitecture := strings.ToLower(string(architectureMatches[0][1]))
	if englishVersion == "" {
		return fileVersion, fileArchitecture, nil
	}
	if normalizedMSVCVersionEvidence(englishVersion) !=
		normalizedMSVCVersionEvidence(fileVersion) ||
		englishArchitecture != fileArchitecture {
		return "", "", errors.New("conflicting MSVC compiler evidence")
	}
	return englishVersion, englishArchitecture, nil
}

func normalizedMSVCVersionEvidence(version string) string {
	parts := strings.Split(version, ".")
	for index, part := range parts {
		part = strings.TrimLeft(part, "0")
		if part == "" {
			part = "0"
		}
		parts[index] = part
	}
	for len(parts) > 2 && parts[len(parts)-1] == "0" {
		parts = parts[:len(parts)-1]
	}
	return strings.Join(parts, ".")
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

func parseMSBuildVersion(output []byte) (string, error) {
	if len(output) == 0 || len(output) > 1024 ||
		!utf8.Valid(output) || bytesContainNUL(output) {
		return "", errors.New("invalid MSBuild version output")
	}
	matches := msbuildVersionPattern.FindAllSubmatch(output, -1)
	if len(matches) == 0 || len(matches) > 4 {
		return "", errors.New("unrecognized MSBuild version output")
	}
	version := string(matches[0][1])
	for _, match := range matches[1:] {
		if !sameVersionMajor(version, string(match[1])) {
			return "", errors.New("conflicting MSBuild version output")
		}
	}
	return version, nil
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
	issues = append([]Issue(nil), issues...)
	sort.Slice(issues, func(left, right int) bool {
		return lessStrings(
			[]string{
				issues[left].Code,
				issues[left].Message,
				fmt.Sprintf("%t", issues[left].Blocking),
			},
			[]string{
				issues[right].Code,
				issues[right].Message,
				fmt.Sprintf("%t", issues[right].Blocking),
			},
		)
	})
	unique := issues[:0]
	for _, issue := range issues {
		if len(unique) != 0 {
			previous := unique[len(unique)-1]
			if previous.Code == issue.Code &&
				previous.Message == issue.Message &&
				previous.Blocking == issue.Blocking {
				continue
			}
		}
		unique = append(unique, issue)
	}
	issues = unique
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
	return []string{
		"/d",
		"/s",
		"/c",
		"call",
		filepath.Clean(path),
		"-no_logo",
		"-host_arch=" + config.HostArchitecture,
		"-arch=" + config.TargetArchitecture,
		"-vcvars_ver=" + config.ToolsetVersion,
		"&&",
		"set",
	}, nil
}

func validVsDevCmdPath(path string) bool {
	return filepath.IsAbs(path) &&
		validBoundedWindowsText(path, maxVSWherePathBytes) &&
		!strings.ContainsAny(path, "\"%!^&|<>") &&
		(!strings.ContainsAny(path, "()") || strings.Contains(path, " ")) &&
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
