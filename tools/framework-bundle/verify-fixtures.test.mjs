import assert from "node:assert/strict";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
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
  assert.deepEqual(parseVerifyFrameworkFixtureArguments([
    "--cmake", "C:/tools/cmake.exe", "--generator", "C:/tools/generator.exe", "--toolchains", "gcc", "--frameworks", "cpputest",
  ]).toolchains, ["gcc"]);
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

test("verifies Unity through the sealed list/run runner protocol", async () => {
  const temporary = await mkdtemp(join(tmpdir(), "utide-unity-verifier-"));
  const calls = [];
  const cases = [
    ["test_pass", "passed"],
    ["test_assertion_failure", "failed"],
    ["test_skipped", "skipped"],
    ["test_cmock_expectation_failure", "failed"],
    ["test_crash", "crash"],
    ["test_timeout", "timeout"],
  ];
  const record = (identity, status) => JSON.stringify({ magic: "unit-test-ide", protocol: "utide.runner.v1", record: "testFinished", identity, status });
  try {
    const fakeExecFile = async (_command, arguments_, options) => {
      calls.push({ arguments_, options });
      if (arguments_.includes("--build")) return { stdout: "", stderr: "" };
      const resultPath = arguments_[arguments_.indexOf("--utide-result") + 1];
      if (arguments_.includes("--utide-mode") && arguments_.includes("list")) {
        await writeFile(resultPath, cases.map(([identity]) => JSON.stringify({ magic: "unit-test-ide", protocol: "utide.runner.v1", record: "case", identity, case: identity })).join("\n") + "\n");
        return { stdout: "", stderr: "" };
      }
      if (arguments_.includes("--utide-mode") && arguments_.includes("run")) {
        const identity = arguments_[arguments_.indexOf("--utide-case") + 1];
        const status = new Map(cases).get(identity);
        if (identity === "test_crash") throw Object.assign(new Error("crashed"), { code: 3, stdout: "", stderr: "" });
        if (identity === "test_timeout") throw Object.assign(new Error("timed out"), { code: null, signal: "SIGTERM", killed: true, stdout: "", stderr: "" });
        await writeFile(resultPath, `${record(identity, status)}\n`);
        if (identity === "test_assertion_failure") throw Object.assign(new Error("assertion"), { code: 1, stdout: "FAIL: expected 1 was 2", stderr: "" });
        if (identity === "test_cmock_expectation_failure") throw Object.assign(new Error("cmock"), { code: 1, stdout: "CMock: Expected channel to be 7 Was 8", stderr: "" });
        return { stdout: identity === "test_skipped" ? "IGNORE: phase9 skip fixture" : "", stderr: "" };
      }
      if (arguments_.includes("-S")) {
        const build = arguments_[arguments_.indexOf("-B") + 1];
        const manifest = { cases: cases.map(([identity]) => ({ identity, name: identity })) };
        const manifestPath = join(build, ".unit-test-ide", "3599003af019a34669698d4cd38b175ce63ea767e834dc13cec3d415b1345988", "manifest.json");
        await mkdir(join(build, ".unit-test-ide", "3599003af019a34669698d4cd38b175ce63ea767e834dc13cec3d415b1345988"), { recursive: true });
        await writeFile(manifestPath, JSON.stringify(manifest));
      }
      return { stdout: "", stderr: "" };
    };
    const summary = await verifyFrameworkFixtures({
      repositoryRoot,
      cmake: "C:/tools/cmake.exe",
      generator: "C:/tools/unity-runner-generator.exe",
      toolchains: ["msvc"], frameworks: ["unity"], platform: "win32",
      fixtureBuildRoot: temporary,
      frameworkInputs: { unityRoot: "C:/frameworks/unity", cmockRoot: "C:/frameworks/cmock", helper: "C:/frameworks/UnitTestIDE.cmake" },
      environment: { PATH: "C:\\fixture-path-without-docker" },
      execFile: fakeExecFile,
    });
    assert.deepEqual(summary, [{ framework: "unity", toolchain: "msvc", scenarios: [
      { id: "pass", outcome: "passed" }, { id: "assertion-failure", outcome: "failed" }, { id: "skip", outcome: "skipped" },
      { id: "mock-failure", outcome: "mock-failure" }, { id: "crash", outcome: "crash" }, { id: "timeout", outcome: "timeout" },
    ] }]);
    const list = calls.find((call) => call.arguments_.includes("list"));
    assert.deepEqual(list.arguments_.slice(-6), ["--utide-protocol", "utide.runner.v1", "--utide-mode", "list", "--utide-result", list.arguments_.at(-1)]);
    assert.equal(calls.filter((call) => call.arguments_.includes("run")).length, 6);
    assert.ok(calls.filter((call) => call.arguments_.includes("run")).every((call) => call.arguments_.includes("--utide-case")));
    assert.ok(calls.every((call) => call.options.env?.PATH === "C:\\fixture-path-without-docker"));
    const forbidden = /ruby|ceedling|lib\/cmock\.rb|update-cmock-fixture|docker run/iu;
    assert.doesNotMatch(await readFile(join(repositoryRoot, "testdata/frameworks/unity/CMakeLists.txt"), "utf8"), forbidden);
    assert.ok(calls.every((call) => !call.arguments_.some((argument) => forbidden.test(argument))));
  } finally {
    await rm(temporary, { recursive: true, force: true });
  }
});

