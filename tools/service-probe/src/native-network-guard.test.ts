import assert from "node:assert/strict";
import { execFile as execFileCallback, spawn } from "node:child_process";
import { access, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import http, { get as httpGet, request as httpRequest } from "node:http";
import http2, { connect as http2Connect } from "node:http2";
import https, { get as httpsGet, request as httpsRequest } from "node:https";
import net from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";
import { promisify } from "node:util";
import {
  installNativeHttpNetworkGuard,
  installWindowsNativeOfflineBoundary,
  type WindowsFirewallGuardian,
  type WindowsFirewallGuardianOperations
} from "./native-network-guard.js";

const execFile = promisify(execFileCallback);

test("native E2E network guard rejects every standard HTTP(S) entry point", () => {
  const originalNetConnect = net.connect;
  const restore = installNativeHttpNetworkGuard();
  try {
    const expected = /native E2E network guard blocked HTTP\(S\)/;
    assert.equal(httpRequest, http.request);
    assert.equal(httpGet, http.get);
    assert.equal(httpsRequest, https.request);
    assert.equal(httpsGet, https.get);
    assert.equal(http2Connect, http2.connect);
    assert.throws(() => http.request("http://127.0.0.1/"), expected);
    assert.throws(() => http.get("http://127.0.0.1/"), expected);
    assert.throws(() => https.request("https://127.0.0.1/"), expected);
    assert.throws(() => https.get("https://127.0.0.1/"), expected);
    assert.throws(() => http2.connect("https://127.0.0.1/"), expected);
    assert.throws(() => fetch("https://127.0.0.1/"), expected);
    assert.equal(net.connect, originalNetConnect);
  } finally {
    restore();
  }
  assert.notEqual(http.request.name, "blockedHttpRequest");
});

test("Windows native offline boundary waits for guardian readiness and explicit close removal", async () => {
  const originalRequest = http.request;
  const trace: string[] = [];
  const release = deferred<void>();
  const operations = fakeGuardianOperations(trace, { release });
  const boundary = await installWindowsNativeOfflineBoundary({
    ownerPid: 4242,
    ruleName: "UnitTestIDE-NativeOffline-0123456789abcdef",
    stateRoot: resolve(".native-e2e", "runtime", "windows-firewall-guardians"),
    operations
  });
  assert.deepEqual(trace, [
    `start:4242:${resolve(".native-e2e", "runtime", "windows-firewall-guardians")}:UnitTestIDE-NativeOffline-0123456789abcdef`,
    "ready:UnitTestIDE-NativeOffline-0123456789abcdef"
  ]);
  assert.throws(() => http.request("http://127.0.0.1/"), /network guard/u);

  let closeFinished = false;
  const closing = boundary.close().finally(() => { closeFinished = true; });
  await new Promise<void>((resolveResult) => setImmediate(resolveResult));
  assert.equal(closeFinished, false, "normal close must wait for guardian removal confirmation");
  assert.throws(() => http.request("http://127.0.0.1/"), /network guard/u);
  release.resolve();
  await closing;
  assert.deepEqual(trace.slice(2), ["release:UnitTestIDE-NativeOffline-0123456789abcdef"]);
  assert.equal(http.request, originalRequest);
  await boundary.close();
  assert.equal(trace.length, 3, "successful cleanup must be idempotent");
});

test("Windows native offline boundary recovers before exposing a failed guardian readiness", async () => {
  const originalRequest = http.request;
  const trace: string[] = [];
  const recovery = deferred<void>();
  const operations = fakeGuardianOperations(trace, { failReady: true, recovery });
  let installFinished = false;
  const installing = installWindowsNativeOfflineBoundary({
    ownerPid: 4343,
    ruleName: "UnitTestIDE-NativeOffline-fedcba9876543210",
    operations
  }).finally(() => { installFinished = true; });
  await new Promise<void>((resolveResult) => setImmediate(resolveResult));
  assert.equal(installFinished, false, "readiness failure must wait for confirmed OS cleanup");
  assert.throws(() => http.request("http://127.0.0.1/"), /network guard/u);
  recovery.resolve();
  await assert.rejects(installing, /cannot establish audited Windows offline boundary/u);
  assert.deepEqual(trace, [
    `start:4343:${resolve(tmpdir(), "unit-test-ide-native-offline-guardians")}:UnitTestIDE-NativeOffline-fedcba9876543210`,
    "ready:UnitTestIDE-NativeOffline-fedcba9876543210",
    "recover:UnitTestIDE-NativeOffline-fedcba9876543210"
  ]);
  assert.equal(http.request, originalRequest, "Node guard restores only after failed install is cleaned");
});

test("Windows native offline boundary remains fail-closed until abnormal close recovery completes", async () => {
  const originalRequest = http.request;
  const trace: string[] = [];
  const recovery = deferred<void>();
  const operations = fakeGuardianOperations(trace, { failRelease: true, recovery });
  const boundary = await installWindowsNativeOfflineBoundary({
    ownerPid: 4444,
    ruleName: "UnitTestIDE-NativeOffline-aabbccddeeff0011",
    operations
  });
  let closeFinished = false;
  const closing = boundary.close().finally(() => { closeFinished = true; });
  await new Promise<void>((resolveResult) => setImmediate(resolveResult));
  assert.equal(closeFinished, false, "guardian fault must wait for recovery");
  assert.throws(() => http.request("http://127.0.0.1/"), /network guard/u);
  recovery.resolve();
  await assert.rejects(closing, /cannot revoke audited Windows offline boundary/u);
  assert.equal(http.request, originalRequest, "HTTP restores only after recovery confirms OS removal");
  assert.deepEqual(trace.slice(2), [
    "release:UnitTestIDE-NativeOffline-aabbccddeeff0011",
    "recover:UnitTestIDE-NativeOffline-aabbccddeeff0011"
  ]);
});

test("Windows firewall script never masks an unavailable firewall audit", {
  skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
}, async () => {
  const systemRoot = process.env.SystemRoot;
  assert.ok(systemRoot);
  const powershell = join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe");
  const script = resolve(import.meta.dirname, "..", "scripts", "windows-offline-boundary.ps1");
  const arguments_ = [
    "-NoLogo",
    "-NoProfile",
    "-NonInteractive",
    "-ExecutionPolicy",
    "Bypass",
    "-File",
    script,
    "-Action",
    "AuditRemoved",
    "-RuleName",
    "UnitTestIDE-NativeOffline-0011223344556677"
  ];
  let firewallAuditAvailable = true;
  try {
    await execFile(powershell, [
      "-NoLogo",
      "-NoProfile",
      "-NonInteractive",
      "-Command",
      "$ErrorActionPreference='Stop'; @(Get-NetFirewallRule -PolicyStore ActiveStore) | Out-Null"
    ], { windowsHide: true, timeout: 30_000 });
  } catch {
    firewallAuditAvailable = false;
  }
  if (firewallAuditAvailable) {
    await execFile(powershell, arguments_, { windowsHide: true, timeout: 30_000 });
  } else {
    await assert.rejects(
      execFile(powershell, arguments_, { windowsHide: true, timeout: 30_000 }),
      "an unavailable OS firewall audit must fail closed"
    );
  }
});

test("Windows firewall script has no default firewall profile query", async () => {
  const script = resolve(import.meta.dirname, "..", "scripts", "windows-offline-boundary.ps1");
  const source = await readFile(script, "utf8");
  const profileQueries = source
    .split(/\r?\n/u)
    .filter((line) => !line.trimStart().startsWith("#") && line.includes("Get-NetFirewallProfile"));
  assert.equal(profileQueries.length, 1, "the installed-rule audit owns exactly one profile query");
  assert.match(profileQueries[0] ?? "", /Get-NetFirewallProfile\s+-PolicyStore\s+ActiveStore\b/u);
});

test("Windows guardian publishes ready only after a closed ActiveStore profile and filter audit", {
  skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
}, async () => {
  const root = await mkdtemp(join(tmpdir(), "offline-guardian-ready-"));
  const ruleName = "UnitTestIDE-NativeOffline-1111222233334444";
  const stateDirectory = join(root, ruleName);
  await mkdir(stateDirectory);
  const child = spawnGuardianFixture({ root, ruleName, stateDirectory, ownerPid: process.pid });
  try {
    await waitForFile(join(stateDirectory, "ready"), child);
    const trace = await readFile(join(root, "fixture-trace.log"), "utf8");
    assert.match(trace, /profile-query:ActiveStore/u);
    assert.doesNotMatch(trace, /^profile-query:\s*$/mu, "default profile-store lookup is forbidden");
    for (const filter of ["application", "address", "port", "service", "interface", "interface-type"]) {
      assert.match(trace, new RegExp(`^filter:${filter}$`, "mu"));
    }
    await writeFile(join(stateDirectory, "release"), "release\n", { flag: "wx" });
    const result = await child.result;
    assert.equal(result.code, 0, result.stderr);
    await access(join(stateDirectory, "removed"));
  } finally {
    child.process.kill();
    await child.result.catch(() => undefined);
    await rm(root, { recursive: true, force: true });
  }
});

for (const profileScenario of ["MissingProfile", "ExtraProfile", "DisabledProfile"] as const) {
  test(`Windows guardian rejects the ${profileScenario} ActiveStore profile set and cleans up`, {
    skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
  }, async () => {
    const root = await mkdtemp(join(tmpdir(), `offline-guardian-${profileScenario}-`));
    const ruleName = "UnitTestIDE-NativeOffline-5555666677778888";
    const stateDirectory = join(root, ruleName);
    await mkdir(stateDirectory);
    const child = spawnGuardianFixture({
      root,
      ruleName,
      stateDirectory,
      ownerPid: process.pid,
      profileScenario
    });
    try {
      const result = await child.result;
      assert.notEqual(result.code, 0, "an open profile set must fail the guardian");
      await assert.rejects(access(join(stateDirectory, "ready")));
      await access(join(stateDirectory, "removed"));
    } finally {
      child.process.kill();
      await child.result.catch(() => undefined);
      await rm(root, { recursive: true, force: true });
    }
  });
}

test("Windows guardian converges after owner death during install and retries removal errors", {
  skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
}, async () => {
  const root = await mkdtemp(join(tmpdir(), "offline-guardian-owner-death-"));
  const ruleName = "UnitTestIDE-NativeOffline-9999aaaabbbbcccc";
  const stateDirectory = join(root, ruleName);
  await mkdir(stateDirectory);
  const owner = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)"], {
    stdio: "ignore",
    windowsHide: true
  });
  assert.ok(owner.pid);
  const child = spawnGuardianFixture({
    root,
    ruleName,
    stateDirectory,
    ownerPid: owner.pid,
    installDelayMilliseconds: 500,
    removeFailures: 2
  });
  try {
    await waitForTrace(root, "install-start", child);
    owner.kill();
    const result = await child.result;
    assert.equal(result.code, 0, result.stderr);
    await assert.rejects(access(join(stateDirectory, "ready")), "a dead owner must never receive ready");
    await access(join(stateDirectory, "removed"));
    const trace = await readFile(join(root, "fixture-trace.log"), "utf8");
    assert.match(trace, /^install-finished$/mu, "the delayed installer completed before cleanup");
    assert.match(trace, /^remove:3$/mu, "transient cleanup failures must be retried");
  } finally {
    owner.kill();
    child.process.kill();
    await child.result.catch(() => undefined);
    await rm(root, { recursive: true, force: true });
  }
});

