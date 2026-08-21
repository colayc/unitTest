import assert from "node:assert/strict";
import { randomBytes } from "node:crypto";
import net from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  GuardianFrameReader,
  startNativeGuardianForTesting,
  installWfpOfflineBoundary,
  type GuardianFrame,
  type WfpOfflineBoundaryDependencies,
} from "./wfp-offline-boundary.js";

function deferred<T>(): { readonly promise: Promise<T>; resolve(value: T): void; reject(error: Error): void } {
  let resolvePromise!: (value: T) => void;
  let rejectPromise!: (error: Error) => void;
  const promise = new Promise<T>((resolve, reject) => {
    resolvePromise = resolve;
    rejectPromise = reject;
  });
  return { promise, resolve: resolvePromise, reject: rejectPromise };
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

test("startup failure waits for guardian termination before rejecting", async () => {
  const terminated = deferred<void>();
  const installing = installWfpOfflineBoundary({
    __dependencies: {
      platform: "win32",
      resolveOwnerCreationTime: async () => "1337",
      runPreflight: async () => verifiedPreflight(),
      startGuardian: async () => ({
        readFrame: async () => ({ kind: "Ready" }),
        writeFrame: async () => undefined,
        waitForExit: async () => undefined,
        terminate: async () => await terminated.promise,
      }),
    },
  });
  let settled = false;
  void installing.catch(() => undefined).then(() => { settled = true; });
  await new Promise<void>((resolve) => setImmediate(resolve));
  assert.equal(settled, false, "startup must not report cleanup before the guardian exits");
  terminated.resolve();
  await assert.rejects(installing, /guardian protocol/u);
});

test("startup termination wait is bounded when the guardian never exits", async () => {
  await assert.rejects(
    Promise.race([
      installWfpOfflineBoundary({
        __dependencies: {
          platform: "win32",
          guardianTimeoutMilliseconds: 20,
          resolveOwnerCreationTime: async () => "1337",
          runPreflight: async () => verifiedPreflight(),
          startGuardian: async () => ({
            readFrame: async () => ({ kind: "Ready" }),
            writeFrame: async () => undefined,
            waitForExit: async () => await new Promise<void>(() => undefined),
            terminate: async () => await new Promise<void>(() => undefined),
          }),
        },
      }),
      new Promise<never>((_, reject) => setTimeout(() => reject(new Error("termination remained pending")), 250)),
    ]),
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

test("guardian crash after Ready prevents a guarded native callback from starting", async () => {
  const nextFrame = deferred<GuardianFrame>();
  const exited = deferred<void>();
  let callbackStarted = false;
  const boundary = await installWfpOfflineBoundary({
    __dependencies: {
      platform: "win32",
      resolveOwnerCreationTime: async () => "1337",
      runPreflight: async () => verifiedPreflight(),
      startGuardian: async () => {
        const inbound: Array<GuardianFrame | Promise<GuardianFrame>> = [
          { kind: "Hello" }, { kind: "Ready" }, nextFrame.promise,
        ];
        return {
          readFrame: async () => await inbound.shift()!,
          writeFrame: async () => undefined,
          waitForExit: async () => await exited.promise,
          terminate: () => undefined,
        };
      },
    },
  });
  assert.equal(boundary.outcome, "installed");
  if (boundary.outcome !== "installed") return;

  exited.reject(new Error("injected guardian crash"));
  await assert.rejects(
    boundary.boundary.runGuarded(async () => { callbackStarted = true; }),
    /guardian.*lost|WFP boundary.*lost/iu,
  );
  assert.equal(callbackStarted, false);
});

test("guardian crash during a guarded native callback aborts it and runs fail-closed cleanup", async () => {
  const nextFrame = deferred<GuardianFrame>();
  const exited = deferred<void>();
  const started = deferred<void>();
  const trace: string[] = [];
  const boundary = await installWfpOfflineBoundary({
    __dependencies: {
      platform: "win32",
      resolveOwnerCreationTime: async () => "1337",
      runPreflight: async () => verifiedPreflight(),
      startGuardian: async () => {
        const inbound: Array<GuardianFrame | Promise<GuardianFrame>> = [
          { kind: "Hello" }, { kind: "Ready" }, nextFrame.promise,
        ];
        return {
          readFrame: async () => await inbound.shift()!,
          writeFrame: async () => undefined,
          waitForExit: async () => await exited.promise,
          terminate: () => undefined,
        };
      },
    },
  });
  assert.equal(boundary.outcome, "installed");
  if (boundary.outcome !== "installed") return;

  const guarded = boundary.boundary.runGuarded(
    async (signal) => {
      trace.push("native-start");
      started.resolve();
      await new Promise<void>((resolve) => signal.addEventListener("abort", () => {
        trace.push("native-abort");
        resolve();
      }, { once: true }));
      trace.push("native-return");
    },
    async () => { trace.push("service-stop"); },
  );
  await started.promise;
  exited.reject(new Error("injected guardian crash"));
  await assert.rejects(guarded, /guardian.*lost|WFP boundary.*lost/iu);
  assert.deepEqual(trace.slice(0, 3), ["native-start", "native-abort", "service-stop"]);
});

test("guardian crash while close waits for Bye rejects instead of hanging", async () => {
  const nextFrame = deferred<GuardianFrame>();
  const exited = deferred<void>();
  let terminated = false;
  const boundary = await installWfpOfflineBoundary({
    __dependencies: {
      platform: "win32",
      resolveOwnerCreationTime: async () => "1337",
      runPreflight: async () => verifiedPreflight(),
      startGuardian: async () => {
        const inbound: Array<GuardianFrame | Promise<GuardianFrame>> = [
          { kind: "Hello" }, { kind: "Ready" }, nextFrame.promise,
        ];
        return {
          readFrame: async () => await inbound.shift()!,
          writeFrame: async () => undefined,
          waitForExit: async () => await exited.promise,
          terminate: () => { terminated = true; },
        };
      },
    },
  });
  assert.equal(boundary.outcome, "installed");
  if (boundary.outcome !== "installed") return;

  const closing = boundary.boundary.close();
  exited.reject(new Error("injected guardian crash"));
  await assert.rejects(
    Promise.race([
      closing,
      new Promise<never>((_, reject) => setTimeout(() => reject(new Error("close remained pending")), 250)),
    ]),
    /guardian protocol did not prove WFP boundary removal/u,
  );
  assert.equal(terminated, true);
});

test("frame reader rejects a pending Hello or Bye read when its socket closes", async () => {
  const server = net.createServer();
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  assert.notEqual(address, null);
  assert.equal(typeof address, "object");
  if (address === null || typeof address === "string") return;
  const accepted = new Promise<net.Socket>((resolve) => server.once("connection", resolve));
  const peer = net.createConnection({ host: "127.0.0.1", port: address.port });
  const socket = await accepted;
  const reader = new GuardianFrameReader(socket);
  const pending = reader.read();
  peer.destroy();
  await assert.rejects(
    Promise.race([
      pending,
      new Promise<never>((_, reject) => setTimeout(() => reject(new Error("frame read remained pending")), 250)),
    ]),
    /guardian frame is invalid/u,
  );
  socket.destroy();
  server.close();
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
