package cmake

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"runtime"
	"sort"
	"strings"

	"unit-test-ide.local/test-service/internal/workspace"
)

type generationPayload struct {
	Config           generationConfig       `json:"config"`
	Installation     generationInstallation `json:"installation"`
	Profiles         []BuildProfile         `json:"profiles"`
	ToolchainIDs     []string               `json:"toolchainIds"`
	InputGenerations []string               `json:"inputGenerations,omitempty"`
}

type generationConfig struct {
	Version    int                   `json:"version"`
	CMake      generationCMakeConfig `json:"cmake"`
	Projects   []generationProject   `json:"projects"`
	Toolchains []generationToolchain `json:"toolchains"`
}

type generationCMakeConfig struct {
	Executable string `json:"executable"`
}

type generationProject struct {
	ID        string             `json:"id"`
	SourceDir string             `json:"sourceDir"`
	Fallback  generationFallback `json:"fallback"`
}

type generationFallback struct {
	Configurations     []string `json:"configurations"`
	PreferredGenerator string   `json:"preferredGenerator"`
}

type generationToolchain struct {
	ID                 string `json:"id"`
	Family             string `json:"family"`
	CCompiler          string `json:"cCompiler"`
	CPPCompiler        string `json:"cppCompiler"`
	InstallationID     string `json:"installationId"`
	ToolsetVersion     string `json:"toolsetVersion"`
	HostArchitecture   string `json:"hostArchitecture"`
	TargetArchitecture string `json:"targetArchitecture"`
}

type generationInstallation struct {
	Executable  string `json:"executable"`
	Version     string `json:"version"`
	Source      string `json:"source"`
	Identity    string `json:"identity"`
	LicensePath string `json:"licensePath"`
}

func WorkspaceGeneration(
	config workspace.Config,
	install Installation,
	profiles []BuildProfile,
	toolchainIDs []string,
	inputGenerations ...string,
) string {
	payload := generationPayload{
		Config:           canonicalGenerationConfig(config),
		Installation:     canonicalGenerationInstallation(install),
		Profiles:         canonicalProfiles(profiles),
		ToolchainIDs:     append([]string{}, toolchainIDs...),
		InputGenerations: append([]string{}, inputGenerations...),
	}
	sort.Strings(payload.ToolchainIDs)
	sort.Strings(payload.InputGenerations)
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic("canonical workspace generation payload cannot fail to encode: " + err.Error())
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func canonicalGenerationConfig(config workspace.Config) generationConfig {
	result := generationConfig{
		Version: config.Version,
		CMake: generationCMakeConfig{
			Executable: canonicalPortablePath(config.CMake.Executable),
		},
		Projects:   make([]generationProject, 0, len(config.Projects)),
		Toolchains: make([]generationToolchain, 0, len(config.Toolchains)),
	}
	for _, project := range config.Projects {
		configurations := append([]string{}, project.Fallback.Configurations...)
		sort.Strings(configurations)
		result.Projects = append(result.Projects, generationProject{
			ID:        project.ID,
			SourceDir: canonicalRelativePath(project.SourceDir),
			Fallback: generationFallback{
				Configurations:     configurations,
				PreferredGenerator: project.Fallback.PreferredGenerator,
			},
		})
	}
	sort.Slice(result.Projects, func(first, second int) bool {
		if result.Projects[first].ID != result.Projects[second].ID {
			return result.Projects[first].ID < result.Projects[second].ID
		}
		return result.Projects[first].SourceDir < result.Projects[second].SourceDir
	})

	for _, toolchain := range config.Toolchains {
		result.Toolchains = append(result.Toolchains, generationToolchain{
			ID:                 toolchain.ID,
			Family:             toolchain.Family,
			CCompiler:          canonicalPortablePath(toolchain.CCompiler),
			CPPCompiler:        canonicalPortablePath(toolchain.CPPCompiler),
			InstallationID:     toolchain.InstallationID,
			ToolsetVersion:     toolchain.ToolsetVersion,
			HostArchitecture:   toolchain.HostArchitecture,
			TargetArchitecture: toolchain.TargetArchitecture,
		})
	}
	sort.Slice(result.Toolchains, func(first, second int) bool {
		return generationToolchainKey(result.Toolchains[first]) <
			generationToolchainKey(result.Toolchains[second])
	})
	return result
}

func generationToolchainKey(toolchain generationToolchain) string {
	encoded, err := json.Marshal(toolchain)
	if err != nil {
		panic("canonical toolchain cannot fail to encode: " + err.Error())
	}
	return string(encoded)
}

func canonicalGenerationInstallation(install Installation) generationInstallation {
	return generationInstallation{
		Executable:  canonicalPortablePath(install.Executable),
		Version:     install.Version,
		Source:      install.Source,
		Identity:    install.Identity,
		LicensePath: canonicalPortablePath(install.LicensePath),
	}
}

func canonicalProfiles(profiles []BuildProfile) []BuildProfile {
	result := make([]BuildProfile, len(profiles))
	for index, profile := range profiles {
		result[index] = canonicalProfile(profile)
	}
	sort.Slice(result, func(first, second int) bool {
		firstEncoded, firstErr := json.Marshal(result[first])
		secondEncoded, secondErr := json.Marshal(result[second])
		if firstErr != nil || secondErr != nil {
			panic("canonical profile cannot fail to encode")
		}
		return string(firstEncoded) < string(secondEncoded)
	})
	return result
}

func canonicalRelativePath(value string) string {
	if value == "" {
		return ""
	}
	canonical := canonicalPortablePath(value)
	if canonical == "." {
		return "."
	}
	return strings.TrimPrefix(canonical, "./")
}

func canonicalPortablePath(value string) string {
	if value == "" {
		return ""
	}
	portable := strings.ReplaceAll(value, `\`, "/")
	unc := strings.HasPrefix(portable, "//") &&
		len(portable) > 2 &&
		portable[2] != '/'
	canonical := path.Clean(portable)
	if unc && strings.HasPrefix(canonical, "/") && !strings.HasPrefix(canonical, "//") {
		canonical = "/" + canonical
	}
	if runtime.GOOS == "windows" {
		canonical = strings.ToLower(canonical)
	}
	return canonical
}
