package testgenpublish

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/testgenrender"
)

const (
	testRun      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testCase     = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testSnapshot = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

type fixture struct {
	root    string
	journal string
	p       *Publisher
	set     CandidateSet
	before  string
	mode    os.FileMode
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "source")
	journal := filepath.Join(base, "journal")
	if err := os.MkdirAll(filepath.Join(root, "tests", "generated"), 0700); err != nil {
		t.Fatal(err)
	}
	cmakeFixture, err := os.ReadFile(filepath.Join("testdata", "CMakeLists.txt"))
	if err != nil {
		t.Fatal(err)
	}
	before := string(cmakeFixture)
	if err := os.WriteFile(filepath.Join(root, "tests", "CMakeLists.txt"), []byte(before), 0640); err != nil {
		t.Fatal(err)
	}
	testFixture, err := os.ReadFile(filepath.Join("testdata", "existing_test.cpp"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tests", "existing_test.cpp"), testFixture, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "tests", "CMakeLists.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(journal, 0700); err != nil {
		t.Fatal(err)
	}
	p, err := New(root, journal, func(context.Context, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	const source = "#include \"CppUTest/TestHarness.h\"\nTEST_GROUP(Generated_choose) {};\nTEST(Generated_choose, case_bbbbbbbbbbbbbbbb) { CHECK_EQUAL(7, choose(2)); }\n"
	after := before + "target_sources(unit_tests PRIVATE \"generated/choose_test.cpp\")\n"
	set := CandidateSet{RunID: testRun, SnapshotDigest: testSnapshot, CaseIDs: []string{testCase}, TestTarget: "unit_tests", ProductionTarget: "core", FrameworkTarget: "CppUTest", Files: []testgenrender.StagedFile{
		{Path: "tests/generated/choose_test.cpp", Content: []byte(source), AfterDigest: digest([]byte(source))},
		{Path: "tests/CMakeLists.txt", Content: []byte(after), BeforeDigest: digest([]byte(before)), AfterDigest: digest([]byte(after))},
	}}
	return fixture{root: root, journal: journal, p: p, set: set, before: before, mode: info.Mode().Perm()}
}

func (f fixture) close(t *testing.T) {
	t.Helper()
	if err := f.p.Close(); err != nil {
		t.Fatal(err)
	}
}
func (f fixture) plan(t *testing.T) PublishPlan {
	t.Helper()
	plan, err := f.p.Plan(context.Background(), f.set)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
func (f fixture) request(plan PublishPlan) AcceptRequest {
	return AcceptRequest{RunID: plan.RunID, CandidateSetDigest: plan.CandidateSetDigest, SnapshotDigest: plan.SnapshotDigest, DiffDigest: plan.DiffDigest, ConfirmationDigest: plan.ConfirmationDigest, CharacterizationDigest: plan.CharacterizationDigest}
}
func readFixture(t *testing.T, f fixture) (string, []byte, os.FileMode) {
	t.Helper()
	cmake, err := os.ReadFile(filepath.Join(f.root, "tests", "CMakeLists.txt"))
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join(f.root, "tests", "generated", "choose_test.cpp"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(f.root, "tests", "CMakeLists.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(cmake), source, info.Mode().Perm()
}

func TestPlanAcceptPublishesAndRepeatsWithoutRewrite(t *testing.T) {
	f := newFixture(t)
	defer f.close(t)
	plan := f.plan(t)
	if len(plan.Edits) != 2 || plan.Diff == "" || plan.DiffDigest != digest([]byte(plan.Diff)) || plan.ConfirmationDigest == "" {
		t.Fatalf("incomplete plan: %+v", plan)
	}
	receipt, err := f.p.Accept(context.Background(), f.request(plan))
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Edits) != 2 || receipt.RunID != testRun || strings.Contains(receipt.String(), f.root) || strings.Contains(receipt.String(), "CHECK_EQUAL") {
		t.Fatalf("unsafe receipt: %+v", receipt)
	}
	cmake, source, _ := readFixture(t, f)
	if !strings.Contains(cmake, "target_sources(unit_tests PRIVATE") || !strings.Contains(string(source), "CHECK_EQUAL") {
		t.Fatal("edits not published")
	}
	info, err := os.Stat(filepath.Join(f.root, "tests", "generated", "choose_test.cpp"))
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := f.p.Accept(context.Background(), f.request(plan))
	if err != nil || !reflect.DeepEqual(receipt, repeat) {
		t.Fatalf("repeat: %+v %v", repeat, err)
	}
	info2, err := os.Stat(filepath.Join(f.root, "tests", "generated", "choose_test.cpp"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(info, info2) {
		t.Fatal("repeat rewrote generated file")
	}
}

func TestAcceptRejectsStalePreimageAndChangedGeneratedFile(t *testing.T) {
	for _, tc := range []struct{ name, path, value string }{{"cmake", "tests/CMakeLists.txt", "user edit\n"}, {"generated", "tests/generated/choose_test.cpp", "user test\n"}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			defer f.close(t)
			plan := f.plan(t)
			if err := os.WriteFile(filepath.Join(f.root, filepath.FromSlash(tc.path)), []byte(tc.value), 0600); err != nil {
				t.Fatal(err)
			}
			_, acceptErr := f.p.Accept(context.Background(), f.request(plan))
			if !errors.Is(acceptErr, ErrConflict) {
				t.Fatalf("want conflict, got %v", acceptErr)
			}
			if tc.name == "cmake" {
				var conflict *ConflictReceipt
				if !errors.As(acceptErr, &conflict) || conflict.Path != "tests/CMakeLists.txt" || conflict.ActualDigest != digest([]byte(tc.value)) {
					t.Fatalf("missing closed conflict receipt: %+v %v", conflict, acceptErr)
				}
			}
			got, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(tc.path)))
			if err != nil || string(got) != tc.value {
				t.Fatalf("user edit overwritten: %q %v", got, err)
			}
		})
	}
}

func TestAcceptFaultsRollBackExactBytesAndMode(t *testing.T) {
	for _, stage := range []string{"stage-first", "stage-last", "journal", "commit-first", "commit-last", "readback", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			f := newFixture(t)
			defer f.close(t)
			plan := f.plan(t)
			f.p.hooks.fail = func(s string) error {
				if s == stage {
					return errors.New("injected")
				}
				return nil
			}
			if _, err := f.p.Accept(context.Background(), f.request(plan)); err == nil {
				t.Fatal("fault accepted")
			}
			cmake, source, mode := readFixture(t, f)
			if cmake != f.before || source != nil || mode != f.mode {
				t.Fatalf("partial edit: cmake=%q source=%q mode=%v", cmake, source, mode)
			}
		})
	}
}

func TestAcceptCancellationBeforeAndDuringCommit(t *testing.T) {
	for _, during := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "during"}[during], func(t *testing.T) {
			f := newFixture(t)
			defer f.close(t)
			plan := f.plan(t)
			ctx, cancel := context.WithCancel(context.Background())
			if during {
				f.p.hooks.fail = func(stage string) error {
					if stage == "commit-first" {
						cancel()
					}
					return nil
				}
			} else {
				cancel()
			}
			if _, err := f.p.Accept(ctx, f.request(plan)); !errors.Is(err, context.Canceled) {
				t.Fatalf("want cancelled, got %v", err)
			}
			cmake, source, mode := readFixture(t, f)
			if cmake != f.before || source != nil || mode != f.mode {
				t.Fatal("cancellation left edit")
			}
		})
	}
}

