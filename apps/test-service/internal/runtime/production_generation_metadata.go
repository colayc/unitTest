package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/testgenrender"
	"unit-test-ide.local/test-service/internal/workspace"
)

var productionDefinitionPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z_0-9]{0,63}(=(0|1|[0-9]{1,9}))?$`)
var productionCMakeIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

type productionCompileBinding struct {
	target                cmake.Target
	unit                  cmake.CompileUnit
	language              testgenrender.Language
	headerRelative        string
	arguments             []string
	compileSnapshotDigest string
	cmakeTargetDigest     string
}

func resolveProductionCompileBinding(root workspace.Root, sourceRelative string, targets []cmake.Target, toolchainID string) (productionCompileBinding, error) {
	if root.NativePath == "" || root.ID == "" || !validProductionRelative(sourceRelative) ||
		!validProductionDigest(toolchainID) || len(targets) == 0 || len(targets) > 1024 {
		return productionCompileBinding{}, errProductionGenerationUnavailable
	}
	source, err := root.ResolveRelative(filepath.FromSlash(sourceRelative))
	if err != nil {
		return productionCompileBinding{}, errProductionGenerationUnavailable
	}
	var selectedTarget *cmake.Target
	var selectedUnit *cmake.CompileUnit
	for targetIndex := range targets {
		for unitIndex := range targets[targetIndex].CompileUnits {
			unit := &targets[targetIndex].CompileUnits[unitIndex]
			if !sameProductionFile(unit.Source, source) {
				continue
			}
			if selectedUnit != nil {
				return productionCompileBinding{}, errProductionGenerationUnavailable
			}
			selectedTarget, selectedUnit = &targets[targetIndex], unit
		}
	}
	if selectedTarget == nil || selectedUnit == nil || selectedUnit.Generated ||
		!validProductionDigest(selectedTarget.ID) || !validProductionDigest(selectedTarget.ProfileID) ||
		!productionCMakeIdentifierPattern.MatchString(selectedTarget.Name) {
		return productionCompileBinding{}, errProductionGenerationUnavailable
	}
	language, standard, ok := productionLanguageMode(selectedUnit.Language, selectedUnit.Standard)
	if !ok {
		return productionCompileBinding{}, errProductionGenerationUnavailable
	}
	header, err := productionHeader(root, *selectedTarget, source, language)
	if err != nil {
		return productionCompileBinding{}, err
	}
	arguments := []string{standard}
	defines := append([]string(nil), selectedUnit.Defines...)
	sort.Strings(defines)
	previous := ""
	for _, define := range defines {
		if !productionDefinitionPattern.MatchString(define) || define == previous {
			return productionCompileBinding{}, errProductionGenerationUnavailable
		}
		previous = define
		arguments = append(arguments, "-D"+define)
	}
	includes := make([]string, 0, len(selectedUnit.Includes))
	for _, include := range selectedUnit.Includes {
		relative, ok := productionWorkspaceRelative(root, include, true)
		if !ok {
			return productionCompileBinding{}, errProductionGenerationUnavailable
		}
		if relative != "" {
			includes = append(includes, relative)
		}
	}
	sort.Strings(includes)
	previous = ""
	for _, include := range includes {
		if include == previous {
			return productionCompileBinding{}, errProductionGenerationUnavailable
		}
		previous = include
		arguments = append(arguments, "-I"+include)
	}
	if len(arguments) > 64 {
		return productionCompileBinding{}, errProductionGenerationUnavailable
	}
	identity := struct {
		Version       int      `json:"version"`
		ToolchainID   string   `json:"toolchainId"`
		TargetID      string   `json:"targetId"`
		TargetName    string   `json:"targetName"`
		Configuration string   `json:"configuration"`
		Source        string   `json:"source"`
		Header        string   `json:"header"`
		Arguments     []string `json:"arguments"`
	}{
		Version: 1, ToolchainID: toolchainID, TargetID: selectedTarget.ID,
		TargetName: selectedTarget.Name, Configuration: selectedTarget.Configuration,
		Source: sourceRelative, Header: header, Arguments: append([]string(nil), arguments...),
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return productionCompileBinding{}, errProductionGenerationUnavailable
	}
	sum := sha256.Sum256(append([]byte("production-compile-snapshot-v1\x00"), encoded...))
	unitCopy := *selectedUnit
	unitCopy.Includes = append([]string(nil), selectedUnit.Includes...)
	unitCopy.Defines = append([]string(nil), selectedUnit.Defines...)
	return productionCompileBinding{
		target: cmake.CloneTargets([]cmake.Target{*selectedTarget})[0], unit: unitCopy,
		language: language, headerRelative: header, arguments: arguments,
		compileSnapshotDigest: hex.EncodeToString(sum[:]), cmakeTargetDigest: selectedTarget.ID,
	}, nil
}

func productionLanguageMode(language, standard string) (testgenrender.Language, string, bool) {
	switch language + ":" + standard {
	case "C:11":
		return testgenrender.LanguageC, "-std=c11", true
	case "C:17":
		return testgenrender.LanguageC, "-std=c17", true
	case "CXX:17":
		return testgenrender.LanguageCPP, "-std=c++17", true
	case "CXX:20":
		return testgenrender.LanguageCPP, "-std=c++20", true
	default:
		return "", "", false
	}
}

func productionHeader(root workspace.Root, target cmake.Target, source string, language testgenrender.Language) (string, error) {
	base := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	result := ""
	for _, candidate := range target.Sources {
		if candidate.Generated || candidate.Compiled || !productionHeaderExtension(filepath.Ext(candidate.Path), language) ||
			!productionNameEqual(strings.TrimSuffix(filepath.Base(candidate.Path), filepath.Ext(candidate.Path)), base) {
			continue
		}
		relative, ok := productionWorkspaceRelative(root, candidate.Path, false)
		if !ok || relative == "" || result != "" {
			return "", errProductionGenerationUnavailable
		}
		result = relative
	}
	if result == "" {
		return "", errProductionGenerationUnavailable
	}
	return result, nil
}

func productionHeaderExtension(extension string, language testgenrender.Language) bool {
	extension = strings.ToLower(extension)
	if language == testgenrender.LanguageC {
		return extension == ".h"
	}
	return extension == ".h" || extension == ".hh" || extension == ".hpp" || extension == ".hxx"
}

func productionWorkspaceRelative(root workspace.Root, path string, directory bool) (string, bool) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || !root.Contains(path) {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil || directory && !info.IsDir() || !directory && !info.Mode().IsRegular() {
		return "", false
	}
	relative, err := filepath.Rel(root.NativePath, path)
	if err != nil {
		return "", false
	}
	if relative == "." {
		return "", directory
	}
	portable := filepath.ToSlash(relative)
	if !validProductionRelative(portable) {
		return "", false
	}
	resolved, err := root.ResolveRelative(relative)
	if err != nil || !sameProductionPath(resolved, path) {
		return "", false
	}
	return portable, true
}

func validProductionRelative(value string) bool {
	if value == "" || len(value) > 240 || strings.ContainsAny(value, "\\:\x00\r\n\"$;") || strings.HasPrefix(value, "/") || strings.Contains(value, "//") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".") {
			return false
		}
		for _, character := range part {
			if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' || character == '-' || character == '.' {
				continue
			}
			return false
		}
	}
	return true
}

func sameProductionFile(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && leftInfo.Mode().IsRegular() && rightInfo.Mode().IsRegular() && os.SameFile(leftInfo, rightInfo)
}

func sameProductionPath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func productionNameEqual(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
