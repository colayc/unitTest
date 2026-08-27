import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { chmod, lstat, mkdtemp, mkdir, readdir, rename, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { validateCodeOssRuntime } from "./code-oss-runtime.mjs";

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

async function withTemporaryRoot(t, run) {
  const root = await mkdtemp(join(tmpdir(), "code-oss-runtime-"));
  t.after(async () => {
    await rm(root, { recursive: true, force: true });
  });
  await run(root);
}

async function writeFixtureFile(root, relativePath, value) {
  const filePath = join(root, ...relativePath.split("/"));
  await mkdir(dirname(filePath), { recursive: true });
  await writeFile(filePath, value);
  return filePath;
}

async function createRuntimeFixture(root, platform) {
  const runtimeRoot = join(root, "runtime");
  const launcherRelativePath = platform === "windows" ? "Code - OSS.exe" : "code-oss";
  const launcherPath = await writeFixtureFile(runtimeRoot, launcherRelativePath, "code oss launcher\n");
  if (platform === "linux") await chmod(launcherPath, 0o755);
  await writeFixtureFile(runtimeRoot, "resources/app/product.json", JSON.stringify({
    applicationName: "code-oss",
    licenseName: "MIT",
    nameShort: "Code - OSS",
  }));
  await writeFixtureFile(runtimeRoot, "resources/app/package.json", JSON.stringify({ name: "code-oss" }));
  await writeFixtureFile(runtimeRoot, "locales/en-US.pak", "locale\n");
  await writeFixtureFile(runtimeRoot, "runtime.dat", "runtime dependency\n");
  return { runtimeRoot, launcherPath, launcherSha256: sha256("code oss launcher\n") };
}

async function expectFailure(run, code) {
  await assert.rejects(run, (error) => {
    assert.equal(error?.code, code);
    assert.match(error?.message ?? "", new RegExp(`^${code}:`, "u"));
    return true;
  });
}

test("validateCodeOssRuntime accepts a complete digest-pinned Windows runtime", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createRuntimeFixture(root, "windows");
    const result = await validateCodeOssRuntime({
      root: fixture.runtimeRoot,
      platform: "windows",
      expectedLauncherSha256: fixture.launcherSha256,
    });
    assert.equal(result.launcherRelativePath, "Code - OSS.exe");
    assert.equal(result.launcherSha256, fixture.launcherSha256);
    assert.deepEqual(result.productIdentity, {
      applicationName: "code-oss",
      licenseName: "MIT",
      nameShort: "Code - OSS",
    });
  });
});

const linuxOnly = process.platform === "linux" ? test : test.skip;

linuxOnly("validateCodeOssRuntime accepts an executable Linux runtime", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createRuntimeFixture(root, "linux");
    const result = await validateCodeOssRuntime({
      root: fixture.runtimeRoot,
      platform: "linux",
      expectedLauncherSha256: fixture.launcherSha256,
    });
    assert.equal(result.launcherRelativePath, "code-oss");
  });
});

test("validator rejects a launcher file in place of the runtime root", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createRuntimeFixture(root, "windows");
    await expectFailure(() => validateCodeOssRuntime({ root: fixture.launcherPath, platform: "windows", expectedLauncherSha256: fixture.launcherSha256 }), "RELEASE_INPUT_INVALID");
  });
});

test("validator rejects a missing fixed platform launcher", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createRuntimeFixture(root, "windows");
    await rm(fixture.launcherPath);
    await expectFailure(() => validateCodeOssRuntime({ root: fixture.runtimeRoot, platform: "windows", expectedLauncherSha256: fixture.launcherSha256 }), "RELEASE_INPUT_MISSING");
  });
});

test("validator rejects wrong-case required paths", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    await t.test("launcher", async () => {
      const fixture = await createRuntimeFixture(root, "windows");
      await rename(fixture.launcherPath, join(fixture.runtimeRoot, "code - oss.exe"));
      await expectFailure(() => validateCodeOssRuntime({ root: fixture.runtimeRoot, platform: "windows", expectedLauncherSha256: fixture.launcherSha256 }), "RELEASE_INPUT_MISSING");
    });
    await t.test("product metadata", async () => {
      const fixture = await createRuntimeFixture(join(root, "product"), "windows");
      await rename(join(fixture.runtimeRoot, "resources", "app", "product.json"), join(fixture.runtimeRoot, "resources", "app", "Product.json"));
      await expectFailure(() => validateCodeOssRuntime({ root: fixture.runtimeRoot, platform: "windows", expectedLauncherSha256: fixture.launcherSha256 }), "RELEASE_INPUT_MISSING");
    });
  });
});

