import { execFile as execFileCallback } from "node:child_process";
import { dirname, join } from "node:path";
import { promisify } from "node:util";

const execFile = promisify(execFileCallback);
const preflightTimeoutMilliseconds = 90_000;
const ruleNamePrefix = "UnitTestIDE-NativeOffline-";

export type GuardianFrame =
  | { readonly kind: "Hello" }
  | { readonly kind: "Ready" }
  | { readonly kind: "Release" }
  | { readonly kind: "Bye" }
  | { readonly kind: "Error"; readonly code: "Startup" };

export interface GuardianProcess {
  readFrame(): Promise<GuardianFrame>;
  writeFrame(frame: Extract<GuardianFrame, { readonly kind: "Release" }>): Promise<void>;
  waitForExit(): Promise<void>;
  terminate(): void;
}

export interface WfpOfflineBoundaryDependencies {
  readonly platform: NodeJS.Platform;
  readonly resolveOwnerCreationTime: (ownerPid: number) => Promise<string>;
  readonly runPreflight: () => Promise<{ readonly stdout: string; readonly stderr: string }>;
  readonly startGuardian: (options: {
    readonly ownerPid: number;
    readonly ownerCreationTime: string;
    readonly ruleName: string;
  }) => Promise<GuardianProcess>;
}

export interface WfpOfflineBoundaryOptions {
  readonly ownerPid?: number;
  readonly ruleName?: string;
  readonly required?: boolean;
  readonly __dependencies?: WfpOfflineBoundaryDependencies;
}

export interface InstalledWfpOfflineBoundary {
  readonly outcome: "installed";
  readonly boundary: { readonly ruleName: string; close(): Promise<void> };
}

export type WfpOfflineBoundaryResult = InstalledWfpOfflineBoundary | {
  readonly outcome: "skipped";
  readonly reason: "ToolchainUnavailable";
};

/** WFP is mandatory here; callers may add HTTP(S) only as a complement. */
export async function installWfpOfflineBoundary(
  options: WfpOfflineBoundaryOptions = {},
): Promise<WfpOfflineBoundaryResult> {
  const dependencies = options.__dependencies ?? defaultDependencies();
  if (dependencies.platform !== "win32") throw new Error("Windows WFP offline boundary is Windows-only");
  const ownerPid = options.ownerPid ?? process.pid;
  if (!Number.isSafeInteger(ownerPid) || ownerPid <= 0) throw new Error("Windows offline boundary owner PID is invalid");
  const ruleName = options.ruleName ?? `${ruleNamePrefix}${cryptoRandomRuleSuffix()}`;
  requireRuleName(ruleName);

  // First native action: no guardian/WFP/service start before exact preflight.
  let preflightOutput: { readonly stdout: string; readonly stderr: string };
  try {
    preflightOutput = await dependencies.runPreflight();
  } catch {
    throw new Error("coverage toolset preflight could not run");
  }
  const preflight = parsePreflight(preflightOutput);
  if (preflight.status === "unavailable") {
    if (options.required === true) throw new Error("coverage toolset is unavailable");
    return { outcome: "skipped", reason: "ToolchainUnavailable" };
  }

  let ownerCreationTime: string;
  try {
    ownerCreationTime = await dependencies.resolveOwnerCreationTime(ownerPid);
  } catch {
    throw new Error("Windows offline boundary owner identity is unavailable");
  }
  if (!/^[1-9][0-9]*$/u.test(ownerCreationTime)) throw new Error("Windows offline boundary owner identity is invalid");
  let guardian: GuardianProcess | undefined;
  try {
    guardian = await dependencies.startGuardian({ ownerPid, ownerCreationTime, ruleName });
    await expectFrame(guardian, "Hello");
    await expectFrame(guardian, "Ready");
  } catch (error) {
    guardian?.terminate();
    void error;
    throw new Error("guardian protocol did not establish an audited WFP boundary");
  }

  let closed = false;
  return {
    outcome: "installed",
    boundary: {
      ruleName,
      async close(): Promise<void> {
        if (closed) return;
        try {
          await guardian.writeFrame({ kind: "Release" });
          await expectFrame(guardian, "Bye");
          await guardian.waitForExit();
          closed = true;
        } catch (error) {
          guardian.terminate();
          void error;
          throw new Error("guardian protocol did not prove WFP boundary removal");
        }
      },
    },
  };
}