test("Windows CI CleanupAll behavior retries removal and proves both stores stably empty", {
  skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
}, async () => {
  const root = await mkdtemp(join(tmpdir(), "offline-guardian-cleanup-all-"));
  const ruleName = "UnitTestIDE-NativeOffline-ddddeeeeffff0000";
  const child = spawnGuardianFixture({
    root,
    ruleName,
    action: "CleanupAll",
    removeFailures: 2,
    queryFailures: 2
  });
  try {
    const result = await child.result;
    assert.equal(result.code, 0, result.stderr);
    const trace = await readFile(join(root, "fixture-trace.log"), "utf8");
    assert.match(trace, /^remove:3$/mu);
    assert.match(trace, /^query-error:2$/mu);
    assert.match(trace, /^rule-query:ActiveStore$/mu);
    assert.match(trace, /^rule-query:PersistentStore$/mu);
  } finally {
    child.process.kill();
    await child.result.catch(() => undefined);
    await rm(root, { recursive: true, force: true });
  }
});

test("Windows CI CleanupAll fails closed when firewall queries never become auditable", {
  skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
}, async () => {
  const root = await mkdtemp(join(tmpdir(), "offline-guardian-cleanup-timeout-"));
  const child = spawnGuardianFixture({
    root,
    ruleName: "UnitTestIDE-NativeOffline-0000111122223333",
    action: "CleanupAll",
    queryFailures: 100,
    deadlineSeconds: 1
  });
  try {
    const result = await child.result;
    assert.notEqual(result.code, 0, "unavailable firewall audits must time out as failure");
    assert.match(result.stderr, /cleanup did not converge/u);
  } finally {
    child.process.kill();
    await child.result.catch(() => undefined);
    await rm(root, { recursive: true, force: true });
  }
});

