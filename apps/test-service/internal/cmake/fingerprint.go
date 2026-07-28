package cmake

import (
	"encoding/hex"
	"sort"
	"strings"
)

type FingerprintFile struct {
	Path     string `json:"path"`
	Identity string `json:"identity"`
	SHA256   string `json:"sha256"`
}

type ProfileFingerprintInput struct {
	Profile           BuildProfile
	CMakeIdentity     string
	ToolchainIdentity string
	PresetInputs      []FingerprintFile
	CMakeInputStates  []FingerprintFile
	Cache             FingerprintFile
	FileAPIState      []FingerprintFile
}

type BuildConfiguration struct {
	Fingerprint string
	Succeeded   bool
}

type configureFingerprintPayload struct {
	Profile           BuildProfile      `json:"profile"`
	CMakeIdentity     string            `json:"cmakeIdentity"`
	ToolchainIdentity string            `json:"toolchainIdentity"`
	PresetInputs      []FingerprintFile `json:"presetInputs"`
	CMakeInputs       []FingerprintFile `json:"cmakeInputs"`
	Cache             FingerprintFile   `json:"cache"`
	FileAPIState      []FingerprintFile `json:"fileApiState"`
}

func ConfigureFingerprint(input ProfileFingerprintInput) string {
	payload := configureFingerprintPayload{
		Profile:           canonicalProfile(input.Profile),
		CMakeIdentity:     input.CMakeIdentity,
		ToolchainIdentity: input.ToolchainIdentity,
		PresetInputs:      canonicalFingerprintFiles(input.PresetInputs),
		CMakeInputs:       canonicalFingerprintFiles(input.CMakeInputStates),
		Cache:             canonicalFingerprintFile(input.Cache),
		FileAPIState:      canonicalFingerprintFiles(input.FileAPIState),
	}
	sum, err := canonicalSHA256(payload)
	if err != nil {
		return ""
	}
	return sum
}

func NeedsConfigure(previous BuildConfiguration, current ProfileFingerprintInput) bool {
	if !previous.Succeeded || previous.Fingerprint == "" || !validProfileFingerprintInput(current) {
		return true
	}
	return previous.Fingerprint != ConfigureFingerprint(current)
}

func validProfileFingerprintInput(input ProfileFingerprintInput) bool {
	if input.Profile.ID == "" || input.Profile.ProjectID == "" ||
		input.CMakeIdentity == "" || input.ToolchainIdentity == "" ||
		len(input.CMakeInputStates) == 0 || len(input.FileAPIState) == 0 ||
		!validFingerprintFile(input.Cache) {
		return false
	}
	files := make([]FingerprintFile, 0,
		len(input.PresetInputs)+len(input.CMakeInputStates)+1+len(input.FileAPIState),
	)
	files = append(files, input.PresetInputs...)
	files = append(files, input.CMakeInputStates...)
	files = append(files, input.Cache)
	files = append(files, input.FileAPIState...)
	return validFingerprintFiles(files)
}

func validFingerprintFiles(files []FingerprintFile) bool {
	byPath := make(map[string]FingerprintFile, len(files))
	for _, file := range files {
		if !validFingerprintFile(file) {
			return false
		}
		canonical := canonicalFingerprintFile(file)
		if previous, exists := byPath[canonical.Path]; exists &&
			(previous.Identity != canonical.Identity || previous.SHA256 != canonical.SHA256) {
			return false
		}
		byPath[canonical.Path] = canonical
	}
	return true
}

func validFingerprintFile(file FingerprintFile) bool {
	if file.Path == "" || file.Identity == "" || len(file.SHA256) != 64 {
		return false
	}
	_, err := hex.DecodeString(file.SHA256)
	return err == nil
}

func canonicalFingerprintFiles(files []FingerprintFile) []FingerprintFile {
	result := make([]FingerprintFile, len(files))
	for index, file := range files {
		result[index] = canonicalFingerprintFile(file)
	}
	sort.Slice(result, func(first, second int) bool {
		if result[first].Path != result[second].Path {
			return result[first].Path < result[second].Path
		}
		if result[first].Identity != result[second].Identity {
			return result[first].Identity < result[second].Identity
		}
		return result[first].SHA256 < result[second].SHA256
	})
	if len(result) == 0 {
		return []FingerprintFile{}
	}
	write := 1
	for read := 1; read < len(result); read++ {
		if result[read] == result[write-1] {
			continue
		}
		result[write] = result[read]
		write++
	}
	return result[:write]
}

func canonicalFingerprintFile(file FingerprintFile) FingerprintFile {
	file.Path = canonicalPortablePath(file.Path)
	file.SHA256 = strings.ToLower(file.SHA256)
	return file
}
