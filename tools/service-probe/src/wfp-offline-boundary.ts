import { execFile as execFileCallback, spawn, type ChildProcess } from "node:child_process";
import { randomBytes } from "node:crypto";
import net from "node:net";
import { dirname, isAbsolute, join } from "node:path";
import { promisify } from "node:util";

const execFile = promisify(execFileCallback);
const preflightTimeoutMilliseconds = 90_000;
const guardianConnectTimeoutMilliseconds = 10_000;
const defaultGuardianLifecycleTimeoutMilliseconds = 10_000;
const maxGuardianFramePayloadBytes = 32;
const ruleNamePrefix = "UnitTestIDE-NativeOffline-";

export type GuardianFrame =
  | { readonly kind: "Hello" }
  | { readonly kind: "Ready" }
  | { readonly kind: "Release" }
  | { readonly kind: "Bye" }
  | { readonly kind: "Error"; readonly code: "Startup" | "WFPAccessDenied" | "SessionCloseFailed" };

export interface GuardianProcess {
  readFrame(): Promise<GuardianFrame>;
  writeFrame(frame: Extract<GuardianFrame, { readonly kind: "Release" }>): Promise<void>;
  waitForExit(): Promise<void>;
  terminate(): void | Promise<void>;
}

export interface WfpOfflineBoundaryDependencies {
  readonly platform: NodeJS.Platform;
  /** Test seam; production uses the fixed bounded guardian lifecycle timeout. */
  readonly guardianTimeoutMilliseconds?: number;
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
  /** Resolve the default preflight and guardian beside this native executable. */
  readonly nativeExecutablePath?: string;
  readonly __dependencies?: WfpOfflineBoundaryDependencies;
}

export interface InstalledWfpOfflineBoundary {
  readonly outcome: "installed";
  readonly boundary: {
    readonly ruleName: string;
    runGuarded<Result>(
      execute: (signal: AbortSignal) => Promise<Result>,
      onBoundaryLoss?: () => Promise<void>,
    ): Promise<Result>;
    close(): Promise<void>;
  };
}

export type WfpOfflineBoundaryResult = InstalledWfpOfflineBoundary | {
  readonly outcome: "skipped";
  readonly reason: "ToolchainUnavailable" | "WFPAccessDenied";
};