test("Windows CI CleanupAll waits out a guardian concurrently finishing installation", {
  skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
}, async () => {
  const root = await mkdtemp(join(tmpdir(), "offline-guardian-cleanup-race-"));
  const ruleName = "UnitTestIDE-NativeOffline-1234aaaabbbb5678";
  const stateDirectory = join(root, ruleName);
  await mkdir(stateDirectory);
  const owner = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)"], {
    stdio: "ignore",
    windowsHide: true
  });
  assert.ok(owner.pid);
  const guardian = spawnGuardianFixture({
    root,
    ruleName,
    stateDirectory,
    ownerPid: owner.pid,
    installDelayMilliseconds: 500,
    traceName: "guardian.log"
  });
  try {
    await waitForTrace(root, "install-start", guardian, "guardian.log");
    const cleanup = spawnGuardianFixture({
      root,
      ruleName,
      action: "CleanupAll",
      traceName: "cleanup.log"
    });
    const cleanupResult = await cleanup.result;
    const guardianResult = await guardian.result;
    assert.equal(cleanupResult.code, 0, cleanupResult.stderr);
    assert.equal(guardianResult.code, 0, guardianResult.stderr);
    await access(join(stateDirectory, "removed"));
    await assert.rejects(access(join(stateDirectory, "ready")));
  } finally {
    owner.kill();
    guardian.process.kill();
    await guardian.result.catch(() => undefined);
    await rm(root, { recursive: true, force: true });
  }
});

