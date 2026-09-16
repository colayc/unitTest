import { execFile as execFileCallback } from "node:child_process";
import { readFile } from "node:fs/promises";
import { dirname, isAbsolute, join, resolve } from "node:path";
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

function usage() {
  throw new Error("usage: verify-fixtures.mjs --cmake <absolute-path> --generator <absolute-path> --toolchains <msvc,clang-cl|gcc> --frameworks <cpputest,unity>");
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
  if (!Array.isArray(arguments_) || arguments_.length !== 8 || arguments_[0] !== "--cmake" || arguments_[2] !== "--generator" || arguments_[4] !== "--toolchains" || arguments_[6] !== "--frameworks") usage();
  const [cmake, generator, toolchains, frameworks] = [arguments_[1], arguments_[3], arguments_[5], arguments_[7]];
  if (!absolute(cmake) || !absolute(generator)) usage();
  return { cmake, generator, toolchains: csv(toolchains, new Set(["msvc", "clang-cl"])), frameworks: csv(frameworks, fixtureFrameworks) };
}

function supportedToolchain(toolchain, platform) {
  if (platform === "win32" && toolchain === "gcc") throw new Error("gcc is not supported on Windows framework fixtures");
  if (platform === "linux" && (toolchain === "msvc" || toolchain === "clang-cl")) throw new Error(`${toolchain} is not supported on Linux framework fixtures`);
  if (platform !== "win32" && platform !== "linux") throw new Error(`unsupported framework fixture platform: ${platform}`);
}

function cmakeGenerator(toolchain) {
  return toolchain === "msvc" ? "Visual Studio 17 2022" : "Ninja";
}

function cmakeCompilerArguments(toolchain) {
  if (toolchain === "clang-cl") return ["-DCMAKE_C_COMPILER=clang-cl", "-DCMAKE_CXX_COMPILER=clang-cl"];
  if (toolchain === "gcc") return ["-DCMAKE_C_COMPILER=gcc", "-DCMAKE_CXX_COMPILER=g++"];
  return [];
}

function commandOptions(timeout) {
  return { shell: false, windowsHide: true, timeout, maxBuffer: 8 * 1024 * 1024 };
}

async function invoke(execFile, command, arguments_, timeout) {
  try {
    const result = await execFile(command, arguments_, commandOptions(timeout));
    return { code: 0, signal: null, killed: false, stdout: result.stdout ?? "", stderr: result.stderr ?? "" };
  } catch (error) {
    return { code: typeof error.code === "number" ? error.code : null, signal: error.signal ?? null, killed: error.killed === true, stdout: error.stdout ?? "", stderr: error.stderr ?? "", error };
  }
}

function resultText(result) { return `${result.stdout}\n${result.stderr}`; }

function assertScenarioResult(scenario, result) {
  const text = resultText(result);
  const nonzero = result.code !== 0 || result.signal !== null;
  const valid = scenario.outcome === "passed" ? result.code === 0
    : scenario.outcome === "failed" ? nonzero && /(CHECK|assert|FAIL)/iu.test(text)
      : scenario.outcome === "skipped" ? result.code === 0 && /IGNORE/iu.test(text)
        : scenario.outcome === "mock-failure" ? nonzero && /(mock|unexpected|parameter|expected)/iu.test(text)
          : scenario.outcome === "crash" ? nonzero && !result.killed
            : scenario.outcome === "timeout" ? result.killed
              : false;
  if (!valid) throw new Error(`CppUTest scenario ${scenario.id} did not produce ${scenario.outcome}`);
}

async function defaultFrameworkInputs(repositoryRoot) {
  const { manifest, manifestSha256 } = await readFrameworkManifest(join(repositoryRoot, "tools", "framework-bundle", "manifest.json"));
  const root = join(repositoryRoot, ".superpowers", "runtime", "framework-bundle", "v2", manifestSha256);
  const cpputest = manifest.frameworks.find((framework) => framework.id === "cpputest");
  if (!cpputest) throw new Error("CppUTest is absent from the framework manifest");
  return { cpputestRoot: join(root, cpputest.sourceDirectory), helper: join(repositoryRoot, manifest.fixtureTools.cmakeHelper.path) };
}

