package managedtest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ManagedFile is a candidate produced by the renderer, not an instruction to
// write to the workspace. The publisher remains the sole writer.
type ManagedFile struct {
	Path    string
	Content []byte
}

type OperationKind string

const (
	OperationAdd       OperationKind = "add"
	OperationUpdate    OperationKind = "update"
	OperationUnchanged OperationKind = "unchanged"
	OperationConflict  OperationKind = "conflict"
	OperationOrphan    OperationKind = "orphan"
)

// CasePreview is bounded source text for a human review. The digest fields
// on Operation remain authoritative, including when a preview is omitted.
type CasePreview struct {
	Accepted  string `json:"accepted,omitempty"`
	Current   string `json:"current,omitempty"`
	Generated string `json:"generated,omitempty"`
}

type Operation struct {
	CaseID          string        `json:"caseId"`
	Kind            OperationKind `json:"kind"`
	Conflict        bool          `json:"conflict"`
	AcceptedDigest  string        `json:"acceptedDigest,omitempty"`
	CurrentDigest   string        `json:"currentDigest,omitempty"`
	GeneratedDigest string        `json:"generatedDigest,omitempty"`
	Preview         CasePreview   `json:"preview"`
}

type ReviewPreview struct {
	Unified string `json:"unified"`
}

// ScaffoldOperation covers bytes outside managed blocks and their placement
// relative to blocks common to the current and generated documents.
type ScaffoldOperation struct {
	Kind            OperationKind `json:"kind"`
	Conflict        bool          `json:"conflict"`
	CurrentDigest   string        `json:"currentDigest"`
	GeneratedDigest string        `json:"generatedDigest"`
	Preview         CasePreview   `json:"preview"`
}

type Review struct {
	Path            string             `json:"path"`
	PreimageDigest  string             `json:"preimageDigest"`
	GeneratedDigest string             `json:"generatedDigest"`
	Operations      []Operation        `json:"operations"`
	Scaffold        *ScaffoldOperation `json:"scaffold,omitempty"`
	Preview         ReviewPreview      `json:"preview"`
}

// Digest binds the exact preimage, generated candidate, sorted operations and
// displayed preview. The caller must compare it afresh before publishing.
func (r Review) Digest() string {
	encoded, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(append([]byte("managed-review-v1\x00"), encoded...))
	return hex.EncodeToString(sum[:])
}
