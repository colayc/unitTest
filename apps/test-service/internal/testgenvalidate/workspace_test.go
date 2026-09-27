package testgenvalidate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"unit-test-ide.local/test-service/internal/testgenrender"
)

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
			v := Validator{Config: Config{SourceRoot: source, TempRoot: t.TempDir(), Planner: planner, VerifyEvidence: trustedFixtureProof}}
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
		v := Validator{Config: Config{SourceRoot: source, TempRoot: t.TempDir(), Planner: planner, VerifyEvidence: trustedFixtureProof}}
		result, err := v.Validate(context.Background(), ValidationRequest{TaskID: testID, CandidateID: testID, Edits: testgenrender.StagedEditSet{Files: []testgenrender.StagedFile{edit}}, BaselineCoverage: coverageFixture(0, 1, 0), Metrics: Metrics{Functions: true}, Assertion: AssertionEvidence{Kind: Verified, IndependentProofDigest: testID}})
		if err != nil || result.Diagnostic != DiagnosticIsolation || len(planner.stages) != 0 {
			t.Fatalf("unsafe edit accepted: %+v %v", result, err)
		}
	}
}
