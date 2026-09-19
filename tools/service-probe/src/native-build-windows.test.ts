import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import type { WorkspaceSnapshot } from "@unit-test-ide/protocol-models";
import type { TaskServiceFixture } from "./probe.js";
import { FRAMEWORK_SCENARIO_IDS } from "./native-framework-report.js";
import {
  __testing,
  type NativeMatrixOptions,
  type PreparedCMakeBundle,
} from "./native-build.js";
import type { F1FrameworkIdentity, FrameworkPlatformOptions } from "./native-framework-matrix.js";
import { verifyRequiredFrameworkReport } from "./native-report.js";

test("Windows framework evidence hashes the unique compiled executable from the fixed Service build root", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "native-framework-executable-win-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const executable = join(
    root, ".native-e2e", "framework-work", "windows", "msvc", "cpputest",
    "service", "data", "build", "a".repeat(64), "bin", "phase9_cpputest.exe",
  );
  await mkdir(dirname(executable), { recursive: true });
  const bytes = Buffer.from("compiled-msvc-cpputest", "utf8");
  await writeFile(executable, bytes);

  assert.equal(
    await __testing.frameworkExecutableDigest(root, "win32", "msvc", "cpputest"),
    createHash("sha256").update(bytes).digest("hex"),
  );

  const duplicate = join(
    root, ".native-e2e", "framework-work", "windows", "msvc", "cpputest",
    "service", "data", "build", "b".repeat(64), "bin", "phase9_cpputest.exe",
  );
  await mkdir(dirname(duplicate), { recursive: true });
  await writeFile(duplicate, "stale executable");
  await assert.rejects(
    __testing.frameworkExecutableDigest(root, "win32", "msvc", "cpputest"),
    /exactly one compiled cpputest executable/u,
  );
});

test("required Windows native completion rejects a missing framework report", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "native-framework-report-win-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  await assert.rejects(
    verifyRequiredFrameworkReport(root, "win32"),
    /required framework report is missing/u,
  );
  await writeFile(join(root, "framework-report.json"), '{"platform":"win32","toolchains":[]}\n');
  await assert.rejects(
    verifyRequiredFrameworkReport(root, "win32"),
    /required framework report is invalid/u,
  );
});