test("Windows native offline boundary keeps HTTP blocked when recovery cannot prove cleanup", async () => {
  const originalRequest = http.request;
  const trace: string[] = [];
  const operations = fakeGuardianOperations(trace, {
    failRelease: true,
    recoveryFailures: 1
  });
  const boundary = await installWindowsNativeOfflineBoundary({
    ownerPid: 4545,
    ruleName: "UnitTestIDE-NativeOffline-abcdefabcdefabcd",
    operations
  });
  await assert.rejects(boundary.close(), /cannot revoke audited Windows offline boundary/u);
  assert.throws(() => http.request("http://127.0.0.1/"), /network guard/u);
  await assert.rejects(boundary.close(), /cannot revoke audited Windows offline boundary/u);
  assert.equal(http.request, originalRequest, "a later confirmed recovery may restore the Node guard");
  assert.equal(trace.filter((entry) => entry.startsWith("recover:" )).length, 2);
});

interface GuardianFixtureOptions {
  readonly root: string;
  readonly ruleName: string;
  readonly stateDirectory?: string;
  readonly ownerPid?: number;
  readonly action?: "Guard" | "CleanupAll";
  readonly profileScenario?: "Valid" | "MissingProfile" | "ExtraProfile" | "DisabledProfile";
  readonly installDelayMilliseconds?: number;
  readonly removeFailures?: number;
  readonly queryFailures?: number;
  readonly deadlineSeconds?: number;
  readonly traceName?: string;
}

interface SpawnedGuardianFixture {
  readonly process: ReturnType<typeof spawn>;
  readonly result: Promise<{ code: number | null; stderr: string }>;
}

