import assert from "node:assert/strict";
import test from "node:test";
import { openCoverageHtml, renderCoverageHtml } from "../src/coverage-viewer.js";

const utf8 = (value: string): Uint8Array => new TextEncoder().encode(value);

test("coverage viewer accepts owned HTML and applies a no-network CSP", async () => {
  let rendered = "";
  await openCoverageHtml(
    { openCoverageHtml: (html) => { rendered = html; } },
    { kind: "coverage-html", bytes: utf8("<h1>Coverage</h1><script>void 0</script>") }
  );
  assert.match(rendered, /Content-Security-Policy/);
  assert.match(rendered, /default-src 'none'/);
  assert.match(rendered, /Coverage/);
});

test("coverage viewer rejects wrong artifact kind, invalid UTF-8, oversized data and remote resources", () => {
  assert.throws(() => renderCoverageHtml({ kind: "coverage-json", bytes: utf8("{}") }), /HTML/);
  assert.throws(() => renderCoverageHtml({ kind: "coverage-html", bytes: new Uint8Array([0xff]) }), /UTF-8/);
  assert.throws(() => renderCoverageHtml({ kind: "coverage-html", bytes: new Uint8Array(64 * 1024 * 1024 + 1) }), /size limit/);
  assert.throws(() => renderCoverageHtml({ kind: "coverage-html", bytes: utf8('<img src="https://example.invalid/x">') }), /remote/);
  assert.throws(() => renderCoverageHtml({ kind: "coverage-html", bytes: utf8("<iframe srcdoc='x'></iframe>") }), /remote/);
});