test("required Windows native run binds both real framework executables and publishes 2x2x17 evidence", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "native-framework-platform-win-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const artifactDirectory = join(root, "artifacts");
  const serviceBinary = join(root, "build", "unit-test-service.exe");
  await mkdir(dirname(serviceBinary), { recursive: true });
  await writeFile(serviceBinary, "fixture");
  const frameworkPlatform = fakeFrameworkPlatform(artifactDirectory);
  const events: string[] = [];
  let launchIndex = 0;
  const dependencies: Parameters<typeof __testing.runNativeMatrixWithDependencies>[1] = {
    environment: {
      UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS: "msvc,clang-cl",
      UNIT_TEST_IDE_P4_FRAMEWORK_MATRIX_REQUIRED: "1",
    },
    architecture: "x64",
    repositoryRoot: root,
    verifyBundle: async () => fakePreparedBundle(root),
    loadFrameworkIdentity: async () => f1Identity,
    frameworkExecutableDigest: async (_root, _platform, family, frameworkId) => {
      events.push(`executable:${family}:${frameworkId}`);
      return digest(`executable:${family}:${frameworkId}`);
    },
    createWorkspace: async (_work, _platform, family) => {
      const familyRoot = join(root, "work", family);
      const workspaceRoot = join(familyRoot, "workspace");
      const serviceDirectory = join(familyRoot, "service");
      await mkdir(workspaceRoot, { recursive: true });
      await mkdir(serviceDirectory, { recursive: true });
      return { root: familyRoot, workspaceRoot, serviceDirectory };
    },
    launchService: async () => {
      const family = (["msvc", "clang-cl"] as const)[launchIndex++]!;
      return {
        client: { inspectWorkspace: async () => workspaceSnapshot(family) },
        dispose: async () => { events.push(`dispose:${family}`); },
      } as unknown as TaskServiceFixture;
    },
    runFrameworkToolchain: async (_options, family, identity) => {
      assert.equal(identity, f1Identity, "validated F1 identity must reach the Service catalog runner");
      events.push(`framework:${family}`);
      return {
        family,
        compilerVersion: "19.50.10000",
        compilerSha256: digest(`compiler:${family}`),
        frameworks: (["cpputest", "unity"] as const).map((id) => ({
          id,
          dependencyVersion: id === "cpputest" ? "4.0" : "2.6.1",
          dependencySha256: digest(`dependency:${id}`),
          dependencyTreeSha256: f1Identity.frameworkTreeSha256[id],
          catalogRevision: digest(`revision:${family}:${id}`),
          catalogArtifactSha256: digest(`catalog:${family}:${id}`),
          sourceArtifactSha256: f1Identity.fixtures[id].sourceSha256,
          sourceLocationDigest: digest(`locations:${id}`),
          executableArtifactSha256: digest(`executable:${family}:${id}`),
          stableIdDigest: digest(`stable:${family}:${id}`),
          ...(id === "unity" ? { cMockProvenance: {
            revision: "6ea503340b1d3fdc0f2bcaf69273ba0160ec83af",
            generatorVersion: "2.7.0",
            inputSha256: "007f23aea2dba06d111f66be95905adde8fe32e7d2031bf8a8c70117a8209f57",
            outputSha256: "1565d1a2d39b655eb551a729663fae7e167f1c0d6cd3f9a8c2aafe2d67348128",
            manifestSha256: f1Identity.cMockProvenanceSha256,
            generatedAtRuntime: false,
          } } : {}),
          scenarios: FRAMEWORK_SCENARIO_IDS.map((scenarioId) => ({ id: scenarioId })),
        })),
      } as never;
    },
    executeScenarios: async ({ family }) => {
      events.push(`core:${family}`);
      return { "default-build": "passed" };
    },
    cleanupWorkspace: async ({ root: workspaceRoot }) => {
      events.push(`cleanup:${workspaceRoot.includes("clang-cl") ? "clang-cl" : "msvc"}`);
    },
    writeReport: async () => {
      events.push("toolchain-report");
      return join(artifactDirectory, "toolchain-report.json");
    },
    publishFrameworkReport: async (_options, toolchains) => {
      const serialized = {
        platform: "win32",
        toolchains: toolchains.map(({ family, frameworks }) => ({
          family,
          frameworks: frameworks.map(({ id, scenarios, sourceArtifactSha256, stableIdDigest, cMockProvenance }) => ({
            id, scenarios, sourceArtifactSha256, stableIdDigest, cMockProvenance,
          })),
        })),
      };
      await mkdir(artifactDirectory, { recursive: true });
      await writeFile(join(artifactDirectory, "framework-report.json"), `${JSON.stringify(serialized)}\n`);
      events.push("framework-report");
      return serialized as never;
    },
    verifyFrameworkReport: async (directory, platform) => {
      const report = JSON.parse(await readFile(join(directory, "framework-report.json"), "utf8")) as {
        platform: string;
        toolchains: Array<{ family: string; frameworks: Array<{
          id: string;
          scenarios: Array<{ id: string }>;
          sourceArtifactSha256: string;
          stableIdDigest: string;
          cMockProvenance?: { manifestSha256: string; generatedAtRuntime: boolean };
        }> }>;
      };
      assert.equal(platform, "win32");
      assert.equal(report.platform, "win32");
      assert.deepEqual(report.toolchains.map(({ family }) => family), ["msvc", "clang-cl"]);
      for (const { family, frameworks } of report.toolchains) {
        assert.deepEqual(frameworks.map(({ id }) => id), ["cpputest", "unity"]);
        for (const framework of frameworks) {
          assert.deepEqual(framework.scenarios.map(({ id }) => id), [...FRAMEWORK_SCENARIO_IDS]);
          assert.equal(
            framework.sourceArtifactSha256,
            f1Identity.fixtures[framework.id as "cpputest" | "unity"].sourceSha256,
          );
          assert.equal(framework.stableIdDigest, digest(`stable:${family}:${framework.id}`));
          if (framework.id === "unity") {
            assert.deepEqual(framework.cMockProvenance, {
              revision: "6ea503340b1d3fdc0f2bcaf69273ba0160ec83af",
              generatorVersion: "2.7.0",
              inputSha256: "007f23aea2dba06d111f66be95905adde8fe32e7d2031bf8a8c70117a8209f57",
              outputSha256: "1565d1a2d39b655eb551a729663fae7e167f1c0d6cd3f9a8c2aafe2d67348128",
              manifestSha256: f1Identity.cMockProvenanceSha256,
              generatedAtRuntime: false,
            });
          } else {
            assert.equal(framework.cMockProvenance, undefined);
          }
        }
      }
      events.push("verify-framework-report");
      return report as never;
    },
  };
  const options: NativeMatrixOptions = {
    platform: "win32",
    requiredFamilies: ["msvc", "clang-cl"],
    artifactDirectory,
    frameworkPlatform,
  };

  await __testing.runNativeMatrixWithDependencies(options, dependencies);
  assert.deepEqual(events, [
    "executable:msvc:cpputest", "executable:msvc:unity", "framework:msvc", "core:msvc", "dispose:msvc", "cleanup:msvc",
    "executable:clang-cl:cpputest", "executable:clang-cl:unity", "framework:clang-cl", "core:clang-cl", "dispose:clang-cl", "cleanup:clang-cl",
    "toolchain-report", "framework-report", "verify-framework-report",
  ]);
});

