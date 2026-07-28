//go:build windows

package toolchain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/sys/windows"

	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/workspace"
)

const (
	maxVSWhereOutputBytes   = 256 * 1024
	maxVSWhereInstallations = 32
	maxVSWhereIDBytes       = 128
	maxVSWherePathBytes     = 4096
	maxVSWhereVersionBytes  = 128

	windowsProbeTimeout          = 5 * time.Second
	maxWindowsProbeOutput        = 64 * 1024
	maxWindowsProbeLines         = 256
	maxWindowsManualToolchains   = 64
	maxWindowsToolsets           = 32
	maxWindowsInstances          = 128
	maxWindowsDiscoveryIssues    = 128
	maxWindowsBaseEnvironment    = 16
	maxWindowsDirectoryPathBytes = 4096
)

var (
	fixedVSWhereArguments = [...]string{
		"-all",
		"-products",
		"*",
		"-requires",
		"Microsoft.VisualStudio.Component.VC.Tools.x86.x64",
		"-format",
		"json",
		"-utf8",
	}
	visualStudioVersionPattern = regexp.MustCompile(
		`^[0-9]+(?:\.[0-9]+){1,3}$`,
	)
)

type visualStudioInstallation struct {
	ID       string
	Path     string
	Version  string
	Identity string
}

type windowsDiscoveryOptions struct {
	VSWherePath      string
	CmdPath          string
	NinjaPath        string
	LLVMRoot         string
	BaseEnvironment  []string
	HostArchitecture string
}

type windowsAdapterOptions struct {
	runner probe.Runner
	config windowsDiscoveryOptions
	manual []workspace.ToolchainConfig
}

type windowsFailedAdapter struct{}

func (windowsFailedAdapter) Discover(context.Context) ([]Instance, error) {
	return nil, invalidProbe("TOOLCHAIN_PROBE_FAILED", "Windows adapter initialization failed")
}

func (windowsFailedAdapter) Probe(context.Context, Candidate) (Instance, error) {
	return Instance{}, invalidProbe("TOOLCHAIN_PROBE_FAILED", "Windows adapter initialization failed")
}

// NewWindowsAdapters constructs Windows-only discovery adapters. Environment
// lookup is limited to fixed OS/tool installation roots; PATH is never used as
// a source of compiler or shell candidates.
func NewWindowsAdapters(runner probe.Runner, manual []workspace.ToolchainConfig) []Adapter {
	config, err := defaultWindowsDiscoveryOptions()
	if err != nil {
		return []Adapter{windowsFailedAdapter{}, windowsFailedAdapter{}}
	}
	adapters, err := newWindowsAdapters(runner, manual, config)
	if err != nil {
		return []Adapter{windowsFailedAdapter{}, windowsFailedAdapter{}}
	}
	return adapters
}

