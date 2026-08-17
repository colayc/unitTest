import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  cp,
  mkdtemp,
  readFile,
  rm,
  stat,
  writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { delimiter, dirname, join, resolve } from "node:path";
import test from "node:test";

const repositoryRoot = resolve(".");
const fixtureTemplate = resolve("testdata/frameworks/helper-smoke");
const helperPath = resolve("sdk/cmake/UnitTestIDE.cmake");
const cmake = process.env.CMAKE ?? "cmake";
const go = process.env.GO ?? "go";
const executableSuffix = process.platform === "win32" ? ".exe" : "";
const ctest = cmake.includes("/") || cmake.includes("\\")
  ? join(dirname(cmake), `ctest${executableSuffix}`)
  : `ctest${executableSuffix}`;

function execute(command, arguments_, options = {}) {
  const result = spawnSync(command, arguments_, {
    cwd: options.cwd ?? repositoryRoot,
    encoding: "utf8",
    env: options.env ?? process.env,
    maxBuffer: 16 * 1024 * 1024,
    windowsHide: true,
  });
  if (result.error) {
    throw result.error;
  }
  if (!options.allowFailure && result.status !== 0) {
    assert.fail([
      `${command} ${arguments_.join(" ")} exited with ${result.status}`,
      result.stdout,
      result.stderr,
    ].join("\n"));
  }
  return result;
}

async function buildGoCommand(output, source) {
  if (source !== undefined) {
    await writeFile(`${output}.go`, source, "utf8");
    execute(go, ["build", "-o", output, `${output}.go`]);
    return;
  }
  execute(go, [
    "build",
    "-o",
    output,
    "./apps/test-service/cmd/unity-runner-generator",
  ]);
}

function configure({
  source,
  build,
  generator,
  scenario = "valid",
  escapeSource,
  omitGenerator = false,
  allowFailure = false,
  env,
}) {
  const arguments_ = [
    "-S", source,
    "-B", build,
    `-DUNIT_TEST_IDE_HELPER=${helperPath}`,
    `-DUTIDE_SCENARIO=${scenario}`,
  ];
  if (!omitGenerator) {
    arguments_.push(`-DUTIDE_UNITY_RUNNER_GENERATOR=${generator}`);
  }
  if (escapeSource !== undefined) {
    arguments_.push(`-DUTIDE_ESCAPE_SOURCE=${escapeSource}`);
  }
  return execute(cmake, arguments_, { allowFailure, env });
}

function testProperty(entry, name) {
  return entry.properties?.find((property) => property.name === name)?.value;
}

