import {
  execFile as execFileCallback,
  spawn,
  type ChildProcessWithoutNullStreams
} from "node:child_process";
import { rm, writeFile } from "node:fs/promises";
import { once } from "node:events";
import { createConnection, type Socket } from "node:net";
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

class WorkspaceTrustRevokedError extends Error {
  constructor() {
    super("workspace is not trusted");
    this.name = "WorkspaceTrustRevokedError";
  }
}

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

interface ServiceConnectionCloseObservable {
  onConnectionClose(listener: (error: Error) => void): () => void;
}

interface ServiceCleanupOperations {
  removeDirectory(directory: string): Promise<void>;
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
  clientCloseUnsubscribe?: () => void;
}

const connectionCloseObservables = new WeakMap<ProtocolClient, ServiceConnectionCloseObservable>();

async function prepareTokenFile(binary: string, tokenFile: string, token: string): Promise<void> {
  await execFile(binary, ["--prepare-token-file", tokenFile], { windowsHide: true });
  await writeFile(tokenFile, token, { flag: "r+" });
}

const defaultOperations: ServiceOperations = {
  prepareTokenFile,
  spawnService: (binary, args) => spawn(binary, args, { windowsHide: true }),
  connect: connectProtocolClient
};

function observeSocketClose(socket: Socket): ServiceConnectionCloseObservable {
  const listeners = new Set<(error: Error) => void>();
  let closeError: Error | undefined;
  let closed = false;
  socket.once("error", (error) => { closeError = error; });
  socket.once("close", () => {
    closed = true;
    const error = closeError ?? new Error("service connection closed");
    for (const listener of [...listeners]) listener(error);
    listeners.clear();
  });
  return {
    onConnectionClose(listener) {
      if (closed) {
        queueMicrotask(() => listener(closeError ?? new Error("service connection closed")));
        return () => undefined;
      }
      listeners.add(listener);
      return () => listeners.delete(listener);
    }
  };
}

async function connectProtocolClient(endpoint: string): Promise<ProtocolClient> {
  const socket = createConnection(endpoint);
  const observable = observeSocketClose(socket);
  try {
    await once(socket, "connect");
  } catch (error) {
    socket.destroy();
    throw error;
  }
  const client = ProtocolClient.attach(socket);
  connectionCloseObservables.set(client, observable);
  return client;
}

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

function connectionCloseObservable(client: ProtocolClient): ServiceConnectionCloseObservable | undefined {
  const registered = connectionCloseObservables.get(client);
  if (registered) return registered;
  const candidate = client as unknown as Partial<ServiceConnectionCloseObservable>;
  return typeof candidate.onConnectionClose === "function"
    ? candidate as ServiceConnectionCloseObservable
    : undefined;
}

async function removeDirectory(directory: string): Promise<void> {
  await rm(directory, { recursive: true, force: true, maxRetries: 5, retryDelay: 50 });
}

async function removeOwnedDirectories(
  remover: (directory: string) => Promise<void>,
  sessionDirectory?: string,
  endpointDirectory?: string
): Promise<void> {
  const directories = new Set([sessionDirectory, endpointDirectory].filter((value): value is string => Boolean(value)));
  for (const directory of directories) {
    await remover(directory);
  }
}

export class ServiceManager {
  readonly #options: ServiceManagerOptions;
  readonly #operations: ServiceOperations;
  readonly #removeDirectory: (directory: string) => Promise<void>;
  #status: ServiceStatus = { state: "stopped" };
  #session: ManagedSession | undefined;
  #lifecycleTail: Promise<void> = Promise.resolve();

  constructor(options: ServiceManagerOptions) {
    this.#options = options;
    this.#operations = { ...defaultOperations, ...options.operations };
    const cleanupOperations = options.operations as (Partial<ServiceOperations> & Partial<ServiceCleanupOperations>) | undefined;
    this.#removeDirectory = cleanupOperations?.removeDirectory?.bind(cleanupOperations) ?? removeDirectory;
  }

  get status(): ServiceStatus {
    return this.#status;
  }

  get session(): ServiceSession | undefined {
    return this.#session;
  }

  start(): Promise<ServiceSession> {
    const trustState: TrustState = this.#options.trusted() ? "trusted" : "blocked-untrusted";
    if (!canStartService(trustState)) return Promise.reject(new WorkspaceTrustRevokedError());
    return this.#enqueueLifecycle(() => this.#startOwned());
  }