func newWindowsAdapters(
	runner probe.Runner,
	manual []workspace.ToolchainConfig,
	config windowsDiscoveryOptions,
) ([]Adapter, error) {
	if nilRunner(runner) {
		return nil, fmt.Errorf("%w: probe runner is nil", ErrInvalidToolchain)
	}
	if len(manual) > maxWindowsManualToolchains ||
		!validArchitectureValue(config.HostArchitecture) ||
		!filepath.IsAbs(config.VSWherePath) ||
		!filepath.IsAbs(config.CmdPath) ||
		config.NinjaPath != "" && !filepath.IsAbs(config.NinjaPath) ||
		config.LLVMRoot != "" && !filepath.IsAbs(config.LLVMRoot) ||
		len(config.BaseEnvironment) > maxWindowsBaseEnvironment {
		return nil, fmt.Errorf("%w: invalid Windows discovery configuration", ErrInvalidToolchain)
	}
	encodedBase := strings.Join(config.BaseEnvironment, "\r\n")
	baseEnvironment, err := parseCapturedEnvironment([]byte(encodedBase))
	if err != nil || len(baseEnvironment) != len(config.BaseEnvironment) {
		return nil, fmt.Errorf("%w: invalid Windows base environment", ErrInvalidToolchain)
	}
	config.VSWherePath = filepath.Clean(config.VSWherePath)
	config.CmdPath = filepath.Clean(config.CmdPath)
	config.NinjaPath = cleanOptionalPath(config.NinjaPath)
	config.LLVMRoot = cleanOptionalPath(config.LLVMRoot)
	config.BaseEnvironment = baseEnvironment

	ownedManual := append([]workspace.ToolchainConfig(nil), manual...)
	sort.SliceStable(ownedManual, func(left, right int) bool {
		return lessStrings(
			[]string{ownedManual[left].Family, ownedManual[left].ID},
			[]string{ownedManual[right].Family, ownedManual[right].ID},
		)
	})
	for _, candidate := range ownedManual {
		switch Family(candidate.Family) {
		case FamilyMSVC:
			if !validInstanceID(candidate.ID) ||
				!validBoundedWindowsText(candidate.InstallationID, maxVSWhereIDBytes) ||
				!versionPattern.MatchString(candidate.ToolsetVersion) ||
				!validArchitectureValue(candidate.HostArchitecture) ||
				!validArchitectureValue(candidate.TargetArchitecture) {
				return nil, fmt.Errorf("%w: invalid manual MSVC configuration", ErrInvalidToolchain)
			}
		case FamilyClangCL:
			if !validInstanceID(candidate.ID) ||
				!filepath.IsAbs(candidate.CCompiler) ||
				!filepath.IsAbs(candidate.CPPCompiler) ||
				!isClangCLFrontendPath(candidate.CCompiler) ||
				!isClangCLFrontendPath(candidate.CPPCompiler) {
				return nil, fmt.Errorf("%w: invalid manual clang-cl configuration", ErrInvalidToolchain)
			}
		case FamilyGCC, FamilyClang:
			continue
		default:
			return nil, fmt.Errorf("%w: unsupported manual toolchain family", ErrInvalidToolchain)
		}
	}
	options := windowsAdapterOptions{
		runner: runner,
		config: config,
		manual: ownedManual,
	}
	return []Adapter{
		newMSVCAdapter(options),
		newClangCLAdapter(options),
	}, nil
}

func defaultWindowsDiscoveryOptions() (windowsDiscoveryOptions, error) {
	programFilesX86 := os.Getenv("ProgramFiles(x86)")
	programFiles := os.Getenv("ProgramFiles")
	systemRoot := os.Getenv("SystemRoot")
	temporary := os.Getenv("TEMP")
	if temporary == "" {
		temporary = os.Getenv("TMP")
	}
	if programFilesX86 == "" || programFiles == "" || systemRoot == "" || temporary == "" {
		return windowsDiscoveryOptions{}, fmt.Errorf("%w: fixed Windows roots are unavailable", ErrInvalidToolchain)
	}
	cmd := filepath.Join(systemRoot, "System32", "cmd.exe")
	return windowsDiscoveryOptions{
		VSWherePath: filepath.Join(
			programFilesX86,
			"Microsoft Visual Studio",
			"Installer",
			"vswhere.exe",
		),
		CmdPath:   cmd,
		NinjaPath: filepath.Join(programFiles, "CMake", "bin", "ninja.exe"),
		LLVMRoot:  filepath.Join(programFiles, "LLVM", "bin"),
		BaseEnvironment: []string{
			"ComSpec=" + cmd,
			"SystemRoot=" + systemRoot,
			"TEMP=" + temporary,
			"TMP=" + temporary,
		},
		HostArchitecture: windowsNativeArchitecture(),
	}, nil
}

func windowsNativeArchitecture() string {
	switch runtime.GOARCH {
	case "386":
		return "x86"
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	default:
		return ""
	}
}

func discoverVisualStudioInstallations(
	ctx context.Context,
	runner probe.Runner,
	config windowsDiscoveryOptions,
) ([]visualStudioInstallation, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrInvalidToolchain)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	vswhere, err := openWindowsToolSnapshot(ctx, config.VSWherePath)
	if err != nil {
		if isContextError(err) {
			return nil, err
		}
		return nil, invalidProbe("TOOLCHAIN_PROBE_FAILED", "vswhere executable is invalid")
	}
	defer vswhere.Close()
	output, err := runWindowsProbe(
		ctx,
		runner,
		vswhere,
		vswhereArguments(),
		config.BaseEnvironment,
		maxVSWhereOutputBytes,
		false,
		func() error { return nil },
	)
	if err != nil {
		return nil, err
	}
	parsed, err := parseVSWhereOutput(output)
	if err != nil {
		return nil, invalidProbe("TOOLCHAIN_PROBE_FAILED", "vswhere output is invalid")
	}
	result := make([]visualStudioInstallation, 0, len(parsed))
	for _, candidate := range parsed {
		canonical, identity, err := canonicalWindowsDirectoryIdentity(candidate.Path)
		if err != nil {
			continue
		}
		candidate.Path = canonical
		candidate.Identity = identity
		result = append(result, candidate)
	}
	if len(result) == 0 && len(parsed) != 0 {
		return nil, invalidProbe("TOOLCHAIN_PROBE_FAILED", "Visual Studio installations are invalid")
	}
	return result, nil
}

