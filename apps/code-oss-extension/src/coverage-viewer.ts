export interface CoverageArtifactInput {
  readonly kind: string;
  readonly bytes: Uint8Array;
}

export interface CoverageViewerHost {
  openCoverageHtml(html: string): void | PromiseLike<void>;
}

const MAX_HTML_BYTES = 64 * 1024 * 1024;
const UTF8 = new TextDecoder("utf-8", { fatal: true });

export function renderCoverageHtml(artifact: CoverageArtifactInput): string {
  if (artifact.kind !== "coverage-html") throw new Error("Coverage report artifact must be HTML.");
  if (artifact.bytes.byteLength > MAX_HTML_BYTES) throw new Error("Coverage report HTML exceeds the client size limit.");
  let html: string;
  try {
    html = UTF8.decode(artifact.bytes);
  } catch {
    throw new Error("Coverage report HTML is not valid UTF-8.");
  }
  if (/<(?:base|iframe|object|embed)\b/i.test(html) || /(?:src|href|action)\s*=\s*["']\s*https?:/i.test(html) || /url\(\s*https?:/i.test(html)) {
    throw new Error("Coverage report HTML contains a remote or embedded resource.");
  }
  const csp = "default-src 'none'; img-src data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; object-src 'none'; frame-src 'none'; base-uri 'none'; form-action 'none'";
  return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${csp}"></head><body>${html}</body></html>`;
}

export async function openCoverageHtml(
  host: CoverageViewerHost,
  artifact: CoverageArtifactInput
): Promise<void> {
  await host.openCoverageHtml(renderCoverageHtml(artifact));
}
