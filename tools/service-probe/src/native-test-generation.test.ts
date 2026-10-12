import assert from "node:assert/strict";
import test from "node:test";
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { tmpdir } from "node:os";
import type { CoverageLineDetailV16, CoverageLinePageV16, ProtocolClient } from "@unit-test-ide/test-client";

import {
  buildNativeGenerationBootstrapConfig,
  buildNativeGenerationWorkspaceConfig,
  buildNativeGenerationToolchainConfig,
  formatNativeCoverageFailure,
  formatNativeTaskFailure,
  nativeGenerationFailureServiceDirectory,
  parseNativeTestGenerationArguments,
  readNativeCoverageLineEvidence,
  selectNativeGenerationProfile,
  selectNativeGenerationTarget,
  shouldPreserveNativeGenerationFailureWorkspace,
  stageNativeGenerationWorkspace,
} from "./native-test-generation.js";

const digest = (value: string) => value.repeat(64).slice(0, 64);

for (const platform of ["linux", "win32"] as const) {
  for (const framework of ["cpputest", "unity"] as const) {
    test(`${platform} ${framework} staging selects only its platform CMake inputs`, async (t) => {
      const root = resolve(import.meta.dirname, "../../..");
      const temporary = await mkdtemp(join(tmpdir(), "utide-native-staging-"));
      t.after(() => rm(temporary, { recursive: true, force: true }));
      const source = join(temporary, "authenticated-framework");
      await mkdir(source);
      await writeFile(join(source, "source-marker"), "authenticated input");
      const workspace = join(temporary, "workspace");
      await stageNativeGenerationWorkspace(root, platform, framework, source, workspace);
      const fixture = join(root, "tools/service-probe/fixtures/test-generation", framework);
      for (const path of ["CMakeLists.txt", "tests/CMakeLists.txt"]) {
        assert.deepEqual(await readFile(join(workspace, path)),
          await readFile(join(fixture, ...(platform === "win32" ? ["windows"] : []), path)));
      }
      assert.equal(await readFile(join(workspace, ".unit-test-ide/inputs", framework, "source-marker"), "utf8"), "authenticated input");
      assert.ok(!(await readdir(workspace)).includes("windows"));
      const recipes = join(workspace, ".unit-test-ide/inputs/framework-recipes");
      if (platform === "linux") await assert.rejects(readdir(recipes));
      else {
        assert.deepEqual((await readdir(recipes)).sort(), ["windows-cpputest-4.0.cmake", "windows-unity-2.6.1.cmake"]);
        for (const name of await readdir(recipes)) {
          assert.deepEqual(await readFile(join(recipes, name)), await readFile(join(root, "sdk/cmake/framework-recipes", name)));
        }
      }
      await writeFile(join(workspace, "CMakeLists.txt"), "user CMake");
      await assert.rejects(stageNativeGenerationWorkspace(root, platform, framework, source, workspace));
      assert.equal(await readFile(join(workspace, "CMakeLists.txt"), "utf8"), "user CMake");
    });
  }
}
const entityId = (value: string) => value.repeat(32).slice(0, 32);
const lineBinding = {
  workspaceGeneration: digest("a"), coverageReportId: entityId("b"),
  fileId: entityId("c"), functionId: entityId("d"),
};
const currentLine = {
  line: 3, count: 1, branchesCovered: 1, branchesTotal: 2,
  baselineCount: null, baselineBranchesCovered: null, baselineBranchesTotal: null,
};
const linePage = (items: CoverageLineDetailV16[], nextCursor?: string): CoverageLinePageV16 => ({
  workspaceGeneration: lineBinding.workspaceGeneration, coverageReportId: lineBinding.coverageReportId,
  items, ...(nextCursor === undefined ? {} : { nextCursor }),
});

test("first-report line evidence keeps real hits with null history", async () => {
  const client = { async listCoverageLines() { return linePage([currentLine]); } };
  const result = await readNativeCoverageLineEvidence(client, lineBinding);
  assert.deepEqual(result.file, [currentLine]);
  assert.deepEqual(result.function, [currentLine]);
});

