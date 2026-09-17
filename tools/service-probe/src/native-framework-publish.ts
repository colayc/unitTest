import { createHash, randomUUID } from "node:crypto";
import fs from "node:fs/promises";
import { dirname, isAbsolute, join, relative, resolve, sep } from "node:path";
import type { PreparedFrameworkRuntime } from "./native-framework-prepare.js";
import type { FrameworkPlatform, FrameworkToolchainFamily } from "./native-framework-report.js";
import { buildFrameworkRuntimeManifest, parseFrameworkRuntimeManifest, type FrameworkRuntimeManifest } from "./native-framework-runtime-contract.js";
import { hashCompiledFrameworkExecutable, validateOwnedFrameworkStage } from "./native-framework-workspace.js";

export interface PublishedFrameworkRuntime {
  readonly schemaVersion: 1;
  readonly platform: FrameworkPlatform;
  readonly candidateCommit: string;
  readonly toolchainFamilies: readonly FrameworkToolchainFamily[];
  readonly manifestSha256: string;
}

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u;
const platformName = (platform: FrameworkPlatform) => platform === "win32" ? "windows" : "linux";
const digest = (bytes: Uint8Array) => createHash("sha256").update(bytes).digest("hex");

/** One exclusive, fail-closed lock shared with runtime readers. Never breaks stale locks. */
export async function acquireFrameworkRuntimeLock(repositoryRoot: string, platform: FrameworkPlatform): Promise<() => Promise<void>> {
  try {
    if (!isAbsolute(repositoryRoot) || (platform !== "linux" && platform !== "win32")) throw new Error("invalid lock coordinate");
    const root = resolve(repositoryRoot);
    await directDirectory(root);
    const directory = join(root, ".native-e2e/framework-runtime");
    await directories(root, directory);
    const lock = join(directory, `${platformName(platform)}.lock`);
    await fs.mkdir(lock, { mode: 0o700 });
    const owner = randomUUID();
    const ownerFile = join(lock, "owner");
    await fs.writeFile(ownerFile, owner, { flag: "wx", mode: 0o600 });
    return async () => {
      try {
        await directDirectory(directory);
        await directDirectory(lock);
        await directFile(ownerFile);
        if (await fs.readFile(ownerFile, "utf8") !== owner || (await fs.readdir(lock)).join() !== "owner") throw new Error("lock ownership changed");
        await fs.unlink(ownerFile);
        await fs.rmdir(lock);
      } catch { throw sanitized("framework runtime publication lock release failed"); }
    };
  } catch { throw sanitized("framework runtime publication locked or unavailable"); }
}

export async function publishFrameworkRuntime(prepared: PreparedFrameworkRuntime): Promise<PublishedFrameworkRuntime> {
  try { return await publish(prepared); }
  catch { throw sanitized("framework runtime publication failed"); }
}

