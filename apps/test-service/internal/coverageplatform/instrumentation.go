package coverageplatform

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
)

// PublishInstrumentation atomically creates one immutable CMake include in an
// empty, direct task root. Platform implementations retain the root while the
// temporary and final entries are created, so a path replacement cannot turn a
// successful publication into an unbound file write.
func PublishInstrumentation(root, name, contents, version string) (Instrumentation, error) {
	if !validInstrumentationInput(root, name, contents, version) {
		return Instrumentation{}, ErrInvalidCapability
	}
	if err := publishInstrumentationFile(root, name, []byte(contents)); err != nil {
		return Instrumentation{}, errors.Join(ErrInvalidCapability, err)
	}
	digest := sha256.Sum256([]byte(contents))
	digestText := hex.EncodeToString(digest[:])
	fingerprint := sha256.Sum256([]byte(version + "\x00" + digestText))
	return Instrumentation{
		IncludePath: filepath.Join(root, name),
		SHA256:      digestText,
		Fingerprint: hex.EncodeToString(fingerprint[:]),
	}, nil
}

func validInstrumentationInput(root, name, contents, version string) bool {
	return root != "" && filepath.IsAbs(root) && filepath.Clean(root) == root && !strings.ContainsRune(root, 0) &&
		name != "" && filepath.Base(name) == name && name != "." && !strings.ContainsRune(name, 0) &&
		contents != "" && version != "" && !strings.ContainsRune(version, 0)
}
