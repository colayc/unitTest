import { createHash } from "node:crypto";
import { lstat, mkdir, open, readdir, writeFile } from "node:fs/promises";
import { basename, dirname, join, resolve } from "node:path";

// This is an allowlist, not a manifest-driven CMake execution entry point.
// Digest pins authenticate the fixed commands as well as the source inventory.
const inventory = {
  "schemaVersion": 1,
  "recipes": [
    {
      "id": "windows-cpputest-4.0",
      "framework": "cpputest",
      "version": "4.0",
      "target": "CppUTest",
      "frameworkRoot": "../cpputest",
      "sources": [
        "src/CppUTest/CommandLineArguments.cpp",
        "src/CppUTest/MemoryLeakWarningPlugin.cpp",
        "src/CppUTest/TestHarness_c.cpp",
        "src/CppUTest/TestRegistry.cpp",
        "src/CppUTest/CommandLineTestRunner.cpp",
        "src/CppUTest/SimpleString.cpp",
        "src/CppUTest/SimpleStringInternalCache.cpp",
        "src/CppUTest/TestMemoryAllocator.cpp",
        "src/CppUTest/TestResult.cpp",
        "src/CppUTest/JUnitTestOutput.cpp",
        "src/CppUTest/TeamCityTestOutput.cpp",
        "src/CppUTest/TestFailure.cpp",
        "src/CppUTest/TestOutput.cpp",
        "src/CppUTest/MemoryLeakDetector.cpp",
        "src/CppUTest/TestFilter.cpp",
        "src/CppUTest/TestPlugin.cpp",
        "src/CppUTest/TestTestingFixture.cpp",
        "src/CppUTest/SimpleMutex.cpp",
        "src/CppUTest/Utest.cpp",
        "src/Platforms/VisualCpp/UtestPlatform.cpp"
      ],
      "defines": [
        "CPPUTEST_USE_OWN_CONFIGURATION=1",
        "CPPUTEST_MEM_LEAK_DETECTION_DISABLED=1"
      ],
      "systemLibraryVariable": "UNIT_TEST_IDE_WINMM_LIBRARY",
      "path": "sdk/cmake/framework-recipes/windows-cpputest-4.0.cmake",
      "sha256": "1d0ca48a63a9d16bd645c5572d29cad99466b8462e8635851f3c9291a1138406"
    },
    {
      "id": "windows-unity-2.6.1",
      "framework": "unity",
      "version": "2.6.1",
      "target": "unity",
      "frameworkRoot": "../unity",
      "sources": [
        "src/unity.c"
      ],
      "defines": [],
      "systemLibraryVariable": null,
      "path": "sdk/cmake/framework-recipes/windows-unity-2.6.1.cmake",
      "sha256": "c8da137cfd278d68217d0ced92b4a70c07332965c5451331693b082abb98abba"
    }
  ]
};
const resourceDirectory = "sdk/cmake/framework-recipes";
const resourceNames = ["README.md", "manifest.json", "windows-cpputest-4.0.cmake", "windows-unity-2.6.1.cmake"];
const resourceLimit = 512 * 1024;
const fail = () => new Error("framework recipe resource or inventory is invalid");

function validateExact(actual, expected) {
  if (Array.isArray(expected)) {
    if (!Array.isArray(actual) || actual.length !== expected.length) throw fail();
    expected.forEach((value, index) => validateExact(actual[index], value));
  } else if (expected !== null && typeof expected === "object") {
    if (actual === null || typeof actual !== "object" || Array.isArray(actual) ||
        JSON.stringify(Object.keys(actual).sort()) !== JSON.stringify(Object.keys(expected).sort())) throw fail();
    for (const key of Object.keys(expected)) validateExact(actual[key], expected[key]);
  } else if (actual !== expected) throw fail();
}

async function assertNoAliases(path) {
  const components = [];
  for (let current = resolve(path); dirname(current) !== current; current = dirname(current)) components.push(current);
  for (const component of components.reverse()) {
    try {
      if ((await lstat(component)).isSymbolicLink()) throw fail();
    } catch (error) {
      if (error.code !== "ENOENT") throw error;
    }
  }
}

async function readResource(root, relativePath) {
  const path = join(root, relativePath);
  await assertNoAliases(path);
  const stat = await lstat(path);
  if (!stat.isFile() || stat.isSymbolicLink() || stat.size > resourceLimit) throw fail();
  const handle = await open(path, "r");
  try {
    const opened = await handle.stat();
    if (!opened.isFile() || opened.dev !== stat.dev || opened.ino !== stat.ino || opened.size > resourceLimit) throw fail();
    const bytes = await handle.readFile();
    if (bytes.length > resourceLimit) throw fail();
    return bytes;
  } finally { await handle.close(); }
}

async function authenticatedRecipes(repositoryRoot) {
  const root = resolve(repositoryRoot);
  const directory = join(root, resourceDirectory);
  await assertNoAliases(directory);
  if (JSON.stringify((await readdir(directory)).sort()) !== JSON.stringify(resourceNames)) throw fail();
  const manifestBytes = await readResource(root, join(resourceDirectory, "manifest.json"));
  const manifest = JSON.parse(manifestBytes.toString("utf8"));
  // Canonical encoding also rejects duplicate keys; walk every nested key/value.
  if (!manifestBytes.equals(Buffer.from(`${JSON.stringify(manifest, null, 2)}\n`))) throw fail();
  validateExact(manifest, inventory);
  const recipes = [];
  for (const recipe of manifest.recipes) {
    const bytes = await readResource(root, recipe.path);
    if (createHash("sha256").update(bytes).digest("hex") !== recipe.sha256) throw fail();
    recipes.push({ name: basename(recipe.path), bytes });
  }
  return recipes;
}

export async function verifyFrameworkRecipes(repositoryRoot) {
  await authenticatedRecipes(repositoryRoot);
}

export async function copyFrameworkRecipeInputs(repositoryRoot, workspaceRoot) {
  // Read and authenticate once: do not re-read unchecked bytes for publication.
  const recipes = await authenticatedRecipes(repositoryRoot);
  const workspace = resolve(workspaceRoot);
  await assertNoAliases(workspace);
  if (!(await lstat(workspace)).isDirectory()) throw fail();
  const inputs = join(workspace, ".unit-test-ide/inputs");
  await assertNoAliases(inputs);
  await mkdir(inputs, { recursive: true });
  await assertNoAliases(inputs);
  const destination = join(inputs, "framework-recipes");
  await mkdir(destination); // exclusive: even an existing empty directory fails
  for (const recipe of recipes) {
    await assertNoAliases(destination);
    await writeFile(join(destination, recipe.name), recipe.bytes, { flag: "wx" });
  }
}