/** WFP is mandatory here; callers may add HTTP(S) only as a complement. */
export async function installWfpOfflineBoundary(
  options: WfpOfflineBoundaryOptions = {},
): Promise<WfpOfflineBoundaryResult> {
  if (options.nativeExecutablePath !== undefined &&
      (!isAbsolute(options.nativeExecutablePath) || options.nativeExecutablePath.length === 0)) {
    throw new Error("native executable path is invalid");
  }
  const dependencies = options.__dependencies ?? defaultDependencies(options.nativeExecutablePath ?? process.execPath);
  if (dependencies.platform !== "win32") throw new Error("Windows WFP offline boundary is Windows-only");
  const ownerPid = options.ownerPid ?? process.pid;
  if (!Number.isSafeInteger(ownerPid) || ownerPid <= 0) throw new Error("Windows offline boundary owner PID is invalid");
  const ruleName = options.ruleName ?? `${ruleNamePrefix}${cryptoRandomRuleSuffix()}`;
  requireRuleName(ruleName);
  const guardianTimeoutMilliseconds = dependencies.guardianTimeoutMilliseconds ?? defaultGuardianLifecycleTimeoutMilliseconds;
  if (!Number.isSafeInteger(guardianTimeoutMilliseconds) || guardianTimeoutMilliseconds <= 0) {
    throw new Error("guardian lifecycle timeout is invalid");
  }

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
    await withTimeout(expectFrame(guardian, "Hello"), guardianTimeoutMilliseconds);
    await withTimeout(expectFrame(guardian, "Ready"), guardianTimeoutMilliseconds);
  } catch (error) {
    if (guardian !== undefined) await terminateGuardian(guardian, guardianTimeoutMilliseconds);
    if (error instanceof GuardianProtocolError && error.code === "WFPAccessDenied") {
      if (options.required === true) throw new Error("Windows Filtering Platform access is unavailable");
      return { outcome: "skipped", reason: "WFPAccessDenied" };
    }
    throw new Error("guardian protocol did not establish an audited WFP boundary");
  }

  type BoundaryPhase = "active" | "releasing" | "closed";
  let phase: BoundaryPhase = "active";
  let byeSeen = false;
  const nextFrame = guardian.readFrame();
  const guardianExit = guardian.waitForExit();
  let rejectLiveness!: (error: Error) => void;
  const livenessFailure = new Promise<never>((_resolve, reject) => { rejectLiveness = reject; });
  void livenessFailure.catch(() => undefined);
  const loseBoundary = () => rejectLiveness(new BoundaryLivenessError());
  void nextFrame.then(
    () => { if (phase === "active") loseBoundary(); },
    loseBoundary,
  );
  void guardianExit.then(
    () => { if (phase !== "closed" && !(phase === "releasing" && byeSeen)) loseBoundary(); },
    loseBoundary,
  );

  const assertLive = async (): Promise<void> => {
    // Let an already-delivered socket/child terminal event settle the monitor
    // before any native callback is allowed to start.
    await new Promise<void>((resolve) => setImmediate(resolve));
    const live = Symbol("guardian-live");
    const status = await Promise.race([livenessFailure, Promise.resolve(live)]);
    if (status !== live) throw new BoundaryLivenessError();
  };
  return {
    outcome: "installed",
    boundary: {
      ruleName,
      async runGuarded<Result>(
        execute: (signal: AbortSignal) => Promise<Result>,
        onBoundaryLoss: () => Promise<void> = async () => undefined,
      ): Promise<Result> {
        if (phase !== "active") throw new Error("WFP boundary is not active");
        const abort = new AbortController();
        try {
          await assertLive();
          const result = await Promise.race([execute(abort.signal), livenessFailure]);
          await assertLive();
          return result;
        } catch (error) {
          if (!(error instanceof BoundaryLivenessError)) throw error;
          abort.abort();
          await withTimeout(onBoundaryLoss(), guardianTimeoutMilliseconds).catch(() => undefined);
          await terminateGuardian(guardian, guardianTimeoutMilliseconds);
          throw new Error("guardian liveness was lost; WFP boundary failed closed");
        }
      },
      async close(): Promise<void> {
        if (phase === "closed") return;
        phase = "releasing";
        try {
          await withTimeout((async () => {
            await guardian.writeFrame({ kind: "Release" });
            const frame = await Promise.race([nextFrame, livenessFailure]);
            if (!isGuardianFrame(frame) || frame.kind !== "Bye") {
              throw new Error("unexpected guardian protocol frame");
            }
            byeSeen = true;
            await Promise.race([guardianExit, livenessFailure]);
          })(), guardianTimeoutMilliseconds);
          phase = "closed";
        } catch (error) {
          await terminateGuardian(guardian, guardianTimeoutMilliseconds);
          void error;
          throw new Error("guardian protocol did not prove WFP boundary removal");
        }
      },
    },
  };
}

class GuardianProtocolError extends Error {
  constructor(readonly code: Extract<GuardianFrame, { readonly kind: "Error" }>["code"]) {
    super("guardian reported a fixed protocol error");
  }
}

class BoundaryLivenessError extends Error {
  constructor() { super("guardian liveness was lost"); }
}

async function terminateGuardian(guardian: GuardianProcess, timeoutMilliseconds: number): Promise<void> {
  await withTimeout(Promise.resolve(guardian.terminate()), timeoutMilliseconds).catch(() => undefined);
}

async function withTimeout<Result>(promise: Promise<Result>, timeoutMilliseconds: number): Promise<Result> {
  let timeout: NodeJS.Timeout | undefined;
  try {
    return await Promise.race([
      promise,
      new Promise<never>((_resolve, reject) => {
        timeout = setTimeout(() => reject(new Error("guardian lifecycle timed out")), timeoutMilliseconds);
      }),
    ]);
  } finally {
    if (timeout !== undefined) clearTimeout(timeout);
  }
}

function expectFrame(guardian: GuardianProcess, expected: GuardianFrame["kind"]): Promise<void> {
  return guardian.readFrame().then((frame) => {
    if (isGuardianFrame(frame) && frame.kind === "Error") throw new GuardianProtocolError(frame.code);
    if (!isGuardianFrame(frame) || frame.kind !== expected) throw new Error("unexpected guardian protocol frame");
  });
}

function isGuardianFrame(frame: GuardianFrame): boolean {
  if (frame.kind === "Hello" || frame.kind === "Ready" || frame.kind === "Release" || frame.kind === "Bye") {
    return Object.keys(frame).length === 1;
  }
  return frame.kind === "Error" &&
    (frame.code === "Startup" || frame.code === "WFPAccessDenied" || frame.code === "SessionCloseFailed") &&
    Object.keys(frame).length === 2;
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
  return randomBytes(16).toString("hex");
}