func TestRecoverInterruptedTransactionRestoresWorkspace(t *testing.T) {
	f := newFixture(t)
	plan := f.plan(t)
	f.p.hooks.fail = func(stage string) error {
		if stage == "commit-last" {
			panic("simulated process crash")
		}
		return nil
	}
	func() {
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Fatal("crash did not interrupt")
			}
		}()
		_, _ = f.p.Accept(context.Background(), f.request(plan))
	}()
	f.close(t)
	p, err := New(f.root, f.journal, func(context.Context, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	cmake, source, mode := readFixture(t, fixture{root: f.root})
	if cmake != f.before || source != nil || mode != f.mode {
		t.Fatalf("recovery incomplete: %q %q %v", cmake, source, mode)
	}
}

func TestRepeatAcceptanceAfterRestartDoesNotRewrite(t *testing.T) {
	f := newFixture(t)
	plan := f.plan(t)
	request := f.request(plan)
	want, err := f.p.Accept(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.root, "tests", "generated", "choose_test.cpp")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	f.close(t)
	p, err := New(f.root, f.journal, func(context.Context, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	got, err := p.Accept(context.Background(), request)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("repeat after restart: %+v %v", got, err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("restart repeat rewrote target")
	}
}

func TestAcceptCreatesMissingGeneratedDirectoryAndRollsItBackOnFault(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			f := newFixture(t)
			defer f.close(t)
			if err := os.Remove(filepath.Join(f.root, "tests", "generated")); err != nil {
				t.Fatal(err)
			}
			plan, err := f.p.Plan(context.Background(), f.set)
			if err != nil {
				t.Fatal(err)
			}
			if fail {
				f.p.hooks.fail = func(stage string) error {
					if stage == "commit-last" {
						return errors.New("injected")
					}
					return nil
				}
			}
			_, err = f.p.Accept(context.Background(), f.request(plan))
			if fail {
				if err == nil {
					t.Fatal("fault accepted")
				}
				if _, statErr := os.Lstat(filepath.Join(f.root, "tests", "generated")); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("orphan directory: %v", statErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConcurrentPreimageEditBeforeCommitConflictsWithoutOverwrite(t *testing.T) {
	f := newFixture(t)
	defer f.close(t)
	plan := f.plan(t)
	f.p.hooks.fail = func(stage string) error {
		if stage == "commit-first" {
			return os.WriteFile(filepath.Join(f.root, "tests", "CMakeLists.txt"), []byte("user change\n"), 0600)
		}
		return nil
	}
	if _, err := f.p.Accept(context.Background(), f.request(plan)); !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(f.root, "tests", "CMakeLists.txt"))
	if err != nil || string(data) != "user change\n" {
		t.Fatalf("user edit lost: %q %v", data, err)
	}
}

func TestReplacementBetweenReadAndRenameIsRestored(t *testing.T) {
	f := newFixture(t)
	defer f.close(t)
	plan := f.plan(t)
	f.p.hooks.beforeRename = func(relative string) {
		if relative != "tests/CMakeLists.txt" {
			return
		}
		target := filepath.Join(f.root, "tests", "CMakeLists.txt")
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("user replacement\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.p.Accept(context.Background(), f.request(plan)); !errors.Is(err, ErrConflict) {
		t.Fatalf("race accepted: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(f.root, "tests", "CMakeLists.txt"))
	if err != nil || string(data) != "user replacement\n" {
		t.Fatalf("raced user file stranded: %q %v", data, err)
	}
}

func TestTrustedSnapshotDriftBeforeAndDuringRenameRollsBack(t *testing.T) {
	for _, stage := range []string{"stage-last", "commit-last"} {
		t.Run(stage, func(t *testing.T) {
			f := newFixture(t)
			defer f.close(t)
			drift := false
			f.p.verify = func(context.Context, string) error {
				if drift {
					return ErrConflict
				}
				return nil
			}
			plan := f.plan(t)
			f.p.hooks.fail = func(s string) error {
				if s == stage {
					drift = true
				}
				return nil
			}
			if _, err := f.p.Accept(context.Background(), f.request(plan)); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale snapshot accepted: %v", err)
			}
			cmake, source, mode := readFixture(t, f)
			if cmake != f.before || source != nil || mode != f.mode {
				t.Fatal("drift left partial edits")
			}
		})
	}
}

func TestPostCommitCleanupFailureReportsRecoveryRequired(t *testing.T) {
	f := newFixture(t)
	plan := f.plan(t)
	f.p.hooks.cleanupRemove = func(name string) error {
		if strings.HasSuffix(name, ".backup") {
			return os.ErrPermission
		}
		return nil
	}
	receipt, err := f.p.Accept(context.Background(), f.request(plan))
	if !errors.Is(err, ErrRecoveryRequired) || receipt.ConfirmationDigest != plan.ConfirmationDigest {
		t.Fatalf("cleanup status: %+v %v", receipt, err)
	}
	if _, err := f.p.Accept(context.Background(), f.request(plan)); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("pending cleanup hidden on repeat: %v", err)
	}
	f.close(t)
	p, err := New(f.root, f.journal, func(context.Context, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Accept(context.Background(), f.request(plan)); err != nil {
		t.Fatalf("committed receipt lost after recovery: %v", err)
	}
}

func TestSnapshotDriftDuringLastRenameRollsBack(t *testing.T) {
	f := newFixture(t)
	defer f.close(t)
	drift := false
	f.p.verify = func(context.Context, string) error {
		if drift {
			return ErrConflict
		}
		return nil
	}
	plan := f.plan(t)
	f.p.hooks.beforeRename = func(relative string) {
		if relative == "tests/CMakeLists.txt" {
			drift = true
		}
	}
	if _, err := f.p.Accept(context.Background(), f.request(plan)); !errors.Is(err, ErrConflict) {
		t.Fatalf("last rename drift accepted: %v", err)
	}
	cmake, source, mode := readFixture(t, f)
	if cmake != f.before || source != nil || mode != f.mode {
		t.Fatal("late drift left edit")
	}
}

func TestUserCreationAfterBackupRenameIsNotOverwritten(t *testing.T) {
	f := newFixture(t)
	defer f.close(t)
	plan := f.plan(t)
	f.p.hooks.afterBackup = func(relative string) {
		if relative == "tests/CMakeLists.txt" {
			if err := os.WriteFile(filepath.Join(f.root, "tests", "CMakeLists.txt"), []byte("new user file\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := f.p.Accept(context.Background(), f.request(plan)); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("want recovery-required conflict: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(f.root, "tests", "CMakeLists.txt"))
	if err != nil || string(data) != "new user file\n" {
		t.Fatalf("user file overwritten: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "tests", "generated", "choose_test.cpp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("earlier generated file not rolled back: %v", err)
	}
}

func TestRollbackPreservesUserReplacementAtRemoveBoundary(t *testing.T) {
	f := newFixture(t)
	defer f.close(t)
	plan := f.plan(t)
	f.p.hooks.fail = func(stage string) error {
		if stage == "cleanup" {
			return errors.New("force rollback")
		}
		return nil
	}
	f.p.hooks.beforeRollbackMove = func(relative string) {
		if relative != "tests/generated/choose_test.cpp" {
			return
		}
		target := filepath.Join(f.root, "tests", "generated", "choose_test.cpp")
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("user replacement\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.p.Accept(context.Background(), f.request(plan)); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("want recovery-required conflict: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(f.root, "tests", "generated", "choose_test.cpp"))
	if err != nil || string(data) != "user replacement\n" {
		t.Fatalf("rollback deleted user replacement: %q %v", data, err)
	}
}

func TestSymlinkReplacementBeforeRenameIsRestoredWithoutFollowing(t *testing.T) {
	f := newFixture(t)
	defer f.close(t)
	plan := f.plan(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside\n"), 0600); err != nil {
		t.Fatal(err)
	}
	linkTarget := filepath.Join(f.root, "tests", "CMakeLists.txt")
	f.p.hooks.beforeRename = func(relative string) {
		if relative != "tests/CMakeLists.txt" {
			return
		}
		if err := os.Remove(linkTarget); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, linkTarget); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
	}
	if _, err := f.p.Accept(context.Background(), f.request(plan)); !errors.Is(err, ErrConflict) {
		t.Fatalf("link race accepted: %v", err)
	}
	info, err := os.Lstat(linkTarget)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link stranded after rollback: %v %v", info, err)
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "outside\n" {
		t.Fatalf("link target changed: %q %v", data, err)
	}
}

func TestCrashAfterExclusiveRestoreConvergesOnRepeatedRecovery(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(map[bool]string{false: "regular", true: "symlink"}[symlink], func(t *testing.T) {
			f := newFixture(t)
			plan := f.plan(t)
			if symlink {
				outside := filepath.Join(t.TempDir(), "outside")
				if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(f.root, "tests", "CMakeLists.txt")
				f.p.hooks.beforeRename = func(rel string) {
					if rel != "tests/CMakeLists.txt" {
						return
					}
					if err := os.Remove(target); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(outside, target); err != nil {
						t.Skipf("symlink unavailable: %v", err)
					}
				}
			} else {
				f.p.hooks.fail = func(stage string) error {
					if stage == "cleanup" {
						return errors.New("rollback")
					}
					return nil
				}
			}
			f.p.hooks.afterRestoreCreate = func(from, to string) {
				if strings.HasSuffix(from, ".backup") && to == "CMakeLists.txt" {
					panic("crash after restore link")
				}
			}
			func() {
				defer func() {
					if recover() == nil {
						t.Fatal("restore crash not reached")
					}
				}()
				_, _ = f.p.Accept(context.Background(), f.request(plan))
			}()
			f.close(t)
			p, err := New(f.root, f.journal, func(context.Context, string) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			if err := p.Recover(context.Background()); err != nil {
				t.Fatalf("first recover: %v", err)
			}
			if err := p.Recover(context.Background()); err != nil {
				t.Fatalf("repeat recover: %v", err)
			}
			if symlink {
				info, err := os.Lstat(filepath.Join(f.root, "tests", "CMakeLists.txt"))
				if err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("symlink not preserved: %v %v", info, err)
				}
			} else {
				cmake, source, _ := readFixture(t, fixture{root: f.root})
				if cmake != f.before || source != nil {
					t.Fatal("regular recovery incomplete")
				}
			}
		})
	}
}

func TestRecoveryPreservesLaterUserReplacementAfterRestoreCrash(t *testing.T) {
	f := newFixture(t)
	plan := f.plan(t)
	f.p.hooks.fail = func(stage string) error {
		if stage == "cleanup" {
			return errors.New("rollback")
		}
		return nil
	}
	f.p.hooks.afterRestoreCreate = func(from, to string) {
		if strings.HasSuffix(from, ".backup") && to == "CMakeLists.txt" {
			panic("crash after restore link")
		}
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("restore crash not reached")
			}
		}()
		_, _ = f.p.Accept(context.Background(), f.request(plan))
	}()
	f.close(t)
	target := filepath.Join(f.root, "tests", "CMakeLists.txt")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("later user edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := New(f.root, f.journal, func(context.Context, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Recover(context.Background()); !errors.Is(err, ErrConflict) {
		t.Fatalf("later edit not surfaced: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "later user edit\n" {
		t.Fatalf("later edit overwritten: %q %v", data, err)
	}
}

func TestCrashAfterHeldUserFileRestoreConverges(t *testing.T) {
	f := newFixture(t)
	plan := f.plan(t)
	f.p.hooks.fail = func(stage string) error {
		if stage == "cleanup" {
			return errors.New("rollback")
		}
		return nil
	}
	f.p.hooks.beforeRollbackMove = func(rel string) {
		if rel != "tests/CMakeLists.txt" {
			return
		}
		target := filepath.Join(f.root, "tests", "CMakeLists.txt")
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("user during rollback\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	f.p.hooks.afterRestoreCreate = func(from, to string) {
		if strings.HasSuffix(from, ".hold") && to == "CMakeLists.txt" {
			panic("crash after held restore")
		}
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("held restore crash not reached")
			}
		}()
		_, _ = f.p.Accept(context.Background(), f.request(plan))
	}()
	f.close(t)
	p, err := New(f.root, f.journal, func(context.Context, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Recover(context.Background()); err != nil {
		t.Fatalf("first recover: %v", err)
	}
	if err := p.Recover(context.Background()); err != nil {
		t.Fatalf("repeat recover: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(f.root, "tests", "CMakeLists.txt"))
	if err != nil || string(data) != "user during rollback\n" {
		t.Fatalf("user file lost: %q %v", data, err)
	}
}

func TestCrashAfterFirstRestoredPairRemovalConverges(t *testing.T) {
	f := newFixture(t)
	plan := f.plan(t)
	f.p.hooks.fail = func(stage string) error {
		if stage == "cleanup" {
			return errors.New("rollback")
		}
		return nil
	}
	f.p.hooks.beforeRollbackMove = func(rel string) {
		if rel != "tests/CMakeLists.txt" {
			return
		}
		target := filepath.Join(f.root, "tests", "CMakeLists.txt")
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("user during rollback\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	f.p.hooks.afterRestoreCreate = func(from, to string) {
		if strings.HasSuffix(from, ".hold") && to == "CMakeLists.txt" {
			panic("crash after held restore")
		}
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("held restore crash not reached")
			}
		}()
		_, _ = f.p.Accept(context.Background(), f.request(plan))
	}()
	f.close(t)
	p, err := New(f.root, f.journal, func(context.Context, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	firstRemoved := ""
	p.hooks.afterReplayRemove = func(name string) {
		firstRemoved = name
		panic("crash after first restored-pair removal")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("restored-pair removal crash not reached")
			}
		}()
		_ = p.Recover(context.Background())
	}()
	if firstRemoved == "" {
		t.Fatal("no private entry removed")
	}
	if !strings.HasSuffix(firstRemoved, ".backup") {
		t.Fatalf("first removal was %s, want backup before hold", firstRemoved)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	p, err = New(f.root, f.journal, func(context.Context, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Recover(context.Background()); err != nil {
		t.Fatalf("recover after first removal of %s: %v", firstRemoved, err)
	}
	if err := p.Recover(context.Background()); err != nil {
		t.Fatalf("repeat recover: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(f.root, "tests", "CMakeLists.txt"))
	if err != nil || string(data) != "user during rollback\n" {
		t.Fatalf("user file lost: %q %v", data, err)
	}
}
