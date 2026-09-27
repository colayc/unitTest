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

func (v Validator) runStages(ctx context.Context, r ValidationRequest, roots Roots, original, staged map[string]string) (ValidationResult, error) {
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
			delta, diagnostic := compareCoverage(baseline, candidate, r.Metrics)
			if diagnostic != DiagnosticNone {
				result.Diagnostic = diagnostic
				return result, nil
			}
			result.Delta = delta
		}
		receipt := struct {
			Stage          Stage
			CandidateID    string
			SnapshotDigest string
			OutputDigest   string
			CoverageDigest string
			Discovery      []string
		}{stage, r.CandidateID, roots.SnapshotDigest, digestBytes(evidence.Output), digestBytes(evidence.CoverageJSON), evidence.DiscoveredCaseIDs}
		encoded, _ := json.Marshal(receipt)
		result.Receipts = append(result.Receipts, StageReceipt{Stage: stage, Digest: digestBytes(encoded), OutputDigest: receipt.OutputDigest, CoverageDigest: receipt.CoverageDigest})
	}
	result.Retained = true
	return result, nil
}

func compareCoverage(before, after coverage.CoverageDocumentV1, selected Metrics) (CoverageDelta, Diagnostic) {
	if !reflect.DeepEqual(before.Provenance, after.Provenance) || len(before.Files) != len(after.Files) {
		return CoverageDelta{}, DiagnosticCoverage
	}
	for i := range before.Files {
		if before.Files[i].URI != after.Files[i].URI || before.Files[i].Sha256 != after.Files[i].Sha256 || before.Files[i].Summary.Functions.Total != after.Files[i].Summary.Functions.Total || before.Files[i].Summary.Lines.Total != after.Files[i].Summary.Lines.Total || before.Files[i].Summary.Branches.Total != after.Files[i].Summary.Branches.Total {
			return CoverageDelta{}, DiagnosticCoverage
		}
	}
	b, a := before.Summary, after.Summary
	if b.Functions.Total != a.Functions.Total || b.Lines.Total != a.Lines.Total || b.Branches.Total != a.Branches.Total {
		return CoverageDelta{}, DiagnosticCoverage
	}
	if a.Functions.Covered < b.Functions.Covered || a.Lines.Covered < b.Lines.Covered || a.Branches.Covered < b.Branches.Covered {
		return CoverageDelta{}, DiagnosticRegression
	}
	delta := CoverageDelta{Functions: a.Functions.Covered - b.Functions.Covered, Lines: a.Lines.Covered - b.Lines.Covered, Branches: a.Branches.Covered - b.Branches.Covered}
	if !selected.Functions || delta.Functions == 0 {
		if !selected.Lines || delta.Lines == 0 {
			if !selected.Branches || delta.Branches == 0 {
				return CoverageDelta{}, DiagnosticNoDelta
			}
		}
	}
	return delta, DiagnosticNone
}
