import { spawnSync } from "node:child_process";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";

const check = process.argv.includes("--check");
const root = resolve(import.meta.dirname, "../..");
const schema = join(root, "packages/protocol-schema/schema/v1/capabilities.schema.json");
const quicktype = join(root, "node_modules", "quicktype", "dist", "index.js");
const targets = [
  {
    output: join(root, "packages/protocol-models/src/generated/capabilities.ts"),
    args: ["--lang", "typescript", "--just-types", "--top-level", "Capabilities"],
    format: null
  },
  {
    output: join(root, "apps/test-service/internal/protocolmodel/generated.go"),
    args: ["--lang", "go", "--just-types", "--package", "protocolmodel", "--top-level", "Capabilities"],
    format: "gofmt",
    packageName: "protocolmodel"
  }
];
const temp = await mkdtemp(join(tmpdir(), "unit-test-ide-protocol-"));

try {
  for (const [index, target] of targets.entries()) {
    const output = check ? join(temp, String(index)) : target.output;
    await mkdir(dirname(output), { recursive: true });
    const result = spawnSync(process.execPath, [quicktype, "--src-lang", "schema", "--src", schema, ...target.args, "--out", output], { cwd: root, stdio: "inherit" });
    if (result.status !== 0) throw new Error(`quicktype failed with status ${result.status ?? 1}`);
    if (target.packageName) {
      await writeFile(output, `package ${target.packageName}\n\n${await readFile(output, "utf8")}`);
    }
    if (target.format === "gofmt") {
      const formatted = spawnSync("gofmt", ["-w", output], { cwd: root, stdio: "inherit" });
      if (formatted.status !== 0) throw new Error(`gofmt failed with status ${formatted.status ?? 1}`);
    }
    if (check) {
      const normalize = (value) => value.replaceAll("\r\n", "\n");
      if (normalize(await readFile(output, "utf8")) !== normalize(await readFile(target.output, "utf8"))) {
        throw new Error(`Generated file is stale: ${target.output}`);
      }
    }
  }
} finally {
  await rm(temp, { recursive: true, force: true });
}
