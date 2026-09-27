package testgenvalidate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	coverage "unit-test-ide.local/test-service/internal/coveragemodel/v1"
)

const otherID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func identityCoverage(targetLine, otherLine, targetFunction, otherFunction, targetBranch, otherBranch int64) []byte {
	file := coverage.CoverageFileV1{URI: "source.c", Sha256: testID, Summary: coverage.CoverageSummaryV1{Functions: coverage.CoverageMetricV1{Covered: targetFunction + otherFunction, Total: 2}, Lines: coverage.CoverageMetricV1{Covered: targetLine + otherLine, Total: 2}, Branches: coverage.CoverageMetricV1{Covered: targetBranch + otherBranch, Total: 2}}, Lines: []coverage.CoverageLineV1{{Line: 1, Count: targetLine, Branches: coverage.CoverageMetricV1{Covered: targetBranch, Total: 1}}, {Line: 2, Count: otherLine, Branches: coverage.CoverageMetricV1{Covered: otherBranch, Total: 1}}}}
	value := coverage.CoverageDocumentV1{SchemaVersion: coverage.The10, Completeness: coverage.CoverageCompletenessV1{Outcome: coverage.Available, Reasons: []coverage.Reason{}}, Provenance: coverage.CoverageProvenanceV1{Platform: coverage.Windows, Architecture: coverage.X64, Compiler: coverage.CoverageCompilerV1{Family: coverage.ClangCl, Version: "22.1.8"}, Driver: coverage.CoverageDriverV1{Name: coverage.FluffyLlvmCov, Version: "22.1.8"}, Collector: coverage.CoverageCollectorV1{Name: coverage.PurpleLlvmCov, Version: "22.1.8"}, NormalizerVersion: "1", InstrumentationFingerprint: testID}, Summary: file.Summary, Files: []coverage.CoverageFileV1{file}}
	data, _ := json.Marshal(value)
	return data
}

func TestTrustedIdentityProofAllowsOnlyTargetFunctionOrBranchGain(t *testing.T) {
	baseline := identityCoverage(0, 0, 0, 0, 0, 0)
	for _, tc := range []struct {
		name                                                     string
		after                                                    []byte
		metric                                                   Metrics
		targetFunction, otherFunction, targetBranch, otherBranch bool
		proof                                                    bool
		retained                                                 bool
	}{
		{"target-function", identityCoverage(0, 0, 1, 0, 0, 0), Metrics{Functions: true}, true, false, false, false, true, true},
		{"other-function", identityCoverage(0, 0, 0, 1, 0, 0), Metrics{Functions: true}, false, true, false, false, true, false},
		{"target-branch", identityCoverage(0, 0, 0, 0, 1, 0), Metrics{Branches: true}, false, false, true, false, true, true},
		{"other-branch", identityCoverage(0, 0, 0, 0, 0, 1), Metrics{Branches: true}, false, false, false, true, true, false},
		{"missing-proof", identityCoverage(0, 0, 1, 0, 0, 0), Metrics{Functions: true}, true, false, false, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := t.TempDir()
			if err := os.WriteFile(filepath.Join(source, "source.c"), []byte("source"), 0600); err != nil {
				t.Fatal(err)
			}
			base := fixtureResolver(source, baseline)
			resolve := func(ctx context.Context, id string) (ResolvedCandidate, error) {
				candidate, err := base(ctx, id)
				candidate.TargetFunctionIDs = []string{testID}
				candidate.TargetBranchIDs = []string{testID}
				return candidate, err
			}
			config := Config{SourceRoot: source, TempRoot: t.TempDir(), Planner: &fixturePlanner{coverage: tc.after}, VerifyEvidence: trustedFixtureProof, ResolveCandidate: resolve}
			if tc.proof {
				config.ResolveCoverage = func(_ context.Context, target ResolvedCandidate, before, after []byte) (TargetCoverageProof, error) {
					return TargetCoverageProof{TargetSymbol: target.TargetSymbol, TargetFileURI: target.TargetFileURI, BaselineSHA256: digestTest(before), CandidateSHA256: digestTest(after), SourceSnapshotDigest: target.SourceSnapshotDigest, EvidenceDigest: testID,
						Functions: []CoverageIdentityChange{{FileURI: "source.c", ID: testID, Line: 1, BeforeCovered: false, AfterCovered: tc.targetFunction}, {FileURI: "source.c", ID: otherID, Line: 2, BeforeCovered: false, AfterCovered: tc.otherFunction}},
						Branches:  []CoverageIdentityChange{{FileURI: "source.c", ID: testID, Line: 1, BeforeCovered: false, AfterCovered: tc.targetBranch}, {FileURI: "source.c", ID: otherID, Line: 2, BeforeCovered: false, AfterCovered: tc.otherBranch}}}, nil
				}
			}
			result, err := (Validator{Config: config}).Validate(context.Background(), ValidationRequest{TaskID: testID, CandidateID: testID, BaselineCoverage: baseline, Metrics: tc.metric, Assertion: AssertionEvidence{Kind: Verified, IndependentProofDigest: testID}})
			if err != nil || result.Retained != tc.retained {
				t.Fatalf("identity proof result: %+v %v", result, err)
			}
			if !tc.retained && result.Diagnostic != DiagnosticNoDelta {
				t.Fatalf("unexpected rejection: %+v", result)
			}
		})
	}
}
