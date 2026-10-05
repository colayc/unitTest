import { cp, lstat, mkdir, mkdtemp, rm, rename } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), "../..");

function platformKey(arguments_) {
  if (arguments_.length === 0) return process.platform === "win32" ? "windows-x64" : "linux-x64";
  if (arguments_.length === 2 && arguments_[0] === "--platform" && ["linux-x64", "windows-x64"].includes(arguments_[1])) {
    return arguments_[1];
  }
  throw new Error("usage: node stage-production-bundles.mjs [--platform <linux-x64|windows-x64>]");
}

async function requireDirectDirectory(path, label) {
  const info = await lstat(path).catch(() => undefined);
  if (info === undefined || !info.isDirectory() || info.isSymbolicLink()) {
    throw new Error(`${label} is not a prepared direct directory`);
  }
}

export async function stageProductionBundles({ root = repositoryRoot, key = platformKey([]) } = {}) {
  const cmake = join(root, ".bundled-tools", "cmake");
  const coverage = join(root, ".superpowers", "runtime", "coverage-bundle", key);
  const testgen = join(root, ".superpowers", "cache", "testgen-bundle", "22.1.8", key);
  await Promise.all([
    requireDirectDirectory(cmake, "CMake bundle"),
    requireDirectDirectory(coverage, "coverage bundle"),
    requireDirectDirectory(testgen, "test-generation bundle"),
  ]);

  const stagedRoot = join(root, ".superpowers", "runtime", "product-bundles", key);
  await mkdir(dirname(stagedRoot), { recursive: true });
  const temporaryRoot = await mkdtemp(join(dirname(stagedRoot), `.stage-${process.pid}-`));
  try {
    const bundleRoot = join(temporaryRoot, "bundles");
    await mkdir(bundleRoot, { recursive: true });
    await Promise.all([
      cp(cmake, join(bundleRoot, "cmake"), { recursive: true, force: false, errorOnExist: true }),
      cp(coverage, join(bundleRoot, "coverage"), { recursive: true, force: false, errorOnExist: true }),
      cp(testgen, join(bundleRoot, "testgen"), { recursive: true, force: false, errorOnExist: true }),
    ]);
    await rm(stagedRoot, { recursive: true, force: true });
    await rename(temporaryRoot, stagedRoot);
    return stagedRoot;
  } catch (error) {
    await rm(temporaryRoot, { recursive: true, force: true });
    throw error;
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  stageProductionBundles({ key: platformKey(process.argv.slice(2)) })
    .then((root) => process.stdout.write(`${root}\n`))
    .catch((error) => {
      process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
      process.exitCode = 1;
    });
}
