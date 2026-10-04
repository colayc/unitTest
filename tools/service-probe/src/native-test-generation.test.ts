import assert from "node:assert/strict";
import test from "node:test";

import {
  buildNativeGenerationWorkspaceConfig,
  parseNativeTestGenerationArguments,
  selectNativeGenerationProfile,
  selectNativeGenerationTarget,
} from "./native-test-generation.js";

const digest = (value: string) => value.repeat(64).slice(0, 64);

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

test("coverage target is an exact current file with one exact current function", () => {
  const fileId = digest("f");
  const functionId = digest("a");
  const target = selectNativeGenerationTarget(
    { items: [{ fileId, relativePath: "src/classifier.c", status: "current" }] },
    { items: [{ functionId, fileId, qualifiedName: "_Z8classifyi", status: "current" }] },
    "src/classifier.c",
  );
  assert.deepEqual(target, { fileId, functionId });
  assert.throws(() => selectNativeGenerationTarget(
    { items: [{ fileId, relativePath: "src/classifier.c", status: "stale" }] },
    { items: [{ functionId, fileId, qualifiedName: "classify", status: "current" }] },
    "src/classifier.c",
  ), /target/u);
  assert.throws(() => selectNativeGenerationTarget(
    { items: [{ fileId, relativePath: "src/classifier.c", status: "current" }] },
    { items: [
      { functionId, fileId, qualifiedName: "classify", status: "current" },
      { functionId: digest("b"), fileId, qualifiedName: "other", status: "current" },
    ] },
    "src/classifier.c",
  ), /target/u);
});