async function readCppUTestFixture(repositoryRoot) {
  const path = join(repositoryRoot, "testdata", "frameworks", "cpputest", "fixture.json");
  const fixture = JSON.parse(await readFile(path, "utf8"));
  if (fixture.schemaVersion !== 1 || fixture.framework !== "cpputest" || fixture.ctestName !== "cpputest.framework" || !Array.isArray(fixture.scenarios) || fixture.scenarios.length !== 8) throw new Error("CppUTest fixture contract is invalid");
  return fixture;
}

async function verifyCppUTestFixture(options, toolchain, inputs) {
  const fixture = await readCppUTestFixture(options.repositoryRoot);
  const fixtureDirectory = join(options.repositoryRoot, "testdata", "frameworks", "cpputest");
  const buildDirectory = join(options.repositoryRoot, ".superpowers", "runtime", "framework-fixtures", toolchain, "cpputest");
  const configure = ["-S", fixtureDirectory, "-B", buildDirectory, "-G", cmakeGenerator(toolchain), "-DCMAKE_POLICY_VERSION_MINIMUM=3.5", `-DUNIT_TEST_IDE_CPPUTEST_ROOT=${inputs.cpputestRoot}`, `-DUNIT_TEST_IDE_HELPER=${inputs.helper}`, ...cmakeCompilerArguments(toolchain)];
  if (toolchain === "msvc") configure.push("-A", "x64");
  const configured = await invoke(options.execFile, options.cmake, configure, configureTimeout);
  if (configured.code !== 0) throw new Error(`CppUTest configure failed: ${resultText(configured)}`);
  const built = await invoke(options.execFile, options.cmake, ["--build", buildDirectory, "--config", "Debug"], configureTimeout);
  if (built.code !== 0) throw new Error(`CppUTest build failed: ${resultText(built)}`);
  const executable = join(buildDirectory, "bin", options.platform === "win32" ? "phase9_cpputest.exe" : "phase9_cpputest");
  const scenarios = [];
  for (const scenario of fixture.scenarios) {
    const arguments_ = ["-g", "Phase9", "-n", scenario.name];
    if (scenario.outcome === "crash") arguments_.push("-p");
    const result = await invoke(options.execFile, executable, arguments_, scenario.outcome === "timeout" ? 1_000 : scenarioTimeout);
    assertScenarioResult(scenario, result);
    scenarios.push({ id: scenario.id, outcome: scenario.outcome });
  }
  return { framework: "cpputest", toolchain, scenarios };
}

export async function verifyFrameworkFixtures(options = {}) {
  const repositoryRoot = resolve(options.repositoryRoot ?? defaultRepositoryRoot);
  const platform = options.platform ?? process.platform;
  if (!absolute(options.cmake) || !absolute(options.generator) || !Array.isArray(options.toolchains) || !Array.isArray(options.frameworks)) usage();
  if (options.frameworks.some((framework) => framework !== "cpputest")) throw new Error("Unity fixture verification is not available yet");
  const inputs = { ...await defaultFrameworkInputs(repositoryRoot), ...(options.frameworkInputs ?? {}) };
  if (!absolute(inputs.cpputestRoot) || !absolute(inputs.helper)) throw new Error("CppUTest fixture inputs must be absolute paths");
  const execFile = options.execFile ?? defaultExecFile;
  const results = [];
  for (const toolchain of options.toolchains) {
    if (!fixtureToolchains.has(toolchain)) usage();
    supportedToolchain(toolchain, platform);
    results.push(await verifyCppUTestFixture({ ...options, repositoryRoot, platform, execFile }, toolchain, inputs));
  }
  return results;
}

if (process.argv[1] && resolve(process.argv[1]) === resolve(fileURLToPath(import.meta.url))) {
  verifyFrameworkFixtures({ ...parseVerifyFrameworkFixtureArguments(process.argv.slice(2)) })
    .then((summary) => process.stdout.write(`${JSON.stringify(summary)}\n`))
    .catch((error) => { process.stderr.write(`verify-fixtures: ${error.message}\n`); process.exitCode = 1; });
}
