package build

import (
	"errors"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/toolchain"
)

const windowsWinMMVariable = "UNIT_TEST_IDE_WINMM_LIBRARY"
const maxWindowsLinkLibraryBytes = 4 * 1024 * 1024

type windowsLinkLibraryBinding struct {
	SDKRoot            string
	SDKIdentity        string
	SDKVersion         string
	TargetArchitecture string
	File               cmake.FingerprintFile
}

var errWindowsLinkLibrary = errors.New("invalid selected Windows SDK library")
var windowsSDKVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$`)

func captureWindowsWinMMLibrary(instance toolchain.Instance) (*windowsLinkLibraryBinding, error) {
	if runtime.GOOS != "windows" || (instance.Family != toolchain.FamilyMSVC && instance.Family != toolchain.FamilyClangCL) {
		return nil, errWindowsLinkLibrary
	}
	environment, err := normalizedToolchainEnvironment(instance.Environment)
	if err != nil {
		return nil, errWindowsLinkLibrary
	}
	var root, version, libVersion, targetArchitecture string
	for _, entry := range environment {
		key, value, _ := strings.Cut(entry, "=")
		switch key {
		case "WINDOWSSDKDIR":
			root = value
		case "WINDOWSSDKVERSION":
			version = value
		case "WINDOWSSDKLIBVERSION":
			libVersion = value
		case "VSCMD_ARG_TGT_ARCH":
			targetArchitecture = value
		}
	}
	root = filepath.Clean(root)
	version = strings.TrimSuffix(version, `\`)
	if libVersion != "" && strings.TrimSuffix(libVersion, `\`) != version || targetArchitecture != "" && targetArchitecture != instance.TargetArchitecture {
		return nil, errWindowsLinkLibrary
	}
	if !filepath.IsAbs(root) || len(version) > 64 || !windowsSDKVersion.MatchString(version) || !windowsSDKArchitecture(instance.TargetArchitecture) {
		return nil, errWindowsLinkLibrary
	}
	canonical, err := canonicalCMakeLaunchPath(root)
	if err != nil || cmakeLaunchPathKey(canonical) != cmakeLaunchPathKey(root) || (instance.Sysroot != "" && cmakeLaunchPathKey(instance.Sysroot) != cmakeLaunchPathKey(root)) {
		return nil, errWindowsLinkLibrary
	}
	directory, err := pinVerifiedDirectory(root)
	if err != nil {
		return nil, errWindowsLinkLibrary
	}
	defer directory.Close()
	path := filepath.Join(root, "Lib", version, "um", instance.TargetArchitecture, "winmm.lib")
	parent, err := pinVerifiedDirectory(filepath.Dir(path))
	if err != nil {
		return nil, errWindowsLinkLibrary
	}
	defer parent.Close()
	canonical, err = canonicalCMakeLaunchPath(path)
	if err != nil || cmakeLaunchPathKey(canonical) != cmakeLaunchPathKey(path) {
		return nil, errWindowsLinkLibrary
	}
	state, _, err := cmake.SnapshotLaunchInput(path, maxWindowsLinkLibraryBytes)
	if err != nil || directory.Verify() != nil || parent.Verify() != nil {
		return nil, errWindowsLinkLibrary
	}
	return &windowsLinkLibraryBinding{SDKRoot: root, SDKIdentity: directory.identity, SDKVersion: version, TargetArchitecture: instance.TargetArchitecture, File: state}, nil
}

func windowsSDKArchitecture(value string) bool {
	return value == "x64" || value == "x86" || value == "arm64"
}

func verifyWindowsWinMMLibrary(binding *windowsLinkLibraryBinding) error {
	if binding == nil || runtime.GOOS != "windows" || !validLowerSHA256(binding.SDKIdentity) || !windowsSDKVersion.MatchString(binding.SDKVersion) || len(binding.SDKVersion) > 64 || !windowsSDKArchitecture(binding.TargetArchitecture) || !filepath.IsAbs(binding.SDKRoot) || filepath.Clean(binding.SDKRoot) != binding.SDKRoot {
		return errWindowsLinkLibrary
	}
	path := filepath.Join(binding.SDKRoot, "Lib", binding.SDKVersion, "um", binding.TargetArchitecture, "winmm.lib")
	if binding.File.Path != path {
		return errWindowsLinkLibrary
	}
	directory, err := pinVerifiedDirectory(binding.SDKRoot)
	if err != nil {
		return errWindowsLinkLibrary
	}
	defer directory.Close()
	parent, err := pinVerifiedDirectory(filepath.Dir(path))
	if err != nil {
		return errWindowsLinkLibrary
	}
	defer parent.Close()
	if directory.identity != binding.SDKIdentity || cmake.VerifyLaunchInput(binding.File, maxWindowsLinkLibraryBytes) != nil || directory.Verify() != nil || parent.Verify() != nil {
		return errWindowsLinkLibrary
	}
	return nil
}

// Decode only the bounded Windows native argument representation used by File
// API libraries fragments. No shell, expansion, or response files are involved.
func windowsLinkFragmentTokens(value string) ([]string, error) {
	if len(value) > 16*1024 || strings.ContainsAny(value, "\x00\r\n&|<>^;%$") {
		return nil, errWindowsLinkLibrary
	}
	result := []string{}
	for offset := 0; offset < len(value); {
		for offset < len(value) && (value[offset] == ' ' || value[offset] == '\t') {
			offset++
		}
		if offset == len(value) {
			break
		}
		var token strings.Builder
		quoted := false
		for offset < len(value) {
			if !quoted && (value[offset] == ' ' || value[offset] == '\t') {
				break
			}
			if value[offset] == '\\' {
				start := offset
				for offset < len(value) && value[offset] == '\\' {
					offset++
				}
				count := offset - start
				if offset < len(value) && value[offset] == '"' {
					token.WriteString(strings.Repeat(`\`, count/2))
					if count%2 == 1 {
						token.WriteByte('"')
					} else {
						quoted = !quoted
					}
					offset++
				} else {
					token.WriteString(strings.Repeat(`\`, count))
				}
				continue
			}
			if value[offset] == '"' {
				quoted = !quoted
				offset++
				continue
			}
			token.WriteByte(value[offset])
			offset++
		}
		decoded := token.String()
		if quoted || decoded == "" || strings.ContainsRune(decoded, '@') {
			return nil, errWindowsLinkLibrary
		}
		result = append(result, decoded)
	}
	return result, nil
}

func verifyWindowsLinkLibraryEvidence(binding *windowsLinkLibraryBinding, targets []cmake.Target, consumerNames []string) error {
	if verifyWindowsWinMMLibrary(binding) != nil || len(consumerNames) == 0 || len(consumerNames) > 1024 {
		return errWindowsLinkLibrary
	}
	byName := map[string]cmake.Target{}
	for _, target := range targets {
		if _, duplicate := byName[target.Name]; duplicate {
			return errWindowsLinkLibrary
		}
		byName[target.Name] = target
	}
	seen := map[string]bool{}
	for _, name := range consumerNames {
		target, exists := byName[name]
		if !exists || seen[name] || target.Type != "EXECUTABLE" || len(target.LinkFragments) == 0 || len(target.LinkFragments) > 4096 {
			return errWindowsLinkLibrary
		}
		seen[name] = true
		bound := false
		for _, fragment := range target.LinkFragments {
			switch fragment.Role {
			case "flags", "libraries", "libraryPath", "frameworkPath":
			default:
				return errWindowsLinkLibrary
			}
			tokens, err := windowsLinkFragmentTokens(fragment.Fragment)
			if err != nil {
				return errWindowsLinkLibrary
			}
			for _, token := range tokens {
				if strings.ContainsAny(token, `"`) {
					return errWindowsLinkLibrary
				}
				path := filepath.FromSlash(token)
				if fragment.Role == "libraries" && filepath.IsAbs(path) && cmakeLaunchPathKey(path) == cmakeLaunchPathKey(binding.File.Path) && filepath.Clean(path) == path {
					bound = true
					continue
				}
				base := strings.ToLower(filepath.Base(path))
				lower := strings.ToLower(token)
				operand := lower
				if colon := strings.LastIndexByte(operand, ':'); colon >= 0 {
					operand = operand[colon+1:]
				}
				if base == "winmm" || base == "winmm.lib" || operand == "winmm" || operand == "winmm.lib" || lower == "-lwinmm" {
					return errWindowsLinkLibrary
				}
			}
		}
		if !bound {
			return errWindowsLinkLibrary
		}
	}
	return nil
}

