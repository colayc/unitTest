package protocol_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/protocol"
	generationv16 "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/testgeneration"
)

func TestV16ReviewApplyRejectsWellFormedStaleDigestAndConflictingChoices(t *testing.T) {
	reviewID, current := strings.Repeat("a", 32), strings.Repeat("b", 64)
	caseID := "utc_" + strings.Repeat("c", 32)
	apply := generationv16.ManagedReviewApplyRequestV16{
		ReviewID: reviewID, ReviewDigest: current,
		Resolutions: []generationv16.ManagedReviewResolutionV16{{CaseID: caseID, Choice: generationv16.KeepCurrent}},
	}
	if !protocol.ValidManagedReviewApplyV16(apply, reviewID, current) {
		t.Fatal("current digest and unique choice rejected")
	}
	ordinary := apply
	ordinary.Resolutions = []generationv16.ManagedReviewResolutionV16{}
	if !protocol.ValidManagedReviewApplyV16(ordinary, reviewID, current) {
		t.Fatal("no-conflict review cannot be applied without inventing a choice")
	}
	scaffold := apply
	scaffold.Resolutions = []generationv16.ManagedReviewResolutionV16{{CaseID: "scaffold:tests/generated/src/a_test.cpp", Choice: generationv16.KeepCurrent}}
	if !protocol.ValidManagedReviewApplyV16(scaffold, reviewID, current) {
		t.Fatal("publisher-required scaffold choice cannot cross the protocol")
	}
	stale := apply
	stale.ReviewDigest = strings.Repeat("d", 64)
	if protocol.ValidManagedReviewApplyV16(stale, reviewID, current) {
		t.Fatal("well-formed stale review digest accepted")
	}
	stale.ReviewDigest = protocol.AbsentBlockDigestV16
	if protocol.ValidManagedReviewApplyV16(stale, reviewID, current) {
		t.Fatal("absent-block sentinel used as review authorization")
	}
	duplicate := apply
	duplicate.Resolutions = []generationv16.ManagedReviewResolutionV16{
		{CaseID: caseID, Choice: generationv16.KeepCurrent},
		{CaseID: caseID, Choice: generationv16.UseGenerated},
	}
	if protocol.ValidManagedReviewApplyV16(duplicate, reviewID, current) {
		t.Fatal("conflicting choices for one case ID accepted")
	}
}

func TestV16ReviewPageRejectsEscapedByteBudgetOverrun(t *testing.T) {
	id, digest := strings.Repeat("a", 32), strings.Repeat("b", 64)
	diff := strings.Repeat("\x00", 4096)
	cases := make([]generationv16.ManagedReviewCaseV16, 32)
	for i := range cases {
		cases[i] = generationv16.ManagedReviewCaseV16{
			CaseID: "utc_" + id, Status: generationv16.ManagedTestStatusV16("conflicted"),
			AcceptedDigest: digest, CurrentDigest: digest, GeneratedDigest: digest, Diff: &diff,
		}
	}
	page := generationv16.ManagedReviewV16{ReviewID: id, ReviewDigest: digest, WorkspaceGeneration: digest, CoverageReportID: id, Cases: cases}
	if protocol.ValidManagedReviewPageV16(page) {
		t.Fatal("over-budget escaped review page accepted")
	}
	page.Cases = page.Cases[:1]
	if !protocol.ValidManagedReviewPageV16(page) {
		t.Fatal("small review page rejected")
	}
}

func TestV16ReviewDigestSentinelRequiresExplicitAbsentSide(t *testing.T) {
	empty := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if protocol.AbsentBlockDigestV16 != empty {
		t.Fatal("absent block must be SHA-256 of zero bytes")
	}
	want, absent, err := protocol.EncodeManagedBlockDigestV16("")
	if err != nil || want != empty || !absent {
		t.Fatalf("encode absent=%q %v %v", want, absent, err)
	}
	want, absent, err = protocol.EncodeManagedBlockDigestV16(empty)
	if err != nil || want != empty || absent {
		t.Fatalf("real empty block should stay present: %q %v %v", want, absent, err)
	}
	for _, malformed := range []string{"bad", strings.ToUpper(empty), strings.Repeat("g", 64)} {
		if _, _, err := protocol.EncodeManagedBlockDigestV16(malformed); err == nil {
			t.Fatalf("malformed digest %q encoded", malformed)
		}
	}
}

