import type { ManagedReviewApplyRequestV16, ManagedReviewV16 } from "./generated/test-generation-v1-6.js";
import { ManagedConflictChoiceV16 } from "./generated/test-generation-v1-6.js";

export const MAX_MANAGED_REVIEW_PAGE_ITEMS_V16 = 32;
export const MAX_MANAGED_REVIEW_PAGE_BYTES_V16 = 512 * 1024;

const id32 = /^[0-9a-f]{32}$/;
const digest64 = /^[0-9a-f]{64}$/;
const caseId = /^utc_[0-9a-f]{32}$/;
const choices = new Set<string>(Object.values(ManagedConflictChoiceV16));

export function validateManagedReviewApplyV16(
  request: ManagedReviewApplyRequestV16,
  currentReviewId: string,
  currentDigest: string
): boolean {
  if (!id32.test(request.reviewId) || !id32.test(currentReviewId) ||
      !digest64.test(request.reviewDigest) || !digest64.test(currentDigest) ||
      request.reviewId !== currentReviewId || request.reviewDigest !== currentDigest ||
      request.resolutions.length < 1 || request.resolutions.length > 200) return false;
  const seen = new Set<string>();
  for (const resolution of request.resolutions) {
    if (!caseId.test(resolution.caseId) || !choices.has(resolution.choice) || seen.has(resolution.caseId)) return false;
    seen.add(resolution.caseId);
  }
  return true;
}

export function validateManagedReviewPageV16(page: ManagedReviewV16): boolean {
  if (page.cases.length > MAX_MANAGED_REVIEW_PAGE_ITEMS_V16) return false;
  if (page.cases.some((item) => item.diff !== undefined && [...item.diff].length > 4096)) return false;
  const serialized = JSON.stringify(page);
  // Go's encoding/json escapes these characters even though JSON.stringify
  // leaves them literal. Match its wire-byte accounting at this boundary.
  let bytes = new TextEncoder().encode(serialized).length;
  for (const [character] of serialized.matchAll(/[<>&\u2028\u2029]/g)) {
    bytes += character === "\u2028" || character === "\u2029" ? 3 : 5;
  }
  return bytes <= MAX_MANAGED_REVIEW_PAGE_BYTES_V16;
}
