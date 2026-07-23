import { randomUUID } from "node:crypto";
import { chmod, mkdtemp, rm } from "node:fs/promises";
import { posix } from "node:path";

export interface EndpointResource {
  path: string;
  directory?: string;
}

export type TemporaryDirectoryFactory = (prefix: string) => Promise<string>;

async function ownerOnlyTemporaryDirectory(prefix: string): Promise<string> {
  const directory = await mkdtemp(prefix);
  try {
    await chmod(directory, 0o700);
    return directory;
  } catch (error) {
    await rm(directory, { recursive: true, force: true }).catch(() => undefined);
    throw error;
  }
}

export async function endpointForDirectory(
  tempDirectory: string,
  platform: NodeJS.Platform = process.platform,
  makeTemporaryDirectory: TemporaryDirectoryFactory = ownerOnlyTemporaryDirectory
): Promise<EndpointResource> {
  if (platform === "win32") {
    return { path: `\\\\.\\pipe\\unit-test-ide-${randomUUID()}` };
  }
  void tempDirectory;
  const directory = await makeTemporaryDirectory("/tmp/utide-");
  const path = posix.join(directory.replaceAll("\\", "/"), "s");
  if (Buffer.byteLength(path, "utf8") + 1 > 108) {
    await rm(directory, { recursive: true, force: true }).catch(() => undefined);
    throw new Error("Unix Socket endpoint exceeds sockaddr_un capacity");
  }
  return { path, directory };
}
