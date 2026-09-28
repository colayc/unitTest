package managedtest

import (
	"strings"
	"testing"
)

func TestStableCaseIDDeterministicAndDimensionBound(t *testing.T) {
	first, err := StableCaseID("project-a", "src/math.cpp", strings.Repeat("a", 32), "empty-input")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 36 || !strings.HasPrefix(first, "utc_") {
		t.Fatalf("case ID %q", first)
	}
	for _, input := range [][4]string{
		{"project-a", "src/math.cpp", strings.Repeat("a", 32), "empty-input"},
		{"project-b", "src/math.cpp", strings.Repeat("a", 32), "empty-input"},
		{"project-a", "src/other.cpp", strings.Repeat("a", 32), "empty-input"},
		{"project-a", "src/math.cpp", strings.Repeat("b", 32), "empty-input"},
		{"project-a", "src/math.cpp", strings.Repeat("a", 32), "nonempty-input"},
	} {
		got, err := StableCaseID(input[0], input[1], input[2], input[3])
		if err != nil {
			t.Fatal(err)
		}
		if input == ([4]string{"project-a", "src/math.cpp", strings.Repeat("a", 32), "empty-input"}) {
			if got != first {
				t.Fatalf("non-deterministic ID: %q != %q", got, first)
			}
		} else if got == first {
			t.Fatalf("identity dimension did not affect case ID: %q", input)
		}
	}
}

func TestStableCaseIDRejectsNoncanonicalIdentity(t *testing.T) {
	valid := [4]string{"project-a", "src/math.cpp", strings.Repeat("a", 32), "empty-input"}
	tests := []struct {
		name  string
		index int
		value string
	}{
		{"project empty", 0, ""}, {"project delimiter", 0, "project/a"},
		{"path traversal", 1, "../secret.cpp"}, {"path encoded traversal", 1, "%2E%2E/secret.cpp"},
		{"path absolute", 1, "/src/math.cpp"}, {"windows absolute", 1, `C:\secret.cpp`},
		{"path redundant", 1, "src//math.cpp"}, {"path NUL", 1, "src/a\x00.cpp"},
		{"path non-NFC", 1, "src/cafe\u0301.cpp"}, {"path raw Unicode", 1, "src/caf\u00e9.cpp"},
		{"path escaped non-NFC", 1, "src/cafe%CC%81.cpp"},
		{"function uppercase", 2, strings.Repeat("A", 32)}, {"function short", 2, "abc"},
		{"scenario empty", 3, ""}, {"scenario line number", 3, "line:42"},
		{"scenario Unicode", 3, "caf\u00e9"}, {"scenario oversized", 3, strings.Repeat("x", 129)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parts := valid
			parts[tt.index] = tt.value
			if got, err := StableCaseID(parts[0], parts[1], parts[2], parts[3]); err == nil {
				t.Fatalf("accepted %q as %q", tt.value, got)
			}
		})
	}
}

func TestStableCaseIDAcceptsCanonicalEscapedUnicodePath(t *testing.T) {
	if _, err := StableCaseID("project-a", "src/caf%C3%A9.cpp", strings.Repeat("a", 32), "empty-input"); err != nil {
		t.Fatal(err)
	}
}
