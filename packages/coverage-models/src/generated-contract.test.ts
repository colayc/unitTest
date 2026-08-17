import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import type {
  CoverageDocumentV1,
  CoverageFileV1,
  CoverageMetricV1
} from "./generated/coverage-v1.js";

test("generated Coverage JSON v1 types expose stable wire names", () => {
  const metric: CoverageMetricV1 = { covered: 1, total: 2 };
  const file: CoverageFileV1 = {
    uri: "src/calculator.cpp",
    sha256: "b".repeat(64),
    summary: { lines: metric, branches: metric, functions: { covered: 1, total: 1 } },
    lines: [{ line: 10, count: 1, branches: metric }]
  };
  const document: CoverageDocumentV1 = {
    schemaVersion: "1.0",
    provenance: {
      platform: "linux",
      architecture: "x64",
      compiler: { family: "clang", version: "22.1.0" },
      driver: { name: "llvm-cov", version: "22.1.0" },
      collector: { name: "llvm-cov", version: "22.1.0" },
      normalizerVersion: "1.0.0",
      instrumentationFingerprint: "a".repeat(64)
    },
    completeness: { outcome: "available", reasons: [] },
    summary: file.summary,
    files: [file]
  };
  assert.equal(document.files[0]?.uri, "src/calculator.cpp");
});

test("root scripts gate Coverage generation drift and regressions", async () => {
  const root = JSON.parse(await readFile(new URL("../../../package.json", import.meta.url), "utf8"));
  assert.equal(root.scripts["generate:coverage"], "node tools/coverage-gen/generate.mjs");
  assert.equal(root.scripts["check:coverage-generated"], "node tools/coverage-gen/generate.mjs --check");
  assert.equal(
    root.scripts["test:coverage-gen"],
    "node --test tools/coverage-gen/generate.test.mjs"
  );
  assert.equal(
    root.scripts.test,
    "pnpm run test:coverage-gen && pnpm run test:cmake-bundle && pnpm run test:workspace && pnpm -r --if-present test && pnpm run test:go"
  );
  assert.equal(
    root.scripts.verify,
    "pnpm check:protocol-generated && pnpm check:coverage-generated && pnpm build && pnpm test && pnpm test:go:race && pnpm test:e2e"
  );
});
