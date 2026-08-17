import { execFile as execFileCallback, spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { randomBytes, randomUUID } from "node:crypto";
import { once } from "node:events";
import { access, mkdtemp, rm, writeFile } from "node:fs/promises";
import net from "node:net";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { createInterface } from "node:readline";
import { pathToFileURL } from "node:url";
import { promisify } from "node:util";
import type {
  Capabilities,
  CapabilitiesV11,
  CapabilitiesV12,
  CapabilitiesV13
} from "@unit-test-ide/protocol-models";
import { ProtocolClient, type ConnectionConnector, type HandshakeResult } from "@unit-test-ide/test-client";
import { endpointForDirectory, type EndpointResource } from "./endpoint.js";

type Exit = [code: number | null, signal: NodeJS.Signals | null];
const execFile = promisify(execFileCallback);
const OPERATION_TIMEOUT_MS = 8_000;

function namedTimeoutError(label: string, milliseconds: number): Error {
  const error = new Error(`${label} timed out after ${milliseconds}ms`);
  error.stack = `${error.name}: ${error.message}`;
  return error;
}

export function withNamedTimeout<T>(label: string, promise: Promise<T>, milliseconds = OPERATION_TIMEOUT_MS): Promise<T> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      reject(namedTimeoutError(label, milliseconds));
    }, milliseconds);
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

function acquireOwnedResource<T>(
  label: string,
  acquisition: Promise<T>,
  cleanupLabel: string,
  cleanup: (resource: T) => Promise<void>,
  milliseconds = OPERATION_TIMEOUT_MS
): Promise<T> {
  return new Promise((resolve, reject) => {
    let timedOut = false;
    const timer = setTimeout(() => {
      timedOut = true;
      reject(namedTimeoutError(label, milliseconds));
    }, milliseconds);
    acquisition.then(
      (resource) => {
        if (timedOut) {
          void withNamedTimeout(cleanupLabel, cleanup(resource), milliseconds).catch(() => undefined);
          return;
        }
        clearTimeout(timer);
        resolve(resource);
      },
      (error: unknown) => {
        if (timedOut) return;
        clearTimeout(timer);
        reject(error);
      }
    );
  });
}

interface ProbeOperations {
  prepareTokenFile?: (serviceBinary: string, tokenFile: string, token: string) => Promise<void>;
  spawnService?: (serviceBinary: string, args: string[]) => ChildProcessWithoutNullStreams;
  connectClient?: (serviceEndpoint: string) => Promise<ProtocolClient>;
  handshakeClient?: (client: ProtocolClient, token: string, serviceEndpoint: string) => Promise<HandshakeResult>;
}

export interface StartServiceOptions {
  timeoutMs?: number;
  workspaceRoot?: string;
  trustedWorkspace?: boolean;
  cmakeBundleRoot?: string;
  devCMakeExecutable?: string;
  operations?: ProbeOperations;
}

export interface ReconnectGate {
  readonly entered: Promise<void>;
  release(): void;
}

interface PendingReconnectGate extends ReconnectGate {
  enter(): void;
}

class PausableConnector {
  readonly #endpoint: string;
  readonly #timeoutMs: number;
  #pendingGate: PendingReconnectGate | undefined;
  #activeGate: PendingReconnectGate | undefined;

  constructor(endpoint: string, timeoutMs: number) {
    this.#endpoint = endpoint;
    this.#timeoutMs = timeoutMs;
  }

