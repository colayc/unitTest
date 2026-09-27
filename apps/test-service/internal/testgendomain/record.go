package testgendomain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
)

// BudgetUsage is the durable, consumable part of the shared ledger. Memory,
// process and wall-time reservations are never adopted across a restart.
type BudgetUsage struct {
	Candidates  int64 `json:"candidates"`
	OutputBytes int64 `json:"outputBytes"`
	Events      int64 `json:"events"`
	Artifacts   int64 `json:"artifacts"`
}

type GenerationRecord struct {
	Version          int         `json:"version"`
	SnapshotDigest   string      `json:"snapshotDigest"`
	BudgetUsed       BudgetUsage `json:"budgetUsed"`
	MinimizedCaseIDs []string    `json:"minimizedCaseIds"`
}

func NewGenerationRecord(request Request) GenerationRecord {
	raw, _ := json.Marshal(request.SnapshotIdentity())
	sum := sha256.Sum256(raw)
	return GenerationRecord{Version: 1, SnapshotDigest: hex.EncodeToString(sum[:]), MinimizedCaseIDs: []string{}}
}

func (r GenerationRecord) IsZero() bool {
	return r.Version == 0 && r.SnapshotDigest == "" && r.BudgetUsed == (BudgetUsage{}) && len(r.MinimizedCaseIDs) == 0
}

func (r GenerationRecord) ValidFor(request Request, candidateCount int) bool {
	if r.Version != 1 || r.SnapshotDigest != NewGenerationRecord(request).SnapshotDigest || r.BudgetUsed.Candidates < 0 || r.BudgetUsed.Candidates > request.Budgets.CandidateCount || r.BudgetUsed.OutputBytes < 0 || r.BudgetUsed.OutputBytes > 1<<30 || r.BudgetUsed.Events < 0 || r.BudgetUsed.Events > 10000 || r.BudgetUsed.Artifacts < 0 || r.BudgetUsed.Artifacts > 1000 || len(r.MinimizedCaseIDs) > candidateCount || len(r.MinimizedCaseIDs) > 1000 {
		return false
	}
	previous := ""
	for _, id := range r.MinimizedCaseIDs {
		if !validID(id) || id <= previous {
			return false
		}
		previous = id
	}
	return true
}

func (r GenerationRecord) MonotonicAfter(previous GenerationRecord) bool {
	return r.Version == previous.Version && r.SnapshotDigest == previous.SnapshotDigest && r.BudgetUsed.Candidates >= previous.BudgetUsed.Candidates && r.BudgetUsed.OutputBytes >= previous.BudgetUsed.OutputBytes && r.BudgetUsed.Events >= previous.BudgetUsed.Events && r.BudgetUsed.Artifacts >= previous.BudgetUsed.Artifacts && (len(previous.MinimizedCaseIDs) == 0 || slices.Equal(r.MinimizedCaseIDs, previous.MinimizedCaseIDs))
}
