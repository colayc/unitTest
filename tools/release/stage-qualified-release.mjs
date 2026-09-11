import { copyFile, lstat, mkdir, mkdtemp, readdir, rename, rm } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";

const destinationRecords = (input) => [
  [input.windowsPackage, `unit-test-ide-${input.version}.msix`],
  [input.windowsManifest, `unit-test-ide-${input.version}.windows-x64.release-manifest.json`],
  [input.windowsLicenseAudit, "license-audit-windows.json"],
  [input.linuxPackage, `unit-test-ide-${input.version}.AppImage`],
  [input.linuxPackageManifest, `unit-test-ide-${input.version}.AppImage.sha256.json`],
  [input.linuxManifest, `unit-test-ide-${input.version}.linux-x64.release-manifest.json`],
  [input.linuxLicenseAudit, "license-audit-linux.json"],
  [input.qualification, "release-qualification.json"],
];

const collated = (left, right) => left.localeCompare(right, "en");
const STAGING_ERROR_CODE = "RELEASE_QUALIFIED_STAGING_FAILED";

const stagingError = () => {
  const error = new Error("Qualified release staging failed");
  error.code = STAGING_ERROR_CODE;
  return error;
};

export async function stageQualifiedRelease(input) {
  let temporaryRoot;
  try {
    const finalRoot = resolve(input.outRoot);
    await mkdir(dirname(finalRoot), { recursive: true });
    if (await pathExists(finalRoot)) throw stagingError();

    temporaryRoot = await mkdtemp(`${finalRoot}.tmp-`);
    const records = destinationRecords(input);
    for (const [source, destination] of records) {
      await copyFile(source, join(temporaryRoot, destination));
    }

    const entries = await readdir(temporaryRoot, { withFileTypes: true });
    const files = entries.map(({ name }) => name).sort(collated);
    const expectedFiles = records.map(([, destination]) => destination).sort(collated);
    if (files.length !== expectedFiles.length || files.some((name, index) => name !== expectedFiles[index])) {
      throw new Error("Qualified release output has an unexpected file set");
    }
    if (entries.some((entry) => !entry.isFile() || entry.isSymbolicLink())) {
      throw new Error("Qualified release output contains a non-regular file");
    }

    await rename(temporaryRoot, finalRoot);
    return { outputRoot: finalRoot, files };
  } catch (error) {
    if (temporaryRoot) {
      try {
        await rm(temporaryRoot, { recursive: true, force: true });
      } catch {
        // Preserve the stable public error even if cleanup itself fails.
      }
    }
    if (error?.code === STAGING_ERROR_CODE && error.message === "Qualified release staging failed") throw error;
    throw stagingError();
  }
}

async function pathExists(path) {
  try {
    await lstat(path);
    return true;
  } catch (error) {
    if (error.code === "ENOENT") return false;
    throw error;
  }
}
