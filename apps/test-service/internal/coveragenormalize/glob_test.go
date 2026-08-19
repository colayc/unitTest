package coveragenormalize

import (
	"errors"
	"testing"
)

func TestGlobMatcherSupportsIncludesExcludesAndDoubleStar(t *testing.T) {
	matcher, err := NewGlobMatcher([]string{"src/**/*.cpp"}, []string{"src/generated/**"})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		"src/main.cpp":             true,
		"src/nested/math.cpp":      true,
		"src/generated/math.cpp":   false,
		"include/header.hpp":       false,
		"src/main.c":               false,
		"src/generated/deep/a.cpp": false,
	} {
		if got := matcher.Include(path); got != want {
			t.Fatalf("Include(%q) = %t, want %t", path, got, want)
		}
	}
}

func TestGlobMatcherSupportsSingleCharacterAndMandatoryExclusions(t *testing.T) {
	matcher, err := NewGlobMatcher([]string{"**/*.c?", "README.*"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		"src/a.c1":        true,
		"src/a.cpp":       false,
		"README.md":       true,
		"build/a.c1":      false,
		"data/fixture.c1": false,
		".git/config.c1":  false,
	} {
		if got := matcher.Include(path); got != want {
			t.Fatalf("Include(%q) = %t, want %t", path, got, want)
		}
	}
}

func TestGlobMatcherRejectsUnsafePatternsAndURIs(t *testing.T) {
	for name, includes := range map[string][]string{
		"empty pattern": {""},
		"absolute":      {"/src/**"},
		"backslash":     {"src\\**"},
		"traversal":     {"src/../**"},
		"too long":      {string(make([]byte, 129))},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewGlobMatcher(includes, nil); !errors.Is(err, ErrInvalidGlob) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	matcher, err := NewGlobMatcher([]string{"**"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/absolute.cpp", "../outside.cpp", "src\\file.cpp", ""} {
		if matcher.Include(path) {
			t.Fatalf("unsafe URI accepted: %q", path)
		}
	}
}
