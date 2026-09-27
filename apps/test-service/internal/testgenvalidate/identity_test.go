package testgenvalidate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	coverage "unit-test-ide.local/test-service/internal/coveragemodel/v1"
)

type cancelAfterChecks struct{ remaining int }

func (c *cancelAfterChecks) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cancelAfterChecks) Done() <-chan struct{}       { return nil }
func (c *cancelAfterChecks) Value(any) any               { return nil }
func (c *cancelAfterChecks) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

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
		tamper                                                   bool
	}{
		{"target-function", identityCoverage(0, 0, 1, 0, 0, 0), Metrics{Functions: true}, true, false, false, false, true, true, false},
		{"other-function", identityCoverage(0, 0, 0, 1, 0, 0), Metrics{Functions: true}, false, true, false, false, true, false, false},
		{"target-branch", identityCoverage(0, 0, 0, 0, 1, 0), Metrics{Branches: true}, false, false, true, false, true, true, false},
		{"other-branch", identityCoverage(0, 0, 0, 0, 0, 1), Metrics{Branches: true}, false, false, false, true, true, false, false},
		{"missing-proof", identityCoverage(0, 0, 1, 0, 0, 0), Metrics{Functions: true}, true, false, false, false, false, false, false},
		{"tampered-proof-digest", identityCoverage(0, 0, 1, 0, 0, 0), Metrics{Functions: true}, true, false, false, false, true, false, true},
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
					proof := TargetCoverageProof{TargetSymbol: target.TargetSymbol, TargetFileURI: target.TargetFileURI, BaselineSHA256: digestTest(before), CandidateSHA256: digestTest(after), SourceSnapshotDigest: target.SourceSnapshotDigest, EvidenceDigest: testID,
						Functions: []CoverageIdentityChange{{FileURI: "source.c", ID: testID, Line: 1, BeforeCovered: false, AfterCovered: tc.targetFunction}, {FileURI: "source.c", ID: otherID, Line: 2, BeforeCovered: false, AfterCovered: tc.otherFunction}},
						Branches:  []CoverageIdentityChange{{FileURI: "source.c", ID: testID, Line: 1, BeforeCovered: false, AfterCovered: tc.targetBranch}, {FileURI: "source.c", ID: otherID, Line: 2, BeforeCovered: false, AfterCovered: tc.otherBranch}}}
					proof = sealFixtureProof(proof)
					if tc.tamper {
						proof.EvidenceDigest = otherID
					}
					return proof, nil
				}
			}
			result, err := (Validator{Config: config}).Validate(context.Background(), ValidationRequest{TaskID: testID, CandidateID: testID, BaselineCoverage: baseline, Metrics: tc.metric, Assertion: AssertionEvidence{Kind: Verified, IndependentProofDigest: testID}})
			if err != nil || result.Retained != tc.retained {
				t.Fatalf("identity proof result: %+v %v", result, err)
			}
			want := DiagnosticNoDelta
			if tc.tamper {
				want = DiagnosticCoverage
			}
			if !tc.retained && result.Diagnostic != want {
				t.Fatalf("unexpected rejection: %+v", result)
			}
		})
	}
}

func sealFixtureProof(proof TargetCoverageProof) TargetCoverageProof {
	copy := proof
	copy.ContentDigest = ""
	encoded, _ := json.Marshal(copy)
	proof.ContentDigest = digestTest(encoded)
	return proof
}

