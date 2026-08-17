package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

var (
	ErrInvalidRoot         = errors.New("invalid workspace root")
	ErrInvalidRelativePath = errors.New("invalid workspace-relative path")
	ErrPathOutsideRoot     = errors.New("path is outside workspace root")
)

type Root struct {
	NativePath string
	URI        string
	ID         string
}

func OpenRoot(path string) (Root, error) {
	if path == "" {
		return Root{}, fmt.Errorf("%w: empty path", ErrInvalidRoot)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Root{}, fmt.Errorf("%w: make absolute: %v", ErrInvalidRoot, err)
	}
	absolute = filepath.Clean(absolute)
	info, err := os.Stat(absolute)
	if err != nil {
		return Root{}, fmt.Errorf("%w: inspect path: %v", ErrInvalidRoot, err)
	}
	if !info.IsDir() {
		return Root{}, fmt.Errorf("%w: path is not a directory", ErrInvalidRoot)
	}
	finalPath, volumeIdentity, err := finalExistingPath(absolute)
	if err != nil {
		return Root{}, fmt.Errorf("%w: resolve final path: %v", ErrInvalidRoot, err)
	}
	uri, err := rootURI(finalPath)
	if err != nil {
		return Root{}, fmt.Errorf("%w: construct URI: %v", ErrInvalidRoot, err)
	}
	id, err := rootID(finalPath, volumeIdentity)
	if err != nil {
		return Root{}, fmt.Errorf("%w: construct ID: %v", ErrInvalidRoot, err)
	}
	return Root{NativePath: finalPath, URI: uri, ID: id}, nil
}

func (r Root) ResolveRelative(relative string) (string, error) {
	if r.NativePath == "" {
		return "", ErrInvalidRoot
	}
	if relative == "" || filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" ||
		os.IsPathSeparator(relative[0]) {
		return "", ErrInvalidRelativePath
	}
	candidate := filepath.Join(r.NativePath, relative)
	finalPath, _, err := finalPathWithMissingTail(candidate)
	if err != nil {
		return "", fmt.Errorf("%w: resolve path: %v", ErrInvalidRelativePath, err)
	}
	if !pathWithinRoot(r.NativePath, finalPath) {
		return "", ErrPathOutsideRoot
	}
	return finalPath, nil
}

func (r Root) Contains(path string) bool {
	if r.NativePath == "" || path == "" {
		return false
	}
	finalPath, _, err := finalPathWithMissingTail(path)
	return err == nil && pathWithinRoot(r.NativePath, finalPath)
}

func finalPathWithMissingTail(path string) (string, string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	probe := filepath.Clean(absolute)
	var tail []string
	for {
		_, err := os.Lstat(probe)
		if err == nil {
			finalPath, volumeIdentity, err := finalExistingPath(probe)
			if err != nil {
				return "", "", err
			}
			for _, component := range tail {
				finalPath = filepath.Join(finalPath, component)
			}
			return filepath.Clean(finalPath), volumeIdentity, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", "", err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", "", err
		}
		tail = append([]string{filepath.Base(probe)}, tail...)
		probe = parent
	}
}

func rootURI(path string) (string, error) {
	host, uriPath, err := fileURLParts(path)
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: "file", Host: host, Path: uriPath}).String(), nil
}

func rootID(path, volumeIdentity string) (string, error) {
	identity := struct {
		Platform string `json:"platform"`
		Volume   string `json:"volume"`
		Path     string `json:"path"`
	}{
		Platform: platformIdentity(),
		Volume:   normalizedVolumeIdentity(volumeIdentity),
		Path:     identityPath(path),
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