func runWindowsProbe(
	ctx context.Context,
	runner probe.Runner,
	executable *executableSnapshot,
	args []string,
	environment []string,
	maximum int,
	allowStderr bool,
	verify func() error,
) ([]byte, error) {
	if ctx == nil || runner == nil || executable == nil || maximum <= 0 {
		return nil, invalidProbe("TOOLCHAIN_PROBE_FAILED", "Windows probe is not initialized")
	}
	verifyAll := func() error {
		if err := executable.Verify(ctx); err != nil {
			return err
		}
		if verify != nil {
			return verify()
		}
		return nil
	}
	if err := verifyAll(); err != nil {
		if isContextError(err) {
			return nil, err
		}
		return nil, invalidProbe("TOOLCHAIN_PROBE_FAILED", "Windows probe identity changed")
	}
	result, runErr := runner.Run(ctx, probe.Spec{
		Executable: executable.path,
		Args:       append([]string(nil), args...),
		Env:        append([]string(nil), environment...),
		Timeout:    windowsProbeTimeout,
		MaxOutput:  maximum,
	})
	if err := verifyAll(); err != nil {
		if isContextError(err) {
			return nil, err
		}
		return nil, invalidProbe("TOOLCHAIN_PROBE_FAILED", "Windows probe identity changed")
	}
	if runErr != nil {
		if isContextError(runErr) {
			return nil, runErr
		}
		return nil, invalidProbe("TOOLCHAIN_PROBE_FAILED", "Windows probe runner failed")
	}
	if result.ExitCode != 0 ||
		len(result.Stdout)+len(result.Stderr) > maximum ||
		!utf8.Valid(result.Stdout) || !utf8.Valid(result.Stderr) ||
		bytesContainNUL(result.Stdout) || bytesContainNUL(result.Stderr) {
		return nil, invalidProbe("TOOLCHAIN_PROBE_FAILED", "Windows probe output is invalid")
	}
	if !allowStderr && len(strings.TrimSpace(string(result.Stderr))) != 0 {
		return nil, invalidProbe("TOOLCHAIN_PROBE_FAILED", "Windows probe wrote diagnostics")
	}
	output := append([]byte(nil), result.Stdout...)
	if allowStderr && len(result.Stderr) != 0 {
		if len(output) != 0 && output[len(output)-1] != '\n' {
			output = append(output, '\n')
		}
		output = append(output, result.Stderr...)
	}
	if lineCount(output) > maxWindowsProbeLines {
		return nil, invalidProbe("TOOLCHAIN_PROBE_FAILED", "Windows probe output has too many lines")
	}
	return output, nil
}

func openWindowsToolSnapshot(ctx context.Context, path string) (*executableSnapshot, error) {
	canonical, err := canonicalWindowsFile(path)
	if err != nil {
		return nil, err
	}
	return openExecutableSnapshot(ctx, canonical)
}

func canonicalWindowsFile(path string) (string, error) {
	if path == "" || len(path) > maxVSWherePathBytes ||
		strings.IndexByte(path, 0) >= 0 || !filepath.IsAbs(path) {
		return "", errors.New("file path is invalid")
	}
	canonical, information, err := canonicalWindowsPathInformation(path, false)
	if err != nil {
		return "", err
	}
	if information.FileAttributes&
		(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		return "", errors.New("path is not a regular file")
	}
	return canonical, nil
}

func canonicalWindowsDirectoryIdentity(path string) (string, string, error) {
	if path == "" || len(path) > maxWindowsDirectoryPathBytes ||
		strings.IndexByte(path, 0) >= 0 || !filepath.IsAbs(path) {
		return "", "", errors.New("directory path is invalid")
	}
	canonical, identity, err := canonicalWindowsPathInformation(path, true)
	if err != nil {
		return "", "", err
	}
	if identity.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 ||
		identity.FileIndexHigh == 0 && identity.FileIndexLow == 0 {
		return "", "", errors.New("directory identity is unavailable")
	}
	return canonical, fmt.Sprintf(
		"windows:%08x:%08x%08x",
		identity.VolumeSerialNumber,
		identity.FileIndexHigh,
		identity.FileIndexLow,
	), nil
}

