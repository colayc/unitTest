package testgenpublish

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/testgenrender"
)

func TestPlanRejectsUnsafeDestinationsAndDuplicates(t *testing.T) {
	paths := []string{"../escape.cpp", "/tmp/escape.cpp", `C:\escape.cpp`, `\\server\share\escape.cpp`, `\\?\C:\escape.cpp`, "tests/CON.cpp", "tests/generated/bad.txt", "src/production.cpp", "tests/../generated/choose_test.cpp", "tests/generated/choose_test.cpp/child"}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			f := newFixture(t)
			defer f.close(t)
			f.set.Files[0].Path = path
			if _, err := f.p.Plan(context.Background(), f.set); !errors.Is(err, ErrInvalidPlan) {
				t.Fatalf("accepted %q: %v", path, err)
			}
		})
	}
	f := newFixture(t)
	defer f.close(t)
	f.set.Files = append(f.set.Files, testgenrender.StagedFile{Path: "tests/generated/CHOOSE_TEST.cpp", Content: []byte("duplicate"), AfterDigest: digest([]byte("duplicate"))})
	if _, err := f.p.Plan(context.Background(), f.set); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("case-fold collision: %v", err)
	}
}

func TestPlanRejectsInvalidCMakeAndDiff(t *testing.T) {
	for _, change := range []struct{ name, after, diff string }{
		{"non-test edit", "set(CMAKE_CXX_FLAGS \"-DUNSAFE\")\n", ""},
		{"foreign target", "target_sources(other PRIVATE \"generated/choose_test.cpp\")\n", ""},
		{"raw command", "execute_process(COMMAND cmd /c whoami)\n", ""},
		{"malformed supplied diff", "", "@@ invalid @@"},
	} {
		t.Run(change.name, func(t *testing.T) {
			f := newFixture(t)
			defer f.close(t)
			if change.after != "" {
				f.set.Files[1].Content = []byte(f.before + change.after)
				f.set.Files[1].AfterDigest = digest(f.set.Files[1].Content)
			}
			f.set.Diff = change.diff
			if _, err := f.p.Plan(context.Background(), f.set); !errors.Is(err, ErrInvalidPlan) {
				t.Fatalf("accepted: %v", err)
			}
		})
	}
}

func TestPlanRejectsSymlinkAndStalePreimage(t *testing.T) {
	f := newFixture(t)
	defer f.close(t)
	if err := os.WriteFile(filepath.Join(f.root, "tests", "CMakeLists.txt"), []byte("user edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.Plan(context.Background(), f.set); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale: %v", err)
	}
	f2 := newFixture(t)
	defer f2.close(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(f2.root, "tests", "generated", "choose_test.cpp")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := f2.p.Plan(context.Background(), f2.set); !errors.Is(err, ErrConflict) {
		t.Fatalf("link: %v", err)
	}
	contents, err := os.ReadFile(outside)
	if err != nil || string(contents) != "outside" {
		t.Fatal("link target changed")
	}
}

func TestPlanBindsCandidateIdentityAndCharacterization(t *testing.T) {
	f := newFixture(t)
	defer f.close(t)
	f.set.CaseIDs = []string{testCase, strings.ToUpper(testCase)}
	if _, err := f.p.Plan(context.Background(), f.set); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("case collision: %v", err)
	}
	f.set.CaseIDs = []string{testCase}
	f.set.CharacterizationIDs = []string{testCase}
	plan, err := f.p.Plan(context.Background(), f.set)
	if err != nil {
		t.Fatal(err)
	}
	if plan.CharacterizationDigest == "" {
		t.Fatal("missing acknowledgement digest")
	}
	req := f.request(plan)
	req.CharacterizationDigest = ""
	if _, err := f.p.Accept(context.Background(), req); !errors.Is(err, ErrConflict) {
		t.Fatalf("missing acknowledgement accepted: %v", err)
	}
}

func TestSnapshotVerifierFailureClosesPlanAndAccept(t *testing.T) {
	f := newFixture(t)
	defer f.close(t)
	var fail bool
	f.p.verify = func(context.Context, string) error {
		if fail {
			return ErrConflict
		}
		return nil
	}
	plan := f.plan(t)
	fail = true
	if _, err := f.p.Accept(context.Background(), f.request(plan)); !errors.Is(err, ErrConflict) {
		t.Fatalf("drift accepted: %v", err)
	}
	if _, err := f.p.Plan(context.Background(), f.set); !errors.Is(err, ErrConflict) {
		t.Fatalf("drift planned: %v", err)
	}
}

func TestUnityCMakePatchPublishesOnlyGeneratedTestTarget(t *testing.T) {
	f := newFixture(t)
	defer f.close(t)
	f.set.SymbolID = strings.Repeat("d", 64)
	f.set.FrameworkTarget = "Unity"
	f.before = strings.Replace(f.before, "CppUTest", "Unity", 1)
	if err := os.WriteFile(filepath.Join(f.root, "tests", "CMakeLists.txt"), []byte(f.before), 0600); err != nil {
		t.Fatal(err)
	}
	f.set.Files[1].BeforeDigest = digest([]byte(f.before))
	f.set.Files[0].Path = "tests/generated/choose_test.c"
	f.set.Files[0].Content = []byte("#include \"unity.h\"\nvoid test_case_bbbbbbbbbbbbbbbb(void) { TEST_ASSERT_EQUAL_INT(7, choose(2)); }\n")
	f.set.Files[0].AfterDigest = digest(f.set.Files[0].Content)
	generated := "unit_tests_generated_" + strings.Repeat("d", 12)
	f.set.Files[1].Content = []byte(f.before + "add_executable(" + generated + " \"generated/choose_test.c\")\n" + "target_link_libraries(" + generated + " PRIVATE core Unity)\n" + "add_test(NAME " + generated + " COMMAND " + generated + ")\n")
	f.set.Files[1].AfterDigest = digest(f.set.Files[1].Content)
	plan, err := f.p.Plan(context.Background(), f.set)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.Accept(context.Background(), f.request(plan)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "tests", "generated", "choose_test.c")); err != nil {
		t.Fatal(err)
	}
}
