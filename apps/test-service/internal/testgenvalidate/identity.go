package testgenvalidate

import (
	"context"
	"encoding/json"
	coverage "unit-test-ide.local/test-service/internal/coveragemodel/v1"
)

// CoverageIdentityChange is a stable native collector identity, before and
// after candidate execution. The resolver must enumerate every function and
// branch in each covered file, including unrelated ones, so regressions cannot
// be hidden by another identity's gain.
type CoverageIdentityChange struct {
	FileURI       string
	ID            string
	Line          int64
	BeforeCovered bool
	AfterCovered  bool
}

type TargetCoverageProof struct {
	TargetSymbol         string
	TargetFileURI        string
	BaselineSHA256       string
	CandidateSHA256      string
	SourceSnapshotDigest string
	EvidenceDigest       string
	ContentDigest        string
	Functions            []CoverageIdentityChange
	Branches             []CoverageIdentityChange
}

func validStableIDs(ids []string) bool {
	if len(ids) > 100000 {
		return false
	}
	previous := ""
	for _, id := range ids {
		if !validDigest(id) || id <= previous {
			return false
		}
		previous = id
	}
	return true
}

func proofDelta(ctx context.Context, before, after coverage.CoverageDocumentV1, target ResolvedCandidate, proof *TargetCoverageProof, baselineDigest, candidateDigest string) (CoverageDelta, Diagnostic) {
	if ctx.Err() != nil {
		return CoverageDelta{}, DiagnosticStageFailed
	}
	if proof == nil {
		return CoverageDelta{}, DiagnosticNone
	}
	if proof.TargetSymbol != target.TargetSymbol || proof.TargetFileURI != target.TargetFileURI || proof.BaselineSHA256 != baselineDigest || proof.CandidateSHA256 != candidateDigest || proof.SourceSnapshotDigest != target.SourceSnapshotDigest || !validDigest(proof.EvidenceDigest) || !validDigest(proof.ContentDigest) || len(proof.Functions) > 100000 || len(proof.Branches) > 100000 {
		return CoverageDelta{}, DiagnosticCoverage
	}
	sealed := *proof
	sealed.ContentDigest = ""
	encoded, err := json.Marshal(sealed)
	if err != nil || digestBytes(encoded) != proof.ContentDigest {
		return CoverageDelta{}, DiagnosticCoverage
	}
	if ctx.Err() != nil {
		return CoverageDelta{}, DiagnosticStageFailed
	}
	targetLines := make(map[int64]struct{}, len(target.TargetLines))
	for i, line := range target.TargetLines {
		if i&255 == 0 && ctx.Err() != nil {
			return CoverageDelta{}, DiagnosticStageFailed
		}
		targetLines[line] = struct{}{}
	}
	targetFunctions := make(map[string]struct{}, len(target.TargetFunctionIDs))
	for i, id := range target.TargetFunctionIDs {
		if i&255 == 0 && ctx.Err() != nil {
			return CoverageDelta{}, DiagnosticStageFailed
		}
		targetFunctions[id] = struct{}{}
	}
	targetBranches := make(map[string]struct{}, len(target.TargetBranchIDs))
	for i, id := range target.TargetBranchIDs {
		if i&255 == 0 && ctx.Err() != nil {
			return CoverageDelta{}, DiagnosticStageFailed
		}
		targetBranches[id] = struct{}{}
	}
	var delta CoverageDelta
	functionIndex, branchIndex, work := 0, 0, 0
	for fileIndex, file := range before.Files {
		if ctx.Err() != nil {
			return CoverageDelta{}, DiagnosticStageFailed
		}
		afterFile := after.Files[fileIndex]
		lineIndexes := make(map[int64]int, len(file.Lines))
		for i, line := range file.Lines {
			work++
			if work&255 == 0 && ctx.Err() != nil {
				return CoverageDelta{}, DiagnosticStageFailed
			}
			lineIndexes[line.Line] = i
		}
		type branchCounts struct{ total, before, after int64 }
		counts := make([]branchCounts, len(file.Lines))
		functionStart := functionIndex
		for functionIndex < len(proof.Functions) && proof.Functions[functionIndex].FileURI == file.URI {
			functionIndex++
			if functionIndex&255 == 0 && ctx.Err() != nil {
				return CoverageDelta{}, DiagnosticStageFailed
			}
		}
		branchStart := branchIndex
		for branchIndex < len(proof.Branches) && proof.Branches[branchIndex].FileURI == file.URI {
			branchIndex++
			if branchIndex&255 == 0 && ctx.Err() != nil {
				return CoverageDelta{}, DiagnosticStageFailed
			}
		}
		if int64(functionIndex-functionStart) != file.Summary.Functions.Total || int64(branchIndex-branchStart) != file.Summary.Branches.Total {
			return CoverageDelta{}, DiagnosticCoverage
		}
		beforeFunctions, afterFunctions := int64(0), int64(0)
		beforeBranches, afterBranches := int64(0), int64(0)
		previousID := ""
		for _, item := range proof.Functions[functionStart:functionIndex] {
			work++
			if work&255 == 0 && ctx.Err() != nil {
				return CoverageDelta{}, DiagnosticStageFailed
			}
			_, lineExists := lineIndexes[item.Line]
			if !validDigest(item.ID) || item.ID <= previousID || !lineExists {
				return CoverageDelta{}, DiagnosticCoverage
			}
			previousID = item.ID
			if item.BeforeCovered {
				beforeFunctions++
			}
			if item.AfterCovered {
				afterFunctions++
			}
			if item.BeforeCovered && !item.AfterCovered {
				return CoverageDelta{}, DiagnosticRegression
			}
			_, wanted := targetFunctions[item.ID]
			if file.URI == target.TargetFileURI && wanted {
				if _, onTargetLine := targetLines[item.Line]; !onTargetLine {
					return CoverageDelta{}, DiagnosticCoverage
				}
				if !item.BeforeCovered && item.AfterCovered {
					delta.Functions++
				}
			}
		}
		previousID = ""
		for _, item := range proof.Branches[branchStart:branchIndex] {
			work++
			if work&255 == 0 && ctx.Err() != nil {
				return CoverageDelta{}, DiagnosticStageFailed
			}
			lineIndex, lineExists := lineIndexes[item.Line]
			if !validDigest(item.ID) || item.ID <= previousID || !lineExists {
				return CoverageDelta{}, DiagnosticCoverage
			}
			previousID = item.ID
			counts[lineIndex].total++
			if item.BeforeCovered {
				beforeBranches++
				counts[lineIndex].before++
			}
			if item.AfterCovered {
				afterBranches++
				counts[lineIndex].after++
			}
			if item.BeforeCovered && !item.AfterCovered {
				return CoverageDelta{}, DiagnosticRegression
			}
			_, wanted := targetBranches[item.ID]
			if file.URI == target.TargetFileURI && wanted {
				if _, onTargetLine := targetLines[item.Line]; !onTargetLine {
					return CoverageDelta{}, DiagnosticCoverage
				}
				if !item.BeforeCovered && item.AfterCovered {
					delta.Branches++
				}
			}
		}
		if beforeFunctions != file.Summary.Functions.Covered || afterFunctions != afterFile.Summary.Functions.Covered || beforeBranches != file.Summary.Branches.Covered || afterBranches != afterFile.Summary.Branches.Covered {
			return CoverageDelta{}, DiagnosticCoverage
		}
		for i, line := range file.Lines {
			work++
			if work&255 == 0 && ctx.Err() != nil {
				return CoverageDelta{}, DiagnosticStageFailed
			}
			count := counts[i]
			if count.total != line.Branches.Total || count.before != line.Branches.Covered || count.after != afterFile.Lines[i].Branches.Covered {
				return CoverageDelta{}, DiagnosticCoverage
			}
		}
	}
	if functionIndex != len(proof.Functions) || branchIndex != len(proof.Branches) {
		return CoverageDelta{}, DiagnosticCoverage
	}
	return delta, DiagnosticNone
}
