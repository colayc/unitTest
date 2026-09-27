package testgenpublish

import "encoding/json"

// Receipt deliberately contains only relative paths and content hashes.
type Receipt struct {
	RunID, CandidateSetDigest, SnapshotDigest, DiffDigest, ConfirmationDigest, CharacterizationDigest string
	Edits                                                                                             []PlannedEdit
}

func (r Receipt) String() string { data, _ := json.Marshal(r); return string(data) }

// ConflictReceipt gives the caller a content-free basis for a fresh preview.
type ConflictReceipt struct{ Path, ExpectedDigest, ActualDigest string }

func (c *ConflictReceipt) Error() string { return ErrConflict.Error() }
func (c *ConflictReceipt) Unwrap() error { return ErrConflict }
func conflictReceipt(path, expected string, actual []byte, exists bool) error {
	got := ""
	if exists {
		got = digest(actual)
	}
	return &ConflictReceipt{Path: path, ExpectedDigest: expected, ActualDigest: got}
}