func TestV16ReviewPageRejectsIncorrectOrDuplicateAbsentSides(t *testing.T) {
	id, digest := strings.Repeat("a", 32), strings.Repeat("b", 64)
	base := generationv16.ManagedReviewCaseV16{CaseID: "utc_" + id, Status: generationv16.Conflicted,
		AcceptedDigest: protocol.AbsentBlockDigestV16, CurrentDigest: digest, GeneratedDigest: digest,
		AbsentSides: []string{"accepted"}}
	page := generationv16.ManagedReviewV16{ReviewID: id, ReviewDigest: digest, WorkspaceGeneration: digest, CoverageReportID: id, Cases: []generationv16.ManagedReviewCaseV16{base}}
	if !protocol.ValidManagedReviewPageV16(page) {
		t.Fatal("valid absent ancestor rejected")
	}
	page.Cases[0].AcceptedDigest = digest
	if protocol.ValidManagedReviewPageV16(page) {
		t.Fatal("incorrect absent sentinel accepted")
	}
	page.Cases[0].AcceptedDigest = protocol.AbsentBlockDigestV16
	page.Cases[0].AbsentSides = append(page.Cases[0].AbsentSides, "accepted")
	if protocol.ValidManagedReviewPageV16(page) {
		t.Fatal("duplicate absent side accepted")
	}
	page.Cases[0].AbsentSides = nil // SHA(empty) can also be a real present block.
	if !protocol.ValidManagedReviewPageV16(page) {
		t.Fatal("real empty block rejected")
	}
}

func TestV16ReviewPageBindsAdvertisedScaffoldPreview(t *testing.T) {
	id, digest := strings.Repeat("a", 32), strings.Repeat("b", 64)
	key := "scaffold:tests/generated/src/a_test.cpp"
	diff := "--- current/tests/generated/src/a_test.cpp\n+++ generated/tests/generated/src/a_test.cpp\n"
	sum := sha256.Sum256([]byte(diff))
	page := generationv16.ManagedReviewV16{ReviewID: id, ReviewDigest: digest, WorkspaceGeneration: digest, CoverageReportID: id,
		Cases:        []generationv16.ManagedReviewCaseV16{{CaseID: "utc_" + id, Status: generationv16.Conflicted, AcceptedDigest: digest, CurrentDigest: digest, GeneratedDigest: digest}},
		ConflictKeys: []string{"utc_" + id, key}, ScaffoldPreviews: []generationv16.ScaffoldPreview{{Key: key, Diff: diff, DiffDigest: hex.EncodeToString(sum[:])}}}
	if !protocol.ValidManagedReviewPageV16(page) {
		t.Fatal("valid advertised scaffold preview rejected")
	}
	page.ScaffoldPreviews[0].DiffDigest = digest
	if protocol.ValidManagedReviewPageV16(page) {
		t.Fatal("unverified scaffold preview accepted")
	}
	page.ScaffoldPreviews = nil
	page.ConflictKeys = []string{"scaffold:tests/generated/../escape_test.cpp"}
	if protocol.ValidManagedReviewPageV16(page) {
		t.Fatal("unsafe scaffold key accepted")
	}
}

func TestV16RecordDigestGuardRejectsIncorrectAbsence(t *testing.T) {
	id, digest := strings.Repeat("a", 32), strings.Repeat("b", 64)
	record := generationv16.ManagedTestRecordV16{CaseID: "utc_" + id, FileID: id, FunctionID: id, Status: generationv16.Current,
		AcceptedDigest: protocol.AbsentBlockDigestV16, CurrentDigest: digest, AbsentSides: []string{"accepted"}}
	if !protocol.ValidManagedRecordDigestsV16(record) {
		t.Fatal("valid absent accepted record rejected")
	}
	record.AcceptedDigest = digest
	if protocol.ValidManagedRecordDigestsV16(record) {
		t.Fatal("incorrect accepted sentinel accepted")
	}
	record.AcceptedDigest = protocol.AbsentBlockDigestV16
	record.AbsentSides = []string{"generated"}
	if protocol.ValidManagedRecordDigestsV16(record) {
		t.Fatal("absent generated side without digest accepted")
	}
}
