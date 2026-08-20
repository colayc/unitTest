import assert from "node:assert/strict";
import { execFile as execFileCallback } from "node:child_process";
import http, { get as httpGet, request as httpRequest } from "node:http";
import http2, { connect as http2Connect } from "node:http2";
import https, { get as httpsGet, request as httpsRequest } from "node:https";
import net from "node:net";
import { join, resolve } from "node:path";
import test from "node:test";
import { promisify } from "node:util";
import {
  installNativeHttpNetworkGuard,
  installWindowsNativeOfflineBoundary,
  type WindowsFirewallOfflineOperations
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

test("Windows native offline boundary audits installation and removal around the full child tree", async () => {
  const originalRequest = http.request;
  const trace: string[] = [];
  const operations = fakeFirewallOperations(trace);
  const boundary = await installWindowsNativeOfflineBoundary({
    ownerPid: 4242,
    ruleName: "UnitTestIDE-NativeOffline-0123456789abcdef",
    operations
  });
  assert.deepEqual(trace, [
    "watch:4242:UnitTestIDE-NativeOffline-0123456789abcdef",
    "install:UnitTestIDE-NativeOffline-0123456789abcdef",
    "audit-installed:UnitTestIDE-NativeOffline-0123456789abcdef"
  ]);
  assert.throws(() => http.request("http://127.0.0.1/"), /network guard/u);

  await boundary.close();
  assert.deepEqual(trace.slice(3), [
    "remove:UnitTestIDE-NativeOffline-0123456789abcdef",
    "audit-removed:UnitTestIDE-NativeOffline-0123456789abcdef"
  ]);
  assert.equal(http.request, originalRequest);
  await boundary.close();
  assert.equal(trace.length, 5, "successful cleanup must be idempotent");
});

test("Windows native offline boundary removes partial firewall state when installation audit fails", async () => {
  const originalRequest = http.request;
  const trace: string[] = [];
  const operations = fakeFirewallOperations(trace, { failAuditInstalled: true });
  await assert.rejects(
    installWindowsNativeOfflineBoundary({
      ownerPid: 4343,
      ruleName: "UnitTestIDE-NativeOffline-fedcba9876543210",
      operations
    }),
    /cannot establish audited Windows offline boundary/u
  );
  assert.deepEqual(trace, [
    "watch:4343:UnitTestIDE-NativeOffline-fedcba9876543210",
    "install:UnitTestIDE-NativeOffline-fedcba9876543210",
    "audit-installed:UnitTestIDE-NativeOffline-fedcba9876543210",
    "remove:UnitTestIDE-NativeOffline-fedcba9876543210",
    "audit-removed:UnitTestIDE-NativeOffline-fedcba9876543210"
  ]);
  assert.equal(http.request, originalRequest, "Node guard must also be restored after failed install");
});

test("Windows native offline boundary retries a failed removal without hiding residual state", async () => {
  const trace: string[] = [];
  const operations = fakeFirewallOperations(trace, { failFirstRemove: true });
  const boundary = await installWindowsNativeOfflineBoundary({
    ownerPid: 4444,
    ruleName: "UnitTestIDE-NativeOffline-aabbccddeeff0011",
    operations
  });
  await assert.rejects(boundary.close(), /cannot revoke audited Windows offline boundary/u);
  await boundary.close();
  assert.deepEqual(trace.slice(3), [
    "remove:UnitTestIDE-NativeOffline-aabbccddeeff0011",
    "audit-removed:UnitTestIDE-NativeOffline-aabbccddeeff0011",
    "remove:UnitTestIDE-NativeOffline-aabbccddeeff0011",
    "audit-removed:UnitTestIDE-NativeOffline-aabbccddeeff0011"
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

function fakeFirewallOperations(
  trace: string[],
  faults: { failAuditInstalled?: boolean; failFirstRemove?: boolean } = {}
): WindowsFirewallOfflineOperations {
  let removeAttempts = 0;
  let installed = false;
  return {
    startWatchdog(ruleName, ownerPid) {
      trace.push(`watch:${ownerPid}:${ruleName}`);
    },
    async install(ruleName) {
      trace.push(`install:${ruleName}`);
      installed = true;
    },
    async auditInstalled(ruleName) {
      trace.push(`audit-installed:${ruleName}`);
      if (faults.failAuditInstalled) throw new Error("firewall audit rejected the rule");
    },
    async remove(ruleName) {
      trace.push(`remove:${ruleName}`);
      removeAttempts++;
      if (faults.failFirstRemove && removeAttempts === 1) throw new Error("firewall removal failed");
      installed = false;
    },
    async auditRemoved(ruleName) {
      trace.push(`audit-removed:${ruleName}`);
      if (installed) throw new Error("firewall rule is still installed");
    }
  };
}
