import { spawnSync } from "node:child_process";

const tests = [
  "dist/coverage-bundle.test.js",
  "dist/native-fixture.test.js",
  "dist/native-build-linux.test.js",
  "dist/native-network-guard.test.js",
  "dist/wfp-offline-boundary.test.js",
  "dist/wfp-offline-boundary.e2e.test.js",
  "dist/windows-offline-boundary-legacy.test.js",
  "dist/native-work-root.test.js",
  "dist/test-framework-fixture.test.js",
  "dist/linux-framework-inputs.test.js",
  "dist/native-framework-report.test.js",
  "dist/native-framework-matrix.test.js",
];

const requested = process.argv.slice(2).filter((value) => value !== "--");
let selected = tests;
if (requested.length > 0) {
  if (requested.length !== 1 || !["native-framework-matrix.test.ts", "dist/native-framework-matrix.test.js"].includes(requested[0])) {
    throw new Error("service-probe test accepts only native-framework-matrix.test.ts as a focused test");
  }
  selected = ["dist/native-framework-matrix.test.js"];
}
const result = spawnSync(process.execPath, ["--test", ...selected], {
  cwd: import.meta.dirname,
  stdio: "inherit",
  windowsHide: true,
});
if (result.error) throw result.error;
process.exitCode = result.status ?? 1;
