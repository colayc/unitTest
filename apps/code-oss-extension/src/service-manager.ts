import {
  execFile as execFileCallback,
  spawn,
  type ChildProcessWithoutNullStreams
} from "node:child_process";
import { rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { promisify } from "node:util";
import { ProtocolClient } from "@unit-test-ide/test-client";
import type { ServiceStatus, TrustState } from "./contracts.js";
import {
  createEndpointResource,
  createSessionDirectory,
  createToken,
  redactServiceError,
  type EndpointResource
} from "./service-resources.js";
import { canStartService } from "./trust-gate.js";

const execFile = promisify(execFileCallback);
const CLIENT_NAME = "code-oss-extension";
const CLIENT_VERSION = "0.1.0";

export interface ServiceOperations {
  prepareTokenFile(binary: string, tokenFile: string, token: string): Promise<void>;
  spawnService(binary: string, args: string[]): ChildProcessWithoutNullStreams;
  connect(endpoint: string): Promise<ProtocolClient>;
}

export interface ServiceManagerOptions {
  serviceExecutable: string;
  workspaceRoot: string;
  dataDirectory: string;
  timeoutMs: number;
  trusted: () => boolean;
  operations?: Partial<ServiceOperations>;
}

export interface ServiceSession {
  readonly endpoint: string;
  readonly tokenFile: string;
  readonly sessionDirectory: string;
  readonly client: ProtocolClient;
}

type Exit = [code: number | null, signal: NodeJS.Signals | null];

interface CapturedOutput {
  stdout: string;
  stderr: string;
}

interface ManagedSession extends ServiceSession {
  readonly child: ChildProcessWithoutNullStreams;
  readonly exit: Promise<Exit>;
  readonly endpointDirectory?: string;
  readonly sensitive: readonly string[];
  readonly output: CapturedOutput;
  readonly stdoutListener: (chunk: Buffer | string) => void;
  readonly stderrListener: (chunk: Buffer | string) => void;
  expectedStop: boolean;
  exited: boolean;
  childExitListener?: (code: number | null, signal: NodeJS.Signals | null) => void;
  childErrorListener?: (error: Error) => void;
  clientCloseListener?: (error?: Error) => void;
}

interface ClientCloseEmitter {
  once(event: "close", listener: (error?: Error) => void): unknown;
  off(event: "close", listener: (error?: Error) => void): unknown;
}

async function prepareTokenFile(binary: string, tokenFile: string, token: string): Promise<void> {
  await execFile(binary, ["--prepare-token-file", tokenFile], { windowsHide: true });
  await writeFile(tokenFile, token, { flag: "r+" });
}

const defaultOperations: ServiceOperations = {
  prepareTokenFile,
  spawnService: (binary, args) => spawn(binary, args, { windowsHide: true }),
  connect: (endpoint) => ProtocolClient.connect(endpoint)
};

function timeoutError(operation: string, timeoutMs: number): Error {
  const error = new Error(`${operation} timed out after ${timeoutMs}ms`);
  error.stack = `${error.name}: ${error.message}`;
  return error;
}

function withTimeout<T>(operation: string, promise: Promise<T>, timeoutMs: number): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(() => reject(timeoutError(operation, timeoutMs)), timeoutMs);
    promise.then(
      (value) => {
        clearTimeout(timer);
        resolve(value);
      },
      (error: unknown) => {
        clearTimeout(timer);
        reject(error);
      }
    );
  });
}

function waitForReady(child: ChildProcessWithoutNullStreams, endpoint: string): Promise<void> {
  return new Promise<void>((resolve, reject) => {
    let pending = "";
    let settled = false;
    const cleanup = () => {
      child.stdout.off("data", onData);
      child.off("error", onError);
      child.off("exit", onExit);
    };
    const finish = (error?: Error) => {
      if (settled) return;
      settled = true;
      cleanup();
      if (error) reject(error);
      else resolve();
    };
    const onData = (chunk: Buffer | string) => {
      pending += String(chunk);
      for (;;) {
        const newline = pending.indexOf("\n");
        if (newline < 0) break;
        const line = pending.slice(0, newline).replace(/\r$/, "");
        pending = pending.slice(newline + 1);
        if (line === `READY ${endpoint}`) {
          finish();
          return;
        }
      }
    };
    const onError = (error: Error) => finish(error);
    const onExit = (code: number | null, signal: NodeJS.Signals | null) => {
      finish(new Error(`service exited before READY with code ${String(code)} and signal ${String(signal)}`));
    };
    child.stdout.on("data", onData);
    child.once("error", onError);
    child.once("exit", onExit);
  });
}

