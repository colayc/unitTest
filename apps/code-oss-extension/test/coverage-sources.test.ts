import assert from "node:assert/strict";
import { mkdtemp, mkdir, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createHash } from "node:crypto";
import {
  resolveCoverageSourcePath,
  verifyCoverageSource,
  type CoverageSourceSnapshot
} from "../src/coverage-sources.js";

test("coverage source paths stay inside the workspace after URI decoding", async () => {
  const root = await mkdtemp(join(tmpdir(), "unit-test-ide-coverage-source-"));
  await mkdir(join(root, "src"), { recursive: true });
  const path = resolveCoverageSourcePath(root, "src/main%20file.cpp");
  assert.equal(path, join(root, "src", "main file.cpp"));

  for (const uri of ["/absolute.cpp", "../outside.cpp", "src/%2e%2e/outside.cpp", "src\\escape.cpp", "C:/outside.cpp", ""]) {
    assert.throws(() => resolveCoverageSourcePath(root, uri), /coverage source path/i, uri);
  }
});

test("verifyCoverageSource returns a path only for a matching bounded digest", async () => {
  const root = await mkdtemp(join(tmpdir(), "unit-test-ide-coverage-source-"));
  await mkdir(join(root, "src"), { recursive: true });
  const content = Buffer.from("int main() { return 0; }\n", "utf8");
  const path = join(root, "src", "main.cpp");
  await writeFile(path, content);
  const sha256 = createHash("sha256").update(content).digest("hex");
  const source: CoverageSourceSnapshot = { uri: "src/main.cpp", sha256 };
  const verified = await verifyCoverageSource(root, source);
  assert.equal(verified.path, path);
  assert.equal(verified.sha256, sha256);

  await writeFile(path, "changed\n");
  await assert.rejects(() => verifyCoverageSource(root, source), /digest/i);
});