test("line evidence closes independent file and function IPC pages at limit 200", async () => {
  const calls: unknown[] = [];
  const client: Pick<ProtocolClient, "listCoverageLines"> = {
    async listCoverageLines(input) {
      calls.push(input);
      return input.cursor === undefined
        ? linePage([currentLine], "page-2")
        : linePage([{ ...currentLine, line: 4, count: 0, branchesCovered: 0 }]);
    },
  };
  const result = await readNativeCoverageLineEvidence(client, lineBinding);
  assert.deepEqual(result.file.map((item) => item.line), [3, 4]);
  assert.deepEqual(result.function.map((item) => item.count), [1, 0]);
  const { workspaceGeneration, coverageReportId, fileId, functionId } = lineBinding;
  assert.deepEqual(calls, [
    { workspaceGeneration, coverageReportId, fileId, limit: 200 },
    { workspaceGeneration, coverageReportId, fileId, cursor: "page-2", limit: 200 },
    { workspaceGeneration, coverageReportId, functionId, limit: 200 },
    { workspaceGeneration, coverageReportId, functionId, cursor: "page-2", limit: 200 },
  ]);
});

for (const [label, page, expected] of [
  ["report binding drift", { ...linePage([currentLine]), coverageReportId: entityId("e") }, /identity|binding/u],
  ["generation binding drift", { ...linePage([currentLine]), workspaceGeneration: digest("e") }, /identity|binding/u],
  ["duplicate lines", linePage([currentLine, currentLine]), /duplicate|unique/u],
  ["empty page disguised as success", linePage([]), /empty|non-empty/u],
  ["over-budget page", linePage(Array.from({ length: 201 }, (_, index) => ({ ...currentLine, line: index + 1 }))), /limit|budget/u],
  ["mixed baseline fields", linePage([{ ...currentLine, baselineCount: 0 }]), /history|baseline/u],
  ["invented numeric baseline", linePage([{ ...currentLine, baselineCount: 0, baselineBranchesCovered: 0, baselineBranchesTotal: 2 }]), /history|baseline/u],
  ["unsafe current count", linePage([{ ...currentLine, count: Number.MAX_SAFE_INTEGER + 1 }]), /current|count/u],
  ["negative current count", linePage([{ ...currentLine, count: -1 }]), /current|count/u],
  ["invalid line number", linePage([{ ...currentLine, line: 0 }]), /current|line/u],
  ["branches exceeding total", linePage([{ ...currentLine, branchesCovered: 3 }]), /current|branch/u],
  ["unsafe branch total", linePage([{ ...currentLine, branchesTotal: Number.MAX_SAFE_INTEGER + 1 }]), /current|branch/u],
  ["empty continuation cursor", linePage([currentLine], ""), /cursor/u],
] as const) {
  test(`line evidence rejects ${label}`, async () => {
    await assert.rejects(readNativeCoverageLineEvidence({ async listCoverageLines() { return page; } }, lineBinding), expected);
  });
}

test("line evidence rejects duplicate lines across pages", async () => {
  const client = { async listCoverageLines(input: { cursor?: string }) {
    return linePage([currentLine], input.cursor === undefined ? "next" : undefined);
  } };
  await assert.rejects(readNativeCoverageLineEvidence(client, lineBinding), /duplicate|unique/u);
});

test("line evidence rejects cursor cycles without unbounded IPC", async () => {
  let requests = 0;
  const client = { async listCoverageLines() {
    return linePage([{ ...currentLine, line: ++requests }], "loop");
  } };
  await assert.rejects(readNativeCoverageLineEvidence(client, lineBinding), /cursor/u);
  assert.equal(requests, 2);
});

test("line evidence requires the function page as well as the file page", async () => {
  const client = { async listCoverageLines(input: { functionId?: string }) {
    return linePage(input.functionId === undefined ? [currentLine] : []);
  } };
  await assert.rejects(readNativeCoverageLineEvidence(client, lineBinding), /empty|non-empty/u);
});

