package protocol

import (
	"crypto/subtle"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/managedtest"
	generationv16 "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/testgeneration"
)

const MaxManagedReviewPageItemsV16 = 32
const MaxManagedReviewPageBytesV16 = 512 * 1024

// ValidManagedReviewApplyV16 checks the request against the current durable
// review identity supplied by the provider. Schema validation alone cannot
// establish freshness or uniqueness by case ID.
func ValidManagedReviewApplyV16(request generationv16.ManagedReviewApplyRequestV16, currentReviewID, currentDigest string) bool {
	if !validLowerHex(request.ReviewID, 32) || !validLowerHex(request.ReviewDigest, 64) ||
		!validLowerHex(currentReviewID, 32) || !validLowerHex(currentDigest, 64) ||
		subtle.ConstantTimeCompare([]byte(request.ReviewID), []byte(currentReviewID)) != 1 ||
		subtle.ConstantTimeCompare([]byte(request.ReviewDigest), []byte(currentDigest)) != 1 ||
		len(request.Resolutions) > 200 {
		return false
	}
	seen := make(map[string]struct{}, len(request.Resolutions))
	for _, resolution := range request.Resolutions {
		caseID := len(resolution.CaseID) == 36 && strings.HasPrefix(resolution.CaseID, "utc_") && validLowerHex(resolution.CaseID[4:], 32)
		scaffold := strings.HasPrefix(resolution.CaseID, "scaffold:") && managedtest.ValidTestPath(strings.TrimPrefix(resolution.CaseID, "scaffold:"))
		if (!caseID && !scaffold) || !validManagedChoiceV16(resolution.Choice) {
			return false
		}
		if _, duplicate := seen[resolution.CaseID]; duplicate {
			return false
		}
		seen[resolution.CaseID] = struct{}{}
	}
	return true
}

// ValidManagedReviewPageV16 is the service-side serialized payload guard.
// The provider must issue a nextCursor and return fewer cases rather than
// truncate or emit a page that could approach the 2 MiB wire ceiling.
func ValidManagedReviewPageV16(page generationv16.ManagedReviewV16) bool {
	if len(page.Cases) > MaxManagedReviewPageItemsV16 {
		return false
	}
	for _, item := range page.Cases {
		if item.Diff != nil && utf8.RuneCountInString(*item.Diff) > 4096 {
			return false
		}
	}
	encoded, err := json.Marshal(page)
	return err == nil && len(encoded) <= MaxManagedReviewPageBytesV16
}

func validManagedChoiceV16(choice generationv16.ManagedConflictChoiceV16) bool {
	return choice == generationv16.KeepCurrent || choice == generationv16.UseGenerated || choice == generationv16.ConvertToManual
}

func validLowerHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