func canonicalWindowsPathInformation(
	path string,
	directory bool,
) (string, windows.ByHandleFileInformation, error) {
	pointer, err := windows.UTF16PtrFromString(filepath.Clean(path))
	if err != nil {
		return "", windows.ByHandleFileInformation{}, err
	}
	flags := uint32(0)
	if directory {
		flags = windows.FILE_FLAG_BACKUP_SEMANTICS
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		flags,
		0,
	)
	if err != nil {
		return "", windows.ByHandleFileInformation{}, err
	}
	defer windows.CloseHandle(handle)
	finalPath, err := windowsFinalPathName(handle)
	if err != nil {
		return "", windows.ByHandleFileInformation{}, err
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		return "", windows.ByHandleFileInformation{}, err
	}
	return filepath.Clean(normalizeWindowsToolFinalPath(finalPath)), information, nil
}

func windowsFinalPathName(handle windows.Handle) (string, error) {
	buffer := make([]uint16, 256)
	for {
		length, err := windows.GetFinalPathNameByHandle(
			handle,
			&buffer[0],
			uint32(len(buffer)),
			0,
		)
		if err != nil {
			return "", err
		}
		if length < uint32(len(buffer)) {
			return string(utf16.Decode(buffer[:length])), nil
		}
		buffer = make([]uint16, int(length)+1)
	}
}

