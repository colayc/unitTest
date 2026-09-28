package protocol_test

import (
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
	stale := apply
	stale.ReviewDigest = strings.Repeat("d", 64)
	if protocol.ValidManagedReviewApplyV16(stale, reviewID, current) {
		t.Fatal("well-formed stale review digest accepted")
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
