import assert from "node:assert/strict";
import { execFile as execFileCallback, spawn } from "node:child_process";
import { mkdirSync } from "node:fs";
import { access, mkdir, mkdtemp, readFile, rename, rm, symlink, writeFile } from "node:fs/promises";
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

test("Windows native offline boundary gives every guardian a distinct strong nonce", async () => {
  const nonces: string[] = [];
  const operations: WindowsFirewallGuardianOperations = {
    async start(_ruleName, _ownerPid, _stateRoot, guardianNonce) {
      nonces.push(guardianNonce);
      return {
        async waitUntilReady() {},
        async release() {},
        async recover() {}
      };
    }
  };
  for (const ruleName of [
    "UnitTestIDE-NativeOffline-1010101010101010",
    "UnitTestIDE-NativeOffline-2020202020202020"
  ]) {
    const boundary = await installWindowsNativeOfflineBoundary({ ruleName, operations });
    await boundary.close();
  }
  assert.equal(nonces.length, 2);
  assert.match(nonces[0] ?? "", /^[0-9a-f]{64}$/u);
  assert.match(nonces[1] ?? "", /^[0-9a-f]{64}$/u);
  assert.notEqual(nonces[0], nonces[1]);
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
  const stateDirectory = join(fixtureStateRoot(root), ruleName);
  await mkdir(stateDirectory, { recursive: true });
  const child = spawnGuardianFixture({ root, ruleName, stateDirectory, ownerPid: process.pid });
  try {
    await waitForFile(join(stateDirectory, "ready"), child);
    const trace = await readFile(join(root, "fixture-trace.log"), "utf8");
    assert.match(trace, /profile-query:ActiveStore/u);
    assert.doesNotMatch(trace, /^profile-query:\s*$/mu, "default profile-store lookup is forbidden");
    for (const store of ["ActiveStore", "PersistentStore"]) {
      for (const filter of ["application", "address", "port", "service", "interface", "interface-type"]) {
        assert.match(trace, new RegExp(`^filter:${store}:${filter}$`, "mu"));
      }
    }
    await writeFile(join(stateDirectory, "release"), `release=${ruleName}\n`, { flag: "wx" });
    const result = await child.result;
    assert.equal(result.code, 0, result.stderr);
    await access(join(stateDirectory, "removed"));
  } finally {
    child.process.kill();
    await child.result.catch(() => undefined);
    await rm(root, { recursive: true, force: true });
  }
});

test("Windows guardian accepts a canonical command-line-bound nonce before publishing ready", {
  skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
}, async () => {
  const root = await mkdtemp(join(tmpdir(), "offline-guardian-nonce-"));
  const ruleName = "UnitTestIDE-NativeOffline-3030303030303030";
  const stateDirectory = join(fixtureStateRoot(root), ruleName);
  const guardianNonce = "a".repeat(64);
  await mkdir(stateDirectory, { recursive: true });
  const child = spawnGuardianFixture({
    root,
    ruleName,
    stateDirectory,
    ownerPid: process.pid,
    guardianNonce
  });
  try {
    await waitForFile(join(stateDirectory, "ready"), child);
    await writeFile(join(stateDirectory, "release"), `release=${ruleName}\n`, { flag: "wx" });
    const result = await child.result;
    assert.equal(result.code, 0, result.stderr);
    await access(join(stateDirectory, "removed"));
  } finally {
    child.process.kill();
    await child.result.catch(() => undefined);
    await rm(root, { recursive: true, force: true });
  }
});