async function publish(prepared: PreparedFrameworkRuntime): Promise<PublishedFrameworkRuntime> {
  if (prepared === null || typeof prepared !== "object" || Object.getPrototypeOf(prepared) !== Object.prototype ||
      Reflect.ownKeys(prepared).map(String).sort().join() !== "manifest,ownedStagingRoots,ownershipId" ||
      !uuid.test(prepared.ownershipId) || !Array.isArray(prepared.ownedStagingRoots) || prepared.ownedStagingRoots.length !== 4) throw new Error("invalid prepared runtime");
  const manifest = buildFrameworkRuntimeManifest(prepared.manifest);
  const first = prepared.ownedStagingRoots[0]!;
  if (typeof first !== "string" || !isAbsolute(first)) throw new Error("invalid staging root");
  const repositoryRoot = resolve(first, "../../../../../../..");
  const workRoot = join(repositoryRoot, ".native-e2e/framework-work");
  const staging = join(workRoot, ".staging", prepared.ownershipId);
  const name = platformName(manifest.platform);
  const stagePlatform = join(staging, name);
  const finalPlatform = join(workRoot, name);
  const finalManifest = join(repositoryRoot, ".native-e2e/framework-runtime", `${name}.json`);
  const stagedManifest = join(staging, "runtime.json");
  const backupPlatform = join(workRoot, `.backup-${name}`);
  const backupManifest = join(dirname(finalManifest), `.backup-${name}.json`);
  const failedPlatform = join(workRoot, `.failed-${prepared.ownershipId}`);
  const roots = manifest.toolchains.flatMap(toolchain => toolchain.frameworks.map(framework => join(stagePlatform, toolchain.family, framework.frameworkId)));
  if (roots.some((root, index) => root !== prepared.ownedStagingRoots[index])) throw new Error("staging coordinates changed");
  const release = await acquireFrameworkRuntimeLock(repositoryRoot, manifest.platform);
  let oldManifest: FrameworkRuntimeManifest | undefined;
  let oldOwner: string | undefined;
  let manifestBackedUp = false;
  let workBackedUp = false;
  let installedWork = false;
  let installedIdentity: { dev: number; ino: number } | undefined;
  let installedManifest = false;
  let ownedStage = false;
  let committed = false;
  try {
    await validateStage(staging, manifest, prepared.ownershipId, false);
    ownedStage = true;
    if (await exists(backupPlatform) || await exists(backupManifest) || await exists(failedPlatform)) throw new Error("unknown backup or quarantine");
    const contractSha256 = digest(await fs.readFile(join(repositoryRoot, "testdata/framework-matrix/contract.json")));
    if (manifest.contractSha256 !== contractSha256) throw new Error("contract changed");
    const hasManifest = await exists(finalManifest);
    if (hasManifest !== await exists(finalPlatform)) throw new Error("incomplete old runtime");
    if (hasManifest) {
      await directFile(finalManifest);
      oldManifest = parseFrameworkRuntimeManifest(await fs.readFile(finalManifest), manifest.platform, contractSha256);
      oldOwner = await platformOwner(finalPlatform, oldManifest);
      await validatePlatform(finalPlatform, oldManifest, oldOwner, true);
    }
    const bytes = Buffer.from(`${JSON.stringify(manifest, null, 2)}\n`);
    await validateStage(staging, manifest, prepared.ownershipId, false);
    await fs.writeFile(stagedManifest, bytes, { flag: "wx", mode: 0o600 });
    if (oldManifest !== undefined) {
      await validatePlatform(finalPlatform, oldManifest, oldOwner!, true);
      await directFile(finalManifest);
      await fs.rename(finalManifest, backupManifest);
      manifestBackedUp = true;
      await validatePlatform(finalPlatform, oldManifest, oldOwner!, true);
      await fs.rename(finalPlatform, backupPlatform);
      workBackedUp = true;
    }
    await validateStage(staging, manifest, prepared.ownershipId, true);
    installedIdentity = await fs.lstat(stagePlatform);
    await fs.rename(stagePlatform, finalPlatform);
    installedWork = true;
    await directFile(stagedManifest);
    await validatePlatform(finalPlatform, manifest, prepared.ownershipId, true);
    await fs.rename(stagedManifest, finalManifest);
    installedManifest = true;
    await directFile(finalManifest);
    const reopened = parseFrameworkRuntimeManifest(await fs.readFile(finalManifest), manifest.platform, contractSha256);
    if (JSON.stringify(reopened) !== JSON.stringify(manifest)) throw new Error("published manifest changed");
    await validatePlatform(finalPlatform, reopened, prepared.ownershipId, true);
    committed = true;
    // The new pair is now validated. Backup cleanup never triggers destructive rollback.
    if (workBackedUp) {
      await validatePlatform(backupPlatform, oldManifest!, oldOwner!, false);
      await fs.rm(backupPlatform, { recursive: true });
    }
    if (manifestBackedUp) {
      await directFile(backupManifest);
      const backup = parseFrameworkRuntimeManifest(await fs.readFile(backupManifest), manifest.platform, contractSha256);
      if (JSON.stringify(backup) !== JSON.stringify(oldManifest)) throw new Error("backup identity changed");
      await fs.unlink(backupManifest);
    }
    await directDirectory(staging);
    await fs.rmdir(staging);
    ownedStage = false;
    return { schemaVersion: 1, platform: manifest.platform, candidateCommit: manifest.candidateCommit,
      toolchainFamilies: manifest.toolchains.map(({ family }) => family), manifestSha256: digest(bytes) };
  } catch (error) {
    if (!committed) {
      if (installedManifest) { await directFile(finalManifest); await fs.unlink(finalManifest); }
      if (installedWork && await exists(finalPlatform)) {
        // Do not revalidate the structure that just failed validation. The
        // directory identity captured before our rename proves which tree we
        // installed; quarantine it intact, including any unknown new contents.
        await directDirectory(finalPlatform);
        const current = await fs.lstat(finalPlatform);
        if (current.dev !== installedIdentity!.dev || current.ino !== installedIdentity!.ino || await exists(failedPlatform)) {
          throw new Error("failed runtime ownership changed");
        }
        await fs.rename(finalPlatform, failedPlatform);
      }
      if (workBackedUp) {
        await validatePlatform(backupPlatform, oldManifest!, oldOwner!, false);
        await fs.rename(backupPlatform, finalPlatform);
      }
      if (manifestBackedUp) {
        await directFile(backupManifest);
        const backup = parseFrameworkRuntimeManifest(await fs.readFile(backupManifest), manifest.platform, oldManifest!.contractSha256);
        if (JSON.stringify(backup) !== JSON.stringify(oldManifest)) throw new Error("backup identity changed");
        await fs.rename(backupManifest, finalManifest);
      }
      if (manifestBackedUp || workBackedUp) {
        const restored = parseFrameworkRuntimeManifest(await fs.readFile(finalManifest), manifest.platform, oldManifest!.contractSha256);
        if (JSON.stringify(restored) !== JSON.stringify(oldManifest)) throw new Error("restored runtime identity changed");
        await validatePlatform(finalPlatform, restored, oldOwner!, true);
      }
      if (ownedStage) await cleanupStage(staging, manifest, prepared.ownershipId);
    }
    throw error;
  } finally { await release(); }
}

