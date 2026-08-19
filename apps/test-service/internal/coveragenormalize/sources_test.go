package coveragenormalize

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCollectSourcesFiltersDigestsAndSortsDeterministically(t *testing.T) {
	root := t.TempDir()
	writeSourceFile(t, filepath.Join(root, "src", "z.cpp"), "z")
	writeSourceFile(t, filepath.Join(root, "src", "a.cpp"), "a")
	writeSourceFile(t, filepath.Join(root, "generated", "skip.cpp"), "skip")
	matcher, err := NewGlobMatcher([]string{"**/*.cpp"}, []string{"generated/**"})
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{
		filepath.Join(root, "src", "z.cpp"),
		filepath.Join(root, "generated", "skip.cpp"),
		filepath.Join(root, "src", "a.cpp"),
	}
	got, err := CollectSources(root, paths, matcher, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if uris := sourceURIs(got); !reflect.DeepEqual(uris, []string{"src/a.cpp", "src/z.cpp"}) {
		t.Fatalf("source URIs = %#v, want sorted selected sources", uris)
	}
	if got[0].SHA256 == "" || got[1].SHA256 == "" || got[0].NativePath == "" {
		t.Fatalf("source digests/native paths missing: %#v", got)
	}
}

func TestCollectSourcesRejectsDuplicatesAndBoundsCandidateCount(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src", "one.cpp")
	writeSourceFile(t, path, "one")
	matcher, err := NewGlobMatcher([]string{"**"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CollectSources(root, []string{path, path}, matcher, DefaultLimits()); !errors.Is(err, ErrDuplicateSource) {
		t.Fatalf("duplicate error = %v, want ErrDuplicateSource", err)
	}
	limits := DefaultLimits()
	limits.MaxFiles = 1
	if _, err := CollectSources(root, []string{path, filepath.Join(root, "src", "other.cpp")}, matcher, limits); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("candidate limit error = %v, want ErrLimitExceeded", err)
	}
}

func TestCollectSourcesRejectsOutsideAndUnsafeCandidates(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside.cpp")
	writeSourceFile(t, outside, "outside")
	matcher, err := NewGlobMatcher([]string{"**"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CollectSources(root, []string{outside}, matcher, DefaultLimits()); !errors.Is(err, ErrInvalidSourcePath) {
		t.Fatalf("outside error = %v, want ErrInvalidSourcePath", err)
	}
}

func sourceURIs(values []SourceBinding) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.URI
	}
	return result
}

func writeSourceFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