  async #startOwned(): Promise<ServiceSession> {
    this.#assertTrusted();
    if (this.#session && this.#status.state === "running") return this.#session;
    this.#status = { state: "starting" };
    this.#assertTrusted();
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
      this.#assertTrusted();
      endpointResource = await createEndpointResource(process.platform);
      this.#assertTrusted();
      tokenFile = join(sessionDirectory, "token");
      const sensitive = this.#sensitive(token, tokenFile, sessionDirectory, endpointResource);
      this.#assertTrusted();
      await withTimeout(
        "token file preparation",
        this.#operations.prepareTokenFile(this.#options.serviceExecutable, tokenFile, token),
        this.#options.timeoutMs
      );
      this.#assertTrusted();
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
      this.#assertTrusted();
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
      this.#assertTrusted();
      requireChildAlive();
      client = await withTimeout(
        "service connection",
        whileChildAlive(this.#operations.connect(endpointResource.path)),
        this.#options.timeoutMs
      );
      this.#assertTrusted();
      requireChildAlive();
      await withTimeout(
        "task protocol handshake",
        whileChildAlive(client.handshake(token, CLIENT_NAME, CLIENT_VERSION)),
        this.#options.timeoutMs
      );
      this.#assertTrusted();
      requireChildAlive();
      await withTimeout(
        "service capabilities",
        whileChildAlive(client.getCapabilities()),
        this.#options.timeoutMs
      );
      this.#assertTrusted();
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
      await removeOwnedDirectories(
        this.#removeDirectory,
        sessionDirectory,
        endpointResource?.directory
      ).catch(() => undefined);
      const failure = diagnostic(error, output, sensitive);
      if (error instanceof WorkspaceTrustRevokedError) {
        this.#status = { state: "stopped" };
        throw failure;
      }
      this.#status = { state: "failed", detail: failure.message };
      throw failure;
    }
  }

  stop(): Promise<void> {
    return this.#enqueueLifecycle(() => this.#stopOwned());
  }

  async #stopOwned(): Promise<void> {
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

  restart(): Promise<ServiceSession> {
    const trustState: TrustState = this.#options.trusted() ? "trusted" : "blocked-untrusted";
    if (!canStartService(trustState)) return Promise.reject(new WorkspaceTrustRevokedError());
    return this.#enqueueLifecycle(async () => {
      await this.#stopOwned();
      return this.#startOwned();
    });
  }

  #enqueueLifecycle<T>(operation: () => Promise<T>): Promise<T> {
    const result = this.#lifecycleTail.then(operation);
    this.#lifecycleTail = result.then(() => undefined, () => undefined);
    return result;
  }

  #assertTrusted(): void {
    if (!this.#options.trusted()) throw new WorkspaceTrustRevokedError();
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
      this.#scheduleFailure(
        session,
        new Error(`service exited unexpectedly with code ${String(code)} and signal ${String(signal)}`)
      );
    };
    session.childErrorListener = (error) => {
      if (session.expectedStop) return;
      this.#scheduleFailure(session, error);
    };
    session.child.once("exit", session.childExitListener);
    session.child.once("error", session.childErrorListener);

    const observable = connectionCloseObservable(session.client);
    if (observable) {
      session.clientCloseUnsubscribe = observable.onConnectionClose((error) => {
        if (session.expectedStop) return;
        this.#scheduleFailure(session, error);
      });
    }
  }

  #removeRuntimeListeners(session: ManagedSession): void {
    if (session.childExitListener) session.child.off("exit", session.childExitListener);
    if (session.childErrorListener) session.child.off("error", session.childErrorListener);
    session.clientCloseUnsubscribe?.();
    session.childExitListener = undefined;
    session.childErrorListener = undefined;
    session.clientCloseUnsubscribe = undefined;
  }

  #scheduleFailure(session: ManagedSession, error: unknown): void {
    if (this.#session !== session || session.expectedStop) return;
    session.expectedStop = true;
    this.#removeRuntimeListeners(session);
    const failure = diagnostic(error, session.output, session.sensitive);
    this.#status = { state: "failed", detail: failure.message };
    void this.#enqueueLifecycle(() => this.#failSession(session, error)).catch(() => undefined);
  }

  async #failSession(session: ManagedSession, error: unknown): Promise<void> {
    if (this.#session !== session) return;
    session.client.close();
    if (!session.exited && session.child.exitCode === null && session.child.signalCode === null) {
      session.child.kill("SIGKILL");
    }
    if (!session.exited) {
      await withTimeout("failed service process exit", session.exit, this.#options.timeoutMs).catch(() => undefined);
    }
    await this.#releaseSession(session).catch(() => undefined);
    const failure = diagnostic(error, session.output, session.sensitive);
    if (this.#session === session) {
      this.#session = undefined;
      this.#status = { state: "failed", detail: failure.message };
    }
  }

  async #releaseSession(session: ManagedSession): Promise<void> {
    session.child.stdout.off("data", session.stdoutListener);
    session.child.stderr.off("data", session.stderrListener);
    await removeOwnedDirectories(this.#removeDirectory, session.sessionDirectory, session.endpointDirectory);
  }
}
