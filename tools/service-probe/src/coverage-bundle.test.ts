import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  buildCoverageBundleReport,
  classifyNegativeEvidence,
  installCoverageBundleNetworkGuard,
  sanitizeCoverageEnvironment,
  validateCoverageBundleReport,
} from "./coverage-bundle.js";

test("coverage probe sanitizes Python, proxy, registry, and user-site environment", () => {
  const sanitized = sanitizeCoverageEnvironment({
    PATH: "fixed",
    PYTHONPATH: "poison",
    PythonPath: "poison-variant",
    PYTHONHOME: "poison",
    PYTHONSTARTUP: "poison",
    PIP_INDEX_URL: "https://registry.invalid/simple",
    HTTP_PROXY: "http://proxy.invalid",
    HTTPS_PROXY: "http://proxy.invalid",
    ALL_PROXY: "http://proxy.invalid",
    NO_PROXY: "*",
    PYTHONUSERBASE: "poison",
    VIRTUAL_ENV: "poison",
    CONDA_PREFIX: "poison",
    LANG: "C",
    LANGUAGE: "en_US",
    LC_ALL: "C.UTF-8",
    lC_CTYPE: "C.UTF-8",
    UNIT_TEST_IDE_COVERAGE_PROBE: "1",
  });
  assert.deepEqual(sanitized, {
    PATH: "fixed",
    UNIT_TEST_IDE_COVERAGE_PROBE: "1",
  });
});

test("coverage network guard blocks DNS, socket, HTTP and restores state", async () => {
  const restore = installCoverageBundleNetworkGuard();
  try {
    const dns = await import("node:dns");
    const net = await import("node:net");
    assert.throws(() => dns.lookup("example.invalid", () => undefined), /network guard/u);
    assert.throws(() => net.createConnection({ host: "example.invalid", port: 80 }), /network guard/u);
    assert.throws(() => fetch("https://example.invalid"), /network guard/u);
  } finally {
    restore();
  }
});

test("coverage report has provenance but never installation paths", () => {
  const report = buildCoverageBundleReport({
    manifestDigest: "a".repeat(64),
    sourceManifestDigest: "b".repeat(64),
    platform: "windows-x64",
    pythonVersion: "3.14.6",
    gcovrVersion: "8.6",
    licenses: ["PSF-2.0", "BSD-3-Clause"],
    smoke: { selfCheck: "passed", descriptor: "passed", negative: "rejected" },
  });
  validateCoverageBundleReport(report);
  assert.equal(JSON.stringify(report).includes("C:\\\\secret"), false);
  assert.equal(JSON.stringify(report).includes("/opt/coverage"), false);
  assert.throws(() => validateCoverageBundleReport({ ...report, installationPath: "/opt/coverage" } as never), /unexpected fields/u);
});

test("coverage report accepts an explicit environment-blocked runner outcome", () => {
  const report = buildCoverageBundleReport({
    manifestDigest: "c".repeat(64),
    sourceManifestDigest: "d".repeat(64),
    platform: "linux-x64",
    pythonVersion: "3.14.6",
    gcovrVersion: "8.6",
    licenses: ["PSF-2.0", "BSD-3-Clause"],
    smoke: { selfCheck: "failed", descriptor: "skipped", negative: "environment-blocked" },
  });
  validateCoverageBundleReport(report);
  assert.equal(report.smoke.negative, "environment-blocked");
});

test("negative evidence distinguishes a real rejection from a null status", () => {
  assert.equal(classifyNegativeEvidence({ status: "rejected", code: 2 }), "rejected");
  assert.equal(classifyNegativeEvidence({ status: "error", code: "ENOENT" }), "environment-blocked");
  assert.equal(classifyNegativeEvidence({ status: "error", code: "EPERM" }), "environment-blocked");
  assert.throws(() => classifyNegativeEvidence({ status: null }), /missing negative evidence/u);
  assert.throws(() => classifyNegativeEvidence({ status: "passed" }), /negative case was accepted/u);
});

test("report writer is atomic and only emits the stable schema", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "coverage-probe-report-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const report = buildCoverageBundleReport({
    manifestDigest: "b".repeat(64),
    sourceManifestDigest: "e".repeat(64),
    platform: "linux-x64",
    pythonVersion: "3.14.6",
    gcovrVersion: "8.6",
    licenses: ["PSF-2.0", "BSD-3-Clause"],
    smoke: { selfCheck: "passed", descriptor: "passed", negative: "rejected" },
  });
  const path = join(root, "coverage-bundle-report.json");
  const { writeCoverageBundleReport } = await import("./coverage-bundle.js");
  await writeCoverageBundleReport(path, report);
  assert.deepEqual(JSON.parse(await readFile(path, "utf8")), report);
  assert.deepEqual(await (await import("node:fs/promises")).readdir(root), ["coverage-bundle-report.json"]);
});
