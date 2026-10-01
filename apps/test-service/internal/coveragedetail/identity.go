package coveragedetail

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/coveragedomain"
)

var ErrInvalidDetail = errors.New("invalid coverage detail")
var ErrStale = errors.New("coverage detail source binding is stale")

func stableID(domain string, parts ...string) string {
	hash := sha256.New()
	hash.Write([]byte(domain))
	for _, part := range parts {
		hash.Write([]byte{0})
		hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil)[:16])
}

func validHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, b := range []byte(value) {
		if b < '0' || b > '9' && (b < 'a' || b > 'f') {
			return false
		}
	}
	return true
}

func canonicalPath(path string) (string, error) {
	if path == "" || len(path) > 4096 || !utf8.ValidString(path) || strings.ContainsAny(path, "\\:\x00") || strings.HasPrefix(path, "/") {
		return "", ErrInvalidDetail
	}
	for _, part := range strings.Split(path, "/") {
		decoded, err := url.PathUnescape(part)
		if err != nil || decoded == "" || decoded == "." || decoded == ".." || strings.ContainsAny(decoded, "/\\:\x00") || url.PathEscape(decoded) != part {
			return "", ErrInvalidDetail
		}
	}
	if runtime.GOOS == "windows" {
		return strings.ToLower(path), nil
	}
	return path, nil
}

func validProjectID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for i, b := range []byte(value) {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || i > 0 && (b == '-' || b == '_' || b == '.') {
			continue
		}
		return false
	}
	return true
}

func StableFileID(projectID, relativePath string) (string, error) {
	path, err := canonicalPath(relativePath)
	if !validProjectID(projectID) || err != nil {
		return "", ErrInvalidDetail
	}
	return stableID("coverage-file-v1", projectID, path), nil
}

func StableFunctionID(fileID, semanticKey string) (string, error) {
	key := strings.Join(strings.Fields(semanticKey), " ")
	if !validHex(fileID, 32) || key == "" || len(key) > 8192 || !utf8.ValidString(key) || strings.ContainsRune(key, 0) {
		return "", ErrInvalidDetail
	}
	return stableID("coverage-function-v1", fileID, key), nil
}

func StableGapID(reportID, functionID, kind string, location coveragedomain.SourceLocation, ordinal int64) (string, error) {
	if !validHex(reportID, 32) || !validHex(functionID, 32) || (kind != "line" && kind != "branch") || location.Line < 1 || location.Line > coveragedomain.MaxSafeInteger || location.Column < 0 || location.Column > coveragedomain.MaxSafeInteger || ordinal < 0 || ordinal > coveragedomain.MaxSafeInteger {
		return "", ErrInvalidDetail
	}
	return stableID("coverage-gap-v1", reportID, functionID, kind, number(location.Line), number(location.Column), number(ordinal)), nil
}

func number(value int64) string { return strconv.FormatInt(value, 10) }