function defaultDependencies(nativeExecutablePath: string): WfpOfflineBoundaryDependencies {
  const guardianExecutable = siblingExecutable(nativeExecutablePath, "native-offline-guardian.exe");
  return {
    platform: process.platform,
    async runPreflight() {
      const executable = siblingExecutable(nativeExecutablePath, "coverage-toolset-preflight.exe");
      return await execFile(executable, [], { encoding: "utf8", timeout: preflightTimeoutMilliseconds, windowsHide: true, maxBuffer: 64 * 1024, env: sanitizedEnvironment() });
    },
    async resolveOwnerCreationTime(ownerPid) {
      let result: { readonly stdout: string; readonly stderr: string };
      try {
        result = await execFile(guardianExecutable, [`--print-owner-creation-time=${ownerPid}`], {
          encoding: "utf8", timeout: preflightTimeoutMilliseconds, windowsHide: true,
          maxBuffer: 4 * 1024, env: sanitizedEnvironment(),
        });
      } catch {
        throw new Error("owner creation time unavailable");
      }
      if (result.stderr !== "" || !/^[1-9][0-9]*\n$/u.test(result.stdout)) {
        throw new Error("owner creation time unavailable");
      }
      return result.stdout.trim();
    },
    async startGuardian(options) {
      return await startNativeGuardian({ executable: guardianExecutable, ...options });
    },
  };
}

function siblingExecutable(nativeExecutablePath: string, name: string): string {
  return join(dirname(nativeExecutablePath), name);
}

export interface NativeGuardianStartOptions {
  readonly executable: string;
  readonly ownerPid: number;
  readonly ownerCreationTime: string;
  readonly ruleName: string;
}

/** Exported solely for the default-wiring regression test. */
export async function startNativeGuardianForTesting(options: NativeGuardianStartOptions): Promise<GuardianProcess> {
  return await startNativeGuardian(options);
}

async function startNativeGuardian(options: NativeGuardianStartOptions): Promise<GuardianProcess> {
  const pipeName = `\\\\.\\pipe\\offlineboundary-${randomBytes(16).toString("hex")}`;
  const server = net.createServer();
  const connection = awaitConnection(server);
  // Spawn may fail before the connection is awaited; retain a rejection
  // handler so that resource cleanup cannot become an unhandled rejection.
  void connection.catch(() => undefined);
  try {
    await listenPipe(server, pipeName);
  } catch {
    server.close();
    throw new Error("guardian process could not start");
  }
  let child: ChildProcess;
  try {
    child = await spawnGuardian(options.executable, [
      `--owner-pid=${options.ownerPid}`,
      `--owner-creation-time=${options.ownerCreationTime}`,
      `--ipc-address=${pipeName}`,
    ]);
  } catch {
    server.close();
    throw new Error("guardian process could not start");
  }
  const exit = observeGuardianExit(child, server);
  void exit.catch(() => undefined);
  const socket = await connection.catch(async () => {
    child.kill();
    await withTimeout(exit.catch(() => undefined), defaultGuardianLifecycleTimeoutMilliseconds).catch(() => undefined);
    throw new Error("guardian process could not start");
  });
  const frames = new GuardianFrameReader(socket);
  return {
    readFrame: async () => await frames.read(),
    writeFrame: async (frame) => await writeGuardianFrame(socket, frame),
    waitForExit: async () => await exit,
    terminate: async () => {
      frames.close();
      socket.destroy();
      server.close();
      child.kill();
      await withTimeout(
        exit.catch(() => undefined),
        defaultGuardianLifecycleTimeoutMilliseconds,
      ).catch(() => undefined);
    },
  };
}

async function listenPipe(server: net.Server, pipeName: string): Promise<void> {
  await new Promise<void>((resolveListen, rejectListen) => {
    server.once("error", rejectListen);
    server.listen(pipeName, () => {
      server.off("error", rejectListen);
      resolveListen();
    });
  });
}

function awaitConnection(server: net.Server): Promise<net.Socket> {
  return new Promise<net.Socket>((resolveConnection, rejectConnection) => {
    const timeout = setTimeout(() => finish(new Error("guardian connection timed out")), guardianConnectTimeoutMilliseconds);
    const finish = (error?: Error, socket?: net.Socket) => {
      clearTimeout(timeout);
      server.off("error", onError);
      server.off("connection", onConnection);
      server.off("close", onClose);
      if (error !== undefined) rejectConnection(error);
      else if (socket !== undefined) resolveConnection(socket);
    };
    const onError = () => finish(new Error("guardian pipe failed"));
    const onClose = () => finish(new Error("guardian pipe closed"));
    const onConnection = (socket: net.Socket) => {
      server.close();
      finish(undefined, socket);
    };
    server.once("error", onError);
    server.once("connection", onConnection);
    server.once("close", onClose);
  });
}