for (const tamper of ["guardian.pid", "guardian.nonce"] as const) {
  test(`Windows guardian rejects ${tamper} tampering before firewall creation`, {
    skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
  }, async () => {
    const root = await mkdtemp(join(tmpdir(), `offline-guardian-preinstall-${tamper}-`));
    const ruleName = "UnitTestIDE-NativeOffline-4040404040404040";
    const stateDirectory = join(fixtureStateRoot(root), ruleName);
    const guardianNonce = "a".repeat(64);
    await mkdir(stateDirectory, { recursive: true });
    const child = spawnGuardianFixture({
      root,
      ruleName,
      stateDirectory,
      ownerPid: process.pid,
      guardianNonce,
      preInstallDelayMilliseconds: 1_500
    });
    try {
      await waitForTrace(root, "preinstall-audit-finished", child);
      await writeFile(
        join(stateDirectory, tamper),
        tamper === "guardian.pid" ? "2147483647\n" : `nonce=${"b".repeat(64)}\n`
      );
      const result = await finishExpectedGuardianRejection(child, stateDirectory, ruleName);
      assert.notEqual(result.code, 0, `${tamper} tampering must fail the guardian`);
      const trace = await readFile(join(root, "fixture-trace.log"), "utf8");
      assert.doesNotMatch(trace, /^install-start$/mu, "tampered identity must never create a rule");
      assert.ok(
        (trace.match(/^rule-query:ActiveStore$/gmu) ?? []).length >= 4,
        "the rejecting guardian must still finish its stable empty-store cleanup"
      );
      await assert.rejects(access(join(stateDirectory, "ready")));
    } finally {
      child.process.kill();
      await child.result.catch(() => undefined);
      await rm(root, { recursive: true, force: true });
    }
  });
}

for (const tamper of ["guardian.pid", "guardian.nonce", "unexpected"] as const) {
  test(`Windows guardian continuously rejects ${tamper} tampering after readiness`, {
    skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
  }, async () => {
    const root = await mkdtemp(join(tmpdir(), `offline-guardian-running-${tamper}-`));
    const ruleName = "UnitTestIDE-NativeOffline-5050505050505050";
    const stateDirectory = join(fixtureStateRoot(root), ruleName);
    const guardianNonce = "a".repeat(64);
    await mkdir(stateDirectory, { recursive: true });
    const child = spawnGuardianFixture({
      root,
      ruleName,
      stateDirectory,
      ownerPid: process.pid,
      guardianNonce
    });
    try {
      await waitForFile(join(stateDirectory, "ready"), child);
      const tamperedContent = tamper === "guardian.pid"
        ? "2147483647\n"
        : tamper === "guardian.nonce"
          ? `nonce=${"b".repeat(64)}\n`
          : "unexpected\n";
      await writeFile(join(stateDirectory, tamper), tamperedContent);
      const result = await child.result;
      assert.notEqual(result.code, 0, `${tamper} tampering must fail the running guardian`);
      const trace = await readFile(join(root, "fixture-trace.log"), "utf8");
      assert.match(trace, /^remove:\d+$/mu, "tampering must still trigger firewall cleanup");
      await assert.rejects(
        access(join(stateDirectory, "removed")),
        "invalid state must not receive canonical removal proof"
      );
    } finally {
      child.process.kill();
      await child.result.catch(() => undefined);
      await rm(root, { recursive: true, force: true });
    }
  });
}

const storeTamperFields = [
  "Name",
  "DisplayName",
  "Enabled",
  "Direction",
  "Action",
  "Profile",
  "Group",
  "Application",
  "Address",
  "Port",
  "Service",
  "Interface",
  "InterfaceType"
] as const;

for (const tamperStore of ["ActiveStore", "PersistentStore"] as const) {
  for (const tamperField of storeTamperFields) {
    test(`Windows guardian rejects ${tamperStore} ${tamperField} tampering independently`, {
      skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
    }, async () => {
      const root = await mkdtemp(join(tmpdir(), `offline-guardian-${tamperStore}-${tamperField}-`));
      const ruleName = "UnitTestIDE-NativeOffline-2468aaaabbbb1357";
      const stateDirectory = join(fixtureStateRoot(root), ruleName);
      await mkdir(stateDirectory, { recursive: true });
      const child = spawnGuardianFixture({
        root,
        ruleName,
        stateDirectory,
        ownerPid: process.pid,
        tamperStore,
        tamperField
      });
      try {
        const result = await finishExpectedGuardianRejection(child, stateDirectory, ruleName);
        assert.notEqual(result.code, 0, `${tamperStore} ${tamperField} tampering reached ready`);
        await assert.rejects(access(join(stateDirectory, "ready")));
        await access(join(stateDirectory, "removed"));
      } finally {
        child.process.kill();
        await child.result.catch(() => undefined);
        await rm(root, { recursive: true, force: true });
      }
    });
  }
}

