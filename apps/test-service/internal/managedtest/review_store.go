package managedtest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxReviewBytes = 512 << 10

// ReviewManifest is immutable. Status is stored separately so a stale review
// cannot acquire a new identity by changing its immutable attestation.
type ReviewManifest struct {
	ReviewID, OwnerDigest, RunID                                 string
	RunRevision                                                  int64
	ProjectID, WorkspaceGeneration, ReportID, ToolchainID        string
	SourceDigest, CurrentPreimageDigest, GeneratedPreimageDigest string
	CandidateSetDigest, ArtifactRef                              string
	CreatedAt                                                    time.Time
}

type ReviewCandidate struct {
	CandidateID                                    string
	Status                                         Status
	AcceptedDigest, CurrentDigest, GeneratedDigest string
	Diff                                           string
	CurrentBytes, GeneratedBytes                   []byte
}

type ReviewDraft struct {
	Manifest   ReviewManifest
	Candidates []ReviewCandidate
}

type ReviewBinding struct {
	OwnerDigest, RunID                                    string
	RunRevision                                           int64
	ProjectID, WorkspaceGeneration, ReportID, ToolchainID string
}

type ReviewListQuery struct {
	ProjectID, Cursor string
	Limit             int
}

type ReviewSummary struct {
	ReviewID, ReviewDigest, RunID, ProjectID, WorkspaceGeneration, ReportID, ToolchainID string
	RunRevision                                                                          int64
	CandidateCount                                                                       int
	CreatedAt                                                                            time.Time
}

type ReviewListPage struct {
	Items      []ReviewSummary
	NextCursor string
}

type ReviewGetQuery struct {
	Binding          ReviewBinding
	ReviewID, Cursor string
	Limit            int
}

type ReviewCase struct {
	CandidateID                                    string
	Status                                         Status
	AcceptedDigest, CurrentDigest, GeneratedDigest string
	Diff                                           string
}

type ReviewPage struct {
	ReviewID, ReviewDigest, WorkspaceGeneration, ReportID string
	Cases                                                 []ReviewCase
	NextCursor                                            string
}

func (m ReviewManifest) Digest() string {
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(append([]byte("managed-review-manifest-v1\x00"), b...))
	return hex.EncodeToString(sum[:])
}

func ReviewCandidateSetDigest(candidates []ReviewCandidate) string {
	ordered := append([]ReviewCandidate(nil), candidates...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].CandidateID < ordered[j].CandidateID })
	b, err := json.Marshal(ordered)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(append([]byte("managed-review-candidate-set-v1\x00"), b...))
	return hex.EncodeToString(sum[:])
}

func ValidReviewManifest(m ReviewManifest) bool {
	if !validLowerHex(m.ReviewID, 32) || !ValidDigest(m.OwnerDigest) || !validLowerHex(m.RunID, 32) || m.RunRevision < 1 ||
		!validProjectID(m.ProjectID) || !ValidDigest(m.WorkspaceGeneration) || !validLowerHex(m.ReportID, 32) ||
		!validShortText(m.ToolchainID, 256) || !ValidDigest(m.SourceDigest) || !ValidDigest(m.CurrentPreimageDigest) ||
		!ValidDigest(m.GeneratedPreimageDigest) || !ValidDigest(m.CandidateSetDigest) || m.CreatedAt.IsZero() ||
		!strings.HasPrefix(m.ArtifactRef, "artifact:") || len(m.ArtifactRef) < 10 || len(m.ArtifactRef) > 256 {
		return false
	}
	for _, c := range m.ArtifactRef[len("artifact:"):] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func ValidReviewCandidate(c ReviewCandidate) bool {
	if !strings.HasPrefix(c.CandidateID, "utc_") || !validLowerHex(strings.TrimPrefix(c.CandidateID, "utc_"), 32) ||
		!ValidStatus(c.Status) || c.Status == StatusInvalid ||
		c.AcceptedDigest != "" && !ValidDigest(c.AcceptedDigest) || !ValidDigest(c.CurrentDigest) || !ValidDigest(c.GeneratedDigest) ||
		c.CurrentBytes == nil || c.GeneratedBytes == nil || len(c.CurrentBytes) > MaxReviewBytes || len(c.GeneratedBytes) > MaxReviewBytes ||
		len(c.Diff) > 128<<10 || !utf8.ValidString(c.Diff) {
		return false
	}
	current := sha256.Sum256(c.CurrentBytes)
	generated := sha256.Sum256(c.GeneratedBytes)
	return c.CurrentDigest == hex.EncodeToString(current[:]) && c.GeneratedDigest == hex.EncodeToString(generated[:])
}

func ValidReviewDraft(d ReviewDraft) bool {
	if !ValidReviewManifest(d.Manifest) || len(d.Candidates) < 1 || len(d.Candidates) > 200 ||
		d.Manifest.CandidateSetDigest != ReviewCandidateSetDigest(d.Candidates) {
		return false
	}
	seen := make(map[string]bool, len(d.Candidates))
	size := 0
	manifest, _ := json.Marshal(d.Manifest)
	size += len(manifest)
	for _, c := range d.Candidates {
		if !ValidReviewCandidate(c) || seen[c.CandidateID] {
			return false
		}
		seen[c.CandidateID] = true
		b, err := json.Marshal(c)
		if err != nil {
			return false
		}
		size += len(b)
		if size > MaxReviewBytes {
			return false
		}
	}
	return true
}

func (m ReviewManifest) Binding() ReviewBinding {
	return ReviewBinding{m.OwnerDigest, m.RunID, m.RunRevision, m.ProjectID, m.WorkspaceGeneration, m.ReportID, m.ToolchainID}
}

func (b ReviewBinding) Valid() bool {
	return ValidDigest(b.OwnerDigest) && validLowerHex(b.RunID, 32) && b.RunRevision >= 1 && validProjectID(b.ProjectID) &&
		ValidDigest(b.WorkspaceGeneration) && validLowerHex(b.ReportID, 32) && validShortText(b.ToolchainID, 256)
}