test("line evidence rejects digest-sized entity bindings before IPC", async () => {
  const client = { async listCoverageLines(): Promise<CoverageLinePageV16> {
    assert.fail("invalid binding must not issue IPC");
  } };
  await assert.rejects(readNativeCoverageLineEvidence(client, { ...lineBinding, coverageReportId: digest("b") }), /binding/u);
});

test("native generation preserves the failure workspace only under the explicit debug switch", () => {
  const previous = process.env.UT_DEBUG_PROCESS_HOST_FAILURES;
  try {
    delete process.env.UT_DEBUG_PROCESS_HOST_FAILURES;
    assert.equal(shouldPreserveNativeGenerationFailureWorkspace(), false);
    process.env.UT_DEBUG_PROCESS_HOST_FAILURES = "1";
    assert.equal(shouldPreserveNativeGenerationFailureWorkspace(), true);
  } finally {
    if (previous === undefined) delete process.env.UT_DEBUG_PROCESS_HOST_FAILURES;
    else process.env.UT_DEBUG_PROCESS_HOST_FAILURES = previous;
  }
});

test("native generation keeps service diagnostics beside the preserved failure workspace", () => {
  assert.equal(
    nativeGenerationFailureServiceDirectory("C:\\native-work", "cpputest"),
    "C:\\native-work\\cpputest\\service-debug",
  );
  assert.throws(() => nativeGenerationFailureServiceDirectory("C:\\native-work", "invalid" as never), /framework/u);
});

test("native coverage failure diagnostics retain bounded task and compiler context", () => {
  const message = formatNativeCoverageFailure({
    run: {
      outcome: "unavailable" as NonNullable<import("@unit-test-ide/test-client").CoverageRun["outcome"]>,
      reason: "build_failed" as NonNullable<import("@unit-test-ide/test-client").CoverageRun["reason"]>,
    },
    task: { outcome: "command_failed", errorCode: "BUILD_FAILED", errorMessage: "compiler exited with code 1" },
    artifacts: [
      { kind: "stderr", text: "clang++: error: missing header\n" },
      { kind: "stdout", text: "build started\n" },
      { kind: "coverage-json", text: "must not be printed" },
    ],
    serviceDiagnostics: "process=running; stderr=cmake: compiler failed",
  });
  assert.match(message, /build_failed/u);
  assert.match(message, /BUILD_FAILED/u);
  assert.match(message, /clang\+\+: error: missing header/u);
  assert.match(message, /build started/u);
  assert.match(message, /cmake: compiler failed/u);
  assert.doesNotMatch(message, /must not be printed/u);
});

test("native task failure diagnostics expose bounded build artifacts only when requested", () => {
  const message = formatNativeTaskFailure("native test-generation build", {
    outcome: "command_failed",
    errorCode: "BUILD_FAILED",
    errorMessage: "cmake exited with code 1",
  }, [
    { kind: "build-summary", text: "configure=ok\nbuild=failed\n" },
    { kind: "stderr", text: "ninja: error: unknown target classifier-tests\n" },
    { kind: "secret", text: "must not be printed" },
  ]);
  assert.match(message, /native test-generation build/u);
  assert.match(message, /BUILD_FAILED/u);
  assert.match(message, /unknown target classifier-tests/u);
  assert.doesNotMatch(message, /must not be printed/u);
});

test("native generation arguments are closed to the current supported platforms", () => {
  assert.deepEqual(parseNativeTestGenerationArguments(["--platform", "linux"]), { platform: "linux" });
  assert.deepEqual(parseNativeTestGenerationArguments(["--platform", "win32"]), { platform: "win32" });
  assert.throws(() => parseNativeTestGenerationArguments([]), /arguments/u);
  assert.throws(() => parseNativeTestGenerationArguments(["--platform", "darwin"]), /arguments/u);
  assert.throws(() => parseNativeTestGenerationArguments(["--platform", "linux", "extra"]), /arguments/u);
});

