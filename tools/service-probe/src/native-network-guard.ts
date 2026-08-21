import http from "node:http";
import http2 from "node:http2";
import https from "node:https";
import { syncBuiltinESMExports } from "node:module";
import {
  installWfpOfflineBoundary,
  type WfpOfflineBoundaryDependencies,
} from "./wfp-offline-boundary.js";

const blockedMessage = "native E2E network guard blocked HTTP(S) request";
let installed = false;

export interface WindowsNativeOfflineBoundary {
  readonly ruleName: string;
  runGuarded<Result>(
    execute: (signal: AbortSignal) => Promise<Result>,
    onBoundaryLoss?: () => Promise<void>,
  ): Promise<Result>;
  close(): Promise<void>;
}

export interface WindowsNativeOfflineBoundaryOptions {
  readonly ownerPid?: number;
  readonly ruleName?: string;
  readonly nativeExecutablePath?: string;
  /** Retained for caller compatibility; WFP uses no marker state directory. */
  readonly stateRoot?: string;
  readonly required?: boolean;
  /** Test-only native process seam; production never uses PowerShell or markers. */
  readonly __dependencies?: WfpOfflineBoundaryDependencies;
}

export function installNativeHttpNetworkGuard(): () => void {
  if (installed) throw new Error("native E2E network guard is already installed");
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

/** WFP is mandatory; HTTP(S) is installed only as a complementary guard. */
export async function installWindowsNativeOfflineBoundary(
  options: WindowsNativeOfflineBoundaryOptions = {},
): Promise<WindowsNativeOfflineBoundary> {
  const result = await installWfpOfflineBoundary({
    ownerPid: options.ownerPid,
    ruleName: options.ruleName,
    required: options.required ?? true,
    nativeExecutablePath: options.nativeExecutablePath,
    __dependencies: options.__dependencies,
  });
  if (result.outcome === "skipped") throw new Error("coverage toolset is unavailable");
  const restoreHttp = installNativeHttpNetworkGuard();
  let closed = false;
  return {
    ruleName: result.boundary.ruleName,
    async runGuarded<Result>(
      execute: (signal: AbortSignal) => Promise<Result>,
      onBoundaryLoss?: () => Promise<void>,
    ): Promise<Result> {
      return await result.boundary.runGuarded(execute, onBoundaryLoss);
    },
    async close(): Promise<void> {
      if (closed) return;
      await result.boundary.close();
      restoreHttp();
      closed = true;
    },
  };
}

function blockedHttpRequest(): never {
  throw new Error(blockedMessage);
}

function replace(target: object, property: PropertyKey, value: unknown, restorers: Array<() => void>): void {
  const descriptor = Object.getOwnPropertyDescriptor(target, property);
  if (descriptor === undefined) throw new Error(`native E2E network guard cannot bind ${String(property)}`);
  Object.defineProperty(target, property, { ...descriptor, value });
  restorers.push(() => Object.defineProperty(target, property, descriptor));
}

function restoreAll(restorers: Array<() => void>): void {
  for (const restore of restorers.reverse()) restore();
}
