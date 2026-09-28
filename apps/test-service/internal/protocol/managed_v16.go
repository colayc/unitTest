package protocol

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/managedtest"
	generationv16 "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/testgeneration"
)

const MaxManagedReviewPageItemsV16 = 32
const MaxManagedReviewPageBytesV16 = 512 * 1024

// AbsentBlockDigestV16 is SHA-256 of the empty byte sequence. A wire digest
// equal to this value is *not* by itself evidence of absence: absentSides must
// name the side. Internally, an empty digest remains the absence marker.
const AbsentBlockDigestV16 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// EncodeManagedBlockDigestV16 converts only the internal empty-absence marker;
// malformed nonempty input never becomes the sentinel or a valid wire digest.
func EncodeManagedBlockDigestV16(internal string) (wire string, absent bool, err error) {
	if internal == "" {
		return AbsentBlockDigestV16, true, nil
	}
	if !validLowerHex(internal, 64) {
		return "", false, ErrInvalidManagedBlockDigestV16
	}
	return internal, false, nil
}

var ErrInvalidManagedBlockDigestV16 = errors.New("invalid managed block digest")

// ValidManagedBlockDigestV16 binds a claimed absent side to the sentinel.
// A present, genuinely empty block has the same digest but absent=false.
func ValidManagedBlockDigestV16(wire string, absent bool) bool {
	return validLowerHex(wire, 64) && (!absent || wire == AbsentBlockDigestV16)
}

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
		if !ValidManagedReviewCaseDigestsV16(item) {
			return false
		}
		if item.Diff != nil && utf8.RuneCountInString(*item.Diff) > 4096 {
			return false
		}
	}
	encoded, err := json.Marshal(page)
	return err == nil && len(encoded) <= MaxManagedReviewPageBytesV16
}

func ValidManagedReviewCaseDigestsV16(item generationv16.ManagedReviewCaseV16) bool {
	seen, ok := validManagedAbsentSidesV16(item.AbsentSides)
	if !ok {
		return false
	}
	return ValidManagedBlockDigestV16(item.AcceptedDigest, seen["accepted"]) &&
		ValidManagedBlockDigestV16(item.CurrentDigest, seen["current"]) &&
		ValidManagedBlockDigestV16(item.GeneratedDigest, seen["generated"])
}

func ValidManagedRecordDigestsV16(item generationv16.ManagedTestRecordV16) bool {
	seen, ok := validManagedAbsentSidesV16(item.AbsentSides)
	if !ok || !ValidManagedBlockDigestV16(item.AcceptedDigest, seen["accepted"]) || !ValidManagedBlockDigestV16(item.CurrentDigest, seen["current"]) {
		return false
	}
	if item.GeneratedDigest == nil {
		return !seen["generated"]
	}
	return ValidManagedBlockDigestV16(*item.GeneratedDigest, seen["generated"])
}

func validManagedAbsentSidesV16(sides []string) (map[string]bool, bool) {
	if len(sides) > 3 {
		return nil, false
	}
	seen := make(map[string]bool, len(sides))
	for _, side := range sides {
		if side != "accepted" && side != "current" && side != "generated" || seen[side] {
			return nil, false
		}
		seen[side] = true
	}
	return seen, true
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
