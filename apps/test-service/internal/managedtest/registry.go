package managedtest

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

type Status string

const (
	StatusCurrent    Status = "current"
	StatusStale      Status = "stale"
	StatusConflicted Status = "conflicted"
	StatusOrphaned   Status = "orphaned"
	StatusInvalid    Status = "invalid"
)

type Record struct {
	CaseID, ProjectID, SourceFileID, FunctionID string
	SourceRelativePath, ScenarioID              string
	TestRelativePath, AcceptedBlockDigest       string
	GeneratorVersion, Framework, ToolchainID    string
	SourceDigest, ValidationReceiptDigest       string
	Status                                      Status
	LastVerifiedAt                              time.Time
}

type Query struct {
	ProjectID, SourceFileID string
	Status                  Status
	Cursor                  string
	Limit                   int
}

type Page struct {
	Items      []Record
	NextCursor string
}

type Acceptance struct {
	AcceptanceID, ReviewDigest, PreimageDigest, PublishedFileDigest string
	Record                                                          Record
	At                                                              time.Time
}

type PendingAcceptance struct {
	Acceptance Acceptance
	Phase      string
}

// LegacyRecoveryEvidence explicitly re-verifies an acceptance created before
// canonical accepted payloads were persisted. The caller must supply exact
// preimage/published document bytes, source bytes, and receipt bytes.
type LegacyRecoveryEvidence struct {
	AcceptanceID                        string
	PreimageDocument, PublishedDocument []byte
	SourceBytes, ValidationReceipt      []byte
}

type SourceSnapshot struct {
	FileID, RelativePath, Digest string
	FunctionIDs                  []string
}

type TestFileSnapshot struct {
	RelativePath string
	Bytes        []byte
}

type WorkspaceSnapshot struct {
	ProjectID, ToolchainID, ReceiptDigest string
	Sources                               []SourceSnapshot
	Tests                                 []TestFileSnapshot
	At                                    time.Time
}

type Registry interface {
	List(context.Context, Query) (Page, error)
	Get(context.Context, string) (Record, error)
	CommitAccepted(context.Context, Acceptance) error
	ReconcileWorkspace(context.Context, WorkspaceSnapshot) error
}

// AcceptanceJournal is used by the existing atomic publisher. The pending row
// remains durable across a crash and is never interpreted as accepted evidence.
type AcceptanceJournal interface {
	BeginManagedAcceptance(context.Context, Acceptance) error
	MarkManagedFileWritten(context.Context, string, string) error
	ListPendingManagedAcceptances(context.Context) ([]PendingAcceptance, error)
	ResolvePendingManagedAcceptance(context.Context, string, string) error
	AuthorizeLegacyManagedRecovery(context.Context, LegacyRecoveryEvidence) error
}

func ValidStatus(value Status) bool {
	switch value {
	case StatusCurrent, StatusStale, StatusConflicted, StatusOrphaned, StatusInvalid:
		return true
	default:
		return false
	}
}

func ValidDigest(value string) bool { return validLowerHex(value, 64) }

func ValidRecord(value Record) bool {
	if !validLowerHex(strings.TrimPrefix(value.CaseID, "utc_"), 32) || !strings.HasPrefix(value.CaseID, "utc_") ||
		!validProjectID(value.ProjectID) || !validLowerHex(value.SourceFileID, 32) ||
		!validLowerHex(value.FunctionID, 32) || !ValidDigest(value.AcceptedBlockDigest) ||
		!ValidDigest(value.SourceDigest) || !ValidDigest(value.ValidationReceiptDigest) ||
		!ValidStatus(value.Status) || value.LastVerifiedAt.IsZero() ||
		!validShortText(value.GeneratorVersion, 128) || !validShortText(value.Framework, 64) ||
		!validShortText(value.ToolchainID, 256) || !validTestPath(value.TestRelativePath) ||
		!validScenarioID(value.ScenarioID) {
		return false
	}
	if _, err := canonicalSourcePath(value.SourceRelativePath); err != nil {
		return false
	}
	id, err := StableCaseID(value.ProjectID, value.SourceRelativePath, value.FunctionID, value.ScenarioID)
	return err == nil && id == value.CaseID
}

func ValidAcceptance(value Acceptance) bool {
	return validLowerHex(value.AcceptanceID, 32) && ValidDigest(value.ReviewDigest) &&
		ValidDigest(value.PreimageDigest) && ValidDigest(value.PublishedFileDigest) &&
		ValidRecord(value.Record) && value.Record.Status == StatusCurrent &&
		!value.At.IsZero() && !value.Record.LastVerifiedAt.After(value.At)
}

func validTestPath(value string) bool {
	if !strings.HasPrefix(value, "tests/generated/") || !strings.HasSuffix(value, "_test.cpp") && !strings.HasSuffix(value, "_test.c") {
		return false
	}
	_, err := canonicalSourcePath(value)
	return err == nil
}

func ValidTestPath(value string) bool { return validTestPath(value) }

func validShortText(value string, max int) bool {
	if value == "" || len(value) > max || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < ' ' || r == 127 {
			return false
		}
	}
	return true
}
