import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { resolve } from "node:path";
import { parseVerifyFrameworkFixtureArguments, verifyFrameworkFixtures } from "./verify-fixtures.mjs";

const repositoryRoot = resolve(import.meta.dirname, "..", "..");

test("parses the closed framework fixture CLI", () => {
  assert.deepEqual(parseVerifyFrameworkFixtureArguments([
    "--cmake", "C:/tools/cmake.exe",
    "--generator", "C:/tools/unity-runner-generator.exe",
    "--toolchains", "msvc,clang-cl",
    "--frameworks", "cpputest,unity",
  ]), {
    cmake: "C:/tools/cmake.exe",
    generator: "C:/tools/unity-runner-generator.exe",
    toolchains: ["msvc", "clang-cl"],
    frameworks: ["cpputest", "unity"],
  });
  assert.throws(() => parseVerifyFrameworkFixtureArguments(["--cmake", "relative"]), /usage/u);
  assert.throws(() => parseVerifyFrameworkFixtureArguments([
    "--cmake", "C:/tools/cmake.exe", "--generator", "C:/tools/generator.exe", "--toolchains", "gcc", "--frameworks", "cpputest",
  ]), /usage/u);
});

test("plans a Windows MSVC fixture build and classifies every CppUTest scenario", async () => {
  const calls = [];
  const fakeExecFile = async (command, arguments_, options) => {
    calls.push({ command, arguments_, options });
    if (arguments_.includes("AssertionFailure")) throw Object.assign(new Error("failed"), { code: 1, stdout: "CHECK_EQUAL(1, 2) failed\n", stderr: "" });
    if (arguments_.includes("Skipped")) return { stdout: "IGNORED\n", stderr: "" };
    if (arguments_.some((argument) => /Mock(?:MissingCall|UnexpectedCall|ParameterMismatch)/u.test(argument))) throw Object.assign(new Error("mock failed"), { code: 1, stdout: "Mock Failure\n", stderr: "" });
    if (arguments_.includes("Crash")) throw Object.assign(new Error("crashed"), { code: 3, stdout: "", stderr: "" });
    if (arguments_.includes("Timeout")) throw Object.assign(new Error("timed out"), { code: null, signal: "SIGTERM", killed: true, stdout: "", stderr: "" });
    return { stdout: "OK\n", stderr: "" };
  };

  const summary = await verifyFrameworkFixtures({
    repositoryRoot,
    cmake: "C:/tools/cmake.exe",
    generator: "C:/tools/unity-runner-generator.exe",
    toolchains: ["msvc"],
    frameworks: ["cpputest"],
    platform: "win32",
    frameworkInputs: {
      cpputestRoot: "C:/frameworks/cpputest",
      helper: "C:/frameworks/UnitTestIDE.cmake",
    },
    execFile: fakeExecFile,
  });

  assert.deepEqual(summary, [{
    framework: "cpputest",
    toolchain: "msvc",
    scenarios: [
      { id: "pass", outcome: "passed" },
      { id: "assertion-failure", outcome: "failed" },
      { id: "skip", outcome: "skipped" },
      { id: "mock-missing-call", outcome: "mock-failure" },
      { id: "mock-unexpected-call", outcome: "mock-failure" },
      { id: "mock-parameter-mismatch", outcome: "mock-failure" },
      { id: "crash", outcome: "crash" },
      { id: "timeout", outcome: "timeout" },
    ],
  }]);
  const configure = calls.find((call) => call.arguments_.includes("Visual Studio 17 2022"));
  assert.ok(configure);
  assert.deepEqual(configure.arguments_.slice(-2), ["-A", "x64"]);
  assert.ok(configure.arguments_.includes("-DCMAKE_POLICY_VERSION_MINIMUM=3.5"));
  assert.equal(configure.options.timeout, 120_000);
  assert.ok(calls.filter((call) => call.arguments_.includes("--build")).every((call) => call.options.timeout === 120_000));
  const timeout = calls.find((call) => call.arguments_.includes("Timeout"));
  assert.equal(timeout.options.timeout, 1_000);
  assert.ok(calls.find((call) => call.arguments_.includes("Crash")).arguments_.includes("-p"));
  assert.ok(calls.filter((call) => call.arguments_.includes("Pass")).every((call) => call.options.timeout === 10_000));
});

