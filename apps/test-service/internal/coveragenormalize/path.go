package coveragenormalize

import (
	"errors"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

var ErrInvalidSourcePath = errors.New("invalid coverage source path")

// SourceBinding retains the native path only for internal digest/identity
// work. Public returns a clone with NativePath removed before serialization.
type SourceBinding struct {
	URI        string
	SHA256     string
	NativePath string
}

func (binding SourceBinding) Public() SourceBinding {
	binding.NativePath = ""
	return binding
}

// BindSourcePath validates a source beneath the workspace and derives a
// canonical, relative URI. It is lexical by design; opened-file identity and
// replacement checks belong to the source digest stage.
func BindSourcePath(workspaceRoot, nativePath string) (SourceBinding, error) {
	if !validPathString(workspaceRoot) || !validPathString(nativePath) ||
		!filepath.IsAbs(workspaceRoot) || !filepath.IsAbs(nativePath) {
		return SourceBinding{}, ErrInvalidSourcePath
	}
	root, err := filepath.Abs(filepath.Clean(workspaceRoot))
	if err != nil || !filepath.IsAbs(root) {
		return SourceBinding{}, ErrInvalidSourcePath
	}
	path, err := filepath.Abs(filepath.Clean(nativePath))
	if err != nil || !filepath.IsAbs(path) {
		return SourceBinding{}, ErrInvalidSourcePath
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return SourceBinding{}, ErrInvalidSourcePath
	}
	components := strings.Split(filepath.ToSlash(relative), "/")
	for _, component := range components {
		if excludedComponent(component) {
			return SourceBinding{}, ErrInvalidSourcePath
		}
	}
	encoded := make([]string, len(components))
	for index, component := range components {
		encoded[index] = url.PathEscape(component)
	}
	return SourceBinding{URI: strings.Join(encoded, "/"), NativePath: path}, nil
}

func validPathString(value string) bool {
	return value != "" && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func excludedComponent(value string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(value, ".git") || strings.EqualFold(value, "build") || strings.EqualFold(value, "data")
	}
	return value == ".git" || value == "build" || value == "data"
}
