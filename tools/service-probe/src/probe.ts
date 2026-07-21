import { execFile as execFileCallback, spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { randomBytes } from "node:crypto";
import { access, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";
import { pathToFileURL } from "node:url";
import { promisify } from "node:util";
import type { Capabilities } from "@unit-test-ide/protocol-models";
import { ProtocolClient } from "@unit-test-ide/test-client";
import { endpoint } from "./endpoint.js";

type Exit = [code: number | null, signal: NodeJS.Signals | null];
const execFile = promisify(execFileCallback);

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

async function terminate(child: ChildProcessWithoutNullStreams, exit: Promise<Exit>): Promise<void> {
  if (child.exitCode !== null || child.signalCode !== null) return;
  child.kill();
  try {
    await within(exit, 1000, "forced service shutdown");
  } catch {
    child.kill("SIGKILL");
    await within(exit, 1000, "forced service kill").catch(() => undefined);
  }
}

export async function runProbe(serviceBinary: string): Promise<Capabilities> {
  const directory = await mkdtemp(join(tmpdir(), "unit-test-ide-probe-"));
  const token = randomBytes(32).toString("base64url");
  const tokenFile = join(directory, "token");
  const serviceEndpoint = endpoint(directory);
  let child: ChildProcessWithoutNullStreams | undefined;
  let exit: Promise<Exit> | undefined;
  let client: ProtocolClient | undefined;
  let stdout = "";
  let stderr = "";

  try {
    await prepareTokenFile(serviceBinary, tokenFile, token);
    child = spawn(serviceBinary, ["--endpoint", serviceEndpoint, "--token-file", tokenFile], { windowsHide: true });
    exit = new Promise((resolve) => child?.once("exit", (code, signal) => resolve([code, signal])));
    child.stdout.on("data", (chunk: Buffer | string) => { stdout += String(chunk); });
    child.stderr.on("data", (chunk: Buffer | string) => { stderr += String(chunk); });
    child.on("error", (error) => { stderr += `${error.message}\n`; });

    await within(ready(child, serviceEndpoint), 5000, "service startup");
    await tokenWasConsumed(tokenFile);
    client = await within(ProtocolClient.connect(serviceEndpoint), 5000, "service connection");
    await within(client.handshake(token, "service-probe", "0.1.0"), 5000, "service handshake");
    const capabilities = await within(client.getCapabilities(), 5000, "capabilities request");
    await within(client.shutdown(), 5000, "service shutdown request");
    const [code, signal] = await within(exit, 5000, "service shutdown");
    if (code !== 0) {
      throw new Error(`service exited with code ${String(code)} and signal ${String(signal)}`);
    }
    return capabilities;
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    throw new Error(`${message}; stdout=${stdout}; stderr=${stderr}`, { cause: error });
  } finally {
    client?.close();
    if (child && exit) await terminate(child, exit);
    await rm(directory, { recursive: true, force: true });
  }
}

const entry = process.argv[1];
if (entry && import.meta.url === pathToFileURL(entry).href) {
  const binary = process.argv[2];
  if (!binary) throw new Error("service binary path is required");
  console.log(JSON.stringify(await runProbe(binary)));
}