test("validator rejects missing product and package metadata", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    for (const [label, relativePath] of [["product", "resources/app/product.json"], ["package", "resources/app/package.json"]]) {
      await t.test(label, async () => {
        const fixture = await createRuntimeFixture(join(root, label), "windows");
        await rm(join(fixture.runtimeRoot, ...relativePath.split("/")));
        await expectFailure(() => validateCodeOssRuntime({ root: fixture.runtimeRoot, platform: "windows", expectedLauncherSha256: fixture.launcherSha256 }), "RELEASE_INPUT_MISSING");
      });
    }
  });
});

test("validator rejects a non-Code-OSS product identity", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createRuntimeFixture(root, "windows");
    await writeFile(join(fixture.runtimeRoot, "resources", "app", "product.json"), JSON.stringify({ applicationName: "code", licenseName: "MIT", nameShort: "Code - OSS" }));
    await expectFailure(() => validateCodeOssRuntime({ root: fixture.runtimeRoot, platform: "windows", expectedLauncherSha256: fixture.launcherSha256 }), "RELEASE_INPUT_INVALID");
  });
});

test("validator rejects malformed and mismatched launcher digests", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createRuntimeFixture(root, "windows");
    await expectFailure(() => validateCodeOssRuntime({ root: fixture.runtimeRoot, platform: "windows", expectedLauncherSha256: "not-a-digest" }), "RELEASE_INPUT_INVALID");
    await expectFailure(() => validateCodeOssRuntime({ root: fixture.runtimeRoot, platform: "windows", expectedLauncherSha256: "0".repeat(64) }), "RELEASE_INPUT_DIGEST_MISMATCH");
  });
});

linuxOnly("validator rejects a non-executable Linux launcher", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createRuntimeFixture(root, "linux");
    await chmod(fixture.launcherPath, 0o644);
    await expectFailure(() => validateCodeOssRuntime({ root: fixture.runtimeRoot, platform: "linux", expectedLauncherSha256: fixture.launcherSha256 }), "RELEASE_INPUT_INVALID");
  });
});

test("validator rejects a descendant symbolic link", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createRuntimeFixture(root, "windows");
    const outside = await writeFixtureFile(root, "outside.txt", "outside bytes\n");
    const linked = join(fixture.runtimeRoot, "runtime.dat");
    await rm(linked);
    try {
      await symlink(outside, linked, "file");
    } catch (error) {
      t.skip(`symbolic links unavailable: ${error.code}`);
      return;
    }
    await expectFailure(() => validateCodeOssRuntime({ root: fixture.runtimeRoot, platform: "windows", expectedLauncherSha256: fixture.launcherSha256 }), "RELEASE_INPUT_INVALID");
  });
});

test("validator rejects a root link", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createRuntimeFixture(join(root, "target"), "windows");
    const linkedRoot = join(root, "linked-runtime");
    try {
      await symlink(fixture.runtimeRoot, linkedRoot, "junction");
    } catch (error) {
      t.skip(`directory links unavailable: ${error.code}`);
      return;
    }
    await expectFailure(() => validateCodeOssRuntime({ root: linkedRoot, platform: "windows", expectedLauncherSha256: fixture.launcherSha256 }), "RELEASE_INPUT_INVALID");
  });
});

test("validator rejects a descendant directory junction", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createRuntimeFixture(root, "windows");
    const outsideDirectory = join(root, "outside-directory");
    await mkdir(outsideDirectory);
    const linkedDirectory = join(fixture.runtimeRoot, "linked-directory");
    try {
      await symlink(outsideDirectory, linkedDirectory, "junction");
    } catch (error) {
      t.skip(`directory junctions unavailable: ${error.code}`);
      return;
    }
    await expectFailure(() => validateCodeOssRuntime({ root: fixture.runtimeRoot, platform: "windows", expectedLauncherSha256: fixture.launcherSha256 }), "RELEASE_INPUT_INVALID");
  });
});

const windowsOnly = process.platform === "win32" ? test : test.skip;

windowsOnly("validator rejects a non-symlink reparse point inside the runtime", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createRuntimeFixture(root, "windows");
    const targetDirectory = join(fixture.runtimeRoot, "real-directory");
    await mkdir(targetDirectory);
    const reparseDirectory = join(fixture.runtimeRoot, "reparse-directory");
    try {
      await symlink(targetDirectory, reparseDirectory, "junction");
    } catch (error) {
      t.skip(`non-symlink reparse points unavailable: ${error.code}`);
      return;
    }
    if ((await lstat(reparseDirectory)).isSymbolicLink()) {
      t.skip("host exposes junctions as symbolic links");
      return;
    }
    await expectFailure(() => validateCodeOssRuntime({ root: fixture.runtimeRoot, platform: "windows", expectedLauncherSha256: fixture.launcherSha256 }), "RELEASE_INPUT_INVALID");
  });
});

const posixOnly = process.platform === "win32" ? test.skip : test;

