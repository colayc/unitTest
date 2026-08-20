import {
  execFile as execFileCallback,
  spawn,
  type ChildProcess
} from "node:child_process";
import { randomBytes } from "node:crypto";
import { lstat, mkdir, open, readFile, rm } from "node:fs/promises";
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
const defaultGuardianStateRoot = join(tmpdir(), "unit-test-ide-native-offline-guardians");
const guardianReadyTimeoutMilliseconds = 30_000;
const guardianReleaseTimeoutMilliseconds = 35_000;
const guardianExitAfterKillTimeoutMilliseconds = 10_000;
const markerPollMilliseconds = 25;
const execFile = promisify(execFileCallback);
let installed = false;

export interface WindowsFirewallGuardian {
  /** Resolves only after the rule and every effective filter/profile audit pass. */
  waitUntilReady(): Promise<void>;
  /** Resolves only after explicit release, stable rule removal, and guardian exit. */
  release(): Promise<void>;
  /** Disarms the creator, confirms exit, then independently converges cleanup. */
  recover(): Promise<void>;
}

export interface WindowsFirewallGuardianOperations {
  /**
   * Starts the sole rule creator. Implementations must not throw after a child
   * can still create; post-spawn failures are reported by the returned handle.
   */
  start(ruleName: string, ownerPid: number, stateRoot: string): Promise<WindowsFirewallGuardian>;
}

export interface WindowsNativeOfflineBoundary {
  readonly ruleName: string;
  close(): Promise<void>;
}

export interface WindowsNativeOfflineBoundaryOptions {
  readonly ownerPid?: number;
  readonly ruleName?: string;
  readonly stateRoot?: string;
  readonly operations?: WindowsFirewallGuardianOperations;
}

interface ChildOutcome {
  readonly code: number | null;
  readonly signal: NodeJS.Signals | null;
  readonly error?: Error;
}

interface DefaultGuardianContext {
  readonly child: ChildProcess;
  readonly outcome: Promise<ChildOutcome>;
  readonly powershell: string;
  readonly ruleName: string;
  readonly stateRoot: string;
  readonly stateDirectory: string;
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
  const stateRoot = resolve(options.stateRoot ?? defaultGuardianStateRoot);
  const operations = options.operations ?? defaultWindowsFirewallGuardianOperations();
  const restoreHTTP = installNativeHttpNetworkGuard();
  let guardian: WindowsFirewallGuardian;
  try {
    guardian = await operations.start(ruleName, ownerPid, stateRoot);
  } catch (error) {
    // start() is contractually allowed to throw only before a creator exists.
    restoreHTTP();
    throw new AggregateError([error], "cannot establish audited Windows offline boundary");
  }

  try {
    await guardian.waitUntilReady();
  } catch (error) {
    const errors: unknown[] = [error];
    try {
      await guardian.recover();
      restoreHTTP();
    } catch (recoveryError) {
      errors.push(recoveryError);
      // Keep the in-process guard installed when OS removal is unproven.
    }
    throw new AggregateError(errors, "cannot establish audited Windows offline boundary");
  }

  let closed = false;
  return {
    ruleName,
    async close(): Promise<void> {
      if (closed) return;
      try {
        await guardian.release();
      } catch (error) {
        const errors: unknown[] = [error];
        try {
          await guardian.recover();
          restoreHTTP();
          closed = true;
        } catch (recoveryError) {
          errors.push(recoveryError);
          // A later close() may retry; until then Node stays fail-closed too.
        }
        throw new AggregateError(errors, "cannot revoke audited Windows offline boundary");
      }
      restoreHTTP();
      closed = true;
    }
  };
}

function defaultWindowsFirewallGuardianOperations(): WindowsFirewallGuardianOperations {
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
  return {
    async start(ruleName, ownerPid, stateRoot) {
      const stateDirectory = await createGuardianStateDirectory(stateRoot, ruleName);
      const child = spawn(
        powershell,
        [
          ...powershellArguments("Guard", ruleName),
          "-OwnerPid",
          String(ownerPid),
          "-StateRoot",
          stateRoot,
          "-StateDirectory",
          stateDirectory,
          "-DeadlineSeconds",
          "30"
        ],
        { detached: true, stdio: "ignore", windowsHide: true }
      );
      const outcome = observeChildOutcome(child);
      child.unref();
      return defaultGuardian({ child, outcome, powershell, ruleName, stateRoot, stateDirectory });
    }
  };
}

