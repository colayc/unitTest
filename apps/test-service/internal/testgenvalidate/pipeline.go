package testgenvalidate

import (
	"context"
	"encoding/json"
	"math"
	"reflect"

	coverage "unit-test-ide.local/test-service/internal/coveragemodel/v1"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

const maxStageOutput = 256 << 10

func decodeCoverage(data []byte) (coverage.CoverageDocumentV1, error) {
	if len(data) == 0 || len(data) > 32<<20 {
		return coverage.CoverageDocumentV1{}, coverage.ErrInvalidDocument
	}
	value, err := coverage.Decode(data)
	if err != nil || value.Completeness.Outcome != coverage.Available {
		return coverage.CoverageDocumentV1{}, coverage.ErrInvalidDocument
	}
	return value, nil
}

func (v Validator) runStages(ctx context.Context, r ValidationRequest, roots Roots, original, staged map[string]string, target ResolvedCandidate) (ValidationResult, error) {
	result := ValidationResult{Receipts: make([]StageReceipt, 0, len(stageOrder))}
	for _, stage := range stageOrder {
		proofDigest := ""
		if ctx.Err() != nil {
			result.Diagnostic = DiagnosticStageFailed
			return result, nil
		}
		evidence, err := v.Config.Planner.Execute(ctx, stage, roots)
		if current, _, sourceErr := sourceFingerprint(v.Config.SourceRoot); sourceErr != nil || !reflect.DeepEqual(original, current) {
			result.Diagnostic = DiagnosticIsolation
			return result, nil
		}
		if current, _, snapshotErr := sourceFingerprint(roots.Source); snapshotErr != nil || !reflect.DeepEqual(staged, current) {
			result.Diagnostic = DiagnosticIsolation
			return result, nil
		}
		if err != nil || evidence.ExitCode != 0 || len(evidence.Output) > maxStageOutput {
			result.Diagnostic = DiagnosticStageFailed
			return result, nil
		}
		if stage != StageDiscover && len(evidence.DiscoveredCaseIDs) != 0 ||
			stage != StageCoverage && (len(evidence.CoverageJSON) != 0 || len(evidence.CoverageDetailJSON) != 0) ||
			len(evidence.CoverageDetailJSON) > 16<<20 {
			result.Diagnostic = DiagnosticStageFailed
			return result, nil
		}
		if stage == StageDiscover {
			if len(evidence.DiscoveredCaseIDs) != 1 || evidence.DiscoveredCaseIDs[0] != r.CandidateID {
				result.Diagnostic = DiagnosticNoDiscovery
				return result, nil
			}
		}
		if stage == StageCoverage {
			baseline, err := decodeCoverage(r.BaselineCoverage)
			if err != nil {
				result.Diagnostic = DiagnosticCoverage
				return result, nil
			}
			candidate, err := decodeCoverage(evidence.CoverageJSON)
			if err != nil {
				result.Diagnostic = DiagnosticCoverage
				return result, nil
			}
			var proof *TargetCoverageProof
			if (r.Metrics.Functions || r.Metrics.Branches) && v.Config.ResolveCoverage != nil {
				resolvedProof, proofErr := v.Config.ResolveCoverage(ctx, target, r.BaselineCoverage, evidence.CoverageJSON)
				if proofErr != nil {
					result.Diagnostic = DiagnosticCoverage
					return result, nil
				}
				proof = &resolvedProof
				proofDigest = resolvedProof.ContentDigest
			}
			delta, diagnostic := compareCoverage(ctx, baseline, candidate, r.Metrics, target, proof, digestBytes(r.BaselineCoverage), digestBytes(evidence.CoverageJSON))
			if diagnostic != DiagnosticNone {
				result.Diagnostic = diagnostic
				return result, nil
			}
			result.Delta = delta
			result.BaselinePercent = coveragePercent(baseline.Summary)
			candidatePercent := coveragePercent(candidate.Summary)
			result.DeltaPercent = testgendomain.CoveragePercent{
				FunctionPercent: boundedPercent(candidatePercent.FunctionPercent - result.BaselinePercent.FunctionPercent),
				LinePercent:     boundedPercent(candidatePercent.LinePercent - result.BaselinePercent.LinePercent),
				BranchPercent:   boundedPercent(candidatePercent.BranchPercent - result.BaselinePercent.BranchPercent),
			}
		}
		receipt := struct {
			Stage                Stage
			CandidateID          string
			TargetSymbol         string
			TargetFileURI        string
			TargetLines          []int64
			BaselineDigest       string
			SnapshotDigest       string
			OutputDigest         string
			CoverageDigest       string
			CoverageDetailDigest string
			IdentityProofDigest  string
			Discovery            []string
		}{stage, r.CandidateID, target.TargetSymbol, target.TargetFileURI, target.TargetLines, target.BaselineSHA256, roots.SnapshotDigest, digestBytes(evidence.Output), digestBytes(evidence.CoverageJSON), digestBytes(evidence.CoverageDetailJSON), proofDigest, evidence.DiscoveredCaseIDs}
		encoded, _ := json.Marshal(receipt)
		result.Receipts = append(result.Receipts, StageReceipt{Stage: stage, Digest: digestBytes(encoded), OutputDigest: receipt.OutputDigest, CoverageDigest: receipt.CoverageDigest})
	}
	result.Retained = true
	return result, nil
}

func boundedPercent(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func coveragePercent(summary coverage.CoverageSummaryV1) testgendomain.CoveragePercent {
	percent := func(covered, total int64) float64 {
		if covered <= 0 || total <= 0 {
			return 0
		}
		return boundedPercent(float64(covered) * 100 / float64(total))
	}
	return testgendomain.CoveragePercent{
		FunctionPercent: percent(summary.Functions.Covered, summary.Functions.Total),
		LinePercent:     percent(summary.Lines.Covered, summary.Lines.Total),
		BranchPercent:   percent(summary.Branches.Covered, summary.Branches.Total),
	}
}

func compareCoverage(ctx context.Context, before, after coverage.CoverageDocumentV1, selected Metrics, target ResolvedCandidate, proof *TargetCoverageProof, baselineDigest, candidateDigest string) (CoverageDelta, Diagnostic) {
	if ctx.Err() != nil {
		return CoverageDelta{}, DiagnosticStageFailed
	}
	if !reflect.DeepEqual(before.Provenance, after.Provenance) || len(before.Files) != len(after.Files) {
		return CoverageDelta{}, DiagnosticCoverage
	}
	var targetDelta CoverageDelta
	foundTarget := false
	for i := range before.Files {
		if ctx.Err() != nil {
			return CoverageDelta{}, DiagnosticStageFailed
		}
		b, a := before.Files[i], after.Files[i]
		if b.URI != a.URI || b.Sha256 != a.Sha256 || b.Summary.Functions.Total != a.Summary.Functions.Total || b.Summary.Lines.Total != a.Summary.Lines.Total || b.Summary.Branches.Total != a.Summary.Branches.Total || len(b.Lines) != len(a.Lines) {
			return CoverageDelta{}, DiagnosticCoverage
		}
		if a.Summary.Functions.Covered < b.Summary.Functions.Covered || a.Summary.Lines.Covered < b.Summary.Lines.Covered || a.Summary.Branches.Covered < b.Summary.Branches.Covered {
			return CoverageDelta{}, DiagnosticRegression
		}
		for j := range b.Lines {
			if j&255 == 0 && ctx.Err() != nil {
				return CoverageDelta{}, DiagnosticStageFailed
			}
			if b.Lines[j].Line != a.Lines[j].Line || b.Lines[j].Branches.Total != a.Lines[j].Branches.Total {
				return CoverageDelta{}, DiagnosticCoverage
			}
			if b.Lines[j].Count > 0 && a.Lines[j].Count == 0 || a.Lines[j].Branches.Covered < b.Lines[j].Branches.Covered {
				return CoverageDelta{}, DiagnosticRegression
			}
		}
		if b.URI == target.TargetFileURI {
			foundTarget = true
			lineIndex := 0
			for targetIndex, targetLine := range target.TargetLines {
				if targetIndex&255 == 0 && ctx.Err() != nil {
					return CoverageDelta{}, DiagnosticStageFailed
				}
				for lineIndex < len(b.Lines) && b.Lines[lineIndex].Line < targetLine {
					lineIndex++
					if lineIndex&255 == 0 && ctx.Err() != nil {
						return CoverageDelta{}, DiagnosticStageFailed
					}
				}
				if lineIndex == len(b.Lines) || b.Lines[lineIndex].Line != targetLine {
					return CoverageDelta{}, DiagnosticCoverage
				}
				if b.Lines[lineIndex].Count == 0 && a.Lines[lineIndex].Count > 0 {
					targetDelta.Lines++
				}
			}
		}
	}
	if !foundTarget {
		return CoverageDelta{}, DiagnosticCoverage
	}
	b, a := before.Summary, after.Summary
	if b.Functions.Total != a.Functions.Total || b.Lines.Total != a.Lines.Total || b.Branches.Total != a.Branches.Total {
		return CoverageDelta{}, DiagnosticCoverage
	}
	if a.Functions.Covered < b.Functions.Covered || a.Lines.Covered < b.Lines.Covered || a.Branches.Covered < b.Branches.Covered {
		return CoverageDelta{}, DiagnosticRegression
	}
	identityDelta, diagnostic := proofDelta(ctx, before, after, target, proof, baselineDigest, candidateDigest)
	if diagnostic != DiagnosticNone {
		return CoverageDelta{}, diagnostic
	}
	targetDelta.Functions = identityDelta.Functions
	targetDelta.Branches = identityDelta.Branches
	if (!selected.Functions || targetDelta.Functions == 0) && (!selected.Lines || targetDelta.Lines == 0) && (!selected.Branches || targetDelta.Branches == 0) {
		return CoverageDelta{}, DiagnosticNoDelta
	}
	return targetDelta, DiagnosticNone
}
