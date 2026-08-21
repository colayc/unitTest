import assert from "node:assert/strict";
import { execFile as execFileCallback } from "node:child_process";
import { mkdtemp, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";
import { promisify } from "node:util";
import {
  installWfpOfflineBoundary,
  type GuardianFrame,
  type WfpOfflineBoundaryDependencies,
} from "./wfp-offline-boundary.js";

const execFile = promisify(execFileCallback);
const requiredEnvironment = "UNIT_TEST_IDE_WFP_INTEGRATION_REQUIRED";
const repositoryRoot = resolve(import.meta.dirname, "../../..");

function verifiedPreflight(): { readonly stdout: string; readonly stderr: string } {
  return {
    stdout: "{\"schemaVersion\":1,\"platform\":\"windows\",\"architecture\":\"x64\",\"status\":\"verified\",\"version\":\"19.42.0\",\"toolchainDigest\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"}\n",
    stderr: "",
  };
}

function accessDeniedGuardian(): WfpOfflineBoundaryDependencies["startGuardian"] {
  return async () => {
    const frames: GuardianFrame[] = [
      { kind: "Hello" },
      { kind: "Error", code: "WFPAccessDenied" },
    ];
    return {
      readFrame: async () => {
        const frame = frames.shift();
        if (frame === undefined) throw new Error("guardian exited");
        return frame;
      },
      writeFrame: async () => undefined,
      waitForExit: async () => { throw new Error("guardian failed"); },
      terminate: () => undefined,
    };
  };
}

test("local WFP access denial fails after verified preflight before Service/native side effects", async () => {
  let serviceStarts = 0;
  await assert.rejects(
    installWfpOfflineBoundary({
      required: false,
      __dependencies: {
        platform: "win32",
        resolveOwnerCreationTime: async () => "1337",
        runPreflight: async () => verifiedPreflight(),
        startGuardian: accessDeniedGuardian(),
      },
    }).then((result) => {
      if (result.outcome === "installed") serviceStarts++;
      return result;
    }),
    /Windows Filtering Platform access is unavailable/u,
  );
  assert.equal(serviceStarts, 0);
});

test("required WFP access denial fails before Service/native side effects", async () => {
  let serviceStarts = 0;
  await assert.rejects(
    installWfpOfflineBoundary({
      required: true,
      __dependencies: {
        platform: "win32",
        resolveOwnerCreationTime: async () => "1337",
        runPreflight: async () => verifiedPreflight(),
        startGuardian: accessDeniedGuardian(),
      },
    }).then((result) => {
      if (result.outcome === "installed") serviceStarts++;
      return result;
    }),
    /Windows Filtering Platform access is unavailable/u,
  );
  assert.equal(serviceStarts, 0);
});

test("default sibling preflight and guardian become Ready before Service/native starts", {
  skip: process.platform === "win32" ? false : "Windows WFP integration is unsupported on this platform",
  timeout: 180_000,
}, async (t) => {
  const root = await mkdtemp(join(tmpdir(), "unit-test-ide-wfp-e2e-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const goCache = join(root, "go-cache");
  const serviceExecutable = join(root, "unit-test-service.exe");
  const preflightExecutable = join(root, "coverage-toolset-preflight.exe");
  const guardianExecutable = join(root, "native-offline-guardian.exe");
  const serviceStartEvidence = join(root, "service-started.token");
  const environment = { ...process.env, GOENV: "off", GOTOOLCHAIN: "local", GOCACHE: goCache };

  for (const [output, command] of [
    [serviceExecutable, "./apps/test-service/cmd/unit-test-service"],
    [preflightExecutable, "./apps/test-service/cmd/coverage-toolset-preflight"],
    [guardianExecutable, "./apps/test-service/cmd/native-offline-guardian"],
  ] as const) {
    await execFile("go", ["build", "-trimpath", "-o", output, command], {
      cwd: repositoryRoot,
      env: environment,
      timeout: 120_000,
      windowsHide: true,
      maxBuffer: 16 * 1024 * 1024,
    });
  }

  const required = process.env[requiredEnvironment] === "1";
  const result = await installWfpOfflineBoundary({
    required,
    nativeExecutablePath: serviceExecutable,
  });
  if (result.outcome === "skipped") {
    await assert.rejects(stat(serviceStartEvidence), (error: NodeJS.ErrnoException) => error.code === "ENOENT");
    t.skip(`SKIP: ${result.reason}; required mode would FAIL`);
    return;
  }

  await execFile(serviceExecutable, [`--prepare-token-file=${serviceStartEvidence}`], {
    cwd: repositoryRoot,
    env: environment,
    timeout: 30_000,
    windowsHide: true,
    maxBuffer: 4 * 1024,
  });
  assert.equal((await stat(serviceStartEvidence)).size, 0);
  await result.boundary.close();
});
