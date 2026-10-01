package testgenanalysis

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var definePattern = regexp.MustCompile(`^-D[A-Za-z_][A-Za-z_0-9]{0,63}(=(0|1|[0-9]{1,9}))?$`)
var languageModes = map[string]bool{"-std=c11": true, "-std=c17": true, "-std=c++17": true, "-std=c++20": true}

// normalizedArguments accepts only a closed compiler option grammar. The
// source and executable are never chosen by the compile database entry.
func normalizedArguments(input []string, root string) ([]string, error) {
	if len(input) > maxArguments {
		return nil, errors.New("compile argument budget exceeded")
	}
	result := make([]string, 0, len(input))
	standard := false
	for _, arg := range input {
		if len(arg) == 0 || len(arg) > 1024 || strings.ContainsAny(arg, "\x00\r\n") || strings.HasPrefix(arg, "@") {
			return nil, errors.New("unsafe compile argument")
		}
		switch {
		case languageModes[arg]:
			if standard {
				return nil, errors.New("duplicate language mode")
			}
			standard = true
			result = append(result, arg)
		case definePattern.MatchString(arg):
			result = append(result, arg)
		case strings.HasPrefix(arg, "-I") && len(arg) > 2:
			rel := strings.TrimPrefix(arg, "-I")
			if !safeRelativeSource(rel) {
				return nil, errors.New("unsafe include root")
			}
			path := filepath.Join(root, filepath.FromSlash(rel))
			if err := verifyIncludeRoot(path, root); err != nil {
				return nil, err
			}
			result = append(result, "-I"+path)
		default:
			return nil, errors.New("unsupported compile argument")
		}
	}
	if !standard {
		return nil, errors.New("missing language mode")
	}
	return result, nil
}

func verifyIncludeRoot(path, root string) error {
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("untrusted include root")
		}
		if current == root {
			return nil
		}
		if filepath.Dir(current) == current {
			return errors.New("include root escaped workspace")
		}
	}
}
