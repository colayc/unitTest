package coveragenormalize

import (
	"errors"
	"strings"
	"unicode/utf8"
)

var ErrInvalidGlob = errors.New("invalid coverage glob")

// Matcher decides whether a normalized, workspace-relative source URI is included.
type Matcher interface {
	Include(relativeURI string) bool
}

type GlobMatcher struct {
	includes []string
	excludes []string
}

const (
	maxGlobPatternBytes = 128
	maxGlobStates       = 4096
)

// NewGlobMatcher builds a bounded matcher. Exclusions, including the mandatory
// build/data/.git exclusions, always take precedence over inclusions.
func NewGlobMatcher(includes, excludes []string) (*GlobMatcher, error) {
	allExcludes := make([]string, 0, len(excludes)+6)
	allExcludes = append(allExcludes, excludes...)
	allExcludes = append(allExcludes,
		".git", ".git/**",
		"build", "build/**",
		"data", "data/**",
	)
	for _, pattern := range includes {
		if err := validateGlob(pattern); err != nil {
			return nil, err
		}
	}
	for _, pattern := range allExcludes {
		if err := validateGlob(pattern); err != nil {
			return nil, err
		}
	}
	return &GlobMatcher{
		includes: append([]string(nil), includes...),
		excludes: allExcludes,
	}, nil
}

func validateGlob(pattern string) error {
	if pattern == "" || len(pattern) > maxGlobPatternBytes || !utf8.ValidString(pattern) || strings.IndexByte(pattern, 0) >= 0 {
		return ErrInvalidGlob
	}
	if strings.HasPrefix(pattern, "/") || strings.Contains(pattern, "\\") {
		return ErrInvalidGlob
	}
	for _, segment := range strings.Split(pattern, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return ErrInvalidGlob
		}
	}
	return nil
}

func (m *GlobMatcher) Include(relativeURI string) bool {
	if !validRelativeURI(relativeURI) {
		return false
	}
	if len(m.includes) > 0 {
		matched := false
		for _, pattern := range m.includes {
			if globMatch(pattern, relativeURI) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, pattern := range m.excludes {
		if globMatch(pattern, relativeURI) {
			return false
		}
	}
	return true
}

func validRelativeURI(value string) bool {
	if value == "" || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func globMatch(pattern, value string) bool {
	type state struct{ pattern, value int }
	memo := make(map[state]bool)
	seen := make(map[state]bool)
	states := 0
	var visit func(int, int) bool
	visit = func(pi, vi int) bool {
		key := state{pi, vi}
		if seen[key] {
			return memo[key]
		}
		states++
		if states > maxGlobStates {
			return false
		}
		seen[key] = true
		var result bool
		switch {
		case pi == len(pattern):
			result = vi == len(value)
		case pattern[pi] == '*':
			if pi+1 < len(pattern) && pattern[pi+1] == '*' {
				next := pi + 2
				if next < len(pattern) && pattern[next] == '/' && visit(next+1, vi) {
					result = true
				} else if visit(next, vi) {
					result = true
				} else if vi < len(value) {
					result = visit(pi, vi+1)
				}
			} else if visit(pi+1, vi) {
				result = true
			} else if vi < len(value) && value[vi] != '/' {
				result = visit(pi, vi+1)
			}
		default:
			if vi < len(value) && (pattern[pi] == '?' || pattern[pi] == value[vi]) && (pattern[pi] == '?' && value[vi] != '/' || pattern[pi] != '?') {
				result = visit(pi+1, vi+1)
			}
		}
		memo[key] = result
		return result
	}
	return visit(0, 0)
}
