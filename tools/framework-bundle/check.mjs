import { lstat, readFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { readCMockGeneration, CMOCK_PROVENANCE_PATHS } from "./cmock-provenance.mjs";
import { frameworkFailure, readFrameworkManifest } from "./manifest.mjs";
import { verifyPreparedFrameworkBundle } from "./prepare.mjs";

const toolDirectory = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(toolDirectory, "..", "..");
function cacheFailure(message, cause) { return frameworkFailure("FRAMEWORK_CACHE_INVALID", message, cause); }
async function existingDirectory(path) {
  try {
    const stat = await lstat(path);
    if (!stat.isDirectory() || stat.isSymbolicLink()) throw cacheFailure("prepared framework cache has an unsafe type");
    return true;
  } catch (error) {
    if (error?.code === "ENOENT") return false;
    if (error?.code === "FRAMEWORK_CACHE_INVALID") throw error;
    throw cacheFailure("framework cache cannot be inspected", error);
  }
}
function validateLicenseInventory(value, manifest) {
  const expected = manifest.frameworks.map((framework) => ({ id: framework.id, version: framework.version, revision: framework.revision, spdx: framework.license.spdx, archiveLicensePath: framework.license.path, archiveLicenseSha256: framework.license.sha256 }));
  if (!value || value.schemaVersion !== 1 || !Array.isArray(value.dependencies) || value.dependencies.length !== expected.length) throw cacheFailure("license inventory is invalid");
  for (const [index, dependency] of value.dependencies.entries()) { const locked = expected[index]; if (!locked || Object.keys(dependency).length !== 7 || Object.entries(locked).some(([key, item]) => dependency[key] !== item) || typeof dependency.sourceLicenseUrl !== "string" || !dependency.sourceLicenseUrl.startsWith("https://github.com/")) throw cacheFailure("license inventory does not match the manifest"); }
}
export async function checkFrameworkBundle(options = {}) {
  const root = resolve(options.repositoryRoot ?? repositoryRoot);
  const manifestPath = options.manifestPath ?? join(root, "tools", "framework-bundle", "manifest.json");
  const { manifest, manifestSha256 } = await readFrameworkManifest(manifestPath);
  let licenses; try { licenses = JSON.parse(await readFile(options.licensesPath ?? join(root, "tools", "framework-bundle", "licenses", "dependencies.json"), "utf8")); } catch (error) { throw cacheFailure("license inventory cannot be read", error); }
  validateLicenseInventory(licenses, manifest);
  const provenance = await readCMockGeneration(options.provenancePath ?? join(root, CMOCK_PROVENANCE_PATHS.outputDirectory, "cmock-generation.json"), { root, manifest, manifestSha256 });
  const preparedRoot = options.preparedRoot ?? join(root, ".superpowers", "runtime", "framework-bundle", "v2", manifestSha256);
  if (await existingDirectory(preparedRoot)) { try { await verifyPreparedFrameworkBundle({ root: preparedRoot, manifest, manifestSha256 }); } catch (error) { throw cacheFailure("prepared framework cache is invalid", error); } }
  return { manifestSha256, cMockProvenanceSha256: provenance.cMockProvenanceSha256 };
}
if (process.argv[1] && resolve(process.argv[1]) === resolve(fileURLToPath(import.meta.url))) checkFrameworkBundle().then(({ manifestSha256, cMockProvenanceSha256 }) => process.stdout.write(`${JSON.stringify({ frameworkBundle: "verified", manifestSha256, cMockProvenanceSha256 })}\n`)).catch((error) => { process.stderr.write(`framework-bundle-check: ${error.message}\n`); process.exitCode = 1; });
