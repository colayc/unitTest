import { execFile as execFileCallback, spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { randomBytes, randomUUID } from "node:crypto";
import { access, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { createInterface } from "node:readline";
import { pathToFileURL } from "node:url";
import { promisify } from "node:util";
import type { Capabilities } from "@unit-test-ide/protocol-models";
import { ProtocolClient } from "@unit-test-ide/test-client";
import { endpoint } from "./endpoint.js";

type Exit = [code: number | null, signal: NodeJS.Signals | null];
const execFile = promisify(execFileCallback);
const OPERATION_TIMEOUT_MS = 8_000;

function within<T>(promise: Promise<T>, milliseconds: number, label: string): Promise<T> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`${label} timed out after ${milliseconds}ms`)), milliseconds);
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
  await execFile(serviceBinary, ["--prepare-token-file", tokenFile], { windowsHide: true });
  await writeFile(tokenFile, token, { flag: "r+" });
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
  return scrubAbsolutePaths(result);
}

interface ServiceInstance {
  readonly client: ProtocolClient;
  readonly child: ChildProcessWithoutNullStreams;
  readonly exit: Promise<Exit>;
  readonly endpoint: string;
  readonly dataDir: string;
  readonly tokenFile: string;
  readonly token: string;
  readonly serviceBinary: string;
  readonly directory: string;
  stdout: string;
  stderr: string;
}

function diagnostics(instance: ServiceInstance): string {
  const sensitive = [
    instance.token,
    instance.endpoint,
    instance.tokenFile,
    instance.dataDir,
    instance.serviceBinary,
    instance.directory
  ];
  return `stdout=${redact(instance.stdout, sensitive)}; stderr=${redact(instance.stderr, sensitive)}`;
}

async function forceStop(instance: ServiceInstance): Promise<void> {
  instance.client.close();
  if (instance.child.exitCode === null && instance.child.signalCode === null) {
    instance.child.kill("SIGKILL");
  }
  await within(instance.exit, OPERATION_TIMEOUT_MS, "forced service process exit");
}

async function launchService(serviceBinary: string, directory: string): Promise<ServiceInstance> {
  const token = randomBytes(32).toString("base64url");
  const tokenFile = join(directory, `token-${randomUUID()}`);
  const serviceEndpoint = endpoint(directory);
  const dataDir = join(directory, "data");
  await prepareTokenFile(serviceBinary, tokenFile, token);
  const child = spawn(serviceBinary, [
    "--endpoint", serviceEndpoint,
    "--token-file", tokenFile,
    "--data-dir", dataDir
  ], { windowsHide: true });
  const exit = new Promise<Exit>((resolve) => child.once("exit", (code, signal) => resolve([code, signal])));
  const instance = {
    child,
    exit,
    endpoint: serviceEndpoint,
    dataDir,
    tokenFile,
    token,
    serviceBinary,
    directory,
    stdout: "",
    stderr: "",
    client: undefined as unknown as ProtocolClient
  };
  child.stdout.on("data", (chunk: Buffer | string) => { instance.stdout += String(chunk); });
  child.stderr.on("data", (chunk: Buffer | string) => { instance.stderr += String(chunk); });
  child.on("error", (error) => { instance.stderr += `${error.message}\n`; });

  let client: ProtocolClient | undefined;
  try {
    await within(ready(child, serviceEndpoint), OPERATION_TIMEOUT_MS, "service startup readiness");
    await tokenWasConsumed(tokenFile);
    client = await within(ProtocolClient.connect(serviceEndpoint), OPERATION_TIMEOUT_MS, "service connection");
    instance.client = client;
    const handshake = await within(
      client.handshake(token, "service-probe", "0.1.0"),
      OPERATION_TIMEOUT_MS,
      "protocol 1.1 handshake"
    );
    if (handshake.negotiatedProtocolVersion !== "1.1") {
      throw new Error(`service negotiated protocol ${handshake.negotiatedProtocolVersion} instead of 1.1`);
    }
    return instance;
  } catch (error) {
    client?.close();
    instance.client = client ?? ({ close() {} } as ProtocolClient);
    await forceStop(instance).catch(() => undefined);
    const message = error instanceof Error ? error.message : String(error);
    throw new Error(`${redact(message, [token, serviceEndpoint, tokenFile, dataDir, serviceBinary, directory])}; ${diagnostics(instance)}`, {
      cause: error
    });
  }
}

export class TaskServiceFixture {
  readonly #serviceBinary: string;
  readonly #directory: string;
  #instance: ServiceInstance | undefined;
  #disposed = false;

  constructor(serviceBinary: string, directory: string, instance: ServiceInstance) {
    this.#serviceBinary = serviceBinary;
    this.#directory = directory;
    this.#instance = instance;
  }

  get client(): ProtocolClient {
    if (this.#disposed) throw new Error("task service fixture is disposed");
    if (!this.#instance) throw new Error("task service fixture is stopped");
    return this.#instance.client;
  }

  async stopGracefully(): Promise<void> {
    const instance = this.#instance;
    if (!instance) return;
    try {
      await within(instance.client.shutdown(), OPERATION_TIMEOUT_MS, "graceful service shutdown request");
      instance.client.close();
      const [code, signal] = await within(instance.exit, OPERATION_TIMEOUT_MS, "graceful service process exit");
      if (code !== 0) {
        throw new Error(`service exited with code ${String(code)} and signal ${String(signal)}`);
      }
      this.#instance = undefined;
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      throw new Error(`${redact(message, [instance.token, instance.endpoint, instance.tokenFile, instance.dataDir, instance.serviceBinary, instance.directory])}; ${diagnostics(instance)}`, {
        cause: error
      });
    }
  }

  async kill(): Promise<void> {
    const instance = this.#instance;
    if (!instance) return;
    try {
      await forceStop(instance);
      this.#instance = undefined;
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      throw new Error(`${redact(message, [instance.token, instance.endpoint, instance.tokenFile, instance.dataDir, instance.serviceBinary, instance.directory])}; ${diagnostics(instance)}`, {
        cause: error
      });
    }
  }

  async restart(): Promise<TaskServiceFixture> {
    if (this.#disposed) throw new Error("task service fixture is disposed");
    if (this.#instance) await this.stopGracefully();
    this.#instance = await launchService(this.#serviceBinary, this.#directory);
    return this;
  }

  async dispose(): Promise<void> {
    if (this.#disposed) return;
    this.#disposed = true;
    const instance = this.#instance;
    this.#instance = undefined;
    if (instance) await forceStop(instance).catch(() => undefined);
    await rm(this.#directory, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
  }
}

export async function startService(serviceBinary: string, directory: string): Promise<TaskServiceFixture> {
  return new TaskServiceFixture(serviceBinary, directory, await launchService(serviceBinary, directory));
}

export async function startTaskService(serviceBinary: string): Promise<TaskServiceFixture> {
  const directory = await mkdtemp(join(dirname(serviceBinary), "unit-test-ide-probe-"));
  try {
    return await startService(serviceBinary, directory);
  } catch (error) {
    await rm(directory, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
    throw error;
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

export async function runProbe(serviceBinary: string): Promise<Capabilities> {
  const fixture = await startTaskService(serviceBinary);
  try {
    const capabilities = await within(fixture.client.getCapabilities(), OPERATION_TIMEOUT_MS, "capabilities request");
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