for (const [name, mutate, expected, stage = "list"] of [
  ["duplicate list identity", (records) => [...records, records[0]], /duplicate Unity list identity/u],
  ["missing list identity", (records) => records.slice(1), /Unity list records do not match fixture/u],
  ["malformed JSONL", () => ["not-json"], /malformed Unity runner JSONL/u],
  ["unknown result status", () => [{ magic: "unit-test-ide", protocol: "utide.runner.v1", record: "testFinished", identity: "test_pass", status: "unknown" }], /unknown Unity runner status/u, "run"],
  ["mismatched result identity", () => [{ magic: "unit-test-ide", protocol: "utide.runner.v1", record: "testFinished", identity: "other", status: "passed" }], /result identity does not match/u, "run"],
]) {
  test(`rejects ${name} from Unity runner protocol`, async () => {
    await assert.rejects(() => verifyFrameworkFixtures({
      repositoryRoot, cmake: "C:/tools/cmake.exe", generator: "C:/tools/generator.exe", toolchains: ["msvc"], frameworks: ["unity"], platform: "win32",
      frameworkInputs: { unityRoot: "C:/unity", cmockRoot: "C:/cmock", helper: "C:/helper" },
      execFile: async (_command, arguments_) => {
        if (arguments_.includes("--build")) return { stdout: "", stderr: "" };
        const resultPath = arguments_[arguments_.indexOf("--utide-result") + 1];
        if (arguments_.includes("-S")) {
          const build = arguments_[arguments_.indexOf("-B") + 1];
          const manifestPath = join(build, ".unit-test-ide", "3599003af019a34669698d4cd38b175ce63ea767e834dc13cec3d415b1345988", "manifest.json");
          await mkdir(join(build, ".unit-test-ide", "3599003af019a34669698d4cd38b175ce63ea767e834dc13cec3d415b1345988"), { recursive: true });
          await writeFile(manifestPath, JSON.stringify({ cases: [{ identity: "test_pass", name: "test_pass" }, { identity: "test_assertion_failure", name: "test_assertion_failure" }, { identity: "test_skipped", name: "test_skipped" }, { identity: "test_cmock_expectation_failure", name: "test_cmock_expectation_failure" }, { identity: "test_crash", name: "test_crash" }, { identity: "test_timeout", name: "test_timeout" }] }));
          return { stdout: "", stderr: "" };
        }
        if (arguments_.includes("list")) {
          const records = ["test_pass", "test_assertion_failure", "test_skipped", "test_cmock_expectation_failure", "test_crash", "test_timeout"].map((identity) => ({ magic: "unit-test-ide", protocol: "utide.runner.v1", record: "case", identity, case: identity }));
          await writeFile(resultPath, (stage === "list" ? mutate(records) : records).map((value) => typeof value === "string" ? value : JSON.stringify(value)).join("\n"));
          return { stdout: "", stderr: "" };
        }
        const record = { magic: "unit-test-ide", protocol: "utide.runner.v1", record: "testFinished", identity: arguments_[arguments_.indexOf("--utide-case") + 1], status: "passed" };
        await writeFile(resultPath, (stage === "run" ? mutate([]) : [record]).map((value) => JSON.stringify(value)).join("\n"));
        return { stdout: "", stderr: "" };
      },
    }), expected);
  });
}

test("fixture is closed, ordered, and never emits a P4 report", async () => {
  const fixture = JSON.parse(await readFile(resolve(repositoryRoot, "testdata/frameworks/cpputest/fixture.json"), "utf8"));
  assert.deepEqual(Object.keys(fixture), ["schemaVersion", "framework", "ctestName", "scenarios"]);
  assert.deepEqual(fixture.scenarios.map((scenario) => scenario.id), ["pass", "assertion-failure", "skip", "mock-missing-call", "mock-unexpected-call", "mock-parameter-mismatch", "crash", "timeout"]);
  assert.equal(fixture.scenarios.find((scenario) => scenario.id === "timeout").outcome, "timeout");
  const verifier = await readFile(resolve(repositoryRoot, "tools/framework-bundle/verify-fixtures.mjs"), "utf8");
  assert.doesNotMatch(verifier, /framework-report\.json/u);
});
