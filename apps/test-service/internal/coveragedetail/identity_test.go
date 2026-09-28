package coveragedetail

import (
	"runtime"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedomain"
)

func TestStableIdentitiesIgnoreLocationButRejectUnsafePaths(t *testing.T) {
	file, err := StableFileID("project-a", "src/a.cpp")
	if err != nil || len(file) != 32 {
		t.Fatalf("file ID = %q, %v", file, err)
	}
	other, _ := StableFileID("project-a", "src/b.cpp")
	if file == other {
		t.Fatal("distinct files share an ID")
	}
	if _, err := StableFileID("project-a", "../secret.cpp"); err == nil {
		t.Fatal("accepted traversal")
	}
	if _, err := StableFileID("project-a", `C:\secret.cpp`); err == nil {
		t.Fatal("accepted native path")
	}
	if runtime.GOOS == "windows" {
		alias, err := StableFileID("project-a", "SRC/A.CPP")
		if err != nil || alias != file {
			t.Fatalf("Windows case alias changed file identity: %q, %v", alias, err)
		}
	}
	first, err := StableFunctionID(file, "_Z3foov")
	if err != nil || len(first) != 32 {
		t.Fatalf("function ID = %q, %v", first, err)
	}
	second, _ := StableFunctionID(file, "_Z3barv")
	if first == second {
		t.Fatal("different semantic keys share an ID")
	}
	if _, err := StableFunctionID(file, strings.Repeat(" ", 3)); err == nil {
		t.Fatal("accepted empty semantic key")
	}
	gap, err := StableGapID(strings.Repeat("a", 32), first, "line", coveragedomain.SourceLocation{Line: 7}, 0)
	if err != nil || len(gap) != 32 {
		t.Fatalf("gap ID = %q, %v", gap, err)
	}
	if _, err := StableGapID(strings.Repeat("a", 32), first, "line", coveragedomain.SourceLocation{Line: -1}, 0); err == nil {
		t.Fatal("accepted invalid line")
	}
}

func TestStableFunctionIdentityDoesNotUseSourceRange(t *testing.T) {
	input := detailInput()
	first, err := Build(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Functions[0].Start.Line = 99
	input.Functions[0].End.Line = 101
	input.Functions[1].Start.Line = 102
	input.Functions[1].End.Line = 110
	second, err := Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Files[0].Functions[0].ID != second.Files[0].Functions[0].ID {
		t.Fatal("source movement changed stable function ID")
	}
}
