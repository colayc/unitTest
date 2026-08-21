import assert from "node:assert/strict";
import http from "node:http";
import test from "node:test";
import {
  installNativeHttpNetworkGuard,
  installWindowsNativeOfflineBoundary,
} from "./native-network-guard.js";

function deferred<T>(): { readonly promise: Promise<T>; resolve(value: T): void } {
  let resolvePromise!: (value: T) => void;
  return { promise: new Promise<T>((resolve) => { resolvePromise = resolve; }), resolve: resolvePromise };
}

test("native E2E network guard rejects HTTP(S) entry points and restores state", () => {
  const originalRequest = http.request;
  const restore = installNativeHttpNetworkGuard();
  try {
    assert.throws(() => http.request("http://127.0.0.1/"), /network guard/u);
  } finally {
    restore();
  }
  assert.equal(http.request, originalRequest);
});

test("public Windows native offline boundary export keeps the boundary contract", async () => {
  const originalRequest = http.request;
  const bye = deferred<{ readonly kind: "Bye" }>();
  const exited = deferred<void>();
  const boundary = await installWindowsNativeOfflineBoundary({
    __dependencies: {
      platform: "win32",
      resolveOwnerCreationTime: async () => "1337",
      runPreflight: async () => ({
        stdout: "{\"schemaVersion\":1,\"platform\":\"windows\",\"architecture\":\"x64\",\"status\":\"verified\",\"version\":\"19.42.0\",\"toolchainDigest\":\"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\"}\n",
        stderr: "",
      }),
      startGuardian: async () => {
        const inbound = [{ kind: "Hello" as const }, { kind: "Ready" as const }, bye.promise];
        return {
          async readFrame() { return await inbound.shift()!; },
          async writeFrame() {
            bye.resolve({ kind: "Bye" });
            setImmediate(() => exited.resolve());
          },
          async waitForExit() { await exited.promise; },
          terminate() { exited.resolve(); },
        };
      },
    },
  });
  assert.equal(typeof boundary.ruleName, "string");
  assert.throws(() => http.request("http://127.0.0.1/"), /network guard/u);
  const guarded = await boundary.runGuarded(async (signal) => {
    assert.equal(signal.aborted, false);
    return "native-result";
  });
  assert.equal(guarded, "native-result");
  await boundary.close();
  assert.equal(http.request, originalRequest);
});

test("HTTP guard remains installed when WFP removal cannot be proven", async () => {
  const boundary = await installWindowsNativeOfflineBoundary({
    __dependencies: {
      platform: "win32",
      resolveOwnerCreationTime: async () => "1337",
      runPreflight: async () => ({
        stdout: "{\"schemaVersion\":1,\"platform\":\"windows\",\"architecture\":\"x64\",\"status\":\"verified\",\"version\":\"19.42.0\",\"toolchainDigest\":\"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\"}\n",
        stderr: "",
      }),
      startGuardian: async () => {
        const inbound = [{ kind: "Hello" as const }, { kind: "Ready" as const }];
        return {
          async readFrame() { return inbound.shift()!; },
          async writeFrame() {},
          async waitForExit() {},
          terminate() {},
        };
      },
    },
  });
  await assert.rejects(boundary.close(), /did not prove WFP boundary removal/u);
  assert.throws(() => http.request("http://127.0.0.1/"), /network guard/u);
});
