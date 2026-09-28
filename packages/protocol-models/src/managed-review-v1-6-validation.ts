import type { ManagedReviewApplyRequestV16, ManagedReviewCaseV16, ManagedReviewV16 } from "./generated/test-generation-v1-6.js";
import { ManagedConflictChoiceV16 } from "./generated/test-generation-v1-6.js";

export const MAX_MANAGED_REVIEW_PAGE_ITEMS_V16 = 32;
export const MAX_MANAGED_REVIEW_PAGE_BYTES_V16 = 512 * 1024;
/** SHA-256 of zero bytes. Absence additionally requires an absentSides marker. */
export const ABSENT_BLOCK_DIGEST_V16 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855";

const id32 = /^[0-9a-f]{32}$/;
const digest64 = /^[0-9a-f]{64}$/;
const caseId = /^utc_[0-9a-f]{32}$/;
const scaffoldKey = /^scaffold:tests\/generated\/[A-Za-z0-9_./-]{1,220}_test\.(?:c|cpp)$/;
const choices = new Set<string>(Object.values(ManagedConflictChoiceV16));
const blockSides = ["accepted", "current", "generated"] as const;
export type ManagedBlockSideV16 = typeof blockSides[number];

/** A real empty block retains the sentinel digest but has absent=false. */
export function decodeManagedBlockDigestV16(wire: string, absent: boolean): string | undefined {
  if (!digest64.test(wire) || (absent && wire !== ABSENT_BLOCK_DIGEST_V16)) throw new Error("Invalid managed block digest or absent-side sentinel.");
  return absent ? undefined : wire;
}

export function validateManagedReviewCaseDigestsV16(value: ManagedReviewCaseV16): boolean {
  const absentSides = value.absentSides ?? [];
  if (!Array.isArray(absentSides) || absentSides.length > 3 || new Set(absentSides).size !== absentSides.length || absentSides.some((side) => !blockSides.includes(side as ManagedBlockSideV16))) return false;
  const absent = new Set<string>(absentSides);
  try {
    for (const side of blockSides) decodeManagedBlockDigestV16(value[`${side}Digest`], absent.has(side));
    return true;
  } catch { return false; }
}

export function validateManagedReviewApplyV16(
  request: ManagedReviewApplyRequestV16,
  currentReviewId: string,
  currentDigest: string
): boolean {
  if (!id32.test(request.reviewId) || !id32.test(currentReviewId) ||
      !digest64.test(request.reviewDigest) || !digest64.test(currentDigest) ||
      request.reviewId !== currentReviewId || request.reviewDigest !== currentDigest ||
      request.resolutions.length > 200) return false;
  const seen = new Set<string>();
  for (const resolution of request.resolutions) {
    if (!(caseId.test(resolution.caseId) || validScaffoldConflictKeyV16(resolution.caseId)) || !choices.has(resolution.choice) || seen.has(resolution.caseId)) return false;
    seen.add(resolution.caseId);
  }
  return true;
}

export function validScaffoldConflictKeyV16(value: string): boolean {
  if (!scaffoldKey.test(value)) return false;
  const path = value.slice("scaffold:".length);
  return !path.split("/").some((part) => part === "" || part === "." || part === "..");
}

export function validateManagedReviewPageV16(page: ManagedReviewV16): boolean {
  if (page.conflictKeys !== undefined && (page.conflictKeys.length > 200 || new Set(page.conflictKeys).size !== page.conflictKeys.length ||
      page.conflictKeys.some((key) => !(caseId.test(key) || validScaffoldConflictKeyV16(key))))) return false;
  if (page.cases.length > MAX_MANAGED_REVIEW_PAGE_ITEMS_V16) return false;
  if (page.cases.some((item) => !validateManagedReviewCaseDigestsV16(item))) return false;
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
