import { spawnSync } from "node:child_process";

const tests = [
  "dist/coverage-bundle.test.js",
  "dist/native-fixture.test.js",
  "dist/native-build-linux.test.js",
  "dist/native-build-windows.test.js",
  "dist/native-network-guard.test.js",
  "dist/wfp-offline-boundary.test.js",
  "dist/wfp-offline-boundary.e2e.test.js",
  "dist/windows-offline-boundary-legacy.test.js",
  "dist/native-work-root.test.js",
  "dist/test-framework-fixture.test.js",
  "dist/linux-framework-inputs.test.js",
  "dist/native-framework-report.test.js",
  "dist/native-framework-matrix.test.js",
  "dist/native-framework-runtime-contract.test.js",
  "dist/native-framework-workspace.test.js",
  "dist/native-framework-prepare.test.js",
];

const requested = process.argv.slice(2).filter((value) => value !== "--");
let selected = tests;
if (requested.length > 0) {
  const nativeBuildPair = ["native-build-windows.test.ts", "native-build-linux.test.ts"];
  const focusedTests = new Map([
    ["native-framework-prepare.test.ts", "dist/native-framework-prepare.test.js"],
    ["dist/native-framework-prepare.test.js", "dist/native-framework-prepare.test.js"],
    ["native-framework-matrix.test.ts", "dist/native-framework-matrix.test.js"],
    ["dist/native-framework-matrix.test.js", "dist/native-framework-matrix.test.js"],
    ["native-framework-runtime-contract.test.ts", "dist/native-framework-runtime-contract.test.js"],
    ["dist/native-framework-runtime-contract.test.js", "dist/native-framework-runtime-contract.test.js"],
    ["native-framework-workspace.test.ts", "dist/native-framework-workspace.test.js"],
    ["dist/native-framework-workspace.test.js", "dist/native-framework-workspace.test.js"],
  ]);
  const focused = requested.length === 1 ? focusedTests.get(requested[0]) : undefined;
  if (focused !== undefined) {
    selected = [focused];
  } else if (requested.length === 2 && nativeBuildPair.every((value) => requested.includes(value))) {
    selected = ["dist/native-build-windows.test.js", "dist/native-build-linux.test.js"];
  } else {
    throw new Error("service-probe focused tests must be a known single test or the Windows/Linux native-build pair");
  }
}
const result = spawnSync(process.execPath, ["--test", ...selected], {
  cwd: import.meta.dirname,
  stdio: "inherit",
  windowsHide: true,
});
if (result.error) throw result.error;
process.exitCode = result.status ?? 1;
