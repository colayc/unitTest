package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	workspaceConfigPath = ".unit-test-ide/workspace.json"
	maxConfigBytes      = 256 * 1024
	maxProjects         = 64
	maxToolchains       = 64
	maxConfigurations   = 64
	maxIdentifierBytes  = 64
)

var (
	ErrInvalidConfig  = errors.New("invalid workspace configuration")
	ErrConfigTooLarge = errors.New("workspace configuration exceeds 256 KiB")
)

type Config struct {
	Version    int               `json:"version"`
	CMake      CMakeConfig       `json:"cmake,omitempty"`
	Projects   []ProjectConfig   `json:"projects,omitempty"`
	Toolchains []ToolchainConfig `json:"toolchains,omitempty"`
}

type CMakeConfig struct {
	Executable string `json:"executable,omitempty"`
}

type ProjectConfig struct {
	ID        string         `json:"id"`
	SourceDir string         `json:"sourceDir"`
	Fallback  FallbackConfig `json:"fallback,omitempty"`
}

type FallbackConfig struct {
	Configurations     []string `json:"configurations,omitempty"`
	PreferredGenerator string   `json:"preferredGenerator,omitempty"`
}

type ToolchainConfig struct {
	ID                 string `json:"id"`
	Family             string `json:"family"`
	CCompiler          string `json:"cCompiler,omitempty"`
	CPPCompiler        string `json:"cppCompiler,omitempty"`
	InstallationID     string `json:"installationId,omitempty"`
	ToolsetVersion     string `json:"toolsetVersion,omitempty"`
	HostArchitecture   string `json:"hostArchitecture,omitempty"`
	TargetArchitecture string `json:"targetArchitecture,omitempty"`
}

type Issue struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Blocking bool   `json:"blocking"`
}

type LoadResult struct {
	Config Config
	Issues []Issue
}

type configWire struct {
	Version    int               `json:"version"`
	CMake      *cmakeWire        `json:"cmake"`
	Projects   []projectWire     `json:"projects"`
	Toolchains []json.RawMessage `json:"toolchains"`
}

type cmakeWire struct {
	Executable *string `json:"executable"`
}

type projectWire struct {
	ID        *string       `json:"id"`
	SourceDir *string       `json:"sourceDir"`
	Fallback  *fallbackWire `json:"fallback"`
}

type fallbackWire struct {
	Configurations     []string `json:"configurations"`
	PreferredGenerator *string  `json:"preferredGenerator"`
}

type familyWire struct {
	Family string `json:"family"`
}

type compilerToolchainWire struct {
	ID          *string `json:"id"`
	Family      string  `json:"family"`
	CCompiler   *string `json:"cCompiler"`
	CPPCompiler *string `json:"cppCompiler"`
}

type msvcToolchainWire struct {
	ID                 *string `json:"id"`
	Family             string  `json:"family"`
	InstallationID     *string `json:"installationId"`
	ToolsetVersion     *string `json:"toolsetVersion"`
	HostArchitecture   *string `json:"hostArchitecture"`
	TargetArchitecture *string `json:"targetArchitecture"`
}

