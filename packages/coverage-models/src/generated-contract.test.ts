import assert from "node:assert/strict";
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
