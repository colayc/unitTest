package cmake

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxGeneratedProfileIdentifierBytes = 64
	maxGeneratedProfileWorkspaceBytes  = 256 * 1024
)

type BuildProfile struct {
	ID              string
	ProjectID       string
	Origin          string
	ConfigurePreset string
	BuildPreset     string
	ToolchainID     string
	Generator       string
	Configuration   string
	BinaryDir       string
}

// GeneratedProfileSpec describes the semantic identity and trusted build-root
// descriptor for a Service-generated CMake profile. BuildRoot may only come
// from a caller-validated, Service-owned trusted build-root provider or
// configuration; workspace or Protocol input is not a trusted source.
type GeneratedProfileSpec struct {
	ProjectID     string
	ToolchainID   string
	Generator     string
	Configuration string
	// BuildRoot is a lexical descriptor. Supplying a nonexistent path or a path
	// that is currently a file does not establish or imply trust.
	BuildRoot string
}

// NewGeneratedProfile constructs pure profile metadata. It never stats,
// creates, opens, or otherwise accesses the filesystem. It validates
// BuildRoot only as valid UTF-8 without NUL, absolute, and clean, then derives
// a lexically contained child whose name is the fixed lowercase hexadecimal
// profile ID.
//
// The caller and the Phase 3C execution layer remain responsible for secure
// directory creation and for validating directory type, symlink, junction and
// reparse-point boundaries, ownership and ACLs, filesystem identity, and the
// trusted root again before every execution.
func NewGeneratedProfile(spec GeneratedProfileSpec) (BuildProfile, error) {
	fields := []struct {
		name     string
		value    string
		maxBytes int
	}{
		{name: "project ID", value: spec.ProjectID, maxBytes: maxGeneratedProfileIdentifierBytes},
		{name: "toolchain ID", value: spec.ToolchainID, maxBytes: maxGeneratedProfileIdentifierBytes},
		{name: "generator", value: spec.Generator, maxBytes: maxGeneratedProfileWorkspaceBytes},
		{name: "configuration", value: spec.Configuration, maxBytes: maxGeneratedProfileWorkspaceBytes},
	}
	for _, field := range fields {
		if err := validateGeneratedProfileField(field.name, field.value, field.maxBytes); err != nil {
			return BuildProfile{}, err
		}
	}

	buildRoot, err := validateGeneratedBuildRoot(spec.BuildRoot)
	if err != nil {
		return BuildProfile{}, err
	}
	profile := BuildProfile{
		ProjectID:     spec.ProjectID,
		Origin:        "generated",
		ToolchainID:   spec.ToolchainID,
		Generator:     spec.Generator,
		Configuration: spec.Configuration,
	}
	profile.ID, err = generatedProfileID(profile)
	if err != nil {
		return BuildProfile{}, err
	}
	profile.BinaryDir = filepath.Join(buildRoot, profile.ID)
	if !generatedBuildDirectoryWithinRoot(buildRoot, profile.BinaryDir) {
		return BuildProfile{}, fmt.Errorf("generated build directory is outside build root")
	}
	return profile, nil
}

func profileID(profile BuildProfile, inputGenerations ...string) (string, error) {
	identity := canonicalProfile(profile)
	identity.ID = ""
	var value any = identity
	if len(inputGenerations) > 0 {
		generations := append([]string{}, inputGenerations...)
		sort.Strings(generations)
		value = struct {
			Profile          BuildProfile `json:"profile"`
			InputGenerations []string     `json:"inputGenerations"`
		}{
			Profile:          identity,
			InputGenerations: generations,
		}
	}
	return hashCanonicalJSON(value, "build profile identity")
}

func generatedProfileID(profile BuildProfile) (string, error) {
	identity := struct {
		ProjectID     string `json:"projectId"`
		Origin        string `json:"origin"`
		ToolchainID   string `json:"toolchainId"`
		Generator     string `json:"generator"`
		Configuration string `json:"configuration"`
	}{
		ProjectID:     profile.ProjectID,
		Origin:        "generated",
		ToolchainID:   profile.ToolchainID,
		Generator:     profile.Generator,
		Configuration: profile.Configuration,
	}
	return hashCanonicalJSON(identity, "generated build profile identity")
}

func hashCanonicalJSON(value any, description string) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode %s: %w", description, err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func validateGeneratedProfileField(name, value string, maxBytes int) error {
	if value == "" {
		return fmt.Errorf("generated profile %s must not be empty", name)
	}
	if len(value) > maxBytes {
		return fmt.Errorf(
			"generated profile %s exceeds %d bytes",
			name,
			maxBytes,
		)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("generated profile %s is not valid UTF-8", name)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("generated profile %s contains a control character", name)
		}
	}
	return nil
}

func validateGeneratedBuildRoot(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("generated profile build root must not be empty")
	}
	if strings.IndexByte(value, 0) >= 0 {
		return "", fmt.Errorf("generated profile build root contains NUL")
	}
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("generated profile build root is not valid UTF-8")
	}
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("generated profile build root must be absolute")
	}
	clean := filepath.Clean(value)
	if clean != value {
		return "", fmt.Errorf("generated profile build root must be clean")
	}
	if filepath.VolumeName(clean) != filepath.VolumeName(value) {
		return "", fmt.Errorf("generated profile build root changed volume when cleaned")
	}
	return clean, nil
}

func generatedBuildDirectoryWithinRoot(root, candidate string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(candidate) {
		return false
	}
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func canonicalProfile(profile BuildProfile) BuildProfile {
	profile.BinaryDir = canonicalPortablePath(profile.BinaryDir)
	return profile
}
