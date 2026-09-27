package testgenvalidate

import (
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

func proofDelta(before, after coverage.CoverageDocumentV1, target ResolvedCandidate, proof *TargetCoverageProof, baselineDigest, candidateDigest string) (CoverageDelta, Diagnostic) {
	if proof == nil {
		return CoverageDelta{}, DiagnosticNone
	}
	if proof.TargetSymbol != target.TargetSymbol || proof.TargetFileURI != target.TargetFileURI || proof.BaselineSHA256 != baselineDigest || proof.CandidateSHA256 != candidateDigest || proof.SourceSnapshotDigest != target.SourceSnapshotDigest || !validDigest(proof.EvidenceDigest) || len(proof.Functions) > 100000 || len(proof.Branches) > 100000 {
		return CoverageDelta{}, DiagnosticCoverage
	}
	var delta CoverageDelta
	functionIndex, branchIndex := 0, 0
	for fileIndex, file := range before.Files {
		afterFile := after.Files[fileIndex]
		functions := []CoverageIdentityChange{}
		for functionIndex < len(proof.Functions) && proof.Functions[functionIndex].FileURI == file.URI {
			functions = append(functions, proof.Functions[functionIndex])
			functionIndex++
		}
		branches := []CoverageIdentityChange{}
		for branchIndex < len(proof.Branches) && proof.Branches[branchIndex].FileURI == file.URI {
			branches = append(branches, proof.Branches[branchIndex])
			branchIndex++
		}
		if int64(len(functions)) != file.Summary.Functions.Total || int64(len(branches)) != file.Summary.Branches.Total {
			return CoverageDelta{}, DiagnosticCoverage
		}
		beforeFunctions, afterFunctions := int64(0), int64(0)
		beforeBranches, afterBranches := int64(0), int64(0)
		previousID := ""
		for _, item := range functions {
			if !validDigest(item.ID) || item.ID <= previousID || item.Line < 1 || !fileHasLine(file, item.Line) {
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
			if file.URI == target.TargetFileURI && containsID(target.TargetFunctionIDs, item.ID) {
				if !containsLine(target.TargetLines, item.Line) {
					return CoverageDelta{}, DiagnosticCoverage
				}
				if !item.BeforeCovered && item.AfterCovered {
					delta.Functions++
				}
			}
		}
		previousID = ""
		for _, item := range branches {
			if !validDigest(item.ID) || item.ID <= previousID || item.Line < 1 || !fileHasLine(file, item.Line) {
				return CoverageDelta{}, DiagnosticCoverage
			}
			previousID = item.ID
			if item.BeforeCovered {
				beforeBranches++
			}
			if item.AfterCovered {
				afterBranches++
			}
			if item.BeforeCovered && !item.AfterCovered {
				return CoverageDelta{}, DiagnosticRegression
			}
			if file.URI == target.TargetFileURI && containsID(target.TargetBranchIDs, item.ID) {
				if !containsLine(target.TargetLines, item.Line) {
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
		for _, line := range file.Lines {
			beforeLine, afterLine, total := int64(0), int64(0), int64(0)
			for _, item := range branches {
				if item.Line == line.Line {
					total++
					if item.BeforeCovered {
						beforeLine++
					}
					if item.AfterCovered {
						afterLine++
					}
				}
			}
			if total != line.Branches.Total || beforeLine != line.Branches.Covered || afterLine != afterFile.Lines[lineIndex(file, line.Line)].Branches.Covered {
				return CoverageDelta{}, DiagnosticCoverage
			}
		}
	}
	if functionIndex != len(proof.Functions) || branchIndex != len(proof.Branches) {
		return CoverageDelta{}, DiagnosticCoverage
	}
	return delta, DiagnosticNone
}

func fileHasLine(file coverage.CoverageFileV1, line int64) bool { return lineIndex(file, line) >= 0 }
func lineIndex(file coverage.CoverageFileV1, line int64) int {
	for i, item := range file.Lines {
		if item.Line == line {
			return i
		}
	}
	return -1
}
func containsLine(lines []int64, value int64) bool {
	for _, line := range lines {
		if line == value {
			return true
		}
	}
	return false
}
func containsID(ids []string, value string) bool {
	for _, id := range ids {
		if id == value {
			return true
		}
	}
	return false
}
