package testgenvalidate

import (
	"context"
	"encoding/json"
	"reflect"

	coverage "unit-test-ide.local/test-service/internal/coveragemodel/v1"
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
		if stage != StageDiscover && len(evidence.DiscoveredCaseIDs) != 0 || stage != StageCoverage && len(evidence.CoverageJSON) != 0 {
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
			delta, diagnostic := compareCoverage(baseline, candidate, r.Metrics, target)
			if diagnostic != DiagnosticNone {
				result.Diagnostic = diagnostic
				return result, nil
			}
			result.Delta = delta
		}
		receipt := struct {
			Stage          Stage
			CandidateID    string
			TargetSymbol   string
			TargetFileURI  string
			TargetLines    []int64
			BaselineDigest string
			SnapshotDigest string
			OutputDigest   string
			CoverageDigest string
			Discovery      []string
		}{stage, r.CandidateID, target.TargetSymbol, target.TargetFileURI, target.TargetLines, target.BaselineSHA256, roots.SnapshotDigest, digestBytes(evidence.Output), digestBytes(evidence.CoverageJSON), evidence.DiscoveredCaseIDs}
		encoded, _ := json.Marshal(receipt)
		result.Receipts = append(result.Receipts, StageReceipt{Stage: stage, Digest: digestBytes(encoded), OutputDigest: receipt.OutputDigest, CoverageDigest: receipt.CoverageDigest})
	}
	result.Retained = true
	return result, nil
}

func compareCoverage(before, after coverage.CoverageDocumentV1, selected Metrics, target ResolvedCandidate) (CoverageDelta, Diagnostic) {
	if !reflect.DeepEqual(before.Provenance, after.Provenance) || len(before.Files) != len(after.Files) {
		return CoverageDelta{}, DiagnosticCoverage
	}
	var targetDelta CoverageDelta
	foundTarget := false
	for i := range before.Files {
		b, a := before.Files[i], after.Files[i]
		if b.URI != a.URI || b.Sha256 != a.Sha256 || b.Summary.Functions.Total != a.Summary.Functions.Total || b.Summary.Lines.Total != a.Summary.Lines.Total || b.Summary.Branches.Total != a.Summary.Branches.Total || len(b.Lines) != len(a.Lines) {
			return CoverageDelta{}, DiagnosticCoverage
		}
		if a.Summary.Functions.Covered < b.Summary.Functions.Covered || a.Summary.Lines.Covered < b.Summary.Lines.Covered || a.Summary.Branches.Covered < b.Summary.Branches.Covered {
			return CoverageDelta{}, DiagnosticRegression
		}
		for j := range b.Lines {
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
			for _, targetLine := range target.TargetLines {
				for lineIndex < len(b.Lines) && b.Lines[lineIndex].Line < targetLine {
					lineIndex++
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
	// Coverage JSON v1 contains no stable function or branch IDs. File-level
	// increases cannot be attributed to TargetSymbol, so those metrics fail
	// closed until a richer trusted collector identity contract is available.
	if !selected.Lines || targetDelta.Lines == 0 {
		return CoverageDelta{}, DiagnosticNoDelta
	}
	return targetDelta, DiagnosticNone
}