for (const profileScenario of ["MissingProfile", "ExtraProfile", "DisabledProfile"] as const) {
  test(`Windows guardian rejects the ${profileScenario} ActiveStore profile set and cleans up`, {
    skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
  }, async () => {
    const root = await mkdtemp(join(tmpdir(), `offline-guardian-${profileScenario}-`));
    const ruleName = "UnitTestIDE-NativeOffline-5555666677778888";
    const stateDirectory = join(fixtureStateRoot(root), ruleName);
    await mkdir(stateDirectory, { recursive: true });
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
  const stateDirectory = join(fixtureStateRoot(root), ruleName);
  await mkdir(stateDirectory, { recursive: true });
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

test("Windows CI CleanupAll creates a missing clean state root and still proves both stores empty", {
  skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
}, async () => {
  const root = await mkdtemp(join(tmpdir(), "offline-guardian-missing-root-"));
  const cleanup = spawnGuardianFixture({
    root,
    ruleName: "UnitTestIDE-NativeOffline-10101010aaaabbbb",
    action: "CleanupAll",
    createStateRoot: false,
    traceName: "cleanup.log"
  });
  try {
    const result = await cleanup.result;
    assert.equal(result.code, 0, result.stderr);
    await access(fixtureStateRoot(root));
    await assertGlobalCleanupTrace(root, "cleanup.log");
  } finally {
    cleanup.process.kill();
    await cleanup.result.catch(() => undefined);
    await rm(root, { recursive: true, force: true });
  }
});

test("Windows CI CleanupAll fails closed on native process enumeration errors after global cleanup", {
  skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
}, async () => {
  const root = await mkdtemp(join(tmpdir(), "offline-guardian-enumeration-error-"));
  const cleanup = spawnGuardianFixture({
    root,
    ruleName: "UnitTestIDE-NativeOffline-20202020aaaabbbb",
    action: "CleanupAll",
    deadlineSeconds: 1,
    processEnumerationScenario: "Failure",
    traceName: "cleanup.log"
  });
  try {
    const result = await cleanup.result;
    assert.notEqual(result.code, 0, "an unprovable guardian enumeration must fail closed");
    assert.match(result.stderr, /process enumeration failure|cleanup did not converge/ui);
    await assertGlobalCleanupTrace(root, "cleanup.log");
  } finally {
    cleanup.process.kill();
    await cleanup.result.catch(() => undefined);
    await rm(root, { recursive: true, force: true });
  }
});

test("Windows CI CleanupAll ignores a legal ordinary PowerShell command with an unmatched quote", {
  skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
}, async () => {
  const root = await mkdtemp(join(tmpdir(), "offline-guardian-native-argv-"));
  const systemRoot = process.env.SystemRoot;
  assert.ok(systemRoot);
  const powershell = join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe");
  let ordinaryStderr = "";
  const ordinary = spawn(powershell, [
    '-NoLogo -NoProfile -NonInteractive -Command "Start-Sleep -Seconds 10'
  ], {
    stdio: ["ignore", "ignore", "pipe"],
    windowsHide: true,
    windowsVerbatimArguments: true
  });
  ordinary.stderr.setEncoding("utf8");
  ordinary.stderr.on("data", (chunk: string) => { ordinaryStderr += chunk; });
  const cleanup = spawnGuardianFixture({
    root,
    ruleName: "UnitTestIDE-NativeOffline-30303030aaaabbbb",
    action: "CleanupAll",
    traceName: "cleanup.log"
  });
  try {
    await new Promise((resolveResult) => setTimeout(resolveResult, 250));
    assert.equal(ordinary.exitCode, null, `ordinary PowerShell did not remain live: ${ordinaryStderr}`);
    const result = await cleanup.result;
    assert.equal(result.code, 0, result.stderr);
    assert.equal(ordinary.exitCode, null, "cleanup must not terminate an unrelated PowerShell process");
    await assertGlobalCleanupTrace(root, "cleanup.log");
  } finally {
    ordinary.kill();
    cleanup.process.kill();
    await cleanup.result.catch(() => undefined);
    await rm(root, { recursive: true, force: true });
  }
});

test("Windows CI CleanupAll disarms a delayed guardian after StateRoot becomes a junction", {
  skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
}, async () => {
  const root = await mkdtemp(join(tmpdir(), "offline-guardian-root-junction-live-"));
  const stateRoot = fixtureStateRoot(root);
  const replacementTarget = join(root, "state-target");
  const ruleName = "UnitTestIDE-NativeOffline-40404040aaaabbbb";
  const stateDirectory = join(stateRoot, ruleName);
  await mkdir(stateDirectory, { recursive: true });
  const guardian = spawnGuardianFixture({
    root,
    ruleName,
    stateDirectory,
    ownerPid: process.pid,
    preInstallDelayMilliseconds: 2_000,
    traceName: "guardian.log"
  });
  let cleanup: SpawnedGuardianFixture | undefined;
  try {
    await waitForTrace(root, "preinstall-audit-finished", guardian, "guardian.log");
    await rename(stateRoot, replacementTarget);
    await symlink(replacementTarget, stateRoot, "junction");
    cleanup = spawnGuardianFixture({
      root,
      ruleName,
      action: "CleanupAll",
      deadlineSeconds: 5,
      traceName: "cleanup.log"
    });
    const earlyResult = await Promise.race([
      cleanup.result.then((result) => result),
      new Promise<undefined>((resolveResult) => setTimeout(resolveResult, 750))
    ]);
    assert.equal(earlyResult, undefined, "cleanup must not return while the command-bound creator is live");
    const guardianResult = await guardian.result;
    const cleanupResult = await cleanup.result;
    assert.notEqual(guardianResult.code, 0, "a guardian must reject a reparse StateRoot");
    assert.notEqual(cleanupResult.code, 0, "StateRoot corruption must fail after cleanup converges");
    const guardianTrace = await readFile(join(root, "guardian.log"), "utf8");
    assert.doesNotMatch(guardianTrace, /^install-start$/mu, "root corruption must remove every late-create path");
    assert.match(guardianTrace, /^rule-query:ActiveStore$/mu);
    assert.match(guardianTrace, /^rule-query:PersistentStore$/mu);
    await assertGlobalCleanupTrace(root, "cleanup.log");
  } finally {
    guardian.process.kill();
    cleanup?.process.kill();
    await guardian.result.catch(() => undefined);
    await cleanup?.result.catch(() => undefined);
    await rm(stateRoot, { recursive: true, force: true });
    await rm(replacementTarget, { recursive: true, force: true });
    await rm(root, { recursive: true, force: true });
  }
});

test("Windows CI CleanupAll globally removes an installed rule after StateRoot becomes a file", {
  skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
}, async () => {
  const root = await mkdtemp(join(tmpdir(), "offline-guardian-root-file-live-"));
  const stateRoot = fixtureStateRoot(root);
  const ruleName = "UnitTestIDE-NativeOffline-50505050aaaabbbb";
  const stateDirectory = join(stateRoot, ruleName);
  await mkdir(stateDirectory, { recursive: true });
  const guardian = spawnGuardianFixture({
    root,
    ruleName,
    stateDirectory,
    ownerPid: process.pid,
    traceName: "guardian.log"
  });
  let cleanup: SpawnedGuardianFixture | undefined;
  try {
    await waitForFile(join(stateDirectory, "ready"), guardian);
    await rm(stateRoot, { recursive: true, force: true });
    await writeFile(stateRoot, "replacement\n");
    cleanup = spawnGuardianFixture({
      root,
      ruleName,
      action: "CleanupAll",
      createStateRoot: false,
      deadlineSeconds: 4,
      traceName: "cleanup.log"
    });
    const guardianResult = await guardian.result;
    const cleanupResult = await cleanup.result;
    assert.notEqual(guardianResult.code, 0, "a guardian must reject an ordinary-file StateRoot");
    assert.notEqual(cleanupResult.code, 0, "StateRoot corruption must fail after cleanup converges");
    const guardianTrace = await readFile(join(root, "guardian.log"), "utf8");
    assert.match(guardianTrace, /^remove:\d+$/mu, "the guardian must remove its installed rule");
    await assertGlobalCleanupTrace(root, "cleanup.log");
  } finally {
    guardian.process.kill();
    cleanup?.process.kill();
    await guardian.result.catch(() => undefined);
    await cleanup?.result.catch(() => undefined);
    await rm(root, { recursive: true, force: true });
  }
});

test("Windows CI CleanupAll waits out a guardian concurrently finishing installation", {
  skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
}, async () => {
  const root = await mkdtemp(join(tmpdir(), "offline-guardian-cleanup-race-"));
  const ruleName = "UnitTestIDE-NativeOffline-1234aaaabbbb5678";
  const stateDirectory = join(fixtureStateRoot(root), ruleName);
  await mkdir(stateDirectory, { recursive: true });
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
    installDelayMilliseconds: 2_000,
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

test("Windows CI CleanupAll ignores dead-PID substitution and forged removal while a guardian is live", {
  skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
}, async () => {
  const root = await mkdtemp(join(tmpdir(), "offline-guardian-forged-removed-"));
  const ruleName = "UnitTestIDE-NativeOffline-aaaabbbbcccc7777";
  const stateDirectory = join(fixtureStateRoot(root), ruleName);
  await mkdir(stateDirectory, { recursive: true });
  const guardian = spawnGuardianFixture({
    root,
    ruleName,
    stateDirectory,
    ownerPid: process.pid,
    installDelayMilliseconds: 3_000,
    traceName: "guardian.log"
  });
  let cleanup: SpawnedGuardianFixture | undefined;
  try {
    await waitForTrace(root, "install-start", guardian, "guardian.log");
    await writeFile(join(stateDirectory, "guardian.pid"), "2147483647\n");
    await writeFile(join(stateDirectory, "removed"), `removed=${ruleName}\n`, { flag: "wx" });
    cleanup = spawnGuardianFixture({
      root,
      ruleName,
      action: "CleanupAll",
      deadlineSeconds: 5,
      traceName: "cleanup.log"
    });
    const firstExit = await Promise.race([
      cleanup.result.then((result) => ({ process: "cleanup" as const, result })),
      guardian.result.then((result) => ({ process: "guardian" as const, result }))
    ]);
    assert.equal(firstExit.process, "guardian", "writable markers must not hide a live creator");
    const guardianResult = await guardian.result;
    const cleanupResult = await cleanup.result;
    assert.notEqual(cleanupResult.code, 0, "cleanup must report the durable forged state after convergence");
    assert.match(cleanupResult.stderr, /guardian state is invalid|PID marker/ui);
    assert.notEqual(guardianResult.code, 0, "self-PID substitution must fail the guardian");
    const trace = await readFile(join(root, "guardian.log"), "utf8");
    assert.match(trace, /^remove:\d+$/mu, "the late creator must remove its rule before exit");
    await assert.rejects(access(join(stateDirectory, "ready")));
  } finally {
    guardian.process.kill();
    cleanup?.process.kill();
    await guardian.result.catch(() => undefined);
    await cleanup?.result.catch(() => undefined);
    await rm(root, { recursive: true, force: true });
  }
});

test("Windows CI CleanupAll rejects an unexpected ordinary file at the state root", {
  skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
}, async () => {
  const root = await mkdtemp(join(tmpdir(), "offline-guardian-root-file-"));
  await mkdir(fixtureStateRoot(root));
  await writeFile(join(fixtureStateRoot(root), "unexpected"), "unexpected\n");
  const cleanup = spawnGuardianFixture({
    root,
    ruleName: "UnitTestIDE-NativeOffline-bbbbccccdddd1111",
    action: "CleanupAll",
    deadlineSeconds: 1
  });
  try {
    const result = await cleanup.result;
    assert.notEqual(result.code, 0, "an unexpected root file must fail closed");
  } finally {
    cleanup.process.kill();
    await cleanup.result.catch(() => undefined);
    await rm(root, { recursive: true, force: true });
  }
});

test("Windows CI CleanupAll rejects a non-directory reparse point at the state root", {
  skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
}, async () => {
  const root = await mkdtemp(join(tmpdir(), "offline-guardian-root-reparse-"));
  const target = join(root, "target.txt");
  await mkdir(fixtureStateRoot(root));
  await writeFile(target, "target\n");
  await symlink(target, join(fixtureStateRoot(root), "unexpected-link"), "junction");
  const cleanup = spawnGuardianFixture({
    root,
    ruleName: "UnitTestIDE-NativeOffline-ccccddddeeee2222",
    action: "CleanupAll",
    deadlineSeconds: 1
  });
  try {
    const result = await cleanup.result;
    assert.notEqual(result.code, 0, "a non-directory root reparse point must fail closed");
  } finally {
    cleanup.process.kill();
    await cleanup.result.catch(() => undefined);
    await rm(root, { recursive: true, force: true });
  }
});

test("Windows CI CleanupAll rejects an unknown directory at the state root", {
  skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
}, async () => {
  const root = await mkdtemp(join(tmpdir(), "offline-guardian-root-directory-"));
  await mkdir(join(fixtureStateRoot(root), "unexpected"), { recursive: true });
  const cleanup = spawnGuardianFixture({
    root,
    ruleName: "UnitTestIDE-NativeOffline-ddddeeeeffff3333",
    action: "CleanupAll",
    deadlineSeconds: 1
  });
  try {
    const result = await cleanup.result;
    assert.notEqual(result.code, 0, "an unknown root directory must fail closed");
  } finally {
    cleanup.process.kill();
    await cleanup.result.catch(() => undefined);
    await rm(root, { recursive: true, force: true });
  }
});

test("Windows CI CleanupAll waits for a delayed guardian whose state directory became a file", {
  skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
}, async () => {
  const root = await mkdtemp(join(tmpdir(), "offline-guardian-state-replaced-"));
  const ruleName = "UnitTestIDE-NativeOffline-eeeeffffaaaa4444";
  const stateDirectory = join(fixtureStateRoot(root), ruleName);
  await mkdir(stateDirectory, { recursive: true });
  const guardian = spawnGuardianFixture({
    root,
    ruleName,
    stateDirectory,
    ownerPid: process.pid,
    installDelayMilliseconds: 2_000,
    traceName: "guardian.log"
  });
  let cleanup: SpawnedGuardianFixture | undefined;
  try {
    await waitForTrace(root, "install-start", guardian, "guardian.log");
    await rm(stateDirectory, { recursive: true, force: true });
    await writeFile(stateDirectory, "replacement\n");
    cleanup = spawnGuardianFixture({
      root,
      ruleName,
      action: "CleanupAll",
      deadlineSeconds: 4,
      traceName: "cleanup.log"
    });
    const earlyResult = await Promise.race([
      cleanup.result.then((result) => result),
      new Promise<undefined>((resolveResult) => setTimeout(resolveResult, 750))
    ]);
    assert.equal(earlyResult, undefined, "cleanup must not return while a matching creator is live");
    const guardianResult = await guardian.result;
    const cleanupResult = await cleanup.result;
    assert.notEqual(guardianResult.code, 0, "a replaced state directory must fail the guardian");
    assert.notEqual(cleanupResult.code, 0, "the unexpected replacement file must fail closed");
    const trace = await readFile(join(root, "guardian.log"), "utf8");
    assert.match(trace, /^install-finished$/mu, "the delayed creator completed before cleanup returned");
    assert.match(trace, /^remove:\d+$/mu, "the failed creator must remove its rule before exit");
  } finally {
    guardian.process.kill();
    cleanup?.process.kill();
    await guardian.result.catch(() => undefined);
    await cleanup?.result.catch(() => undefined);
    await rm(root, { recursive: true, force: true });
  }
});

const corruptGuardianStates = [
  ["rule-name", "rule=UnitTestIDE-NativeOffline-deadbeefdeadbeef\n"],
  ["owner.pid", "owner=0\n"],
  ["guardian.nonce", "nonce=not-a-nonce\n"],
  ["guardian.pid", "guardian=not-a-pid\n"],
  ["release", "release=wrong\n"],
  ["ready", "ready=wrong\n"],
  ["removed", "removed=wrong\n"]
] as const;

for (const [markerName, corruptContent] of corruptGuardianStates) {
  test(`Windows CI CleanupAll rejects non-canonical ${markerName} state`, {
    skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
  }, async () => {
    const root = await mkdtemp(join(tmpdir(), `offline-guardian-corrupt-${markerName}-`));
    const ruleName = "UnitTestIDE-NativeOffline-88889999aaaabbbb";
    const stateDirectory = join(fixtureStateRoot(root), ruleName);
    await mkdir(stateDirectory, { recursive: true });
    await writeCanonicalConvergedState(stateDirectory, ruleName);
    await writeFile(join(stateDirectory, markerName), corruptContent);
    const cleanup = spawnGuardianFixture({
      root,
      ruleName,
      action: "CleanupAll",
      deadlineSeconds: 1
    });
    try {
      const result = await cleanup.result;
      assert.notEqual(result.code, 0, `${markerName} corruption must fail closed`);
      assert.match(result.stderr, /state|marker|cleanup did not converge/ui);
    } finally {
      cleanup.process.kill();
      await cleanup.result.catch(() => undefined);
      await rm(root, { recursive: true, force: true });
    }
  });
}

test("Windows CI CleanupAll rejects an extra state leaf and inconsistent marker lifecycle", {
  skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
}, async () => {
  const root = await mkdtemp(join(tmpdir(), "offline-guardian-extra-state-"));
  const ruleName = "UnitTestIDE-NativeOffline-777788889999aaaa";
  const stateDirectory = join(fixtureStateRoot(root), ruleName);
  await mkdir(stateDirectory, { recursive: true });
  await writeCanonicalConvergedState(stateDirectory, ruleName);
  await writeFile(join(stateDirectory, "unexpected"), "unexpected\n");
  const cleanup = spawnGuardianFixture({
    root,
    ruleName,
    action: "CleanupAll",
    deadlineSeconds: 1
  });
  try {
    const result = await cleanup.result;
    assert.notEqual(result.code, 0, "an extra state leaf must fail closed");
  } finally {
    cleanup.process.kill();
    await cleanup.result.catch(() => undefined);
    await rm(root, { recursive: true, force: true });
  }
});

test("Windows CI CleanupAll rejects a reparse marker", {
  skip: process.platform === "win32" ? false : "Windows PowerShell is unavailable"
}, async () => {
  const root = await mkdtemp(join(tmpdir(), "offline-guardian-reparse-marker-"));
  const target = await mkdtemp(join(tmpdir(), "offline-guardian-reparse-target-"));
  const ruleName = "UnitTestIDE-NativeOffline-6666777788889999";
  const stateDirectory = join(fixtureStateRoot(root), ruleName);
  await mkdir(stateDirectory, { recursive: true });
  await writeCanonicalConvergedState(stateDirectory, ruleName, false);
  await symlink(target, join(stateDirectory, "removed"), "junction");
  const cleanup = spawnGuardianFixture({
    root,
    ruleName,
    action: "CleanupAll",
    deadlineSeconds: 1
  });
  try {
    const result = await cleanup.result;
    assert.notEqual(result.code, 0, "a marker reparse point must fail closed");
  } finally {
    cleanup.process.kill();
    await cleanup.result.catch(() => undefined);
    await rm(root, { recursive: true, force: true });
    await rm(target, { recursive: true, force: true });
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
  readonly guardianNonce?: string;
  readonly action?: "Guard" | "CleanupAll";
  readonly profileScenario?: "Valid" | "MissingProfile" | "ExtraProfile" | "DisabledProfile";
  readonly tamperStore?: "None" | "ActiveStore" | "PersistentStore";
  readonly tamperField?: "None" | typeof storeTamperFields[number];
  readonly installDelayMilliseconds?: number;
  readonly preInstallDelayMilliseconds?: number;
  readonly removeFailures?: number;
  readonly queryFailures?: number;
  readonly deadlineSeconds?: number;
  readonly createStateRoot?: boolean;
  readonly processEnumerationScenario?: "Normal" | "Failure";
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
  const stateRoot = fixtureStateRoot(options.root);
  if (options.createStateRoot ?? true) mkdirSync(stateRoot, { recursive: true });
  const arguments_ = [
    "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
    "-File", fixture,
    "-BoundaryScript", script,
    "-Action", options.action ?? "Guard",
    "-RuleName", options.ruleName,
    "-StateRoot", stateRoot,
    "-GuardianNonce", options.guardianNonce ?? "a".repeat(64),
    "-DeadlineSeconds", String(options.deadlineSeconds ?? 5),
    "-ProfileScenario", options.profileScenario ?? "Valid",
    "-TamperStore", options.tamperStore ?? "None",
    "-TamperField", options.tamperField ?? "None",
    "-InstallDelayMilliseconds", String(options.installDelayMilliseconds ?? 0),
    "-PreInstallDelayMilliseconds", String(options.preInstallDelayMilliseconds ?? 0),
    "-RemoveFailures", String(options.removeFailures ?? 0),
    "-QueryFailures", String(options.queryFailures ?? 0)
  ];
  arguments_.push("-ProcessEnumerationScenario", options.processEnumerationScenario ?? "Normal");
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

function fixtureStateRoot(root: string): string {
  return join(root, "state");
}

async function finishExpectedGuardianRejection(
  child: SpawnedGuardianFixture,
  stateDirectory: string,
  ruleName: string
): Promise<{ code: number | null; stderr: string }> {
  const readyPath = join(stateDirectory, "ready");
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    try {
      await access(readyPath);
      await writeFile(join(stateDirectory, "release"), `release=${ruleName}\n`, { flag: "wx" });
      return child.result;
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
    }
    const exited = await Promise.race([
      child.result.then((result) => result),
      new Promise<undefined>((resolveResult) => setTimeout(resolveResult, 25))
    ]);
    if (exited !== undefined) return exited;
  }
  throw new Error("guardian neither rejected tampering nor published ready");
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

async function writeCanonicalConvergedState(
  stateDirectory: string,
  ruleName: string,
  includeRemoved = true
): Promise<void> {
  await writeFile(join(stateDirectory, "rule-name"), `rule=${ruleName}\n`);
  await writeFile(join(stateDirectory, "owner.pid"), `owner=${process.pid}\n`);
  await writeFile(
    join(stateDirectory, "guardian.nonce"),
    `nonce=${"a".repeat(64)}\n`
  );
  await writeFile(join(stateDirectory, "guardian.pid"), "2147483647\n");
  if (includeRemoved) {
    await writeFile(join(stateDirectory, "removed"), `removed=${ruleName}\n`);
  }
}

async function assertGlobalCleanupTrace(root: string, traceName: string): Promise<void> {
  const trace = await readFile(join(root, traceName), "utf8");
  assert.match(trace, /^remove:\d+$/mu, "CleanupAll must attempt the global convergent removal");
  assert.match(trace, /^rule-query:ActiveStore$/mu, "CleanupAll must audit ActiveStore");
  assert.match(trace, /^rule-query:PersistentStore$/mu, "CleanupAll must audit PersistentStore");
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
