package cmake

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
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
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode build profile identity: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalProfile(profile BuildProfile) BuildProfile {
	profile.BinaryDir = canonicalPortablePath(profile.BinaryDir)
	return profile
}
