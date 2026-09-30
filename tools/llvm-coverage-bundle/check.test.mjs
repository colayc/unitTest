import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import test from "node:test";

import { checkBundle } from "./prepare.mjs";

const source = new URL("./", import.meta.url);

test("checkBundle is read-only and detects tool substitution", async () => {
  const manifest = JSON.parse(await readFile(new URL("manifest.json", source), "utf8"));
  await assert.rejects(
    checkBundle({ root: "C:/missing-approved-llvm-root", manifest }),
  );
});

test("checkBundle loads the reviewed source manifest when no manifest is passed", async () => {
  await assert.rejects(
    checkBundle({ root: join(tmpdir(), "missing-approved-llvm-root") }),
    /ENOENT|no such file/u,
  );
});