posixOnly("validator rejects special entries and non-portable entry names", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    await t.test("FIFO", async (subtest) => {
      const fixture = await createRuntimeFixture(join(root, "fifo"), "windows");
      const fifoPath = join(fixture.runtimeRoot, "runtime.fifo");
      const result = spawnSync("mkfifo", [fifoPath]);
      if (result.status !== 0) {
        subtest.skip("FIFO creation unavailable");
        return;
      }
      await expectFailure(() => validateCodeOssRuntime({ root: fixture.runtimeRoot, platform: "windows", expectedLauncherSha256: fixture.launcherSha256 }), "RELEASE_INPUT_INVALID");
    });
    for (const [label, relativePath] of [["colon", "unsafe:name"], ["backslash", "unsafe\\name"]]) {
      await t.test(label, async (subtest) => {
        const fixture = await createRuntimeFixture(join(root, label), "windows");
        try {
          await writeFixtureFile(fixture.runtimeRoot, relativePath, "unsafe\n");
        } catch (error) {
          subtest.skip(`filesystem cannot represent ${label}: ${error.code}`);
          return;
        }
        await expectFailure(() => validateCodeOssRuntime({ root: fixture.runtimeRoot, platform: "windows", expectedLauncherSha256: fixture.launcherSha256 }), "RELEASE_INPUT_INVALID");
      });
    }
  });
});

const portableNameCases = [
  ["reserved device basename", "con.txt"],
  ["reserved device basename with numeric suffix", "LPT9"],
  ["trailing dot component", "runtime."],
  ["trailing space component", "runtime "],
];

test("validator rejects Windows-reserved and trailing portable path components", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    for (const [label, relativePath] of portableNameCases) {
      await t.test(label, async (subtest) => {
        const fixture = await createRuntimeFixture(join(root, label), "windows");
        try {
          await writeFixtureFile(fixture.runtimeRoot, relativePath, "unsafe\n");
          await lstat(join(fixture.runtimeRoot, relativePath));
        } catch (error) {
          subtest.skip(`filesystem cannot represent ${label}: ${error.code}`);
          return;
        }
        await expectFailure(() => validateCodeOssRuntime({ root: fixture.runtimeRoot, platform: "windows", expectedLauncherSha256: fixture.launcherSha256 }), "RELEASE_INPUT_INVALID");
      });
    }
  });
});

test("validator rejects case-insensitive runtime path aliases", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createRuntimeFixture(root, "windows");
    await writeFixtureFile(fixture.runtimeRoot, "locales/EN-us.pak", "alias locale\n");
    const names = (await readdir(join(fixture.runtimeRoot, "locales"))).sort();
    if (!names.includes("en-US.pak") || !names.includes("EN-us.pak")) {
      t.skip("filesystem cannot represent case-insensitive aliases");
      return;
    }
    await expectFailure(() => validateCodeOssRuntime({ root: fixture.runtimeRoot, platform: "windows", expectedLauncherSha256: fixture.launcherSha256 }), "RELEASE_INPUT_INVALID");
  });
});

test("validator CLI emits closed path-free JSON", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createRuntimeFixture(root, "windows");
    const script = fileURLToPath(new URL("./code-oss-runtime.mjs", import.meta.url));
    const result = spawnSync(process.execPath, [script, "--platform", "windows", "--root", fixture.runtimeRoot, "--launcher-sha256", fixture.launcherSha256], { encoding: "utf8" });
    assert.equal(result.status, 0, result.stderr);
    const output = JSON.parse(result.stdout);
    assert.deepEqual(Object.keys(output).sort(), ["applicationName", "launcherRelativePath", "launcherSha256", "licenseName", "nameShort", "platform", "schemaVersion"]);
    assert.equal(JSON.stringify(output).includes(root), false);
  });
});

test("validator CLI failure is closed and path-free", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const script = fileURLToPath(new URL("./code-oss-runtime.mjs", import.meta.url));
    const missingRoot = join(root, "missing-runtime");
    const result = spawnSync(process.execPath, [script, "--platform", "windows", "--root", missingRoot, "--launcher-sha256", "0".repeat(64)], { encoding: "utf8" });
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /^RELEASE_INPUT_MISSING:/u);
    assert.equal(result.stderr.includes(root), false);
  });
});

test("validator catches launcher mutation on a second validation", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createRuntimeFixture(root, "windows");
    await validateCodeOssRuntime({ root: fixture.runtimeRoot, platform: "windows", expectedLauncherSha256: fixture.launcherSha256 });
    await writeFile(fixture.launcherPath, "mutated launcher\n");
    await expectFailure(() => validateCodeOssRuntime({ root: fixture.runtimeRoot, platform: "windows", expectedLauncherSha256: fixture.launcherSha256 }), "RELEASE_INPUT_DIGEST_MISMATCH");
  });
});
