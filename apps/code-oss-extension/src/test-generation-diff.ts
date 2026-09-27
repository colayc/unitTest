import { createHash } from "node:crypto";
import type { TestGenerationRunV15 } from "@unit-test-ide/test-client";

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
