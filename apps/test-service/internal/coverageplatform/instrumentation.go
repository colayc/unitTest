package coverageplatform

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// PublishInstrumentation atomically creates one immutable CMake include in an
// empty, direct task root.  It deliberately accepts bytes rather than a tool
// identity: each toolchain owns its own stable contract fingerprint.
func PublishInstrumentation(root, name, contents, version string) (Instrumentation, error) {
	if !validInstrumentationInput(root, name, contents, version) {
		return Instrumentation{}, ErrInvalidCapability
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Instrumentation{}, ErrInvalidCapability
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		return Instrumentation{}, ErrInvalidCapability
	}
	temporary, temporaryPath, err := createInstrumentationTemporary(root)
	if err != nil {
		return Instrumentation{}, errors.Join(ErrInvalidCapability, err)
	}
	closed := false
	fail := func(cause error) (Instrumentation, error) {
		if !closed {
			_ = temporary.Close()
		}
		_ = os.Remove(temporaryPath)
		return Instrumentation{}, errors.Join(ErrInvalidCapability, cause)
	}
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temporary, hash), strings.NewReader(contents)); err != nil {
		return fail(err)
	}
	if err := temporary.Chmod(0o400); err != nil {
		return fail(err)
	}
	if err := temporary.Sync(); err != nil {
		return fail(err)
	}
	if err := temporary.Close(); err != nil {
		closed = true
		return fail(err)
	}
	closed = true
	finalPath := filepath.Join(root, name)
	if _, err := os.Lstat(finalPath); !os.IsNotExist(err) {
		return fail(errors.New("instrumentation destination already exists"))
	}
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return fail(err)
	}
	after, err := os.Lstat(root)
	if err != nil || !os.SameFile(info, after) {
		return Instrumentation{}, ErrInvalidCapability
	}
	published, err := os.Lstat(finalPath)
	if err != nil || !published.Mode().IsRegular() || published.Mode()&os.ModeSymlink != 0 {
		return Instrumentation{}, ErrInvalidCapability
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	fingerprint := sha256.Sum256([]byte(version + "\x00" + digest))
	return Instrumentation{IncludePath: finalPath, SHA256: digest, Fingerprint: hex.EncodeToString(fingerprint[:])}, nil
}

func validInstrumentationInput(root, name, contents, version string) bool {
	return root != "" && filepath.IsAbs(root) && filepath.Clean(root) == root && !strings.ContainsRune(root, 0) &&
		name != "" && filepath.Base(name) == name && name != "." && !strings.ContainsRune(name, 0) &&
		contents != "" && version != "" && !strings.ContainsRune(version, 0)
}

func createInstrumentationTemporary(root string) (*os.File, string, error) {
	for attempt := 0; attempt < 32; attempt++ {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, "", err
		}
		path := filepath.Join(root, ".coverage-instrumentation-"+hex.EncodeToString(nonce[:])+".tmp")
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if os.IsExist(err) {
			continue
		}
		return file, path, err
	}
	return nil, "", errors.New("unable to allocate instrumentation temporary")
}
