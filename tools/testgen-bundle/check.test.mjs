import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtemp, mkdir, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";

import { verifyFileInventory } from "./check.mjs";

const contents = {
  "bin/clang.exe": "clang executable fixture\n",
  "lib/clang/22/include/stddef.h": "resource fixture\n",
  "licenses/LICENSE.TXT": "upstream license fixture\n",
};

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), "testgen-check-test-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  for (const [path, bytes] of Object.entries(contents)) {
    const absolute = join(root, ...path.split("/"));
    await mkdir(dirname(absolute), { recursive: true });
    await writeFile(absolute, bytes);
  }
  const platform = {
    target: "windows-x64",
    executable: "bin/clang.exe",
    resourceDir: "lib/clang/22",
    licenses: [{
      path: "licenses/LICENSE.TXT",
      url: "https://raw.githubusercontent.com/llvm/llvm-project/ca7933e47d3a3451d81e72ac174dcb5aa28b59d1/LICENSE.TXT",
      sha256: sha256(contents["licenses/LICENSE.TXT"]),
      size: Buffer.byteLength(contents["licenses/LICENSE.TXT"]),
    }],
    files: Object.entries(contents).filter(([path]) => !path.startsWith("licenses/")).map(([path, bytes]) => ({
      path,
      sha256: sha256(bytes),
      size: Buffer.byteLength(bytes),
    })),
  };
  return { root, platform };
}

test("offline inventory check detects executable substitution and extra files", async (t) => {
  const { root, platform } = await fixture(t);
  await verifyFileInventory(root, platform);
  await writeFile(join(root, "bin", "clang.exe"), "substitute executable\n");
  await assert.rejects(() => verifyFileInventory(root, platform), /digest|SHA-256/u);
  await writeFile(join(root, "bin", "clang.exe"), contents["bin/clang.exe"]);
  await writeFile(join(root, "unlisted.txt"), "extra\n");
  await assert.rejects(() => verifyFileInventory(root, platform), /extra|unlisted|inventory/u);
});

test("offline inventory check rejects missing licenses and links", async (t) => {
  const { root, platform } = await fixture(t);
  await rm(join(root, "licenses", "LICENSE.TXT"));
  await assert.rejects(() => verifyFileInventory(root, platform), /missing|license|inventory/u);
  await writeFile(join(root, "licenses", "LICENSE.TXT"), contents["licenses/LICENSE.TXT"]);
  const outsideRoot = await mkdtemp(join(tmpdir(), "testgen-link-target-"));
  t.after(() => rm(outsideRoot, { recursive: true, force: true }));
  const target = join(outsideRoot, "outside.txt");
  await writeFile(target, "outside\n");
  await rm(join(root, "lib", "clang", "22", "include", "stddef.h"));
  try {
    await symlink(target, join(root, "lib", "clang", "22", "include", "stddef.h"));
  } catch (error) {
    if (error?.code === "EPERM") return t.skip("host cannot create symlinks");
    throw error;
  }
  await assert.rejects(() => verifyFileInventory(root, platform), /symbolic|reparse|link/u);
  assert.equal(await readFile(target, "utf8"), "outside\n");
});
