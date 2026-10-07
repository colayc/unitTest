package testgenvalidate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"unit-test-ide.local/test-service/internal/testgenrender"
)

func TestWorkspaceSnapshotDigestUsesValidatorIdentity(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.c")
	if err := os.WriteFile(path, []byte("int value(void) { return 1; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, internal, err := sourceFingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	public, err := WorkspaceSnapshotDigest(root)
	if err != nil || public != internal {
		t.Fatalf("WorkspaceSnapshotDigest() = %q, %v; want %q", public, err, internal)
	}
	if err := os.WriteFile(path, []byte("int value(void) { return 2; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := WorkspaceSnapshotDigest(root)
	if err != nil || changed == public {
		t.Fatalf("changed WorkspaceSnapshotDigest() = %q, %v; original %q", changed, err, public)
	}
}

func TestWorkspaceSnapshotDigestIgnoresRepositoryMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "objects"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "objects", "state"), []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("build/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source.c"), []byte("int value;\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := WorkspaceSnapshotDigest(root)
	if err != nil {
		t.Fatalf("WorkspaceSnapshotDigest() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "objects", "state"), []byte("two"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := WorkspaceSnapshotDigest(root)
	if err != nil || before != after {
		t.Fatalf("repository metadata changed source identity: before=%s after=%s error=%v", before, after, err)
	}
}

func TestValidateRejectsEscapingAndHardLinkedSnapshotFiles(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			source := t.TempDir()
			outside := filepath.Join(t.TempDir(), "outside.c")
			if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			if kind == "symlink" {
				err = os.Symlink(outside, filepath.Join(source, "source.c"))
			} else {
				err = os.Link(outside, filepath.Join(source, "source.c"))
			}
			if err != nil {
				t.Skipf("host cannot create %s: %v", kind, err)
			}
			planner := &fixturePlanner{coverage: coverageFixture(1, 1, 0)}
			v := Validator{Config: Config{SourceRoot: source, TempRoot: t.TempDir(), Planner: planner, VerifyEvidence: trustedFixtureProof, ResolveCandidate: fixtureResolver(source, coverageFixture(0, 1, 0))}}
			result, err := v.Validate(context.Background(), ValidationRequest{TaskID: testID, CandidateID: testID, BaselineCoverage: coverageFixture(0, 1, 0), Metrics: Metrics{Functions: true}, Assertion: AssertionEvidence{Kind: Verified, IndependentProofDigest: testID}})
			if err != nil || result.Diagnostic != DiagnosticIsolation || len(planner.stages) != 0 {
				t.Fatalf("unsafe source accepted: %+v %v", result, err)
			}
		})
	}
}

func TestValidateRejectsAbsoluteAndStaleStagedEdits(t *testing.T) {
	for _, edit := range []testgenrender.StagedFile{{Path: "C:/escape.c", Content: []byte("x"), AfterDigest: digestTest([]byte("x"))}, {Path: "tests/generated.c", Content: []byte("x"), BeforeDigest: testID, AfterDigest: digestTest([]byte("x"))}} {
		source := t.TempDir()
		planner := &fixturePlanner{coverage: coverageFixture(1, 1, 0)}
		v := Validator{Config: Config{SourceRoot: source, TempRoot: t.TempDir(), Planner: planner, VerifyEvidence: trustedFixtureProof, ResolveCandidate: fixtureResolver(source, coverageFixture(0, 1, 0))}}
		result, err := v.Validate(context.Background(), ValidationRequest{TaskID: testID, CandidateID: testID, Edits: testgenrender.StagedEditSet{Files: []testgenrender.StagedFile{edit}}, BaselineCoverage: coverageFixture(0, 1, 0), Metrics: Metrics{Functions: true}, Assertion: AssertionEvidence{Kind: Verified, IndependentProofDigest: testID}})
		if err != nil || result.Diagnostic != DiagnosticIsolation || len(planner.stages) != 0 {
			t.Fatalf("unsafe edit accepted: %+v %v", result, err)
		}
	}
}

func TestSnapshotRejectsDriftFromTrustedPreCopyIdentity(t *testing.T) {
	source := t.TempDir()
	temp := t.TempDir()
	path := filepath.Join(source, "source.c")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	expected, _, err := sourceFingerprint(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after"), 0600); err != nil {
		t.Fatal(err)
	}
	_, root, err := snapshot(source, temp, nil, expected)
	if root != "" {
		_ = cleanup(root)
	}
	if err == nil {
		t.Fatal("source drift accepted after trusted identity captured")
	}
}