test("UnitTestIDE CMake helper has strict deterministic framework registration", async () => {
  const temporary = await mkdtemp(join(tmpdir(), "utide-cmake-helper-"));
  try {
    const source = join(temporary, "source with spaces-单元");
    const build = join(temporary, "build with spaces-单元");
    const generator = join(temporary, `unity-runner-generator${executableSuffix}`);
    await cp(fixtureTemplate, source, { recursive: true });
    await buildGoCommand(generator);

    configure({ source, build, generator });
    execute(cmake, ["--build", build, "--config", "Debug"]);
    const testModel = JSON.parse(execute(ctest, [
      "--test-dir", build,
      "--show-only=json-v1",
      "-C", "Debug",
    ]).stdout);
    const byName = new Map(testModel.tests.map((entry) => [entry.name, entry]));
    assert.deepEqual([...byName.keys()].sort(), ["cpputest.case", "unity case-单元"]);
    assert.deepEqual(testProperty(byName.get("cpputest.case"), "LABELS"), [
      "utide.framework.cpputest",
    ]);
    assert.deepEqual(testProperty(byName.get("unity case-单元"), "LABELS"), [
      "utide.framework.unity",
      "utide.runner.v1",
    ]);

    const identity = createHash("sha256").update("unity case-单元", "utf8").digest("hex");
    const outputDirectory = join(build, ".unit-test-ide", identity);
    const runnerPath = join(outputDirectory, "runner.c");
    const manifestPath = join(outputDirectory, "manifest.json");
    const runnerBefore = await readFile(runnerPath);
    const manifestBefore = await readFile(manifestPath);
    const runnerInfoBefore = await stat(runnerPath);
    const manifestInfoBefore = await stat(manifestPath);
    const manifest = JSON.parse(manifestBefore);
    assert.equal(manifest.version, "utide.unity.manifest.v1");
    assert.equal(manifest.generatorVersion, "1.0.0");
    assert.deepEqual(manifest.sources, ["unity extra-单元.c", "unity_tests.c"]);
    assert.match(runnerBefore.toString("utf8"), /test_unity_helper/);
    assert.doesNotMatch(runnerBefore.toString("utf8"), /source with spaces-单元|build with spaces-单元/u);

    configure({ source, build, generator });
    assert.deepEqual(await readFile(runnerPath), runnerBefore);
    assert.deepEqual(await readFile(manifestPath), manifestBefore);
    assert.equal((await stat(runnerPath)).mtimeMs, runnerInfoBefore.mtimeMs);
    assert.equal((await stat(manifestPath)).mtimeMs, manifestInfoBefore.mtimeMs);

    const cache = await readFile(join(build, "CMakeCache.txt"), "utf8");
    if (/^CMAKE_CONFIGURATION_TYPES:/mu.test(cache)) {
      for (const name of ["cpputest.case", "unity case-单元"]) {
        const command = byName.get(name).command;
        assert.ok(Array.isArray(command), JSON.stringify(byName.get(name)));
        assert.match(
          command[0].replaceAll("\\", "/"),
          /\/Debug\//u,
          `${name} does not resolve through the selected multi-config output`,
        );
      }
    }

    const outsideSource = join(temporary, "outside.c");
    await writeFile(outsideSource, "void test_outside(void) {}\n", "utf8");
    const failures = [
      { name: "duplicate test", scenario: "duplicate-test", pattern: /already exists|duplicate/iu },
      { name: "missing target", scenario: "missing-target", pattern: /does not\s+exist/iu },
      { name: "wrong target", scenario: "wrong-target", pattern: /executable target/iu },
      { name: "Unity target already has main", scenario: "unity-existing-main", pattern: /main/iu },
      {
        name: "source escape",
        scenario: "source-escape",
        escapeSource: outsideSource,
        pattern: /outside|escape|source root/iu,
      },
      { name: "unparsed keyword", scenario: "unparsed-keyword", pattern: /unparsed|unexpected|argument/iu },
      { name: "unsafe keyword", scenario: "unsafe-keyword", pattern: /COMMAND|argument/iu },
    ];
    for (const failure of failures) {
      const result = configure({
        source,
        build: join(temporary, `failure-${failure.scenario}`),
        generator,
        scenario: failure.scenario,
        escapeSource: failure.escapeSource,
        allowFailure: true,
      });
      assert.notEqual(result.status, 0, `${failure.name} unexpectedly configured`);
      const diagnostic = `${result.stdout ?? ""}\n${result.stderr ?? ""}`;
      assert.match(
        diagnostic,
        failure.pattern,
        `${failure.name}: status=${result.status}, signal=${result.signal}, diagnostic=${diagnostic}`,
      );
    }

    const missing = configure({
      source,
      build: join(temporary, "failure-missing-generator"),
      generator,
      omitGenerator: true,
      allowFailure: true,
      env: {
        ...process.env,
        PATH: `${temporary}${delimiter}${process.env.PATH ?? ""}`,
      },
    });
    assert.notEqual(missing.status, 0);
    assert.match(`${missing.stdout}\n${missing.stderr}`, /UTIDE_UNITY_RUNNER_GENERATOR|generator/iu);

    const wrongGenerator = join(temporary, `wrong-generator${executableSuffix}`);
    await buildGoCommand(wrongGenerator, [
      "package main",
      "import \"fmt\"",
      "func main() {",
      "  fmt.Println(`{\"schemaVersion\":1,\"name\":\"unity-runner-generator\",\"version\":\"9.9.9\",\"runnerProtocol\":\"utide.runner.v1\"}`)",
      "}",
      "",
    ].join("\n"));
    const wrongVersion = configure({
      source,
      build: join(temporary, "failure-wrong-generator"),
      generator: wrongGenerator,
      allowFailure: true,
    });
    assert.notEqual(wrongVersion.status, 0);
    assert.match(`${wrongVersion.stdout}\n${wrongVersion.stderr}`, /version|1\.0\.0/iu);
  } finally {
    await rm(temporary, { recursive: true, force: true });
  }
});