function spawnGuardianFixture(options: GuardianFixtureOptions): SpawnedGuardianFixture {
  const systemRoot = process.env.SystemRoot;
  assert.ok(systemRoot);
  const powershell = join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe");
  const fixture = resolve(import.meta.dirname, "..", "testdata", "windows-offline-guardian-fixture.ps1");
  const script = resolve(import.meta.dirname, "..", "scripts", "windows-offline-boundary.ps1");
  const arguments_ = [
    "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
    "-File", fixture,
    "-BoundaryScript", script,
    "-Action", options.action ?? "Guard",
    "-RuleName", options.ruleName,
    "-StateRoot", options.root,
    "-DeadlineSeconds", String(options.deadlineSeconds ?? 5),
    "-ProfileScenario", options.profileScenario ?? "Valid",
    "-InstallDelayMilliseconds", String(options.installDelayMilliseconds ?? 0),
    "-RemoveFailures", String(options.removeFailures ?? 0),
    "-QueryFailures", String(options.queryFailures ?? 0)
  ];
  arguments_.push("-TraceName", options.traceName ?? "fixture-trace.log");
  if (options.stateDirectory !== undefined) arguments_.push("-StateDirectory", options.stateDirectory);
  if (options.ownerPid !== undefined) arguments_.push("-OwnerPid", String(options.ownerPid));
  const child = spawn(powershell, arguments_, { stdio: ["ignore", "pipe", "pipe"], windowsHide: true });
  let stderr = "";
  child.stderr.setEncoding("utf8");
  child.stderr.on("data", (chunk: string) => { stderr += chunk; });
  return {
    process: child,
    result: new Promise((resolveResult, reject) => {
      child.once("error", reject);
      child.once("exit", (code) => resolveResult({ code, stderr }));
    })
  };
}

async function waitForFile(path: string, child: SpawnedGuardianFixture): Promise<void> {
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    try {
      await access(path);
      return;
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
    }
    const exited = await Promise.race([
      child.result.then((result) => result),
      new Promise<undefined>((resolveResult) => setTimeout(resolveResult, 25))
    ]);
    if (exited !== undefined) throw new Error(`guardian exited before ${path}: ${exited.stderr}`);
  }
  throw new Error(`guardian did not create ${path}`);
}

async function waitForTrace(
  root: string,
  expected: string,
  child: SpawnedGuardianFixture,
  traceName = "fixture-trace.log"
): Promise<void> {
  const tracePath = join(root, traceName);
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    try {
      const trace = await readFile(tracePath, "utf8");
      if (trace.split(/\r?\n/u).includes(expected)) return;
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
    }
    const exited = await Promise.race([
      child.result.then((result) => result),
      new Promise<undefined>((resolveResult) => setTimeout(resolveResult, 25))
    ]);
    if (exited !== undefined) throw new Error(`guardian exited before ${expected}: ${exited.stderr}`);
  }
  throw new Error(`guardian trace did not contain ${expected}`);
}

function fakeGuardianOperations(
  trace: string[],
  faults: {
    failReady?: boolean;
    failRelease?: boolean;
    release?: Deferred<void>;
    recovery?: Deferred<void>;
    recoveryFailures?: number;
  } = {}
): WindowsFirewallGuardianOperations {
  let recoveryAttempts = 0;
  return {
    async start(ruleName, ownerPid, stateRoot): Promise<WindowsFirewallGuardian> {
      trace.push(`start:${ownerPid}:${stateRoot}:${ruleName}`);
      return {
        async waitUntilReady() {
          trace.push(`ready:${ruleName}`);
          if (faults.failReady) throw new Error("guardian readiness failed");
        },
        async release() {
          trace.push(`release:${ruleName}`);
          if (faults.failRelease) throw new Error("guardian release failed");
          await faults.release?.promise;
        },
        async recover() {
          trace.push(`recover:${ruleName}`);
          recoveryAttempts++;
          if (recoveryAttempts <= (faults.recoveryFailures ?? 0)) {
            throw new Error("guardian recovery did not prove cleanup");
          }
          await faults.recovery?.promise;
        }
      };
    }
  };
}

interface Deferred<T> {
  readonly promise: Promise<T>;
  resolve(value: T | PromiseLike<T>): void;
}

function deferred<T>(): Deferred<T> {
  let resolvePromise!: (value: T | PromiseLike<T>) => void;
  const promise = new Promise<T>((resolveResult) => { resolvePromise = resolveResult; });
  return { promise, resolve: resolvePromise };
}