func LoadConfig(root Root) (LoadResult, error) {
	configPath, err := root.ResolveRelative(workspaceConfigPath)
	if err != nil {
		return LoadResult{}, fmt.Errorf("resolve workspace configuration: %w", err)
	}
	file, err := os.Open(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return loadDefaultConfig(root)
	}
	if err != nil {
		return LoadResult{}, fmt.Errorf("open workspace configuration: %w", err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return LoadResult{}, fmt.Errorf("read workspace configuration: %w", err)
	}
	if len(data) > maxConfigBytes {
		return LoadResult{}, fmt.Errorf("%w: got at least %d bytes", ErrConfigTooLarge, len(data))
	}

	config, err := decodeConfig(root, data)
	if err != nil {
		return LoadResult{}, err
	}
	return LoadResult{Config: config}, nil
}

func loadDefaultConfig(root Root) (LoadResult, error) {
	result := LoadResult{Config: Config{Version: 1}}
	cmakeListsPath, err := root.ResolveRelative("CMakeLists.txt")
	if err != nil {
		return LoadResult{}, fmt.Errorf("resolve root CMakeLists.txt: %w", err)
	}
	info, err := os.Stat(cmakeListsPath)
	switch {
	case err == nil && !info.IsDir():
		result.Config.Projects = []ProjectConfig{{ID: "root", SourceDir: "."}}
	case errors.Is(err, os.ErrNotExist):
		result.Issues = []Issue{{
			Code:     "workspace.no-root-project",
			Message:  "workspace root has no CMakeLists.txt and no workspace configuration",
			Blocking: false,
		}}
	case err != nil:
		return LoadResult{}, fmt.Errorf("inspect root CMakeLists.txt: %w", err)
	default:
		result.Issues = []Issue{{
			Code:     "workspace.no-root-project",
			Message:  "workspace root CMakeLists.txt is not a file",
			Blocking: false,
		}}
	}
	return result, nil
}

func decodeConfig(root Root, data []byte) (Config, error) {
	var wire configWire
	if err := decodeStrict(data, &wire); err != nil {
		return Config{}, invalidConfig("decode JSON: %v", err)
	}
	if wire.Version != 1 {
		return Config{}, invalidConfig("version = %d, want 1", wire.Version)
	}
	if len(wire.Projects) > maxProjects {
		return Config{}, invalidConfig("projects contains %d items, maximum is %d", len(wire.Projects), maxProjects)
	}
	if len(wire.Toolchains) > maxToolchains {
		return Config{}, invalidConfig("toolchains contains %d items, maximum is %d", len(wire.Toolchains), maxToolchains)
	}

	config := Config{Version: wire.Version}
	if wire.CMake != nil && wire.CMake.Executable != nil {
		if !isPortableAbsolute(*wire.CMake.Executable) {
			return Config{}, invalidConfig("cmake.executable must be an absolute path")
		}
		config.CMake.Executable = *wire.CMake.Executable
	}

	projectIDs := make(map[string]struct{}, len(wire.Projects))
	for index, project := range wire.Projects {
		decoded, err := decodeProject(root, index, project)
		if err != nil {
			return Config{}, err
		}
		if _, duplicate := projectIDs[decoded.ID]; duplicate {
			return Config{}, invalidConfig("projects contains duplicate id %q", decoded.ID)
		}
		projectIDs[decoded.ID] = struct{}{}
		config.Projects = append(config.Projects, decoded)
	}

	toolchainIDs := make(map[string]struct{}, len(wire.Toolchains))
	for index, raw := range wire.Toolchains {
		toolchain, err := decodeToolchain(index, raw)
		if err != nil {
			return Config{}, err
		}
		if _, duplicate := toolchainIDs[toolchain.ID]; duplicate {
			return Config{}, invalidConfig("toolchains contains duplicate id %q", toolchain.ID)
		}
		toolchainIDs[toolchain.ID] = struct{}{}
		config.Toolchains = append(config.Toolchains, toolchain)
	}
	return config, nil
}

func decodeProject(root Root, index int, wire projectWire) (ProjectConfig, error) {
	if wire.ID == nil || !validIdentifier(*wire.ID) {
		return ProjectConfig{}, invalidConfig("projects[%d].id is not a valid identifier", index)
	}
	if wire.SourceDir == nil || !validRelativeWorkspacePath(*wire.SourceDir) {
		return ProjectConfig{}, invalidConfig("projects[%d].sourceDir must be a safe relative path", index)
	}
	if _, err := root.ResolveRelative(*wire.SourceDir); err != nil {
		return ProjectConfig{}, invalidConfig("projects[%d].sourceDir: %v", index, err)
	}

	project := ProjectConfig{ID: *wire.ID, SourceDir: *wire.SourceDir}
	if wire.Fallback == nil {
		return project, nil
	}
	if len(wire.Fallback.Configurations) > maxConfigurations {
		return ProjectConfig{}, invalidConfig(
			"projects[%d].fallback.configurations contains %d items, maximum is %d",
			index,
			len(wire.Fallback.Configurations),
			maxConfigurations,
		)
	}
	seenConfigurations := make(map[string]struct{}, len(wire.Fallback.Configurations))
	for configurationIndex, configuration := range wire.Fallback.Configurations {
		if configuration == "" {
			return ProjectConfig{}, invalidConfig(
				"projects[%d].fallback.configurations[%d] must not be empty",
				index,
				configurationIndex,
			)
		}
		if _, duplicate := seenConfigurations[configuration]; duplicate {
			return ProjectConfig{}, invalidConfig(
				"projects[%d].fallback.configurations contains duplicate %q",
				index,
				configuration,
			)
		}
		seenConfigurations[configuration] = struct{}{}
	}
	project.Fallback.Configurations = wire.Fallback.Configurations
	if wire.Fallback.PreferredGenerator != nil {
		if *wire.Fallback.PreferredGenerator == "" {
			return ProjectConfig{}, invalidConfig("projects[%d].fallback.preferredGenerator must not be empty", index)
		}
		project.Fallback.PreferredGenerator = *wire.Fallback.PreferredGenerator
	}
	return project, nil
}

func decodeToolchain(index int, raw json.RawMessage) (ToolchainConfig, error) {
	var family familyWire
	if err := json.Unmarshal(raw, &family); err != nil {
		return ToolchainConfig{}, invalidConfig("toolchains[%d]: decode family: %v", index, err)
	}
	switch family.Family {
	case "gcc", "clang", "clang-cl":
		var wire compilerToolchainWire
		if err := decodeStrict(raw, &wire); err != nil {
			return ToolchainConfig{}, invalidConfig("toolchains[%d]: %v", index, err)
		}
		if wire.ID == nil || !validIdentifier(*wire.ID) {
			return ToolchainConfig{}, invalidConfig("toolchains[%d].id is not a valid identifier", index)
		}
		if wire.CCompiler == nil || !isPortableAbsolute(*wire.CCompiler) {
			return ToolchainConfig{}, invalidConfig("toolchains[%d].cCompiler must be an absolute path", index)
		}
		if wire.CPPCompiler == nil || !isPortableAbsolute(*wire.CPPCompiler) {
			return ToolchainConfig{}, invalidConfig("toolchains[%d].cppCompiler must be an absolute path", index)
		}
		return ToolchainConfig{
			ID:          *wire.ID,
			Family:      wire.Family,
			CCompiler:   *wire.CCompiler,
			CPPCompiler: *wire.CPPCompiler,
		}, nil
	case "msvc":
		var wire msvcToolchainWire
		if err := decodeStrict(raw, &wire); err != nil {
			return ToolchainConfig{}, invalidConfig("toolchains[%d]: %v", index, err)
		}
		if wire.ID == nil || !validIdentifier(*wire.ID) {
			return ToolchainConfig{}, invalidConfig("toolchains[%d].id is not a valid identifier", index)
		}
		if wire.InstallationID == nil || *wire.InstallationID == "" {
			return ToolchainConfig{}, invalidConfig("toolchains[%d].installationId must not be empty", index)
		}
		if wire.ToolsetVersion == nil || *wire.ToolsetVersion == "" {
			return ToolchainConfig{}, invalidConfig("toolchains[%d].toolsetVersion must not be empty", index)
		}
		if wire.HostArchitecture == nil || !validArchitecture(*wire.HostArchitecture) {
			return ToolchainConfig{}, invalidConfig("toolchains[%d].hostArchitecture is not supported", index)
		}
		if wire.TargetArchitecture == nil || !validArchitecture(*wire.TargetArchitecture) {
			return ToolchainConfig{}, invalidConfig("toolchains[%d].targetArchitecture is not supported", index)
		}
		return ToolchainConfig{
			ID:                 *wire.ID,
			Family:             wire.Family,
			InstallationID:     *wire.InstallationID,
			ToolsetVersion:     *wire.ToolsetVersion,
			HostArchitecture:   *wire.HostArchitecture,
			TargetArchitecture: *wire.TargetArchitecture,
		}, nil
	default:
		return ToolchainConfig{}, invalidConfig("toolchains[%d].family %q is not supported", index, family.Family)
	}
}

func decodeStrict(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func validIdentifier(value string) bool {
	if len(value) == 0 || len(value) > maxIdentifierBytes || !isASCIIAlphaNumeric(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if !isASCIIAlphaNumeric(character) && character != '.' && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func isASCIIAlphaNumeric(character byte) bool {
	return character >= 'a' && character <= 'z' ||
		character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9'
}

func validRelativeWorkspacePath(path string) bool {
	if path == "" || strings.HasPrefix(path, `\`) || isPortableAbsolute(path) || hasPortableVolume(path) {
		return false
	}
	for _, segment := range strings.FieldsFunc(path, func(character rune) bool {
		return character == '/' || character == '\\'
	}) {
		if segment == ".." {
			return false
		}
	}
	return true
}

func isPortableAbsolute(path string) bool {
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\\`) {
		return true
	}
	return len(path) >= 3 &&
		isASCIIAlpha(path[0]) &&
		path[1] == ':' &&
		(path[2] == '/' || path[2] == '\\')
}

func hasPortableVolume(path string) bool {
	return len(path) >= 2 && isASCIIAlpha(path[0]) && path[1] == ':'
}

func isASCIIAlpha(character byte) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
}

func validArchitecture(architecture string) bool {
	return architecture == "x86" || architecture == "x64" || architecture == "arm64"
}

func invalidConfig(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidConfig, fmt.Sprintf(format, arguments...))
}
