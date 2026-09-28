package managedtest

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// StableCaseID binds a scenario to a project, canonical source-relative path,
// and stable function identity. Source line locations are intentionally absent.
func StableCaseID(projectID, sourceRelativePath, functionID, scenarioID string) (string, error) {
	path, err := canonicalSourcePath(sourceRelativePath)
	if !validProjectID(projectID) || err != nil || !validLowerHex(functionID, 32) || !validScenarioID(scenarioID) {
		return "", ErrInvalidManagedTest
	}
	hash := sha256.New()
	hash.Write([]byte("managed-case-v1"))
	for _, part := range []string{projectID, path, functionID, scenarioID} {
		hash.Write([]byte{0})
		hash.Write([]byte(part))
	}
	return "utc_" + hex.EncodeToString(hash.Sum(nil)[:16]), nil
}

func validProjectID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || i > 0 && (b == '-' || b == '_' || b == '.') {
			continue
		}
		return false
	}
	return true
}

func validScenarioID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || i > 0 && (b == '-' || b == '_' || b == '.') {
			continue
		}
		return false
	}
	return true
}

func canonicalSourcePath(path string) (string, error) {
	if path == "" || len(path) > 4096 || !utf8.ValidString(path) || strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\\:\x00") {
		return "", ErrInvalidManagedTest
	}
	for _, part := range strings.Split(path, "/") {
		decoded, err := url.PathUnescape(part)
		if err != nil || decoded == "" || decoded == "." || decoded == ".." || !norm.NFC.IsNormalString(decoded) || strings.ContainsAny(decoded, "/\\:\x00") || url.PathEscape(decoded) != part {
			return "", ErrInvalidManagedTest
		}
		for _, r := range decoded {
			if unicode.IsControl(r) {
				return "", ErrInvalidManagedTest
			}
		}
	}
	return path, nil
}

func validLowerHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b < '0' || b > '9' && (b < 'a' || b > 'f') {
			return false
		}
	}
	return true
}