test("plans clang-cl and Linux GCC compilers and rejects incompatible toolchains", async () => {
  const calls = [];
  const fakeExecFile = async (_command, arguments_, options) => {
    calls.push({ arguments_, options });
    if (arguments_.includes("AssertionFailure")) throw Object.assign(new Error("failed"), { code: 1, stdout: "CHECK failed", stderr: "" });
    if (arguments_.some((argument) => /Mock/u.test(argument))) throw Object.assign(new Error("mock failed"), { code: 1, stdout: "Mock Failure", stderr: "" });
    if (arguments_.includes("Crash")) throw Object.assign(new Error("crashed"), { code: 3, stdout: "", stderr: "" });
    if (arguments_.includes("Timeout")) throw Object.assign(new Error("timed out"), { code: null, signal: "SIGTERM", killed: true, stdout: "", stderr: "" });
    return { stdout: arguments_.includes("Skipped") ? "IGNORED" : "OK", stderr: "" };
  };
  await verifyFrameworkFixtures({ repositoryRoot, cmake: "/tools/cmake", generator: "/tools/generator", toolchains: ["clang-cl"], frameworks: ["cpputest"], platform: "win32", frameworkInputs: { cpputestRoot: "/frameworks/cpputest", helper: "/frameworks/helper.cmake" }, execFile: fakeExecFile });
  const clangConfigure = calls.find((call) => call.arguments_.includes("Ninja"));
  assert.ok(clangConfigure.arguments_.includes("-DCMAKE_C_COMPILER=clang-cl"));
  assert.ok(clangConfigure.arguments_.includes("-DCMAKE_CXX_COMPILER=clang-cl"));
  await assert.rejects(() => verifyFrameworkFixtures({ repositoryRoot, cmake: "/tools/cmake", generator: "/tools/generator", toolchains: ["gcc"], frameworks: ["cpputest"], platform: "win32", frameworkInputs: { cpputestRoot: "/frameworks/cpputest", helper: "/frameworks/helper.cmake" }, execFile: fakeExecFile }), /not supported/u);

  calls.length = 0;
  await verifyFrameworkFixtures({ repositoryRoot, cmake: "/tools/cmake", generator: "/tools/generator", toolchains: ["gcc"], frameworks: ["cpputest"], platform: "linux", frameworkInputs: { cpputestRoot: "/frameworks/cpputest", helper: "/frameworks/helper.cmake" }, execFile: fakeExecFile });
  const gccConfigure = calls.find((call) => call.arguments_.includes("Ninja"));
  assert.ok(gccConfigure.arguments_.includes("-DCMAKE_C_COMPILER=gcc"));
  assert.ok(gccConfigure.arguments_.includes("-DCMAKE_CXX_COMPILER=g++"));
  await assert.rejects(() => verifyFrameworkFixtures({ repositoryRoot, cmake: "/tools/cmake", generator: "/tools/generator", toolchains: ["msvc"], frameworks: ["cpputest"], platform: "linux", frameworkInputs: { cpputestRoot: "/frameworks/cpputest", helper: "/frameworks/helper.cmake" }, execFile: fakeExecFile }), /not supported/u);
});

test("fixture is closed, ordered, and never emits a P4 report", async () => {
  const fixture = JSON.parse(await readFile(resolve(repositoryRoot, "testdata/frameworks/cpputest/fixture.json"), "utf8"));
  assert.deepEqual(Object.keys(fixture), ["schemaVersion", "framework", "ctestName", "scenarios"]);
  assert.deepEqual(fixture.scenarios.map((scenario) => scenario.id), ["pass", "assertion-failure", "skip", "mock-missing-call", "mock-unexpected-call", "mock-parameter-mismatch", "crash", "timeout"]);
  assert.equal(fixture.scenarios.find((scenario) => scenario.id === "timeout").outcome, "timeout");
  const verifier = await readFile(resolve(repositoryRoot, "tools/framework-bundle/verify-fixtures.mjs"), "utf8");
  assert.doesNotMatch(verifier, /framework-report\.json/u);
});