test("workspace configuration binds one generated Debug profile and exposes one all-test coverage profile", () => {
  const config = buildNativeGenerationWorkspaceConfig("unity", digest("a"));
  assert.deepEqual(config, {
    version: 3,
    projects: [{
      id: "classifier",
      sourceDir: ".",
      fallback: { configurations: ["Debug"], preferredGenerator: "Ninja" },
      tests: { containers: [{ ctestName: "classifier-tests", framework: "unity" }] },
    }],
    coverageProfiles: [{
      id: "generation-coverage",
      baseBuildProfileId: digest("a"),
      include: ["src/**"],
      exclude: ["tests/**", ".unit-test-ide/**"],
    }],
  });
  assert.throws(() => buildNativeGenerationWorkspaceConfig("unity", "bad"), /profile/u);
  assert.throws(() => buildNativeGenerationWorkspaceConfig("unity", "8c41ec4cd1ae1c444276f6fed5c16bab"), /profile/u);
});

test("Linux generation bootstrap binds the approved LLVM toolchain before profile discovery", () => {
	const root = "/opt/unit-test-ide/llvm-coverage-bundle";
	const toolchain = buildNativeGenerationToolchainConfig("linux", root);
	assert.deepEqual(toolchain, {
		id: "approved-clang-llvm",
		family: "clang",
		cCompiler: `${root}/bin/clang`,
		cppCompiler: `${root}/bin/clang++`,
	});
	assert.deepEqual(buildNativeGenerationBootstrapConfig("cpputest", toolchain), {
		version: 2,
		projects: [{
			id: "classifier",
			sourceDir: ".",
			fallback: { configurations: ["Debug"], preferredGenerator: "Ninja" },
			tests: { containers: [{ ctestName: "classifier-tests", framework: "cpputest" }] },
		}],
		toolchains: [toolchain],
	});
	assert.deepEqual(buildNativeGenerationWorkspaceConfig("cpputest", digest("a"), toolchain), {
		version: 3,
		projects: [{
			id: "classifier",
			sourceDir: ".",
			fallback: { configurations: ["Debug"], preferredGenerator: "Ninja" },
			tests: { containers: [{ ctestName: "classifier-tests", framework: "cpputest" }] },
		}],
		coverageProfiles: [{
			id: "generation-coverage",
			baseBuildProfileId: digest("a"),
			include: ["src/**"],
			exclude: ["tests/**", ".unit-test-ide/**"],
		}],
		toolchains: [toolchain],
	});
});

test("native generation chooses only the production LLVM family and exact generated profile", () => {
  const profile = (id: string, toolchainId: string, origin = "generated") => ({
    buildProfileId: id, toolchainId, origin, generator: "Ninja", configuration: "Debug",
  });
  const snapshot = {
    workspaceGeneration: digest("w"),
    projects: [{ projectId: "classifier", buildProfiles: [
      profile(digest("1"), "gcc"), profile(digest("2"), "clang"), profile(digest("3"), "clang-cl"),
    ] }],
    toolchains: [
      { toolchainId: "gcc", family: "gcc", hostArchitecture: "x64", targetArchitecture: "x64", capabilities: { coverageDrivers: ["gcov"] } },
      { toolchainId: "clang", family: "clang", hostArchitecture: "x64", targetArchitecture: "x64", capabilities: { coverageDrivers: ["llvm-cov"] } },
      { toolchainId: "clang-cl", family: "clang-cl", hostArchitecture: "x64", targetArchitecture: "x64", capabilities: { coverageDrivers: ["llvm-cov"] } },
    ],
  };
  assert.equal(selectNativeGenerationProfile(snapshot as never, "linux").profile.buildProfileId, digest("2"));
  assert.equal(selectNativeGenerationProfile(snapshot as never, "win32").profile.buildProfileId, digest("3"));
});

