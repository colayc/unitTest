import { createHash } from "node:crypto";
import type { ManagedReviewCaseV16, TestGenerationRunV15 } from "@unit-test-ide/test-client";
import { validateManagedReviewCaseDigestsV16 } from "@unit-test-ide/test-client";

export interface GenerationDiffPreview {
  readonly diff: string;
  readonly diffDigest: string;
  readonly confirmationDigest: string;
}

export interface DiffReview {
  readonly title: string;
  readonly content: string;
  readonly digest: string;
  readonly confirmationDigest: string;
}

export interface ManagedCaseReview {
  readonly title: string;
  readonly content: string;
  readonly previewAvailable: boolean;
  readonly panes: { readonly accepted: string; readonly current: string; readonly generated: string };
}

/** v1.6 transports only a bounded diff plus the three operation digests. */
export function createManagedCaseReview(value: ManagedReviewCaseV16): ManagedCaseReview {
  if (!/^utc_[0-9a-f]{32}$/.test(value.caseId) || !validateManagedReviewCaseDigestsV16(value) || value.diff !== undefined && (value.diff.length > 32_768 || value.diff.includes("\0"))) {
    throw new Error("The managed case preview or digest is invalid.");
  }
  const absent = new Set<string>(value.absentSides ?? []);
  return { title: `Managed test ${value.caseId}`, content: value.diff || "Preview unavailable: the service supplied digests only; exact text changes cannot be shown.", previewAvailable: !!value.diff,
    panes: { accepted: absent.has("accepted") ? "No accepted ancestor" : value.acceptedDigest,
      current: absent.has("current") ? "No current block" : value.currentDigest,
      generated: absent.has("generated") ? "No generated block" : value.generatedDigest } };
}

function digest(value: string): string {
  return createHash("sha256").update(value, "utf8").digest("hex");
}

/** Verify the bytes returned by the service before showing or accepting them. */
export function readGenerationDiff(run: TestGenerationRunV15): GenerationDiffPreview {
  const preview = run.preview;
  if (preview?.diff === undefined) throw new Error("The generation preview does not contain an exact diff.");
  const actual = digest(preview.diff);
  if (actual !== preview.diffDigest) throw new Error("The generation preview diff digest is stale.");
  if (!/^[0-9a-f]{64}$/.test(preview.confirmationDigest)) {
    throw new Error("The generation preview confirmation digest is invalid.");
  }
  return {
    diff: preview.diff,
    diffDigest: actual,
    confirmationDigest: preview.confirmationDigest
  };
}

/** Build a review object using only service-provided preview bytes. */
export function createGenerationDiffReview(run: TestGenerationRunV15): DiffReview {
  const preview = readGenerationDiff(run);
  return {
    title: `Generated tests (${run.runId})`,
    content: preview.diff,
    digest: preview.diffDigest,
    confirmationDigest: preview.confirmationDigest
  };
}

/**
 * Replace host-specific absolute paths in diff headers while preserving the
 * patch body. The returned text is display-only and must never be sent back
 * to the service as an acceptance request.
 */
export function redactGenerationDiffPaths(diff: string): string {
  return diff.split("\n").map((line) => {
    if (!/^(---|\+\+\+|diff --git)\s/.test(line)) return line;
    const separator = line.indexOf("/a/") >= 0 ? "/a/" : line.indexOf("/b/") >= 0 ? "/b/" : undefined;
    if (separator) return line.slice(0, line.indexOf(separator)) + separator + line.slice(line.indexOf(separator) + separator.length).replace(/^.*?[\\/](?=[^\\/]+$)/, "");
    return line.replace(/[A-Za-z]:[\\/][^\t ]*/g, "<workspace-path>").replace(/(?:^|\s)(?:\\\\|\/)(?:Users|home|private|workspace)[^\t ]*/gi, " <workspace-path>");
  }).join("\n");
}

export function diffDigest(diff: string): string {
  return digest(diff);
}