async function spawnGuardian(executable: string, args: readonly string[]): Promise<ChildProcess> {
  const child = spawn(executable, args, { stdio: "ignore", windowsHide: true, env: sanitizedEnvironment() });
  await new Promise<void>((resolveSpawn, rejectSpawn) => {
    child.once("spawn", resolveSpawn);
    child.once("error", rejectSpawn);
  });
  return child;
}

function observeGuardianExit(child: ChildProcess, server: net.Server): Promise<void> {
  return new Promise<void>((resolveExit, rejectExit) => {
    child.once("error", () => { server.close(); rejectExit(new Error("guardian process failed")); });
    child.once("exit", (code, signal) => {
      server.close();
      if (code === 0 && signal === null) resolveExit();
      else rejectExit(new Error("guardian process failed"));
    });
  });
}

export class GuardianFrameReader {
  #buffer = Buffer.alloc(0);
  #frames: GuardianFrame[] = [];
  #waiting: {
    readonly resolve: (frame: GuardianFrame) => void;
    readonly reject: (error: Error) => void;
  } | undefined;
  #failure: Error | undefined;

  constructor(private readonly socket: net.Socket) {
    socket.on("data", (chunk: Buffer) => this.push(chunk));
    socket.once("error", () => this.fail());
    socket.once("end", () => this.fail());
    socket.once("close", () => this.fail());
  }

  async read(): Promise<GuardianFrame> {
    const frame = this.#frames.shift();
    if (frame !== undefined) return frame;
    if (this.#failure !== undefined) throw this.#failure;
    if (this.#waiting !== undefined) throw new Error("guardian frame read is already pending");
    return await new Promise<GuardianFrame>((resolve, reject) => { this.#waiting = { resolve, reject }; });
  }

  close(): void { this.fail(); }

  private push(chunk: Buffer): void {
    if (this.#failure !== undefined) return;
    this.#buffer = Buffer.concat([this.#buffer, chunk]);
    try {
      while (this.#buffer.length >= 4) {
        const length = this.#buffer.readUInt32LE(0);
        if (length === 0 || length > maxGuardianFramePayloadBytes) throw new Error("guardian frame is invalid");
        if (this.#buffer.length < 4 + length) return;
        const payload = this.#buffer.subarray(4, 4 + length);
        this.#buffer = this.#buffer.subarray(4 + length);
        this.deliver(decodeGuardianPayload(payload));
      }
    } catch { this.fail(); }
  }

  private deliver(frame: GuardianFrame): void {
    const waiter = this.#waiting;
    this.#waiting = undefined;
    if (waiter !== undefined) waiter.resolve(frame);
    else this.#frames.push(frame);
  }

  private fail(): void {
    if (this.#failure !== undefined) return;
    this.#failure = new Error("guardian frame is invalid");
    const waiter = this.#waiting;
    this.#waiting = undefined;
    waiter?.reject(this.#failure);
  }
}

function decodeGuardianPayload(payload: Buffer): GuardianFrame {
  const kind = payload[0];
  if (payload.length === 1 && kind === 1) return { kind: "Hello" };
  if (payload.length === 1 && kind === 2) return { kind: "Ready" };
  if (payload.length === 1 && kind === 3) return { kind: "Release" };
  if (payload.length === 1 && kind === 5) return { kind: "Bye" };
  if (payload.length === 2 && kind === 4 && payload[1] === 1) return { kind: "Error", code: "Startup" };
  if (payload.length === 2 && kind === 4 && payload[1] === 2) return { kind: "Error", code: "WFPAccessDenied" };
  if (payload.length === 2 && kind === 4 && payload[1] === 3) return { kind: "Error", code: "SessionCloseFailed" };
  throw new Error("guardian frame is invalid");
}

async function writeGuardianFrame(socket: net.Socket, frame: Extract<GuardianFrame, { readonly kind: "Release" }>): Promise<void> {
  const payload = Buffer.from([3]);
  const header = Buffer.alloc(4);
  header.writeUInt32LE(payload.length);
  await new Promise<void>((resolveWrite, rejectWrite) => socket.write(Buffer.concat([header, payload]), (error) => error === undefined ? resolveWrite() : rejectWrite(error)));
}

function sanitizedEnvironment(): NodeJS.ProcessEnv {
  const names = ["SystemRoot", "WINDIR", "ComSpec", "ProgramData", "ProgramFiles", "ProgramFiles(x86)", "CommonProgramFiles", "CommonProgramFiles(x86)", "TEMP", "TMP", "Path"];
  return Object.fromEntries(names.flatMap((name) => process.env[name] === undefined ? [] : [[name, process.env[name]]]));
}
