import { execFile as execFileCallback } from "node:child_process";
import { mkdir, readFile, rm } from "node:fs/promises";
import { dirname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { readFrameworkManifest } from "./manifest.mjs";

const defaultExecFile = promisify(execFileCallback);
const toolDirectory = dirname(fileURLToPath(import.meta.url));
const defaultRepositoryRoot = resolve(toolDirectory, "..", "..");
const configureTimeout = 120_000;
const scenarioTimeout = 10_000;
const fixtureFrameworks = new Set(["cpputest", "unity"]);
const fixtureToolchains = new Set(["msvc", "clang-cl", "gcc"]);
class FixtureError extends Error {
  constructor(message) { super(message); this.code = "FRAMEWORK_FIXTURE_VALIDATION_FAILED"; }
}
function crashEvidence(result, platform) {
  if (result.killed || (result.error && typeof result.error.code === "string")) return false;
  return ["SIGABRT", "SIGSEGV", "SIGILL", "SIGFPE", "SIGBUS"].includes(result.signal)
    || (platform === "win32" && Number.isInteger(result.code) && [3, 0xc0000005, 0xc0000409, 0x40000015].includes(result.code >>> 0));
}

function usage() {
  throw new FixtureError("usage: verify-fixtures.mjs --cmake <absolute-path> --generator <absolute-path> --toolchains <msvc,clang-cl|gcc> --frameworks <cpputest,unity>");
}

function absolute(value) {
  return typeof value === "string" && value.length > 0 && !value.includes("\0") && (isAbsolute(value) || /^[A-Za-z]:[\\/]/u.test(value));
}

function csv(value, allowed) {
  if (typeof value !== "string" || value.length === 0) usage();
  const entries = value.split(",");
  if (entries.some((entry) => !allowed.has(entry)) || new Set(entries).size !== entries.length) usage();
  return entries;
}

export function parseVerifyFrameworkFixtureArguments(arguments_) {
  if (Array.isArray(arguments_) && arguments_[0] === "--") arguments_ = arguments_.slice(1);
  if (!Array.isArray(arguments_) || arguments_.length !== 8 || arguments_[0] !== "--cmake" || arguments_[2] !== "--generator" || arguments_[4] !== "--toolchains" || arguments_[6] !== "--frameworks") usage();
  const [cmake, generator, toolchains, frameworks] = [arguments_[1], arguments_[3], arguments_[5], arguments_[7]];
  if (!absolute(cmake) || !absolute(generator)) usage();
  return { cmake, generator, toolchains: csv(toolchains, fixtureToolchains), frameworks: csv(frameworks, fixtureFrameworks) };
}

function supportedToolchain(toolchain, platform) {
  if (platform === "win32" && toolchain === "gcc") throw new FixtureError("gcc is not supported on Windows framework fixtures");
  if (platform === "linux" && (toolchain === "msvc" || toolchain === "clang-cl")) throw new FixtureError("Windows toolchain is not supported on Linux framework fixtures");
  if (platform !== "win32" && platform !== "linux") throw new FixtureError("unsupported framework fixture platform");
}

function cmakeGenerator(toolchain) {
  return toolchain === "msvc" ? "Visual Studio 17 2022" : "Ninja";
}

function cmakeCompilerArguments(toolchain) {
  if (toolchain === "clang-cl") return ["-DCMAKE_C_COMPILER=clang-cl", "-DCMAKE_CXX_COMPILER=clang-cl"];
  if (toolchain === "gcc") return ["-DCMAKE_C_COMPILER=gcc", "-DCMAKE_CXX_COMPILER=g++"];
  return [];
}

function commandOptions(timeout, environment) {
  // Only our spawn-aware deadline may classify a timeout, not execFile's killed flag.
  return { shell: false, windowsHide: true, timeout: 0, deadlineMs: timeout, maxBuffer: 8 * 1024 * 1024, ...(environment ? { env: environment } : {}) };
}

async function invoke(execFile, command, arguments_, timeout, environment) {
  let timer;
  let timedOut = false;
  let settled = false;
  try {
    const pending = execFile(command, arguments_, commandOptions(timeout, environment));
    pending.child?.once("spawn", () => {
      if (settled) return;
      timer = setTimeout(() => {
        if (!settled && pending.child.exitCode == null && pending.child.signalCode == null) {
          timedOut = true;
          pending.child.kill();
        }
      }, timeout);
    });
    const result = await pending;
    return { code: 0, signal: null, killed: false, timedOut, stdout: result.stdout ?? "", stderr: result.stderr ?? "" };
  } catch (error) {
    return { code: typeof error.code === "number" ? error.code : null, signal: error.signal ?? null, killed: error.killed === true, timedOut, stdout: error.stdout ?? "", stderr: error.stderr ?? "", error };
  } finally {
    settled = true;
    clearTimeout(timer);
  }
}

export const __testing = { invoke };

function resultText(result) { return `${result.stdout}\n${result.stderr}`; }

function assertScenarioResult(scenario, result, platform) {
  const text = resultText(result);
  const nonzero = result.code !== 0 || result.signal !== null;
  const valid = scenario.outcome === "passed" ? result.code === 0
    : scenario.outcome === "failed" ? nonzero && /(CHECK|assert|FAIL)/iu.test(text)
      : scenario.outcome === "skipped" ? result.code === 0 && /IGNORE/iu.test(text)
        : scenario.outcome === "mock-failure" ? nonzero && /(mock|unexpected|parameter|expected)/iu.test(text)
          : scenario.outcome === "crash" ? crashEvidence(result, platform)
            : scenario.outcome === "timeout" ? result.timedOut
              : false;
  if (!valid) throw new FixtureError("CppUTest scenario did not produce its contracted outcome");
}

async function defaultFrameworkInputs(repositoryRoot) {
  const { manifest, manifestSha256 } = await readFrameworkManifest(join(repositoryRoot, "tools", "framework-bundle", "manifest.json"));
  const root = join(repositoryRoot, ".superpowers", "runtime", "framework-bundle", "v2", manifestSha256);
  const frameworkRoot = (id) => {
    const framework = manifest.frameworks.find((candidate) => candidate.id === id);
    if (!framework) throw new FixtureError("required framework is absent from the manifest");
    return join(root, framework.sourceDirectory);
  };
  return {
    cpputestRoot: frameworkRoot("cpputest"),
    unityRoot: frameworkRoot("unity"),
    cmockRoot: frameworkRoot("cmock"),
    helper: join(repositoryRoot, manifest.fixtureTools.cmakeHelper.path),
  };
}

async function readCppUTestFixture(repositoryRoot) {
  const path = join(repositoryRoot, "testdata", "frameworks", "cpputest", "fixture.json");
  const fixture = JSON.parse(await readFile(path, "utf8"));
  if (fixture.schemaVersion !== 1 || fixture.framework !== "cpputest" || fixture.ctestName !== "cpputest.framework" || !Array.isArray(fixture.scenarios) || fixture.scenarios.length !== 8) throw new FixtureError("CppUTest fixture contract is invalid");
  return fixture;
}

async function verifyCppUTestFixture(options, toolchain, inputs) {
  const fixture = await readCppUTestFixture(options.repositoryRoot);
  const fixtureDirectory = join(options.repositoryRoot, "testdata", "frameworks", "cpputest");
  const buildDirectory = join(options.repositoryRoot, ".superpowers", "runtime", "framework-fixtures", toolchain, "cpputest");
  const configure = ["-S", fixtureDirectory, "-B", buildDirectory, "-G", cmakeGenerator(toolchain), "-DCMAKE_POLICY_VERSION_MINIMUM=3.5", `-DUNIT_TEST_IDE_CPPUTEST_ROOT=${inputs.cpputestRoot}`, `-DUNIT_TEST_IDE_HELPER=${inputs.helper}`, ...cmakeCompilerArguments(toolchain)];
  if (toolchain === "msvc") configure.push("-A", "x64");
  const configured = await invoke(options.execFile, options.cmake, configure, configureTimeout);
  if (configured.code !== 0) throw new FixtureError("CppUTest configure failed");
  const built = await invoke(options.execFile, options.cmake, ["--build", buildDirectory, "--config", "Debug"], configureTimeout);
  if (built.code !== 0) throw new FixtureError("CppUTest build failed");
  const executable = join(buildDirectory, "bin", options.platform === "win32" ? "phase9_cpputest.exe" : "phase9_cpputest");
  const scenarios = [];
  for (const scenario of fixture.scenarios) {
    const arguments_ = ["-g", "Phase9", "-n", scenario.name];
    const result = await invoke(options.execFile, executable, arguments_, scenario.outcome === "timeout" ? 1_000 : scenarioTimeout);
    assertScenarioResult(scenario, result, options.platform);
    scenarios.push({ id: scenario.id, outcome: scenario.outcome });
  }
  return { framework: "cpputest", toolchain, scenarios };
}

const unityManifestDirectory = join(".unit-test-ide", "3599003af019a34669698d4cd38b175ce63ea767e834dc13cec3d415b1345988");
const runnerProtocol = "utide.runner.v1";
const runnerStatuses = new Set(["passed", "failed", "skipped"]);

function containedPath(root, path) {
  const resolvedRoot = resolve(root);
  const resolvedPath = resolve(path);
  const pathRelative = relative(resolvedRoot, resolvedPath);
  return pathRelative === "" || (!pathRelative.startsWith(`..${sep}`) && pathRelative !== ".." && !isAbsolute(pathRelative));
}

export function controlledUnityResultPath(root, name) {
  const output = resolve(root, name);
  if (!containedPath(root, output)) throw new FixtureError("Unity runner result file escapes its controlled directory");
  return output;
}

async function readJsonl(path, kind) {
  let text;
  try {
    text = await readFile(path, "utf8");
  } catch (error) {
    if (error?.code === "ENOENT") return [];
    throw error;
  }
  if (text.length === 0) return [];
  const lines = text.endsWith("\n") ? text.slice(0, -1).split("\n") : text.split("\n");
  if (lines.some((line) => line.length === 0)) throw new FixtureError("malformed Unity runner JSONL");
  return lines.map((line) => {
    try {
      const record = JSON.parse(line);
      if (!record || typeof record !== "object" || Array.isArray(record) || record.magic !== "unit-test-ide" || record.protocol !== runnerProtocol || record.record !== kind || typeof record.identity !== "string" || record.identity.length === 0) throw new FixtureError("invalid record");
      return record;
    } catch {
      throw new FixtureError("malformed Unity runner JSONL");
    }
  });
}

async function readUnityFixture(repositoryRoot) {
  const fixture = JSON.parse(await readFile(join(repositoryRoot, "testdata", "frameworks", "unity", "fixture.json"), "utf8"));
  if (fixture.schemaVersion !== 1 || fixture.framework !== "unity" || fixture.ctestName !== "unity.framework" || !Array.isArray(fixture.scenarios) || fixture.scenarios.length !== 6) throw new FixtureError("Unity fixture contract is invalid");
  const expected = [
    ["pass", "test_pass", "passed"], ["assertion-failure", "test_assertion_failure", "failed"], ["skip", "test_skipped", "skipped"],
    ["mock-failure", "test_cmock_expectation_failure", "mock-failure"], ["crash", "test_crash", "crash"], ["timeout", "test_timeout", "timeout"],
  ];
  if (fixture.scenarios.some((scenario, index) => scenario.id !== expected[index][0] || scenario.name !== expected[index][1] || scenario.outcome !== expected[index][2])) throw new FixtureError("Unity fixture contract is invalid");
  return fixture;
}

async function verifyUnityManifest(buildDirectory, fixture) {
  let manifest;
  try {
    manifest = JSON.parse(await readFile(join(buildDirectory, unityManifestDirectory, "manifest.json"), "utf8"));
  } catch (error) {
    throw new FixtureError("Unity runner manifest is invalid");
  }
  if (!Array.isArray(manifest.cases)) throw new FixtureError("Unity runner manifest is invalid");
  const expected = fixture.scenarios.map((scenario) => scenario.name);
  const actual = manifest.cases.map((testCase) => testCase?.identity);
  if (actual.length !== expected.length || new Set(actual).size !== actual.length || actual.some((identity) => !expected.includes(identity)) || manifest.cases.some((testCase) => testCase?.name !== testCase?.identity)) throw new FixtureError("Unity runner manifest does not match fixture");
  return actual;
}

function validateListRecords(records, identities) {
  const actual = records.map((record) => record.identity);
  if (new Set(actual).size !== actual.length) throw new FixtureError("duplicate Unity list identity");
  if (records.some((record) => typeof record.case !== "string" || record.case !== record.identity) || actual.length !== identities.length || actual.some((identity, index) => identity !== identities[index])) throw new FixtureError("Unity list records do not match fixture");
}

function completeResult(records, requestedIdentity) {
  if (records.length !== 1) return null;
  const [record] = records;
  if (record.identity !== requestedIdentity) throw new FixtureError("Unity runner result identity does not match requested identity");
  if (!runnerStatuses.has(record.status)) throw new FixtureError("unknown Unity runner status");
  return record;
}

async function verifyUnityScenario(options, executable, resultDirectory, scenario) {
  const output = controlledUnityResultPath(resultDirectory, `${scenario.name}.jsonl`);
  const invocation = await invoke(options.execFile, executable, [
    "--utide-protocol", runnerProtocol, "--utide-mode", "run", "--utide-case", scenario.name, "--utide-result", output,
  ], scenario.outcome === "timeout" ? 1_000 : scenarioTimeout, options.environment);
  const records = await readJsonl(output, "testFinished");
  const record = completeResult(records, scenario.name);
  if (scenario.outcome === "crash") {
    if (record !== null || !crashEvidence(invocation, options.platform)) throw new FixtureError("Unity crash scenario lacks abnormal termination evidence");
    return;
  }
  if (scenario.outcome === "timeout") {
    if (!invocation.timedOut || record !== null) throw new FixtureError("Unity timeout scenario did not reach the verifier deadline");
    return;
  }
  if (record === null) throw new FixtureError("Unity scenario did not publish a complete result");
  const expectedStatus = scenario.outcome === "mock-failure" ? "failed" : scenario.outcome;
  if (record.status !== expectedStatus) throw new FixtureError("Unity scenario produced an unexpected status");
  if (scenario.outcome === "mock-failure" && !/(cmock|mismatch|expected|was)/iu.test(resultText(invocation))) throw new FixtureError("Unity mock-failure scenario lacks CMock mismatch evidence");
}

async function verifyUnityFixture(options, toolchain, inputs) {
  const fixture = await readUnityFixture(options.repositoryRoot);
  const fixtureDirectory = join(options.repositoryRoot, "testdata", "frameworks", "unity");
  const buildRoot = resolve(options.fixtureBuildRoot ?? join(options.repositoryRoot, ".superpowers", "runtime", "framework-fixtures"));
  const buildDirectory = join(buildRoot, toolchain, "unity");
  const resultDirectory = join(buildDirectory, "runner-results");
  await rm(buildDirectory, { recursive: true, force: true });
  await mkdir(resultDirectory, { recursive: true });
  const configure = ["-S", fixtureDirectory, "-B", buildDirectory, "-G", cmakeGenerator(toolchain), "-DCMAKE_POLICY_VERSION_MINIMUM=3.5", `-DUNIT_TEST_IDE_UNITY_ROOT=${inputs.unityRoot}`, `-DUNIT_TEST_IDE_CMOCK_ROOT=${inputs.cmockRoot}`, `-DUNIT_TEST_IDE_HELPER=${inputs.helper}`, `-DUTIDE_UNITY_RUNNER_GENERATOR=${options.generator}`, ...cmakeCompilerArguments(toolchain)];
  if (toolchain === "msvc") configure.push("-A", "x64");
  const configured = await invoke(options.execFile, options.cmake, configure, configureTimeout, options.environment);
  if (configured.code !== 0) throw new FixtureError("Unity configure failed");
  const built = await invoke(options.execFile, options.cmake, ["--build", buildDirectory, "--config", "Debug"], configureTimeout, options.environment);
  if (built.code !== 0) throw new FixtureError("Unity build failed");
  const identities = await verifyUnityManifest(buildDirectory, fixture);
  const executable = join(buildDirectory, "bin", options.platform === "win32" ? "phase9_unity.exe" : "phase9_unity");
  const listOutput = controlledUnityResultPath(resultDirectory, "list.jsonl");
  const listed = await invoke(options.execFile, executable, ["--utide-protocol", runnerProtocol, "--utide-mode", "list", "--utide-result", listOutput], scenarioTimeout, options.environment);
  if (listed.code !== 0) throw new FixtureError("Unity list failed");
  validateListRecords(await readJsonl(listOutput, "case"), identities);
  for (const scenario of fixture.scenarios) await verifyUnityScenario(options, executable, resultDirectory, scenario);
  return { framework: "unity", toolchain, scenarios: fixture.scenarios.map((scenario) => ({ id: scenario.id, outcome: scenario.outcome })) };
}

async function verifyFrameworkFixturesInternal(options = {}) {
  const repositoryRoot = resolve(options.repositoryRoot ?? defaultRepositoryRoot);
  const platform = options.platform ?? process.platform;
  if (!absolute(options.cmake) || !absolute(options.generator) || !Array.isArray(options.toolchains) || !Array.isArray(options.frameworks)) usage();
  const inputs = { ...await defaultFrameworkInputs(repositoryRoot), ...(options.frameworkInputs ?? {}) };
  if (!absolute(inputs.cpputestRoot) || !absolute(inputs.unityRoot) || !absolute(inputs.cmockRoot) || !absolute(inputs.helper)) throw new FixtureError("framework fixture inputs must be absolute paths");
  const execFile = options.execFile ?? defaultExecFile;
  const results = [];
  for (const toolchain of options.toolchains) {
    if (!fixtureToolchains.has(toolchain)) usage();
    supportedToolchain(toolchain, platform);
    const fixtureOptions = { ...options, repositoryRoot, platform, execFile };
    for (const framework of options.frameworks) {
      results.push(framework === "cpputest" ? await verifyCppUTestFixture(fixtureOptions, toolchain, inputs) : await verifyUnityFixture(fixtureOptions, toolchain, inputs));
    }
  }
  return results;
}

export async function verifyFrameworkFixtures(options = {}) {
  try { return await verifyFrameworkFixturesInternal(options); }
  catch (error) { if (error instanceof FixtureError) throw error; throw new FixtureError("framework fixture inputs or native results could not be inspected"); }
}

if (process.argv[1] && resolve(process.argv[1]) === resolve(fileURLToPath(import.meta.url))) {
  Promise.resolve().then(() => verifyFrameworkFixtures({ ...parseVerifyFrameworkFixtureArguments(process.argv.slice(2)) }))
    .then((summary) => process.stdout.write(`${JSON.stringify(summary)}\n`))
    .catch((error) => { process.stderr.write(`verify-fixtures: ${error.code}: ${error.message}\n`); process.exitCode = 1; });
}