  readonly connect: ConnectionConnector = async () => {
    const gate = this.#pendingGate;
    if (gate) {
      this.#pendingGate = undefined;
      this.#activeGate = gate;
      gate.enter();
      try {
        await gateReleased(gate);
      } finally {
        if (this.#activeGate === gate) this.#activeGate = undefined;
      }
    }
    const socket = net.createConnection(this.#endpoint);
    try {
      await withNamedTimeout("socket connection", once(socket, "connect").then(() => undefined), this.#timeoutMs);
      return socket;
    } catch (error) {
      socket.destroy();
      throw error;
    }
  };

  pauseNext(): ReconnectGate {
    if (this.#pendingGate) throw new Error("a reconnect connector gate is already pending");
    let enter!: () => void;
    let release!: () => void;
    const entered = new Promise<void>((resolve) => { enter = resolve; });
    const released = new Promise<void>((resolve) => { release = resolve; });
    let releasedOnce = false;
    const gate: PendingReconnectGate & { released: Promise<void> } = {
      entered,
      released,
      enter,
      release: () => {
        if (releasedOnce) return;
        releasedOnce = true;
        release();
      }
    };
    this.#pendingGate = gate;
    return gate;
  }

  releasePending(): void {
    this.#pendingGate?.release();
    this.#activeGate?.release();
    this.#pendingGate = undefined;
    this.#activeGate = undefined;
  }
}

function gateReleased(gate: PendingReconnectGate): Promise<void> {
  return (gate as PendingReconnectGate & { released: Promise<void> }).released;
}

function ready(child: ChildProcessWithoutNullStreams, expectedEndpoint: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const lines = createInterface({ input: child.stdout });
    let settled = false;

    const cleanup = () => {
      lines.off("line", onLine);
      child.off("error", onError);
      child.off("exit", onExit);
      lines.close();
    };
    const finish = (error?: Error) => {
      if (settled) return;
      settled = true;
      cleanup();
      if (error) reject(error);
      else resolve();
    };
    const onLine = (line: string) => {
      if (line === `READY ${expectedEndpoint}`) finish();
    };
    const onError = (error: Error) => finish(error);
    const onExit = (code: number | null, signal: NodeJS.Signals | null) => {
      finish(new Error(`service exited before READY with code ${String(code)} and signal ${String(signal)}`));
    };

    lines.on("line", onLine);
    child.once("error", onError);
    child.once("exit", onExit);
  });
}

async function tokenWasConsumed(tokenFile: string): Promise<void> {
  try {
    await access(tokenFile);
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === "ENOENT") return;
    throw error;
  }
  throw new Error("service did not delete the token file after reading it");
}

export async function prepareTokenFile(serviceBinary: string, tokenFile: string, token: string): Promise<void> {
  try {
    await withNamedTimeout(
      "token file permission preparation",
      execFile(serviceBinary, ["--prepare-token-file", tokenFile], { windowsHide: true, timeout: OPERATION_TIMEOUT_MS })
    );
    await withNamedTimeout("token secret write", writeFile(tokenFile, token, { flag: "r+" }));
  } catch (error) {
    throw safeError(error, [serviceBinary, tokenFile, token]);
  }
}

function scrubAbsolutePaths(value: string): string {
  return value
    .replace(/[A-Za-z]:\\[^\r\n"']+/g, "<absolute-path>")
    .replace(/(^|[\s=("'])\/(?:[^\s\r\n"']+)/gm, "$1<absolute-path>");
}

function redact(value: string, sensitive: readonly string[]): string {
  let result = value;
  for (const item of sensitive) {
    if (item) result = result.split(item).join("<redacted>");
  }
  return scrubAbsolutePaths(result).replace(/\b[A-Za-z_][A-Za-z0-9_]*=[^\s;,]+/g, "<environment>");
}

function safeError(error: unknown, sensitive: readonly string[], suffix = ""): Error {
  const raw = error instanceof Error ? error.message : String(error);
  const message = `${redact(raw, sensitive)}${suffix}`;
  const result = new Error(message);
  result.stack = `${result.name}: ${result.message}`;
  return result;
}

interface ServiceInstance {
  readonly client: ProtocolClient;
  readonly child: ChildProcessWithoutNullStreams;
  readonly exit: Promise<Exit>;
  readonly endpoint: string;
  readonly endpointDirectory?: string;
  readonly dataDir: string;
  readonly tokenFile: string;
  readonly token: string;
  readonly connector: PausableConnector;
  readonly serviceBinary: string;
  readonly directory: string;
  readonly workspaceRoot: string;
  readonly cmakeBundleRoot?: string;
  readonly devCMakeExecutable?: string;
  stdout: string;
  stderr: string;
}

function serviceSensitive(instance: ServiceInstance): string[] {
  return [
    instance.token,
    instance.endpoint,
    instance.tokenFile,
    instance.dataDir,
    instance.workspaceRoot,
    instance.cmakeBundleRoot ?? "",
    instance.devCMakeExecutable ?? "",
    instance.serviceBinary,
    instance.directory
  ];
}

function diagnostics(instance: ServiceInstance): string {
  const sensitive = serviceSensitive(instance);
  return `stdout=${redact(instance.stdout, sensitive)}; stderr=${redact(instance.stderr, sensitive)}`;
}

async function forceStop(instance: ServiceInstance): Promise<void> {
  instance.connector.releasePending();
  instance.client.close();
  if (instance.child.exitCode === null && instance.child.signalCode === null) {
    instance.child.kill("SIGKILL");
  }
  let processError: unknown;
  try {
    await withNamedTimeout("forced service process exit", instance.exit);
  } catch (error) {
    processError = error;
  }
  let endpointError: unknown;
  if (instance.endpointDirectory) {
    try {
      await withNamedTimeout(
        "Unix endpoint cleanup",
        rm(instance.endpointDirectory, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 })
      );
    } catch (error) {
      endpointError = error;
    }
  }
  if (processError || endpointError) throw processError ?? endpointError;
}

async function launchService(serviceBinary: string, directory: string, options: StartServiceOptions = {}): Promise<ServiceInstance> {
  const token = randomBytes(32).toString("base64url");
  const tokenFile = join(directory, `token-${randomUUID()}`);
  const dataDir = join(directory, "data");
  const workspaceRoot = options.workspaceRoot ?? directory;
  const timeoutMs = options.timeoutMs ?? OPERATION_TIMEOUT_MS;
  let endpointResource: EndpointResource | undefined;
  let child: ChildProcessWithoutNullStreams | undefined;
  let exit: Promise<Exit> | undefined;
  let stdout = "";
  let stderr = "";
  let client: ProtocolClient | undefined;
  try {
    endpointResource = await acquireOwnedResource(
      "endpoint allocation",
      endpointForDirectory(directory),
      "late Unix endpoint cleanup",
      async (resource) => {
        if (resource.directory) {
          await rm(resource.directory, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
        }
      },
      timeoutMs
    );
    await withNamedTimeout(
      "token file preparation",
      (options.operations?.prepareTokenFile ?? prepareTokenFile)(serviceBinary, tokenFile, token),
      timeoutMs
    );
    const serviceArguments = [
      "--endpoint", endpointResource.path,
      "--token-file", tokenFile,
      "--data-dir", dataDir,
      "--workspace-root", workspaceRoot
    ];
    if (options.trustedWorkspace !== undefined) {
      serviceArguments.push(`--trusted-workspace=${String(options.trustedWorkspace)}`);
    }
    if (options.cmakeBundleRoot) {
      serviceArguments.push("--cmake-bundle-root", options.cmakeBundleRoot);
    }
    if (options.devCMakeExecutable) {
      serviceArguments.push("--dev-cmake-executable", options.devCMakeExecutable);
    }
    child = (options.operations?.spawnService ?? ((binary, args) => spawn(binary, args, { windowsHide: true })))(
      serviceBinary,
      serviceArguments
    );
    const launchedChild = child;
    exit = new Promise<Exit>((resolve) => launchedChild.once("exit", (code, signal) => resolve([code, signal])));
    child.stdout.on("data", (chunk: Buffer | string) => { stdout += String(chunk); });
    child.stderr.on("data", (chunk: Buffer | string) => { stderr += String(chunk); });
    child.on("error", (error) => { stderr += `${error.message}\n`; });

    await withNamedTimeout("service startup readiness", ready(child, endpointResource.path), timeoutMs);
    await withNamedTimeout("token file consumption confirmation", tokenWasConsumed(tokenFile), timeoutMs);
    const connector = new PausableConnector(endpointResource.path, timeoutMs);
    client = await withNamedTimeout(
      "service connection",
      options.operations?.connectClient
        ? options.operations.connectClient(endpointResource.path)
        : ProtocolClient.connect(connector.connect),
      timeoutMs
    );
    const handshake = await withNamedTimeout(
      "task protocol handshake",
      (options.operations?.handshakeClient ?? ((value, secret) => value.handshake(secret, "service-probe", "0.1.0")))(
        client,
        token,
        endpointResource.path
      ),
      timeoutMs
    );
    if (handshake.negotiatedProtocolVersion === "1.0") {
      throw new Error("service negotiated protocol 1.0 without task support");
    }
    return {
      child,
      exit,
      endpoint: endpointResource.path,
      ...(endpointResource.directory ? { endpointDirectory: endpointResource.directory } : {}),
      dataDir,
      tokenFile,
      token,
      connector,
      serviceBinary,
      directory,
      workspaceRoot,
      ...(options.cmakeBundleRoot ? { cmakeBundleRoot: options.cmakeBundleRoot } : {}),
      ...(options.devCMakeExecutable ? { devCMakeExecutable: options.devCMakeExecutable } : {}),
      get stdout() { return stdout; },
      get stderr() { return stderr; },
      client
    };
  } catch (error) {
    client?.close();
    if (child && exit) {
      if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
      await withNamedTimeout("failed service process exit", exit, timeoutMs).catch(() => undefined);
    }
    if (endpointResource?.directory) {
      await withNamedTimeout(
        "failed Unix endpoint cleanup",
        rm(endpointResource.directory, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 }),
        timeoutMs
      ).catch(() => undefined);
    }
    const sensitive = [
      token, endpointResource?.path ?? "", tokenFile, dataDir, workspaceRoot,
      options.cmakeBundleRoot ?? "", options.devCMakeExecutable ?? "", serviceBinary, directory
    ];
    const details = `; stdout=${redact(stdout, sensitive)}; stderr=${redact(stderr, sensitive)}`;
    throw safeError(error, sensitive, details);
  }
}

export class TaskServiceFixture {
  readonly #serviceBinary: string;
  readonly #directory: string;
  readonly #options: StartServiceOptions;
  #instance: ServiceInstance | undefined;
  #lifecycleTail: Promise<void> = Promise.resolve();
  #disposeRequested = false;
  #disposePromise: Promise<void> | undefined;
  #disposed = false;

  constructor(serviceBinary: string, directory: string, instance: ServiceInstance, options: StartServiceOptions = {}) {
    this.#serviceBinary = serviceBinary;
    this.#directory = directory;
    this.#instance = instance;
    this.#options = options;
  }

  #assertAvailable(): void {
    if (this.#disposed || this.#disposeRequested) throw new Error("task service fixture is disposed");
  }

  #enqueueLifecycle<T>(operation: () => Promise<T>): Promise<T> {
    const result = this.#lifecycleTail.then(operation);
    this.#lifecycleTail = result.then(() => undefined, () => undefined);
    return result;
  }

  get client(): ProtocolClient {
    this.#assertAvailable();
    if (!this.#instance) throw new Error("task service fixture is stopped");
    return this.#instance.client;
  }

  pauseNextReconnect(): ReconnectGate {
    this.#assertAvailable();
    if (!this.#instance) throw new Error("task service fixture is stopped");
    return this.#instance.connector.pauseNext();
  }

  connectClient(): Promise<ProtocolClient> {
    this.#assertAvailable();
    return this.#enqueueLifecycle(async () => {
      this.#assertAvailable();
      const instance = this.#instance;
      if (!instance) throw new Error("task service fixture is stopped");
      const timeoutMs = this.#options.timeoutMs ?? OPERATION_TIMEOUT_MS;
      let client: ProtocolClient | undefined;
      try {
        client = await withNamedTimeout("secondary service connection", ProtocolClient.connect(instance.endpoint), timeoutMs);
        const handshake = await withNamedTimeout(
          "secondary task protocol handshake",
          client.handshake(instance.token, "service-probe-secondary", "0.1.0"),
          timeoutMs
        );
        if (handshake.negotiatedProtocolVersion === "1.0") {
          throw new Error("secondary client negotiated protocol 1.0 without task support");
        }
        this.#assertAvailable();
        return client;
      } catch (error) {
        client?.close();
        throw safeError(error, serviceSensitive(instance));
      }
    });
  }

  async #stopGracefullyOwned(): Promise<void> {
    const instance = this.#instance;
    if (!instance) return;
    try {
      instance.connector.releasePending();
      await withNamedTimeout("graceful service shutdown request", instance.client.shutdown(), this.#options.timeoutMs);
      instance.client.close();
      const [code, signal] = await withNamedTimeout("graceful service process exit", instance.exit, this.#options.timeoutMs);
      if (code !== 0) {
        throw new Error(`service exited with code ${String(code)} and signal ${String(signal)}`);
      }
      if (instance.endpointDirectory) {
        await withNamedTimeout(
          "graceful Unix endpoint cleanup",
          rm(instance.endpointDirectory, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 }),
          this.#options.timeoutMs
        );
      }
      this.#instance = undefined;
    } catch (error) {
      const sensitive = serviceSensitive(instance);
      throw safeError(error, sensitive, `; ${diagnostics(instance)}`);
    }
  }

  stopGracefully(): Promise<void> {
    if (this.#disposed) return Promise.resolve();
    this.#assertAvailable();
    return this.#enqueueLifecycle(async () => {
      this.#assertAvailable();
      await this.#stopGracefullyOwned();
    });
  }

  async #killOwned(): Promise<void> {
    const instance = this.#instance;
    if (!instance) return;
    try {
      await forceStop(instance);
      this.#instance = undefined;
    } catch (error) {
      const sensitive = serviceSensitive(instance);
      throw safeError(error, sensitive, `; ${diagnostics(instance)}`);
    }
  }

  kill(): Promise<void> {
    if (this.#disposed) return Promise.resolve();
    this.#assertAvailable();
    return this.#enqueueLifecycle(async () => {
      this.#assertAvailable();
      await this.#killOwned();
    });
  }

  restart(): Promise<TaskServiceFixture> {
    this.#assertAvailable();
    return this.#enqueueLifecycle(async () => {
      this.#assertAvailable();
      if (this.#instance) await this.#stopGracefullyOwned();
      const launched = await launchService(this.#serviceBinary, this.#directory, this.#options);
      if (this.#disposeRequested) {
        try {
          await forceStop(launched);
        } catch (error) {
          this.#instance = launched;
          const sensitive = serviceSensitive(launched);
          throw safeError(error, sensitive, "; restart cancelled because task service fixture is disposed");
        }
        throw new Error("task service fixture is disposed during restart");
      }
      this.#instance = launched;
      return this;
    });
  }

  async #disposeOwned(): Promise<void> {
    const instance = this.#instance;
    instance?.connector.releasePending();
    if (instance) {
      try {
        await forceStop(instance);
      } catch (error) {
        const sensitive = serviceSensitive(instance);
        throw safeError(error, sensitive, `; ${diagnostics(instance)}`);
      }
      if (this.#instance === instance) this.#instance = undefined;
    }
    try {
      await withNamedTimeout(
        "fixture directory cleanup",
        rm(this.#directory, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 }),
        this.#options.timeoutMs
      );
    } catch (error) {
      const sensitive = instance
        ? serviceSensitive(instance)
        : [this.#serviceBinary, this.#directory];
      throw safeError(error, sensitive);
    }
  }

  dispose(): Promise<void> {
    if (this.#disposed) return Promise.resolve();
    if (this.#disposePromise) return this.#disposePromise;
    this.#disposeRequested = true;
    const operation = this.#enqueueLifecycle(() => this.#disposeOwned());
    const tracked = operation.then(
      () => {
        this.#disposed = true;
      },
      (error: unknown) => {
        this.#disposeRequested = false;
        throw error;
      }
    );
    const result = tracked.finally(() => {
      if (this.#disposePromise === result) this.#disposePromise = undefined;
    });
    this.#disposePromise = result;
    return result;
  }
}

export async function startService(
  serviceBinary: string,
  directory: string,
  options: StartServiceOptions = {}
): Promise<TaskServiceFixture> {
  return new TaskServiceFixture(serviceBinary, directory, await launchService(serviceBinary, directory, options), options);
}

export async function startTaskService(
  serviceBinary: string,
  options: StartServiceOptions = {}
): Promise<TaskServiceFixture> {
  const timeoutMs = options.timeoutMs ?? OPERATION_TIMEOUT_MS;
  let directory: string | undefined;
  try {
    directory = await acquireOwnedResource(
      "fixture directory allocation",
      mkdtemp(join(dirname(serviceBinary), "unit-test-ide-probe-")),
      "late fixture directory cleanup",
      (lateDirectory) => rm(lateDirectory, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 }),
      timeoutMs
    );
    return await startService(serviceBinary, directory, options);
  } catch (error) {
    if (directory) {
      try {
        await withNamedTimeout(
          "failed fixture directory cleanup",
          rm(directory, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 }),
          timeoutMs
        );
      } catch {
        // Preserve the already-sanitized launch error; cleanup remains bounded.
      }
    }
    throw safeError(error, [serviceBinary, directory ?? ""]);
  }
}

export async function assertProcessGone(pid: number): Promise<void> {
  if (!Number.isSafeInteger(pid) || pid <= 0) throw new Error("process PID must be a positive safe integer");
  const deadline = Date.now() + OPERATION_TIMEOUT_MS;
  while (Date.now() < deadline) {
    try {
      process.kill(pid, 0);
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code === "ESRCH") return;
      throw error;
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error(`process ${pid} still exists after ${OPERATION_TIMEOUT_MS}ms`);
}

export async function runProbe(
  serviceBinary: string
): Promise<
  Capabilities |
  CapabilitiesV11 |
  CapabilitiesV12 |
  CapabilitiesV13
> {
  const fixture = await startTaskService(serviceBinary);
  try {
    const capabilities = await withNamedTimeout("capabilities request", fixture.client.getCapabilities());
    await fixture.stopGracefully();
    return capabilities;
  } finally {
    await fixture.dispose();
  }
}

const entry = process.argv[1];
if (entry && import.meta.url === pathToFileURL(entry).href) {
  const binary = process.argv[2];
  if (!binary) throw new Error("service binary path is required");
  console.log(JSON.stringify(await runProbe(binary)));
}