function diagnostic(error: unknown, output: CapturedOutput, sensitive: readonly string[]): Error {
  const raw = error instanceof Error ? error.message : String(error);
  return redactServiceError(
    new Error(`${raw}; stdout=${output.stdout}; stderr=${output.stderr}`),
    sensitive
  );
}

function closeEmitter(client: ProtocolClient): ClientCloseEmitter | undefined {
  const candidate = client as unknown as Partial<ClientCloseEmitter>;
  return typeof candidate.once === "function" && typeof candidate.off === "function"
    ? candidate as ClientCloseEmitter
    : undefined;
}

async function removeOwnedDirectories(sessionDirectory?: string, endpointDirectory?: string): Promise<void> {
  const directories = new Set([sessionDirectory, endpointDirectory].filter((value): value is string => Boolean(value)));
  for (const directory of directories) {
    await rm(directory, { recursive: true, force: true, maxRetries: 5, retryDelay: 50 });
  }
}

export class ServiceManager {
  readonly #options: ServiceManagerOptions;
  readonly #operations: ServiceOperations;
  #status: ServiceStatus = { state: "stopped" };
  #session: ManagedSession | undefined;
  #startPromise: Promise<ServiceSession> | undefined;
  #stopPromise: Promise<void> | undefined;

  constructor(options: ServiceManagerOptions) {
    this.#options = options;
    this.#operations = { ...defaultOperations, ...options.operations };
  }

  get status(): ServiceStatus {
    return this.#status;
  }

  get session(): ServiceSession | undefined {
    return this.#session;
  }