test("coverage target accepts real 32-character entity IDs", () => {
  const fileId = "8c41ec4cd1ae1c444276f6fed5c16bab";
  const functionId = "c9096d1efd8a87e00f4cfba79e459610";
  const target = selectNativeGenerationTarget(
    { items: [{ fileId, relativePath: "src/classifier.cpp", status: "current" }] },
    { items: [{ functionId, fileId, qualifiedName: "_Z8classifyi", status: "current" }] },
    "src/classifier.cpp",
  );
  assert.deepEqual(target, { fileId, functionId });
});

for (const [label, invalidId] of [
  ["31 characters", "a".repeat(31)],
  ["33 characters", "a".repeat(33)],
  ["64 characters", "a".repeat(64)],
  ["non-hex", "g".repeat(32)],
  ["uppercase", "A".repeat(32)],
] as const) {
  for (const entity of ["file", "function"] as const) {
    test(`coverage target rejects ${entity} IDs with ${label}`, () => {
      const fileId = entity === "file" ? invalidId : "8c41ec4cd1ae1c444276f6fed5c16bab";
      const functionId = entity === "function" ? invalidId : "c9096d1efd8a87e00f4cfba79e459610";
      assert.throws(() => selectNativeGenerationTarget(
        { items: [{ fileId, relativePath: "src/classifier.cpp", status: "current" }] },
        { items: [{ fileId, functionId, qualifiedName: "_Z8classifyi", status: "current" }] },
        "src/classifier.cpp",
      ), /coverage target is unavailable/u);
    });
  }
}

test("coverage target rejects a pair of digest-sized entity IDs", () => {
  const fileId = "f".repeat(64);
  const functionId = "a".repeat(64);
  assert.throws(() => selectNativeGenerationTarget(
    { items: [{ fileId, relativePath: "src/classifier.cpp", status: "current" }] },
    { items: [{ fileId, functionId, qualifiedName: "_Z8classifyi", status: "current" }] },
    "src/classifier.cpp",
  ), /coverage target is unavailable/u);
});

test("coverage target preserves exact, current, unique, parent-bound and closed-page selection", () => {
  const fileId = "8c41ec4cd1ae1c444276f6fed5c16bab";
  const functionId = "c9096d1efd8a87e00f4cfba79e459610";
  const file = { fileId, relativePath: "src/classifier.cpp", status: "current" };
  const fn = { fileId, functionId, qualifiedName: "_Z8classifyi", status: "current" };
  const cases = [
    { label: "stale file", files: { items: [{ ...file, status: "stale" }] }, functions: { items: [fn] } },
    { label: "inexact path", files: { items: [{ ...file, relativePath: "src/other.cpp" }] }, functions: { items: [fn] } },
    { label: "duplicate file", files: { items: [file, file] }, functions: { items: [fn] } },
    { label: "stale function", files: { items: [file] }, functions: { items: [{ ...fn, status: "stale" }] } },
    { label: "wrong parent file", files: { items: [file] }, functions: { items: [{ ...fn, fileId: "a".repeat(32) }] } },
    { label: "empty name", files: { items: [file] }, functions: { items: [{ ...fn, qualifiedName: "" }] } },
    { label: "duplicate function", files: { items: [file] }, functions: { items: [fn, fn] } },
    { label: "second current function", files: { items: [file] }, functions: { items: [fn, { ...fn, functionId: "a".repeat(32), qualifiedName: "other" }] } },
    { label: "second unmatched function", files: { items: [file] }, functions: { items: [fn, { ...fn, fileId: "a".repeat(32) }] } },
    { label: "file pagination", files: { items: [file], nextCursor: "more" }, functions: { items: [fn] } },
    { label: "function pagination", files: { items: [file] }, functions: { items: [fn], nextCursor: "more" } },
  ];
  for (const { label, files, functions } of cases) {
    assert.throws(() => selectNativeGenerationTarget(files, functions, "src/classifier.cpp"), /coverage target is unavailable/u, label);
  }
});
