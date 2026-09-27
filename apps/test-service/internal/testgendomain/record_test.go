package testgendomain

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestGenerationRecordRejectsUnknownVersion(t *testing.T) {
	r := validRequest()
	record := NewGenerationRecord(r)
	if !record.ValidFor(r, 0) {
		t.Fatal("current record invalid")
	}
	record.Version = 2
	if record.ValidFor(r, 0) {
		t.Fatal("accepted unknown version")
	}
}

func TestMinimizedSetCannotChangeAfterCheckpoint(t *testing.T) {
	r := validRequest()
	a := NewGenerationRecord(r)
	a.MinimizedCaseIDs = []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	b := a
	b.MinimizedCaseIDs = []string{"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	if b.MonotonicAfter(a) {
		t.Fatal("minimized set changed after durable checkpoint")
	}
}

func TestPreviewIdentityPersistsAndCannotChangeAfterCheckpoint(t *testing.T) {
	r := validRequest()
	a := NewGenerationRecord(r)
	a.MinimizedCaseIDs = []string{strings.Repeat("a", 32)}
	a.Preview = &PreviewIdentity{
		CandidateSetDigest: strings.Repeat("b", 64), DiffDigest: strings.Repeat("c", 64),
		ConfirmationDigest: strings.Repeat("d", 64), CharacterizationDigest: "",
	}
	if !a.ValidFor(r, 1) {
		t.Fatal("valid durable preview rejected")
	}
	b := a
	b.Preview = &PreviewIdentity{CandidateSetDigest: strings.Repeat("e", 64), DiffDigest: a.Preview.DiffDigest, ConfirmationDigest: a.Preview.ConfirmationDigest}
	if b.MonotonicAfter(a) {
		t.Fatal("preview identity changed after checkpoint")
	}
	b = a
	b.Preview = nil
	if b.MonotonicAfter(a) {
		t.Fatal("durable preview was dropped")
	}
	a.Preview.ConfirmationDigest = "malformed"
	if a.ValidFor(r, 1) {
		t.Fatal("malformed preview accepted")
	}
}

func TestPreviewDiffMustBeBoundedAndDigestBound(t *testing.T) {
	r := validRequest()
	record := NewGenerationRecord(r)
	record.MinimizedCaseIDs = []string{strings.Repeat("a", 32)}
	diff := "--- a/tests/test.cpp\n+++ b/tests/test.cpp\n@@ -0,0 +1 @@\n+TEST(example) {}\n"
	sum := sha256.Sum256([]byte(diff))
	record.Preview = &PreviewIdentity{CandidateSetDigest: strings.Repeat("b", 64), DiffDigest: hex.EncodeToString(sum[:]), ConfirmationDigest: strings.Repeat("d", 64), Diff: diff}
	if !record.ValidFor(r, 1) {
		t.Fatal("valid exact preview diff rejected")
	}
	modified := record
	changed := *record.Preview
	changed.Diff += "+evil\n"
	modified.Preview = &changed
	if modified.ValidFor(r, 1) || modified.MonotonicAfter(record) {
		t.Fatal("modified preview diff accepted")
	}
	changed = *record.Preview
	changed.Diff = strings.Repeat("x", 262145)
	modified.Preview = &changed
	if modified.ValidFor(r, 1) {
		t.Fatal("oversized preview diff accepted")
	}
}