const f1Identity: F1FrameworkIdentity = {
  manifestSha256: "2ed58a385314968813598dd0ca89cbdc491e7c1eda87d58a676efb61a5af5fa0",
  frameworkTreeSha256: {
    cpputest: "c564fb5e4e32836dc66f46efb86edb6f1f2fa6afa255a57052031aa00fc56f04",
    unity: "abfb7b2b7aec36739a7b138490d2e9dd178cc4f00e806ed372cbb8cfe98f73ae",
    cmock: "19e013d70a3f032decb2e836b875a997d085ca72b34c92c91b528e6ad2e49ac3",
  },
  cMockProvenanceSha256: "038b46e53f833097d6311564c3d647c418ba254e1b129ada161ab40f3b0d961a",
  fixtures: {
    cpputest: {
      metadataSha256: "6eef50ec7940e4a6b80891d0ff452ed503b5996de313df711ecad607185761ee",
      sourceSha256: "114b3d7c6aadcc487b2df01a917c4c0702ba1fdb381456b12c406839181ac5f2",
      executableSha256: "fed16783995c7fe860a931f8d783e50b1bac04c64bd98843fa61613b41a821b3",
    },
    unity: {
      metadataSha256: "1287993f09fb2d8079933ac89a85bd464122edac7b3c92621692a02484536333",
      sourceSha256: "cfec3aea0fec4f1c6a84a4e835edb060415266deb156893d1c10de5ddb0fae15",
      executableSha256: "c094e9ebbf45592d8b087410f6905844d82132bec626dbc549f1db75a9d5e44e",
    },
  },
};

function fakeFrameworkPlatform(artifactDirectory: string): FrameworkPlatformOptions {
  return {
    artifactDirectory,
    benchmark: {
      id: "catalog-10000", itemCount: 10000, sampleCount: 3, allocationBudgetPerOperation: 300000,
      allocationsPerOperation: [1, 1, 1], catalogRevision: digest("benchmark-catalog"),
      catalogArtifactSha256: digest("benchmark-artifact"), stableIdDigest: digest("benchmark-stable"), status: "passed",
    },
    candidateCommit: "1".repeat(40),
    platform: "win32",
    toolchains: (["msvc", "clang-cl"] as const).map((family) => ({
      family,
      compilerVersion: "19.50.10000",
      compilerSha256: digest(`compiler:${family}`),
      frameworks: (["cpputest", "unity"] as const).map((frameworkId) => ({
        candidateCommit: "1".repeat(40),
        catalogArtifactSha256: digest(`catalog:${family}:${frameworkId}`),
        dependencyVersion: frameworkId === "cpputest" ? "4.0" : "2.6.1",
        dependencySha256: frameworkId === "cpputest"
          ? "21c692105db15299b5529af81a11a7ad80397f92c122bd7bf1e4a4b0e85654f7"
          : "b41a66d45a6b99758fb3202ace6178177014d52fc524bf1f72687d93e9867292",
        dependencyTreeSha256: f1Identity.frameworkTreeSha256[frameworkId],
        evidence: {
          sourceArtifactSha256: f1Identity.fixtures[frameworkId].sourceSha256,
          sourceLocationDigest: digest(`locations:${frameworkId}`),
          executableArtifactSha256: digest(`executable:${family}:${frameworkId}`),
        },
        fixture: {} as TaskServiceFixture,
        frameworkId,
        platform: "win32" as const,
        stableIdDigest: digest(`stable:${frameworkId}`),
        toolchainFamily: family,
        ...(frameworkId === "unity" ? { cMockProvenance: {
          revision: "6ea503340b1d3fdc0f2bcaf69273ba0160ec83af",
          generatorVersion: "2.7.0",
          inputSha256: "007f23aea2dba06d111f66be95905adde8fe32e7d2031bf8a8c70117a8209f57",
          outputSha256: "1565d1a2d39b655eb551a729663fae7e167f1c0d6cd3f9a8c2aafe2d67348128",
          manifestSha256: f1Identity.cMockProvenanceSha256,
          generatedAtRuntime: false as const,
        } } : {}),
      })),
    })),
  };
}

function workspaceSnapshot(family: "msvc" | "clang-cl"): WorkspaceSnapshot {
  const toolchainId = `${family}-test`;
  return {
    workspaceUri: "file:///workspace", workspaceGeneration: "a".repeat(64),
    capabilities: { workspaceInspect: true, targetList: true, cmakeBuild: true }, diagnostics: [],
    toolchains: [{
      toolchainId, family, version: "19.50.10000", targetTriple: "x86_64-windows-msvc",
      hostArchitecture: "x64", targetArchitecture: "x64", generators: ["Ninja"], capabilities: { coverageDrivers: [] },
    }],
    projects: [{ projectId: "root", sourceUri: "file:///workspace", buildProfiles: [{
      buildProfileId: "b".repeat(64), name: "Debug", origin: "generated", toolchainId, generator: "Ninja", configuration: "Debug",
    }] }],
  } as WorkspaceSnapshot;
}

function fakePreparedBundle(root: string): PreparedCMakeBundle {
  return {
    bundleRoot: join(root, ".bundled-tools", "cmake"), installRoot: join(root, "cmake-install"),
    executable: join(root, "cmake.exe"), key: "win32-x64", cmakeVersion: "4.3.4", archiveSha256: "a".repeat(64),
  };
}

function digest(value: string): string {
  return createHash("sha256").update(value).digest("hex");
}