type windowsTargetLink struct{ target, scope string }

// Walk only already validated, exactly spelled declarations. STATIC PRIVATE
// dependencies propagate at link time as CMake's link-only interface; INTERFACE
// edges do not link the declaring executable itself.
func (validator *cmakeLaunchValidator) windowsLinkConsumers() ([]string, error) {
	if len(validator.windowsLibraryOwners) == 0 {
		return nil, nil
	}
	var consumers []string
	for name, kind := range validator.definedTargetTypes {
		if kind != "EXECUTABLE" {
			continue
		}
		active := map[string]bool{}
		memo := map[string]bool{}
		visited := map[string]bool{}
		var consumes func(string, bool) (bool, error)
		consumes = func(target string, root bool) (bool, error) {
			if !root && visited[target] {
				return memo[target], nil
			}
			if active[target] {
				return false, errWindowsLinkLibrary
			}
			active[target] = true
			defer delete(active, target)
			for _, scope := range validator.windowsLibraryOwners[target] {
				if !root || scope != "INTERFACE" {
					if !root {
						visited[target] = true
						memo[target] = true
					}
					return true, nil
				}
			}
			for _, edge := range validator.targetLinks[target] {
				if root && edge.scope == "INTERFACE" {
					continue
				}
				found, err := consumes(edge.target, false)
				if err != nil || found {
					if err == nil && !root {
						visited[target] = true
						memo[target] = found
					}
					return found, err
				}
			}
			if !root {
				visited[target] = true
				memo[target] = false
			}
			return false, nil
		}
		found, err := consumes(name, true)
		if err != nil {
			return nil, err
		}
		if found {
			consumers = append(consumers, name)
		}
	}
	sort.Strings(consumers)
	if len(consumers) == 0 {
		return nil, errWindowsLinkLibrary
	}
	return consumers, nil
}
