import http from "node:http";
import http2 from "node:http2";
import https from "node:https";
import { syncBuiltinESMExports } from "node:module";

const blockedMessage = "native E2E network guard blocked HTTP(S) request";
let installed = false;

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
