import { execFile as execFileCallback, spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { lstat, readFile, rm } from "node:fs/promises";
import http from "node:http";
import http2 from "node:http2";
import https from "node:https";
import { syncBuiltinESMExports } from "node:module";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { promisify } from "node:util";

const blockedMessage = "native E2E network guard blocked HTTP(S) request";
const firewallRulePrefix = "UnitTestIDE-NativeOffline-";
const firewallScript = resolve(import.meta.dirname, "..", "scripts", "windows-offline-boundary.ps1");
const execFile = promisify(execFileCallback);
let installed = false;

export interface WindowsFirewallOfflineOperations {
  startWatchdog(ruleName: string, ownerPid: number): void | Promise<void>;
  install(ruleName: string): Promise<void>;
  auditInstalled(ruleName: string): Promise<void>;
  remove(ruleName: string): Promise<void>;
  auditRemoved(ruleName: string): Promise<void>;
}

export interface WindowsNativeOfflineBoundary {
  readonly ruleName: string;
  close(): Promise<void>;
}

export interface WindowsNativeOfflineBoundaryOptions {
  readonly ownerPid?: number;
  readonly ruleName?: string;
  readonly operations?: WindowsFirewallOfflineOperations;
}

export function installNativeHttpNetworkGuard(): () => void {
  if (installed) {
    throw new Error("native E2E network guard is already installed");
  }
  installed = true;
  const restorers: Array<() => void> = [];
  try {
    replace(http, "request", blockedHttpRequest, restorers);
    replace(http, "get", blockedHttpRequest, restorers);
    replace(https, "request", blockedHttpRequest, restorers);
    replace(https, "get", blockedHttpRequest, restorers);
    replace(http2, "connect", blockedHttpRequest, restorers);
    replace(globalThis, "fetch", blockedHttpRequest, restorers);
    syncBuiltinESMExports();
  } catch (error) {
    restoreAll(restorers);
    syncBuiltinESMExports();
    installed = false;
    throw error;
  }

  let restored = false;
  return () => {
    if (restored) return;
    restored = true;
    restoreAll(restorers);
    syncBuiltinESMExports();
    installed = false;
  };
}

export async function installWindowsNativeOfflineBoundary(
  options: WindowsNativeOfflineBoundaryOptions = {}
): Promise<WindowsNativeOfflineBoundary> {
  const ruleName = options.ruleName ?? `${firewallRulePrefix}${randomBytes(16).toString("hex")}`;
  requireRuleName(ruleName);
  const ownerPid = options.ownerPid ?? process.pid;
  if (!Number.isSafeInteger(ownerPid) || ownerPid <= 0) {
    throw new Error("Windows offline boundary owner PID is invalid");
  }
  const operations = options.operations ?? defaultWindowsFirewallOperations();
  const restoreHTTP = installNativeHttpNetworkGuard();
  try {
    await operations.startWatchdog(ruleName, ownerPid);
    await operations.install(ruleName);
    await operations.auditInstalled(ruleName);
  } catch (error) {
    const errors = [error];
    errors.push(...await removeAndAudit(ruleName, operations));
    restoreHTTP();
    throw new AggregateError(errors, "cannot establish audited Windows offline boundary");
  }

  let closed = false;
  return {
    ruleName,
    async close(): Promise<void> {
      if (closed) return;
      const errors = await removeAndAudit(ruleName, operations);
      restoreHTTP();
      if (errors.length > 0) {
        throw new AggregateError(errors, "cannot revoke audited Windows offline boundary");
      }
      closed = true;
    }
  };
}

async function removeAndAudit(
  ruleName: string,
  operations: WindowsFirewallOfflineOperations
): Promise<unknown[]> {
  const errors: unknown[] = [];
  try {
    await operations.remove(ruleName);
  } catch (error) {
    errors.push(error);
  }
  try {
    await operations.auditRemoved(ruleName);
  } catch (error) {
    errors.push(error);
  }
  return errors;
}

function defaultWindowsFirewallOperations(): WindowsFirewallOfflineOperations {
  if (process.platform !== "win32") {
    throw new Error("Windows native offline boundary is Windows-only");
  }
  const systemRoot = process.env.SystemRoot?.trim();
  if (systemRoot === undefined || systemRoot.length === 0) {
    throw new Error("Windows native offline boundary cannot resolve SystemRoot");
  }
  const powershell = join(
    systemRoot,
    "System32",
    "WindowsPowerShell",
    "v1.0",
    "powershell.exe"
  );
  const run = async (action: "Install" | "AuditInstalled" | "Remove" | "AuditRemoved", ruleName: string) => {
    await execFile(
      powershell,
      powershellArguments(action, ruleName),
      {
        encoding: "utf8",
        timeout: 30_000,
        windowsHide: true,
        maxBuffer: 1024 * 1024
      }
    );
  };
  return {
    async startWatchdog(ruleName, ownerPid) {
      const readyPath = join(
        tmpdir(),
        `.unit-test-ide-offline-watchdog-${ownerPid}-${randomBytes(16).toString("hex")}.ready`
      );
      await rm(readyPath, { force: true });
      const child = spawn(
        powershell,
        [
          ...powershellArguments("Watch", ruleName),
          "-OwnerPid",
          String(ownerPid),
          "-ReadyPath",
          readyPath
        ],
        { detached: true, stdio: "ignore", windowsHide: true }
      );
      let childFailure: Error | undefined;
      child.once("error", (error) => { childFailure = error; });
      child.once("exit", (code, signal) => {
        childFailure ??= new Error(
          `Windows offline watchdog exited before readiness (code ${String(code)}, signal ${String(signal)})`
        );
      });
      const deadline = Date.now() + 10_000;
      try {
        while (Date.now() < deadline) {
          if (childFailure !== undefined) throw childFailure;
          try {
            const info = await lstat(readyPath);
            if (!info.isFile() || info.isSymbolicLink()) {
              throw new Error("Windows offline watchdog readiness marker is unsafe");
            }
            const marker = await readFile(readyPath, "utf8");
            if (marker === `${ownerPid}\n`) {
              return;
            }
          } catch (error) {
            if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
          }
          await delay(25);
        }
        throw new Error("Windows offline watchdog did not prove readiness");
      } finally {
        child.unref();
        await rm(readyPath, { force: true }).catch(() => undefined);
      }
    },
    install: (ruleName) => run("Install", ruleName),
    auditInstalled: (ruleName) => run("AuditInstalled", ruleName),
    remove: (ruleName) => run("Remove", ruleName),
    auditRemoved: (ruleName) => run("AuditRemoved", ruleName)
  };
}

function powershellArguments(
  action: "Install" | "AuditInstalled" | "Remove" | "AuditRemoved" | "Watch",
  ruleName: string
): string[] {
  return [
    "-NoLogo",
    "-NoProfile",
    "-NonInteractive",
    "-ExecutionPolicy",
    "Bypass",
    "-File",
    firewallScript,
    "-Action",
    action,
    "-RuleName",
    ruleName
  ];
}

function requireRuleName(ruleName: string): void {
  if (!/^UnitTestIDE-NativeOffline-[0-9a-f]{16,64}$/u.test(ruleName)) {
    throw new Error("Windows offline boundary rule name is invalid");
  }
}

function blockedHttpRequest(): never {
  throw new Error(blockedMessage);
}

function replace(
  target: object,
  property: PropertyKey,
  value: unknown,
  restorers: Array<() => void>,
): void {
  const descriptor = Object.getOwnPropertyDescriptor(target, property);
  if (descriptor === undefined) {
    throw new Error(`native E2E network guard cannot bind ${String(property)}`);
  }
  Object.defineProperty(target, property, {
    ...descriptor,
    value,
  });
  restorers.push(() => Object.defineProperty(target, property, descriptor));
}

function restoreAll(restorers: Array<() => void>): void {
  for (const restore of restorers.reverse()) restore();
}
