package testgenvalidate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	coverage "unit-test-ide.local/test-service/internal/coveragemodel/v1"
	"unit-test-ide.local/test-service/internal/testgenrender"
)

const testID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func digestTest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func coverageFixture(functions, lines, branches int64) []byte {
	return []byte(fmt.Sprintf(`{"schemaVersion":"1.0","provenance":{"platform":"windows","architecture":"x64","compiler":{"family":"clang-cl","version":"22.1.8"},"driver":{"name":"llvm-cov","version":"22.1.8"},"collector":{"name":"llvm-cov","version":"22.1.8"},"normalizerVersion":"1","instrumentationFingerprint":"%s"},"completeness":{"outcome":"available","reasons":[]},"summary":{"functions":{"covered":%d,"total":1},"lines":{"covered":%d,"total":1},"branches":{"covered":%d,"total":1}},"files":[{"uri":"source.c","sha256":"%s","summary":{"functions":{"covered":%d,"total":1},"lines":{"covered":%d,"total":1},"branches":{"covered":%d,"total":1}},"lines":[{"line":1,"count":%d,"branches":{"covered":%d,"total":1}}]}]}`, testID, functions, lines, branches, testID, functions, lines, branches, lines, branches))
}

func twoFileCoverage(targetCovered, otherCovered int64) []byte {
	file := func(uri, sha string, covered int64) coverage.CoverageFileV1 {
		return coverage.CoverageFileV1{URI: uri, Sha256: sha, Summary: coverage.CoverageSummaryV1{Lines: coverage.CoverageMetricV1{Covered: covered, Total: 1}}, Lines: []coverage.CoverageLineV1{{Line: 1, Count: covered}}}
	}
	value := coverage.CoverageDocumentV1{SchemaVersion: coverage.The10, Completeness: coverage.CoverageCompletenessV1{Outcome: coverage.Available, Reasons: []coverage.Reason{}}, Provenance: coverage.CoverageProvenanceV1{Platform: coverage.Windows, Architecture: coverage.X64, Compiler: coverage.CoverageCompilerV1{Family: coverage.ClangCl, Version: "22.1.8"}, Driver: coverage.CoverageDriverV1{Name: coverage.FluffyLlvmCov, Version: "22.1.8"}, Collector: coverage.CoverageCollectorV1{Name: coverage.PurpleLlvmCov, Version: "22.1.8"}, NormalizerVersion: "1", InstrumentationFingerprint: testID}, Summary: coverage.CoverageSummaryV1{Lines: coverage.CoverageMetricV1{Covered: targetCovered + otherCovered, Total: 2}}, Files: []coverage.CoverageFileV1{file("source.c", testID, targetCovered), file("unrelated.c", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", otherCovered)}}
	data, _ := json.Marshal(value)
	return data
}

func sameFileCoverage(targetLine, otherLine, functionCovered, otherBranch int64) []byte {
	line := func(number, count, branches int64) coverage.CoverageLineV1 {
		return coverage.CoverageLineV1{Line: number, Count: count, Branches: coverage.CoverageMetricV1{Covered: branches, Total: number - 1}}
	}
	file := coverage.CoverageFileV1{URI: "source.c", Sha256: testID, Lines: []coverage.CoverageLineV1{line(1, targetLine, 0), line(2, otherLine, otherBranch)}, Summary: coverage.CoverageSummaryV1{Functions: coverage.CoverageMetricV1{Covered: functionCovered, Total: 2}, Lines: coverage.CoverageMetricV1{Covered: targetLine + otherLine, Total: 2}, Branches: coverage.CoverageMetricV1{Covered: otherBranch, Total: 1}}}
	value := coverage.CoverageDocumentV1{SchemaVersion: coverage.The10, Completeness: coverage.CoverageCompletenessV1{Outcome: coverage.Available, Reasons: []coverage.Reason{}}, Provenance: coverage.CoverageProvenanceV1{Platform: coverage.Windows, Architecture: coverage.X64, Compiler: coverage.CoverageCompilerV1{Family: coverage.ClangCl, Version: "22.1.8"}, Driver: coverage.CoverageDriverV1{Name: coverage.FluffyLlvmCov, Version: "22.1.8"}, Collector: coverage.CoverageCollectorV1{Name: coverage.PurpleLlvmCov, Version: "22.1.8"}, NormalizerVersion: "1", InstrumentationFingerprint: testID}, Summary: file.Summary, Files: []coverage.CoverageFileV1{file}}
	data, _ := json.Marshal(value)
	return data
}

func TestSameFileUnrelatedGainsCannotRetainTarget(t *testing.T) {
	baseline := sameFileCoverage(0, 0, 0, 0)
	for _, tc := range []struct {
		name     string
		after    []byte
		metrics  Metrics
		retained bool
	}{
		{"other-function", sameFileCoverage(0, 0, 1, 0), Metrics{Functions: true}, false},
		{"other-line", sameFileCoverage(0, 1, 0, 0), Metrics{Lines: true}, false},
		{"other-branch", sameFileCoverage(0, 0, 0, 1), Metrics{Branches: true}, false},
		{"target-line", sameFileCoverage(1, 0, 0, 0), Metrics{Lines: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := t.TempDir()
			_ = os.WriteFile(filepath.Join(source, "source.c"), []byte("source"), 0600)
			v := Validator{Config: Config{SourceRoot: source, TempRoot: t.TempDir(), Planner: &fixturePlanner{coverage: tc.after}, VerifyEvidence: trustedFixtureProof, ResolveCandidate: fixtureResolver(source, baseline)}}
			result, err := v.Validate(context.Background(), ValidationRequest{TaskID: testID, CandidateID: testID, BaselineCoverage: baseline, Metrics: tc.metrics, Assertion: AssertionEvidence{Kind: Verified, IndependentProofDigest: testID}})
			if err != nil || result.Retained != tc.retained {
				t.Fatalf("wrong target-symbol retention: %+v %v", result, err)
			}
			if !tc.retained && result.Diagnostic != DiagnosticNoDelta {
				t.Fatalf("wrong diagnostic: %+v", result)
			}
		})
	}
}

func TestValidateRequiresTargetSpecificGain(t *testing.T) {
	for _, tc := range []struct {
		name            string
		baseline, after []byte
		want            Diagnostic
	}{
		{"unrelated-only", twoFileCoverage(0, 0), twoFileCoverage(0, 1), DiagnosticNoDelta},
		{"target-regression", twoFileCoverage(1, 0), twoFileCoverage(0, 1), DiagnosticRegression},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := t.TempDir()
			_ = os.WriteFile(filepath.Join(source, "source.c"), []byte("source"), 0600)
			v := Validator{Config: Config{SourceRoot: source, TempRoot: t.TempDir(), Planner: &fixturePlanner{coverage: tc.after}, VerifyEvidence: trustedFixtureProof, ResolveCandidate: fixtureResolver(source, tc.baseline)}}
			result, err := v.Validate(context.Background(), ValidationRequest{TaskID: testID, CandidateID: testID, BaselineCoverage: tc.baseline, Metrics: Metrics{Lines: true}, Assertion: AssertionEvidence{Kind: Verified, IndependentProofDigest: testID}})
			if err != nil || result.Retained || result.Diagnostic != tc.want {
				t.Fatalf("target-only check failed: %+v %v", result, err)
			}
		})
	}
}

func TestValidateOrderedStagesAndReceipts(t *testing.T) {
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "tests"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "tests", "CMakeLists.txt"), []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	planner := &fixturePlanner{coverage: coverageFixture(0, 1, 0)}
	v := Validator{Config: Config{SourceRoot: source, TempRoot: t.TempDir(), Planner: planner, VerifyEvidence: trustedFixtureProof, ResolveCandidate: fixtureResolver(source, coverageFixture(0, 0, 0))}}
	r := ValidationRequest{TaskID: testID, CandidateID: testID, Edits: testgenrender.StagedEditSet{Files: []testgenrender.StagedFile{{Path: "tests/generated/test.c", Content: []byte("test\n"), AfterDigest: digestTest([]byte("test\n"))}, {Path: "tests/CMakeLists.txt", Content: []byte("after\n"), BeforeDigest: digestTest([]byte("before\n")), AfterDigest: digestTest([]byte("after\n"))}}}, BaselineCoverage: coverageFixture(0, 0, 0), Metrics: Metrics{Lines: true}, Assertion: AssertionEvidence{Kind: Verified, IndependentProofDigest: testID}}
	result, err := v.Validate(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Retained || result.Diagnostic != DiagnosticNone {
		t.Fatalf("not retained: %+v", result)
	}
	if !reflect.DeepEqual(planner.stages, []Stage{StageConfigure, StageCompile, StageDiscover, StageCandidate, StageSuite, StageCoverage}) {
		t.Fatalf("stage order: %v", planner.stages)
	}
	if len(result.Receipts) != 6 {
		t.Fatalf("receipts: %+v", result.Receipts)
	}
	for _, receipt := range result.Receipts {
		if len(receipt.Digest) != 64 {
			t.Fatalf("receipt digest missing: %+v", receipt)
		}
	}
	content, err := os.ReadFile(filepath.Join(source, "tests", "CMakeLists.txt"))
	if err != nil || string(content) != "before\n" {
		t.Fatalf("source changed: %s %v", content, err)
	}
}

type fixturePlanner struct {
	stages   []Stage
	fail     Stage
	coverage []byte
	mutate   func(Stage, Roots)
}

func (p *fixturePlanner) Execute(_ context.Context, stage Stage, roots Roots) (StageEvidence, error) {
	p.stages = append(p.stages, stage)
	if p.mutate != nil {
		p.mutate(stage, roots)
	}
	if stage == p.fail {
		return StageEvidence{ExitCode: 1}, nil
	}
	if stage == StageDiscover {
		return StageEvidence{DiscoveredCaseIDs: []string{testID}}, nil
	}
	if stage == StageCoverage {
		return StageEvidence{CoverageJSON: p.coverage}, nil
	}
	return StageEvidence{}, nil
}

func TestValidateRejectsSourceMutation(t *testing.T) {
	source := t.TempDir()
	path := filepath.Join(source, "source.c")
	if err := os.WriteFile(path, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	planner := &fixturePlanner{coverage: coverageFixture(1, 1, 0), mutate: func(stage Stage, _ Roots) {
		if stage == StageCompile {
			_ = os.WriteFile(path, []byte("changed"), 0600)
		}
	}}
	v := Validator{Config: Config{SourceRoot: source, TempRoot: t.TempDir(), Planner: planner, VerifyEvidence: trustedFixtureProof, ResolveCandidate: fixtureResolver(source, coverageFixture(0, 1, 0))}}
	result, err := v.Validate(context.Background(), ValidationRequest{TaskID: testID, CandidateID: testID, BaselineCoverage: coverageFixture(0, 1, 0), Metrics: Metrics{Functions: true}, Assertion: AssertionEvidence{Kind: Verified, IndependentProofDigest: testID}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Retained || result.Diagnostic != DiagnosticIsolation || len(planner.stages) != 2 {
		t.Fatalf("source mutation not detected immediately: %+v, %v", result, planner.stages)
	}
}

func TestValidateRejectsStagedSnapshotMutation(t *testing.T) {
	source := t.TempDir()
	_ = os.WriteFile(filepath.Join(source, "source.c"), []byte("source"), 0600)
	planner := &fixturePlanner{coverage: coverageFixture(1, 1, 0), mutate: func(stage Stage, roots Roots) {
		if stage == StageCompile {
			path := filepath.Join(roots.Source, "source.c")
			if err := os.Chmod(path, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("forged"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}}
	v := Validator{Config: Config{SourceRoot: source, TempRoot: t.TempDir(), Planner: planner, VerifyEvidence: trustedFixtureProof, ResolveCandidate: fixtureResolver(source, coverageFixture(0, 1, 0))}}
	result, err := v.Validate(context.Background(), ValidationRequest{TaskID: testID, CandidateID: testID, BaselineCoverage: coverageFixture(0, 1, 0), Metrics: Metrics{Functions: true}, Assertion: AssertionEvidence{Kind: Verified, IndependentProofDigest: testID}})
	if err != nil || result.Retained || result.Diagnostic != DiagnosticIsolation {
		t.Fatalf("snapshot mutation accepted: %+v %v", result, err)
	}
}

func TestValidateRejectsFalseProgressAndUnprovenAssertion(t *testing.T) {
	for _, tc := range []struct {
		name     string
		coverage []byte
		metrics  Metrics
		proof    bool
		want     Diagnostic
	}{
		{"no-delta", coverageFixture(0, 1, 0), Metrics{Functions: true}, true, DiagnosticNoDelta},
		{"wrong-metric", coverageFixture(1, 1, 0), Metrics{Branches: true}, true, DiagnosticNoDelta},
		{"unproven", coverageFixture(1, 1, 0), Metrics{Functions: true}, false, DiagnosticAssertion},
		{"malformed-profile", []byte(`{}`), Metrics{Functions: true}, true, DiagnosticCoverage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := t.TempDir()
			temp := t.TempDir()
			_ = os.WriteFile(filepath.Join(source, "source.c"), []byte("source"), 0600)
			verifier := trustedFixtureProof
			if !tc.proof {
				verifier = func(context.Context, string, AssertionEvidence) bool { return false }
			}
			v := Validator{Config: Config{SourceRoot: source, TempRoot: temp, Planner: &fixturePlanner{coverage: tc.coverage}, VerifyEvidence: verifier, ResolveCandidate: fixtureResolver(source, coverageFixture(0, 1, 0))}}
			result, err := v.Validate(context.Background(), ValidationRequest{TaskID: testID, CandidateID: testID, BaselineCoverage: coverageFixture(0, 1, 0), Metrics: tc.metrics, Assertion: AssertionEvidence{Kind: Verified, IndependentProofDigest: testID}})
			if err != nil || result.Retained || result.Diagnostic != tc.want {
				t.Fatalf("false progress: %+v %v", result, err)
			}
		})
	}
}

func TestValidateStopsOnStageFailure(t *testing.T) {
	for _, failed := range []Stage{StageConfigure, StageCompile, StageDiscover, StageCandidate, StageSuite, StageCoverage} {
		t.Run(string(failed), func(t *testing.T) {
			source := t.TempDir()
			if err := os.WriteFile(filepath.Join(source, "source.c"), []byte("source"), 0600); err != nil {
				t.Fatal(err)
			}
			planner := &fixturePlanner{fail: failed, coverage: coverageFixture(1, 1, 0)}
			v := Validator{Config: Config{SourceRoot: source, TempRoot: t.TempDir(), Planner: planner, VerifyEvidence: trustedFixtureProof, ResolveCandidate: fixtureResolver(source, coverageFixture(0, 1, 0))}}
			result, err := v.Validate(context.Background(), ValidationRequest{TaskID: testID, CandidateID: testID, BaselineCoverage: coverageFixture(0, 1, 0), Metrics: Metrics{Functions: true}, Assertion: AssertionEvidence{Kind: Verified, IndependentProofDigest: testID}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Retained || result.Diagnostic != DiagnosticStageFailed || planner.stages[len(planner.stages)-1] != failed {
				t.Fatalf("failed stage accepted: %+v %v", result, planner.stages)
			}
		})
	}
}

func trustedFixtureProof(_ context.Context, _ string, evidence AssertionEvidence) bool {
	return evidence.Kind == Verified && evidence.IndependentProofDigest == testID
}

func fixtureResolver(source string, baseline []byte) CandidateResolver {
	return func(context.Context, string) (ResolvedCandidate, error) {
		_, fingerprint, _ := sourceFingerprint(source)
		return ResolvedCandidate{ID: testID, Kind: Verified, TargetSymbol: testID, TargetFileURI: "source.c", TargetLines: []int64{1}, BaselineSHA256: digestTest(baseline), SourceSnapshotDigest: fingerprint, AssertionDigest: testID}, nil
	}
}

func TestValidateRejectsAuthoritativeKindMismatch(t *testing.T) {
	source := t.TempDir()
	_ = os.WriteFile(filepath.Join(source, "source.c"), []byte("source"), 0600)
	baseline := coverageFixture(0, 1, 0)
	v := Validator{Config: Config{SourceRoot: source, TempRoot: t.TempDir(), Planner: &fixturePlanner{coverage: coverageFixture(1, 1, 0)}, VerifyEvidence: trustedFixtureProof, ResolveCandidate: func(context.Context, string) (ResolvedCandidate, error) {
		return ResolvedCandidate{ID: testID, Kind: Characterization, TargetSymbol: testID, TargetFileURI: "source.c", BaselineSHA256: digestTest(baseline), AssertionDigest: testID}, nil
	}}}
	result, err := v.Validate(context.Background(), ValidationRequest{TaskID: testID, CandidateID: testID, BaselineCoverage: baseline, Metrics: Metrics{Functions: true}, Assertion: AssertionEvidence{Kind: Verified, IndependentProofDigest: testID}})
	if err != nil || result.Retained || result.Diagnostic != DiagnosticAssertion {
		t.Fatalf("kind mismatch accepted: %+v %v", result, err)
	}
}

func TestValidateRejectsSourceSnapshotIdentityMismatch(t *testing.T) {
	source := t.TempDir()
	_ = os.WriteFile(filepath.Join(source, "source.c"), []byte("source"), 0600)
	baseline := coverageFixture(0, 1, 0)
	v := Validator{Config: Config{SourceRoot: source, TempRoot: t.TempDir(), Planner: &fixturePlanner{coverage: coverageFixture(1, 1, 0)}, VerifyEvidence: trustedFixtureProof, ResolveCandidate: func(context.Context, string) (ResolvedCandidate, error) {
		return ResolvedCandidate{ID: testID, Kind: Verified, TargetSymbol: testID, TargetFileURI: "source.c", TargetLines: []int64{1}, BaselineSHA256: digestTest(baseline), SourceSnapshotDigest: testID, AssertionDigest: testID}, nil
	}}}
	result, err := v.Validate(context.Background(), ValidationRequest{TaskID: testID, CandidateID: testID, BaselineCoverage: baseline, Metrics: Metrics{Functions: true}, Assertion: AssertionEvidence{Kind: Verified, IndependentProofDigest: testID}})
	if err != nil || result.Retained || result.Diagnostic != DiagnosticIsolation {
		t.Fatalf("stale source snapshot accepted: %+v %v", result, err)
	}
}
