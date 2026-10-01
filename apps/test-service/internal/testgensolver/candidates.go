package testgensolver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func candidateID(gap CoverageGap, inputs []Input) string {
	encoded, _ := json.Marshal(struct {
		Version         string  `json:"solverVersion"`
		AnalyzerVersion string  `json:"analyzerVersion"`
		Snapshot        string  `json:"compileSnapshot"`
		SymbolID        string  `json:"symbolId"`
		GapKind         GapKind `json:"gapKind"`
		BranchID        string  `json:"branchId"`
		Outcome         Outcome `json:"outcome"`
		Inputs          []Input `json:"inputs"`
	}{SolverVersion, gap.AnalyzerVersion, gap.CompileSnapshot, gap.SymbolID, gap.Kind, gap.BranchID, gap.Outcome, inputs})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func digest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' && r < 'a' || r > 'f' {
			return false
		}
	}
	return true
}

func identifier(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for i, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func version(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.' || r == '_' {
			continue
		}
		return false
	}
	return true
}