function expectFrame(guardian: GuardianProcess, expected: GuardianFrame["kind"]): Promise<void> {
  return guardian.readFrame().then((frame) => {
    if (!isGuardianFrame(frame) || frame.kind !== expected) throw new Error("unexpected guardian protocol frame");
  });
}

function isGuardianFrame(frame: GuardianFrame): boolean {
  if (frame.kind === "Hello" || frame.kind === "Ready" || frame.kind === "Release" || frame.kind === "Bye") {
    return Object.keys(frame).length === 1;
  }
  return frame.kind === "Error" && frame.code === "Startup" && Object.keys(frame).length === 2;
}

function parsePreflight(result: { readonly stdout: string; readonly stderr: string }): { readonly status: "verified" | "unavailable" } {
  if (result.stderr !== "" || !/^\{[^\r\n]+\}\n$/u.test(result.stdout)) throw new Error("coverage toolset preflight output is invalid");
  let value: unknown;
  try { value = JSON.parse(result.stdout); } catch { throw new Error("coverage toolset preflight output is invalid"); }
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new Error("coverage toolset preflight output is invalid");
  const report = value as Record<string, unknown>;
  const baseKeys = ["architecture", "platform", "schemaVersion", "status"];
  if (report.schemaVersion !== 1 || report.platform !== "windows" || report.architecture !== "x64") throw new Error("coverage toolset preflight output is invalid");
  if (report.status === "unavailable" && sameKeys(report, baseKeys)) return { status: "unavailable" };
  if (report.status === "verified" && sameKeys(report, [...baseKeys, "version"]) && typeof report.version === "string" && /^\d+\.\d+(?:\.\d+)?$/u.test(report.version)) return { status: "verified" };
  throw new Error("coverage toolset preflight output is invalid");
}

function sameKeys(value: Record<string, unknown>, expected: readonly string[]): boolean {
  const keys = Object.keys(value).sort();
  const sorted = [...expected].sort();
  return keys.length === sorted.length && keys.every((key, index) => key === sorted[index]);
}

function requireRuleName(ruleName: string): void {
  if (!/^UnitTestIDE-NativeOffline-[0-9a-f]{16,64}$/u.test(ruleName)) throw new Error("Windows offline boundary rule name is invalid");
}

function cryptoRandomRuleSuffix(): string {
  return Buffer.from(globalThis.crypto.getRandomValues(new Uint8Array(16))).toString("hex");
}

function defaultDependencies(): WfpOfflineBoundaryDependencies {
  return {
    platform: process.platform,
    async runPreflight() {
      const executable = join(dirname(process.execPath), "coverage-toolset-preflight.exe");
      return await execFile(executable, [], { encoding: "utf8", timeout: preflightTimeoutMilliseconds, windowsHide: true, maxBuffer: 64 * 1024, env: sanitizedEnvironment() });
    },
    async resolveOwnerCreationTime() {
      // Node does not expose GetProcessTimes. Guessing would weaken PID reuse protection.
      throw new Error("Windows WFP guardian requires an owner identity provider");
    },
    async startGuardian() {
      // No PowerShell or marker-file fallback is permitted for this transport.
      throw new Error("Windows WFP guardian transport is unavailable");
    },
  };
}

function sanitizedEnvironment(): NodeJS.ProcessEnv {
  const names = ["SystemRoot", "WINDIR", "ComSpec", "ProgramData", "ProgramFiles", "ProgramFiles(x86)", "CommonProgramFiles", "CommonProgramFiles(x86)", "TEMP", "TMP", "Path"];
  return Object.fromEntries(names.flatMap((name) => process.env[name] === undefined ? [] : [[name, process.env[name]]]));
}
