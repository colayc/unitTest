package cmake

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

func profileID(profile BuildProfile) (string, error) {
	identity := canonicalProfile(profile)
	identity.ID = ""
	encoded, err := json.Marshal(identity)
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
