import { randomBytes, randomUUID } from "node:crypto";
import { chmod, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, posix } from "node:path";

export interface EndpointResource {
  path: string;
  directory?: string;
}

export async function createSessionDirectory(prefix: string): Promise<string> {
  const directory = await mkdtemp(join(tmpdir(), prefix));
  try {
    await chmod(directory, 0o700);
    return directory;
  } catch (error) {
    await rm(directory, { recursive: true, force: true }).catch(() => undefined);
    throw error;
  }
}

export async function createEndpointResource(platform: NodeJS.Platform): Promise<EndpointResource> {
  if (platform === "win32") {
    return { path: `\\\\.\\pipe\\unit-test-ide-${randomUUID()}` };
  }

  const allocatedDirectory = await createSessionDirectory("utide-");
  const directory = allocatedDirectory.replaceAll("\\", "/");
  const path = posix.join(directory, "s");
  if (Buffer.byteLength(path, "utf8") + 1 > 108) {
    await rm(allocatedDirectory, { recursive: true, force: true }).catch(() => undefined);
    throw new Error("Unix Socket endpoint exceeds sockaddr_un capacity");
  }
  return { path, directory };
}

export function createToken(): string {
  return randomBytes(32).toString("base64url");
}

function scrubAbsolutePaths(value: string): string {
  return value
    .replace(/[A-Za-z]:[\\/][^\r\n"']+/g, "<absolute-path>")
    .replace(/(^|[\s=("'])\/(?:[^\s\r\n"']+)/gm, "$1<absolute-path>");
}

export function redactServiceError(error: unknown, sensitive: readonly string[]): Error {
  let message = error instanceof Error ? error.message : String(error);
  for (const value of sensitive) {
    if (value) message = message.split(value).join("<redacted>");
  }
  message = scrubAbsolutePaths(message)
    .replace(/\b(?!(?:stdout|stderr)=)[A-Za-z_][A-Za-z0-9_]*=[^\s;,]+/g, "<environment>");
  const redacted = new Error(message);
  redacted.stack = `${redacted.name}: ${redacted.message}`;
  return redacted;
}