  start(): Promise<ServiceSession> {
    const trustState: TrustState = this.#options.trusted() ? "trusted" : "blocked-untrusted";
    if (!canStartService(trustState)) return Promise.reject(new Error("workspace is not trusted"));
    if (this.#session && this.#status.state === "running") return Promise.resolve(this.#session);
    if (this.#startPromise) return this.#startPromise;

    const operation = this.#startOwned();
    const tracked = operation.finally(() => {
      if (this.#startPromise === tracked) this.#startPromise = undefined;
    });
    this.#startPromise = tracked;
    return tracked;
  }

  async #startOwned(): Promise<ServiceSession> {
    if (this.#stopPromise) await this.#stopPromise;
    this.#status = { state: "starting" };
    const token = createToken();
    const output: CapturedOutput = { stdout: "", stderr: "" };
    let sessionDirectory: string | undefined;
    let endpointResource: EndpointResource | undefined;
    let tokenFile = "";
    let child: ChildProcessWithoutNullStreams | undefined;
    let client: ProtocolClient | undefined;
    let exit: Promise<Exit> | undefined;
    let exited = false;
    let stdoutListener: ((chunk: Buffer | string) => void) | undefined;
    let stderrListener: ((chunk: Buffer | string) => void) | undefined;
    let startupExitListener: ((code: number | null, signal: NodeJS.Signals | null) => void) | undefined;
    let startupErrorListener: ((error: Error) => void) | undefined;
    let startupFailureReason: Error | undefined;

    try {
      sessionDirectory = await createSessionDirectory("unit-test-ide-session-");
      endpointResource = await createEndpointResource(process.platform);
      tokenFile = join(sessionDirectory, "token");
      const sensitive = this.#sensitive(token, tokenFile, sessionDirectory, endpointResource);
      await withTimeout(
        "token file preparation",
        this.#operations.prepareTokenFile(this.#options.serviceExecutable, tokenFile, token),
        this.#options.timeoutMs
      );
      child = this.#operations.spawnService(this.#options.serviceExecutable, [
        "--endpoint", endpointResource.path,
        "--token-file", tokenFile,
        "--data-dir", this.#options.dataDirectory,
        "--workspace-root", this.#options.workspaceRoot,
        "--trusted-workspace=true"
      ]);
      const launchedChild = child;
      exit = new Promise<Exit>((resolve) => launchedChild.once("exit", (code, signal) => {
        exited = true;
        resolve([code, signal]);
      }));
      const startupFailure = new Promise<never>((_resolve, reject) => {
        startupExitListener = (code, signal) => {
          startupFailureReason = new Error(
            `service exited during startup with code ${String(code)} and signal ${String(signal)}`
          );
          reject(startupFailureReason);
        };
        startupErrorListener = (error) => {
          startupFailureReason = error;
          reject(error);
        };
        launchedChild.once("exit", startupExitListener);
        launchedChild.once("error", startupErrorListener);
      });
      const whileChildAlive = <T>(operation: Promise<T>): Promise<T> => Promise.race([operation, startupFailure]);
      const requireChildAlive = () => {
        if (startupFailureReason) throw startupFailureReason;
      };
      stdoutListener = (chunk) => { output.stdout += String(chunk); };
      stderrListener = (chunk) => { output.stderr += String(chunk); };
      child.stdout.on("data", stdoutListener);
      child.stderr.on("data", stderrListener);
      await withTimeout(
        "service startup readiness",
        whileChildAlive(waitForReady(child, endpointResource.path)),
        this.#options.timeoutMs
      );
      requireChildAlive();
      client = await withTimeout(
        "service connection",
        whileChildAlive(this.#operations.connect(endpointResource.path)),
        this.#options.timeoutMs
      );
      requireChildAlive();
      await withTimeout(
        "task protocol handshake",
        whileChildAlive(client.handshake(token, CLIENT_NAME, CLIENT_VERSION)),
        this.#options.timeoutMs
      );
      requireChildAlive();
      await withTimeout(
        "service capabilities",
        whileChildAlive(client.getCapabilities()),
        this.#options.timeoutMs
      );
      requireChildAlive();
      if (startupExitListener) child.off("exit", startupExitListener);
      if (startupErrorListener) child.off("error", startupErrorListener);

      const managed: ManagedSession = {
        endpoint: endpointResource.path,
        tokenFile,
        sessionDirectory,
        client,
        child,
        exit,
        ...(endpointResource.directory ? { endpointDirectory: endpointResource.directory } : {}),
        sensitive,
        output,
        stdoutListener,
        stderrListener,
        expectedStop: false,
        get exited() { return exited; },
        set exited(value: boolean) { exited = value; }
      };
      this.#session = managed;
      this.#installRuntimeListeners(managed);
      this.#status = { state: "running" };
      return managed;
    } catch (error) {
      const sensitive = this.#sensitive(token, tokenFile, sessionDirectory, endpointResource);
      if (client) client.close();
      if (child) {
        if (startupExitListener) child.off("exit", startupExitListener);
        if (startupErrorListener) child.off("error", startupErrorListener);
        if (stdoutListener) child.stdout.off("data", stdoutListener);
        if (stderrListener) child.stderr.off("data", stderrListener);
        if (!exited && child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
        if (exit) await withTimeout("failed service process exit", exit, this.#options.timeoutMs).catch(() => undefined);
      }
      await removeOwnedDirectories(sessionDirectory, endpointResource?.directory).catch(() => undefined);
      const failure = diagnostic(error, output, sensitive);
      this.#status = { state: "failed", detail: failure.message };
      throw failure;
    }
  }

  stop(): Promise<void> {
    if (this.#stopPromise) return this.#stopPromise;
    const operation = this.#stopOwned();
    const tracked = operation.finally(() => {
      if (this.#stopPromise === tracked) this.#stopPromise = undefined;
    });
    this.#stopPromise = tracked;
    return tracked;
  }

  async #stopOwned(): Promise<void> {
    if (this.#startPromise) {
      await this.#startPromise.catch(() => undefined);
    }
    const session = this.#session;
    if (!session) {
      this.#status = { state: "stopped" };
      return;
    }

    this.#status = { state: "stopping" };
    session.expectedStop = true;
    this.#removeRuntimeListeners(session);
    let shutdownError: unknown;
    try {
      await withTimeout("graceful service shutdown request", session.client.shutdown(), this.#options.timeoutMs);
    } catch (error) {
      shutdownError = error;
    }
    session.client.close();

    if (!session.exited) {
      try {
        await withTimeout("graceful service process exit", session.exit, this.#options.timeoutMs);
      } catch {
        if (session.child.exitCode === null && session.child.signalCode === null) session.child.kill("SIGKILL");
        await withTimeout("forced service process exit", session.exit, this.#options.timeoutMs).catch(() => undefined);
      }
    }
    await this.#releaseSession(session);
    if (this.#session === session) this.#session = undefined;

    if (shutdownError) {
      const failure = diagnostic(shutdownError, session.output, session.sensitive);
      this.#status = { state: "failed", detail: failure.message };
      throw failure;
    }
    this.#status = { state: "stopped" };
  }

  async restart(): Promise<ServiceSession> {
    await this.stop();
    return this.start();
  }

  #sensitive(
    token: string,
    tokenFile: string,
    sessionDirectory: string | undefined,
    endpointResource: EndpointResource | undefined
  ): string[] {
    return [
      token,
      endpointResource?.path ?? "",
      endpointResource?.directory ?? "",
      tokenFile,
      sessionDirectory ?? "",
      this.#options.serviceExecutable,
      this.#options.workspaceRoot,
      this.#options.dataDirectory
    ];
  }

  #installRuntimeListeners(session: ManagedSession): void {
    session.childExitListener = (code, signal) => {
      if (session.expectedStop) return;
      void this.#failSession(
        session,
        new Error(`service exited unexpectedly with code ${String(code)} and signal ${String(signal)}`)
      ).catch(() => undefined);
    };
    session.childErrorListener = (error) => {
      if (session.expectedStop) return;
      void this.#failSession(session, error).catch(() => undefined);
    };
    session.child.once("exit", session.childExitListener);
    session.child.once("error", session.childErrorListener);

    const emitter = closeEmitter(session.client);
    if (emitter) {
      session.clientCloseListener = (error) => {
        if (session.expectedStop) return;
        void this.#failSession(session, error ?? new Error("service connection closed")).catch(() => undefined);
      };
      emitter.once("close", session.clientCloseListener);
    }
  }

  #removeRuntimeListeners(session: ManagedSession): void {
    if (session.childExitListener) session.child.off("exit", session.childExitListener);
    if (session.childErrorListener) session.child.off("error", session.childErrorListener);
    const emitter = closeEmitter(session.client);
    if (emitter && session.clientCloseListener) emitter.off("close", session.clientCloseListener);
    session.childExitListener = undefined;
    session.childErrorListener = undefined;
    session.clientCloseListener = undefined;
  }

  async #failSession(session: ManagedSession, error: unknown): Promise<void> {
    if (this.#session !== session || session.expectedStop) return;
    session.expectedStop = true;
    this.#removeRuntimeListeners(session);
    this.#session = undefined;
    session.client.close();
    if (!session.exited && session.child.exitCode === null && session.child.signalCode === null) {
      session.child.kill("SIGKILL");
    }
    if (!session.exited) {
      await withTimeout("failed service process exit", session.exit, this.#options.timeoutMs).catch(() => undefined);
    }
    await this.#releaseSession(session).catch(() => undefined);
    const failure = diagnostic(error, session.output, session.sensitive);
    this.#status = { state: "failed", detail: failure.message };
  }

  async #releaseSession(session: ManagedSession): Promise<void> {
    session.child.stdout.off("data", session.stdoutListener);
    session.child.stderr.off("data", session.stderrListener);
    await removeOwnedDirectories(session.sessionDirectory, session.endpointDirectory);
  }
}