async function platformOwner(root: string, manifest: FrameworkRuntimeManifest): Promise<string> {
  const path = join(root, manifest.toolchains[0]!.family, "cpputest", "owner.json");
  await directFile(path);
  const owner = JSON.parse(await fs.readFile(path, "utf8"));
  if (!uuid.test(owner.invocationId)) throw new Error("unknown runtime owner");
  return owner.invocationId;
}

async function validatePlatform(root: string, manifest: FrameworkRuntimeManifest, owner: string, hashes: boolean): Promise<void> {
  await directDirectory(root);
  await exactEntries(root, manifest.toolchains.map(({ family }) => family));
  for (const toolchain of manifest.toolchains) {
    const familyRoot = join(root, toolchain.family);
    await directDirectory(familyRoot);
    await exactEntries(familyRoot, ["cpputest", "unity"]);
    for (const framework of toolchain.frameworks) {
      const stage = join(familyRoot, framework.frameworkId);
      await validateOwnedFrameworkStage(stage, owner);
      const record = JSON.parse(await fs.readFile(join(stage, "owner.json"), "utf8"));
      if (record.candidate !== manifest.candidateCommit || record.platform !== manifest.platform) throw new Error("stage ownership changed");
      await exactEntries(stage, ["owner.json", "service", "workspace"]);
      await directDirectory(join(stage, "workspace"));
      await directDirectory(join(stage, "service"));
      await directTree(stage);
      if (hashes) {
        if (await hashCompiledFrameworkExecutable(dirname(root), manifest.platform, toolchain.family, framework.frameworkId) !== framework.evidence.executableArtifactSha256) throw new Error("compiled evidence changed");
      }
    }
  }
}

async function validateStage(staging: string, manifest: FrameworkRuntimeManifest, owner: string, hasManifest: boolean): Promise<void> {
  await directDirectory(staging);
  await exactEntries(staging, [platformName(manifest.platform), ...(hasManifest ? ["runtime.json"] : [])]);
  await validatePlatform(join(staging, platformName(manifest.platform)), manifest, owner, true);
  if (hasManifest) await directFile(join(staging, "runtime.json"));
}

async function cleanupStage(staging: string, manifest: FrameworkRuntimeManifest, owner: string): Promise<void> {
  await directDirectory(staging);
  const entries = await fs.readdir(staging);
  if (entries.some(entry => ![platformName(manifest.platform), "runtime.json"].includes(entry))) throw new Error("unknown staging contents");
  const platform = join(staging, platformName(manifest.platform));
  if (await exists(platform)) {
    await validatePlatform(platform, manifest, owner, false);
    await fs.rm(platform, { recursive: true });
  }
  const manifestPath = join(staging, "runtime.json");
  if (await exists(manifestPath)) { await directFile(manifestPath); await fs.unlink(manifestPath); }
  await fs.rmdir(staging);
}

async function directTree(root: string): Promise<void> {
  await directDirectory(root);
  for (const entry of await fs.readdir(root, { withFileTypes: true })) {
    const path = join(root, entry.name);
    if (entry.isDirectory()) await directTree(path);
    else await directFile(path);
  }
}
async function exactEntries(root: string, expected: string[]): Promise<void> {
  if ((await fs.readdir(root)).sort().join() !== [...expected].sort().join()) throw new Error("unknown runtime contents");
}
async function directDirectory(path: string): Promise<void> {
  const info = await fs.lstat(path);
  if (!info.isDirectory() || info.isSymbolicLink() || await fs.realpath(path) !== resolve(path)) throw new Error("unsafe runtime directory");
}
async function directFile(path: string): Promise<void> {
  const info = await fs.lstat(path);
  if (!info.isFile() || info.isSymbolicLink() || await fs.realpath(path) !== resolve(path)) throw new Error("unsafe runtime file");
}
async function directories(root: string, destination: string): Promise<void> {
  let current = root;
  for (const component of relative(root, destination).split(sep)) {
    if (!component || component === "..") throw new Error("invalid runtime coordinate");
    current = join(current, component);
    if (!await exists(current)) await fs.mkdir(current, { mode: 0o700 });
    await directDirectory(current);
  }
}
async function exists(path: string): Promise<boolean> {
  try { await fs.lstat(path); return true; }
  catch (error) { if ((error as NodeJS.ErrnoException).code === "ENOENT") return false; throw error; }
}
function sanitized(message: string): Error { const error = new Error(message); error.stack = message; return error; }
