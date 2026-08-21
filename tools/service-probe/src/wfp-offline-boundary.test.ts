import assert from "node:assert/strict";
import { randomBytes } from "node:crypto";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  startNativeGuardianForTesting,
  installWfpOfflineBoundary,
  type GuardianFrame,
  type WfpOfflineBoundaryDependencies,
} from "./wfp-offline-boundary.js";

function deferred<T>(): { readonly promise: Promise<T>; resolve(value: T): void } {
  let resolvePromise!: (value: T) => void;
  return { promise: new Promise<T>((resolve) => { resolvePromise = resolve; }), resolve: resolvePromise };
}

function verifiedPreflight(): { readonly stdout: string; readonly stderr: string } {
  return {
    stdout: "{\"schemaVersion\":1,\"platform\":\"windows\",\"architecture\":\"x64\",\"status\":\"verified\",\"version\":\"19.42.0\"}\n",
    stderr: "",
  };
}

function unavailablePreflight(): { readonly stdout: string; readonly stderr: string } {
  return {
    stdout: "{\"schemaVersion\":1,\"platform\":\"windows\",\"architecture\":\"x64\",\"status\":\"unavailable\"}\n",
    stderr: "",
  };
}

function frames(...values: GuardianFrame[]): WfpOfflineBoundaryDependencies["startGuardian"] {
  return async () => {
    const inbound = [...values];
    return {
      readFrame: async () => {
        const frame = inbound.shift();
        if (frame === undefined) throw new Error("unexpected guardian read");
        return frame;
      },
      writeFrame: async () => undefined,
      waitForExit: async () => undefined,
      terminate: () => undefined,
    };
  };
}

test("local unavailable preflight skips before guardian side effects", async () => {
  let guardianStarts = 0;
  const result = await installWfpOfflineBoundary({
    __dependencies: {
      platform: "win32",
      resolveOwnerCreationTime: async () => "1337",
      runPreflight: async () => unavailablePreflight(),
      startGuardian: async () => { guardianStarts++; throw new Error("must not start"); },
    },
  });
  assert.deepEqual(result, { outcome: "skipped", reason: "ToolchainUnavailable" });
  assert.equal(guardianStarts, 0);
});

test("required unavailable preflight fails before guardian side effects", async () => {
  let guardianStarts = 0;
  await assert.rejects(
    installWfpOfflineBoundary({
      required: true,
      __dependencies: {
        platform: "win32",
        resolveOwnerCreationTime: async () => "1337",
        runPreflight: async () => unavailablePreflight(),
        startGuardian: async () => { guardianStarts++; throw new Error("must not start"); },
      },
    }),
    /coverage toolset is unavailable/u,
  );
  assert.equal(guardianStarts, 0);
});

test("verified preflight requires Hello then Ready before boundary is returned", async () => {
  let started = false;
  const boundaryPromise = installWfpOfflineBoundary({
    __dependencies: {
      platform: "win32",
      resolveOwnerCreationTime: async () => "1337",
      runPreflight: async () => verifiedPreflight(),
      startGuardian: async () => {
        started = true;
        return await frames({ kind: "Hello" }, { kind: "Ready" }, { kind: "Bye" })({} as never);
      },
    },
  });
  assert.equal(started, false);
  const boundary = await boundaryPromise;
  assert.equal(started, true);
  assert.equal(boundary.outcome, "installed");
  if (boundary.outcome === "installed") await boundary.boundary.close();
});

test("guardian Ready without Hello fails closed", async () => {
  await assert.rejects(
    installWfpOfflineBoundary({
      __dependencies: {
        platform: "win32",
        resolveOwnerCreationTime: async () => "1337",
        runPreflight: async () => verifiedPreflight(),
        startGuardian: frames({ kind: "Ready" }),
      },
    }),
    /guardian protocol/u,
  );
});

test("close sends Release then waits for Bye and process exit", async () => {
  const bye = deferred<GuardianFrame>();
  const exited = deferred<void>();
  const writes: GuardianFrame[] = [];
  const boundary = await installWfpOfflineBoundary({
    __dependencies: {
      platform: "win32",
      resolveOwnerCreationTime: async () => "1337",
      runPreflight: async () => verifiedPreflight(),
      startGuardian: async () => {
        const inbound: Array<GuardianFrame | Promise<GuardianFrame>> = [
          { kind: "Hello" }, { kind: "Ready" }, bye.promise,
        ];
        return {
          readFrame: async () => await inbound.shift()!,
          writeFrame: async (frame) => { writes.push(frame); },
          waitForExit: async () => await exited.promise,
          terminate: () => undefined,
        };
      },
    },
  });
  assert.equal(boundary.outcome, "installed");
  if (boundary.outcome !== "installed") return;
  const closing = boundary.boundary.close();
  assert.deepEqual(writes, [{ kind: "Release" }]);
  bye.resolve({ kind: "Bye" });
  let settled = false;
  void closing.then(() => { settled = true; });
  await Promise.resolve();
  assert.equal(settled, false, "close must wait for process exit after Bye");
  exited.resolve();
  await closing;
});

test("malformed guardian frames terminate and fail closed", async () => {
  let terminated = false;
  await assert.rejects(
    installWfpOfflineBoundary({
      __dependencies: {
        platform: "win32",
        resolveOwnerCreationTime: async () => "1337",
        runPreflight: async () => verifiedPreflight(),
        startGuardian: async () => ({
          readFrame: async () => ({ kind: "Error", code: "Other" } as unknown as GuardianFrame),
          writeFrame: async () => undefined,
          waitForExit: async () => undefined,
          terminate: () => { terminated = true; },
        }),
      },
    }),
    /guardian protocol/u,
  );
  assert.equal(terminated, true);
});

test("default native guardian wiring canonicalizes a sibling spawn failure", {
  skip: process.platform === "win32" ? false : "Windows named pipes are unavailable",
}, async () => {
  const nonexistentGuardian = join(tmpdir(), `missing-guardian-${randomBytes(8).toString("hex")}.exe`);
  await assert.rejects(
    startNativeGuardianForTesting({
      executable: nonexistentGuardian,
      ownerPid: process.pid,
      ownerCreationTime: "1337",
      ruleName: "UnitTestIDE-NativeOffline-0123456789abcdef",
    }),
    (error: unknown) => {
      assert.match(String(error), /guardian process could not start/u);
      assert.doesNotMatch(String(error), /missing-guardian|[A-Za-z]:[\\/]/u);
      return true;
    },
  );
});