function defaultGuardian(context: DefaultGuardianContext): WindowsFirewallGuardian {
  const expectedMarker = `${context.ruleName}\n`;
  return {
    async waitUntilReady() {
      await waitForMarker(
        join(context.stateDirectory, "ready"),
        expectedMarker,
        context.outcome,
        guardianReadyTimeoutMilliseconds,
        "readiness"
      );
    },
    async release() {
      await writeExclusiveOrValidateMarker(
        join(context.stateDirectory, "release"),
        expectedMarker
      );
      await waitForMarker(
        join(context.stateDirectory, "removed"),
        expectedMarker,
        context.outcome,
        guardianReleaseTimeoutMilliseconds,
        "removal"
      );
      const outcome = await waitForOutcome(
        context.outcome,
        guardianReleaseTimeoutMilliseconds,
        "guardian exit after removal"
      );
      requireSuccessfulGuardianExit(outcome);
      await rm(context.stateDirectory, { recursive: true, force: true });
    },
    async recover() {
      const errors: unknown[] = [];
      try {
        await writeExclusiveOrValidateMarker(
          join(context.stateDirectory, "release"),
          expectedMarker
        );
      } catch (error) {
        errors.push(error);
      }

      let exited = false;
      try {
        await waitForOutcome(
          context.outcome,
          guardianReleaseTimeoutMilliseconds,
          "guardian exit during recovery"
        );
        exited = true;
      } catch (error) {
        errors.push(error);
        context.child.kill();
        try {
          await waitForOutcome(
            context.outcome,
            guardianExitAfterKillTimeoutMilliseconds,
            "guardian exit after termination"
          );
          exited = true;
        } catch (killError) {
          errors.push(killError);
        }
      }
      if (!exited) {
        throw new AggregateError(errors, "cannot disarm Windows offline guardian creator");
      }

      // The sole creator has exited, so an exact cleanup process cannot race a
      // late installation. Its script retries every removal/query failure.
      try {
        await execFile(
          context.powershell,
          [
            ...powershellArguments("CleanupExact", context.ruleName),
            "-StateRoot",
            context.stateRoot,
            "-StateDirectory",
            context.stateDirectory,
            "-DeadlineSeconds",
            "30"
          ],
          {
            encoding: "utf8",
            timeout: 40_000,
            windowsHide: true,
            maxBuffer: 1024 * 1024
          }
        );
        await requireMarker(join(context.stateDirectory, "removed"), expectedMarker);
        await rm(context.stateDirectory, { recursive: true, force: true });
      } catch (error) {
        errors.push(error);
        throw new AggregateError(errors, "cannot confirm Windows offline guardian recovery");
      }
    }
  };
}

async function createGuardianStateDirectory(stateRoot: string, ruleName: string): Promise<string> {
  await mkdir(stateRoot, { recursive: true });
  const rootInfo = await lstat(stateRoot);
  if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink()) {
    throw new Error("Windows offline guardian state root is unsafe");
  }
  const stateDirectory = join(stateRoot, ruleName);
  await mkdir(stateDirectory);
  const stateInfo = await lstat(stateDirectory);
  if (!stateInfo.isDirectory() || stateInfo.isSymbolicLink()) {
    throw new Error("Windows offline guardian state directory is unsafe");
  }
  return stateDirectory;
}

function observeChildOutcome(child: ChildProcess): Promise<ChildOutcome> {
  return new Promise((resolveOutcome) => {
    let settled = false;
    const settle = (outcome: ChildOutcome) => {
      if (settled) return;
      settled = true;
      resolveOutcome(outcome);
    };
    child.once("error", (error) => settle({ code: null, signal: null, error }));
    child.once("exit", (code, signal) => settle({ code, signal }));
  });
}

async function waitForMarker(
  path: string,
  expected: string,
  outcomePromise: Promise<ChildOutcome>,
  timeoutMilliseconds: number,
  label: string
): Promise<void> {
  const deadline = Date.now() + timeoutMilliseconds;
  while (Date.now() < deadline) {
    if (await markerMatches(path, expected)) return;
    const remaining = Math.max(1, Math.min(markerPollMilliseconds, deadline - Date.now()));
    const outcome = await Promise.race([
      outcomePromise,
      delay(remaining).then(() => undefined)
    ]);
    if (outcome !== undefined) {
      if (await markerMatches(path, expected)) return;
      const cause = childOutcomeError(outcome);
      throw new Error(`Windows offline guardian exited before ${label}`, { cause });
    }
  }
  throw new Error(`Windows offline guardian timed out before ${label}`);
}

async function waitForOutcome(
  outcomePromise: Promise<ChildOutcome>,
  timeoutMilliseconds: number,
  label: string
): Promise<ChildOutcome> {
  const outcome = await Promise.race([
    outcomePromise,
    delay(timeoutMilliseconds).then(() => undefined)
  ]);
  if (outcome === undefined) {
    throw new Error(`Windows offline guardian timed out before ${label}`);
  }
  return outcome;
}

function requireSuccessfulGuardianExit(outcome: ChildOutcome): void {
  if (outcome.error !== undefined || outcome.code !== 0 || outcome.signal !== null) {
    throw new Error("Windows offline guardian exited abnormally", {
      cause: childOutcomeError(outcome)
    });
  }
}

function childOutcomeError(outcome: ChildOutcome): Error {
  return outcome.error ?? new Error(
    `guardian exit code ${String(outcome.code)}, signal ${String(outcome.signal)}`
  );
}

async function markerMatches(path: string, expected: string): Promise<boolean> {
  try {
    await requireMarker(path, expected);
    return true;
  } catch (error) {
    const code = (error as NodeJS.ErrnoException).code;
    if (code === "ENOENT" || code === "EBUSY" || code === "EPERM" || code === "EACCES") {
      return false;
    }
    throw error;
  }
}

async function requireMarker(path: string, expected: string): Promise<void> {
  const info = await lstat(path);
  if (!info.isFile() || info.isSymbolicLink()) {
    throw new Error("Windows offline guardian marker is unsafe");
  }
  const marker = await readFile(path, "utf8");
  if (marker !== expected) {
    throw new Error("Windows offline guardian marker content is invalid");
  }
}

async function writeExclusiveOrValidateMarker(path: string, content: string): Promise<void> {
  try {
    const handle = await open(path, "wx");
    try {
      await handle.writeFile(content, "utf8");
      await handle.sync();
    } finally {
      await handle.close();
    }
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== "EEXIST") throw error;
    await requireMarker(path, content);
  }
}

function powershellArguments(
  action: "Guard" | "CleanupExact" | "CleanupAll" | "AuditRemoved",
  ruleName?: string
): string[] {
  const arguments_ = [
    "-NoLogo",
    "-NoProfile",
    "-NonInteractive",
    "-ExecutionPolicy",
    "Bypass",
    "-File",
    firewallScript,
    "-Action",
    action
  ];
  if (ruleName !== undefined) arguments_.push("-RuleName", ruleName);
  return arguments_;
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