func TestProofDeltaCancellationAndLinearBound(t *testing.T) {
	const n = 20000
	lines := make([]coverage.CoverageLineV1, n)
	branches := make([]CoverageIdentityChange, n)
	for i := range lines {
		lines[i] = coverage.CoverageLineV1{Line: int64(i + 1), Branches: coverage.CoverageMetricV1{Total: 1}}
		branches[i] = CoverageIdentityChange{FileURI: "source.c", ID: fmt.Sprintf("%064x", i+1), Line: int64(i + 1)}
	}
	file := coverage.CoverageFileV1{URI: "source.c", Lines: lines, Summary: coverage.CoverageSummaryV1{Lines: coverage.CoverageMetricV1{Total: n}, Branches: coverage.CoverageMetricV1{Total: n}}}
	doc := coverage.CoverageDocumentV1{Files: []coverage.CoverageFileV1{file}, Summary: file.Summary}
	target := ResolvedCandidate{TargetSymbol: testID, TargetFileURI: "source.c", TargetLines: []int64{1}, TargetBranchIDs: []string{branches[0].ID}, SourceSnapshotDigest: testID}
	proof := sealFixtureProof(TargetCoverageProof{TargetSymbol: target.TargetSymbol, TargetFileURI: target.TargetFileURI, BaselineSHA256: testID, CandidateSHA256: testID, SourceSnapshotDigest: testID, EvidenceDigest: testID, Branches: branches})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, diagnostic := proofDelta(canceled, doc, doc, target, &proof, testID, testID); diagnostic != DiagnosticStageFailed {
		t.Fatalf("canceled proof should fail immediately, got %s", diagnostic)
	}
	if _, diagnostic := proofDelta(&cancelAfterChecks{remaining: 8}, doc, doc, target, &proof, testID, testID); diagnostic != DiagnosticStageFailed {
		t.Fatalf("mid-scan cancellation should fail, got %s", diagnostic)
	}
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	start := time.Now()
	if _, diagnostic := proofDelta(ctx, doc, doc, target, &proof, testID, testID); diagnostic != DiagnosticNone {
		t.Fatalf("bounded proof rejected: %s", diagnostic)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("proof validation exceeded linear budget: %s", elapsed)
	}
}

func TestProofBindingRejectsChangedDigestTargetAndSnapshot(t *testing.T) {
	raw := identityCoverage(0, 0, 0, 0, 0, 0)
	doc, err := decodeCoverage(raw)
	if err != nil {
		t.Fatal(err)
	}
	target := ResolvedCandidate{TargetSymbol: testID, TargetFileURI: "source.c", TargetLines: []int64{1}, TargetFunctionIDs: []string{testID}, SourceSnapshotDigest: testID}
	base := sealFixtureProof(TargetCoverageProof{TargetSymbol: testID, TargetFileURI: "source.c", BaselineSHA256: testID, CandidateSHA256: testID, SourceSnapshotDigest: testID, EvidenceDigest: testID,
		Functions: []CoverageIdentityChange{{FileURI: "source.c", ID: testID, Line: 1}, {FileURI: "source.c", ID: otherID, Line: 2}},
		Branches:  []CoverageIdentityChange{{FileURI: "source.c", ID: testID, Line: 1}, {FileURI: "source.c", ID: otherID, Line: 2}}})
	for _, tc := range []struct {
		name   string
		change func(*TargetCoverageProof)
	}{
		{"content", func(p *TargetCoverageProof) { p.Functions[0].AfterCovered = true }},
		{"target", func(p *TargetCoverageProof) { p.TargetSymbol = otherID; *p = sealFixtureProof(*p) }},
		{"snapshot", func(p *TargetCoverageProof) { p.SourceSnapshotDigest = otherID; *p = sealFixtureProof(*p) }},
		{"baseline", func(p *TargetCoverageProof) { p.BaselineSHA256 = otherID; *p = sealFixtureProof(*p) }},
		{"candidate", func(p *TargetCoverageProof) { p.CandidateSHA256 = otherID; *p = sealFixtureProof(*p) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proof := base
			proof.Functions = append([]CoverageIdentityChange(nil), base.Functions...)
			proof.Branches = append([]CoverageIdentityChange(nil), base.Branches...)
			tc.change(&proof)
			if _, diagnostic := proofDelta(context.Background(), doc, doc, target, &proof, testID, testID); diagnostic != DiagnosticCoverage {
				t.Fatalf("changed binding accepted: %s", diagnostic)
			}
		})
	}
}