func normalizeWindowsToolFinalPath(path string) string {
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\\` + path[len(`\\?\UNC\`):]
	}
	if strings.HasPrefix(path, `\\?\`) {
		return path[len(`\\?\`):]
	}
	return path
}

func verifyWindowsDirectory(path, identity string) error {
	current, currentIdentity, err := canonicalWindowsDirectoryIdentity(path)
	if err != nil || identityPath(current) != identityPath(path) || currentIdentity != identity {
		return errors.New("directory identity changed")
	}
	return nil
}

func pathWithinWindowsRoot(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && (relative == "." ||
		relative != ".." && !filepath.IsAbs(relative) &&
			!strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func cleanOptionalPath(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func isClangCLFrontendPath(path string) bool {
	return strings.EqualFold(filepath.Base(filepath.Clean(path)), "clang-cl.exe")
}

type vswhereInstallationWire struct {
	InstanceID          *string         `json:"instanceId"`
	InstallDate         string          `json:"installDate,omitempty"`
	InstallationName    string          `json:"installationName,omitempty"`
	InstallationPath    *string         `json:"installationPath"`
	InstallationVersion *string         `json:"installationVersion"`
	ProductID           string          `json:"productId,omitempty"`
	ProductPath         string          `json:"productPath,omitempty"`
	State               json.Number     `json:"state,omitempty"`
	IsComplete          *bool           `json:"isComplete"`
	IsLaunchable        *bool           `json:"isLaunchable"`
	IsPrerelease        bool            `json:"isPrerelease,omitempty"`
	IsRebootRequired    bool            `json:"isRebootRequired,omitempty"`
	DisplayName         string          `json:"displayName,omitempty"`
	Description         string          `json:"description,omitempty"`
	ChannelID           string          `json:"channelId,omitempty"`
	ChannelURI          string          `json:"channelUri,omitempty"`
	EnginePath          string          `json:"enginePath,omitempty"`
	InstalledChannelID  string          `json:"installedChannelId,omitempty"`
	InstalledChannelURI string          `json:"installedChannelUri,omitempty"`
	ReleaseNotes        string          `json:"releaseNotes,omitempty"`
	ResolvedPath        string          `json:"resolvedInstallationPath,omitempty"`
	ThirdPartyNotices   string          `json:"thirdPartyNotices,omitempty"`
	UpdateDate          string          `json:"updateDate,omitempty"`
	Catalog             json.RawMessage `json:"catalog,omitempty"`
	Properties          json.RawMessage `json:"properties,omitempty"`
}

func vswhereArguments() []string {
	return append([]string(nil), fixedVSWhereArguments[:]...)
}

func parseVSWhereOutput(output []byte) ([]visualStudioInstallation, error) {
	if len(output) == 0 || len(output) > maxVSWhereOutputBytes ||
		!utf8.Valid(output) || bytesContainNUL(output) {
		return nil, fmt.Errorf("%w: invalid vswhere output", ErrInvalidToolchain)
	}
	if err := validateUniqueJSONKeys(output); err != nil {
		return nil, fmt.Errorf("%w: invalid vswhere JSON", ErrInvalidToolchain)
	}
	var wire []vswhereInstallationWire
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&wire); err != nil {
		return nil, fmt.Errorf("%w: decode vswhere JSON", ErrInvalidToolchain)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("%w: trailing vswhere JSON", ErrInvalidToolchain)
	}
	if len(wire) > maxVSWhereInstallations {
		return nil, fmt.Errorf("%w: too many Visual Studio installations", ErrInvalidToolchain)
	}

	installations := make([]visualStudioInstallation, 0, len(wire))
	seenIDs := make(map[string]struct{}, len(wire))
	seenPaths := make(map[string]struct{}, len(wire))
	for _, candidate := range wire {
		if candidate.InstanceID == nil || candidate.InstallationPath == nil ||
			candidate.InstallationVersion == nil || candidate.IsComplete == nil ||
			candidate.IsLaunchable == nil ||
			!validBoundedWindowsText(*candidate.InstanceID, maxVSWhereIDBytes) ||
			!validBoundedWindowsText(*candidate.InstallationPath, maxVSWherePathBytes) ||
			!validBoundedWindowsText(*candidate.InstallationVersion, maxVSWhereVersionBytes) ||
			!filepath.IsAbs(*candidate.InstallationPath) ||
			!visualStudioVersionPattern.MatchString(*candidate.InstallationVersion) ||
			!validVSWhereOptionalFields(candidate) {
			return nil, fmt.Errorf("%w: malformed Visual Studio installation", ErrInvalidToolchain)
		}
		if !*candidate.IsComplete || !*candidate.IsLaunchable {
			continue
		}
		idKey := strings.ToLower(*candidate.InstanceID)
		pathKey := identityPath(*candidate.InstallationPath)
		if _, duplicate := seenIDs[idKey]; duplicate {
			return nil, fmt.Errorf("%w: duplicate Visual Studio installation id", ErrInvalidToolchain)
		}
		if _, duplicate := seenPaths[pathKey]; duplicate {
			return nil, fmt.Errorf("%w: duplicate Visual Studio installation path", ErrInvalidToolchain)
		}
		seenIDs[idKey] = struct{}{}
		seenPaths[pathKey] = struct{}{}
		installations = append(installations, visualStudioInstallation{
			ID:      *candidate.InstanceID,
			Path:    filepath.Clean(*candidate.InstallationPath),
			Version: *candidate.InstallationVersion,
		})
	}
	sort.Slice(installations, func(left, right int) bool {
		return lessStrings(
			[]string{strings.ToLower(installations[left].ID), identityPath(installations[left].Path)},
			[]string{strings.ToLower(installations[right].ID), identityPath(installations[right].Path)},
		)
	})
	return installations, nil
}

func validVSWhereOptionalFields(candidate vswhereInstallationWire) bool {
	for _, value := range []string{
		candidate.InstallDate,
		candidate.InstallationName,
		candidate.ProductID,
		candidate.ProductPath,
		candidate.DisplayName,
		candidate.Description,
		candidate.ChannelID,
		candidate.ChannelURI,
		candidate.EnginePath,
		candidate.InstalledChannelID,
		candidate.InstalledChannelURI,
		candidate.ReleaseNotes,
		candidate.ResolvedPath,
		candidate.ThirdPartyNotices,
		candidate.UpdateDate,
	} {
		if value != "" && !validBoundedWindowsText(value, maxVSWherePathBytes) {
			return false
		}
	}
	return true
}

func validateUniqueJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var visit func() error
	visit = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, composite := token.(json.Delim)
		if !composite {
			return nil
		}
		switch delimiter {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("object key is not a string")
				}
				identity := strings.ToLower(key)
				if _, duplicate := seen[identity]; duplicate {
					return errors.New("duplicate object key")
				}
				seen[identity] = struct{}{}
				if err := visit(); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return errors.New("unterminated object")
			}
		case '[':
			for decoder.More() {
				if err := visit(); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return errors.New("unterminated array")
			}
		default:
			return errors.New("unexpected JSON delimiter")
		}
		return nil
	}
	if err := visit(); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func requireJSONEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func validBoundedWindowsText(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.IndexByte(value, 0) >= 0 {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}
