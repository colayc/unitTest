import { spawnSync } from "node:child_process";
import { mkdtemp, mkdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, relative, resolve } from "node:path";

const root = resolve(import.meta.dirname, "../..");
const quicktype = join(root, "node_modules", "quicktype", "dist", "index.js");
const schema = join(root, "packages", "coverage-schema", "schema", "v1", "coverage.schema.json");
const check = process.argv.includes("--check");
const targets = [
  {
    language: "typescript",
    output: "packages/coverage-models/src/generated/coverage-v1.ts",
    extra: ["--just-types", "--prefer-unions"]
  },
  {
    language: "go",
    output: "apps/test-service/internal/coveragemodel/v1/generated.go",
    extra: ["--package", "coveragemodelv1", "--just-types"]
  }
];

function normalized(value) {
  return value.replaceAll("\r\n", "\n").replace(/\n*$/, "\n");
}

function generate(target, output) {
  const result = spawnSync(process.execPath, [
    quicktype, "--quiet", "--src-lang", "schema", "--src", schema,
    "--lang", target.language, "--top-level", "CoverageDocumentV1", ...target.extra,
    "--out", output
  ], { cwd: root, stdio: "inherit" });
  if (result.status !== 0) throw new Error(`quicktype failed for ${target.output} with status ${result.status ?? 1}`);
}

async function writeNormalized(target, output) {
  let source = normalized(await readFile(output, "utf8"));
  if (target.language === "go") source = `package coveragemodelv1\n\n${source}`;
  await writeFile(output, normalized(source), "utf8");
}

const temporary = await mkdtemp(join(tmpdir(), "unit-test-ide-coverage-"));
const stagedPaths = [];
try {
  const generatedTargets = [];
  for (const [index, target] of targets.entries()) {
    const destination = join(root, target.output);
    const generated = join(temporary, String(index));
    await mkdir(dirname(generated), { recursive: true });
    generate(target, generated);
    await writeNormalized(target, generated);
    generatedTargets.push({
      destination,
      content: await readFile(generated),
      staged: `${destination}.tmp-${process.pid}-${index}`
    });
  }

  if (check) {
    const drifted = [];
    for (const { destination, content } of generatedTargets) {
      let committed;
      try { committed = await readFile(destination); } catch { committed = undefined; }
      if (!committed?.equals(content)) drifted.push(relative(root, destination).replaceAll("\\", "/"));
    }
    if (drifted.length) {
      for (const path of drifted) console.error(`Generated file is stale: ${path}`);
      process.exitCode = 1;
    }
  } else {
    for (const { destination, content, staged } of generatedTargets) {
      stagedPaths.push(staged);
      await mkdir(dirname(destination), { recursive: true });
      await writeFile(staged, content);
    }
    for (const { destination, staged } of generatedTargets) await rename(staged, destination);
  }
} finally {
  const cleanupErrors = [];
  for (const staged of stagedPaths) {
    try { await rm(staged, { force: true }); } catch (error) { cleanupErrors.push(error); }
  }
  try { await rm(temporary, { recursive: true, force: true }); } catch (error) { cleanupErrors.push(error); }
  if (cleanupErrors.length) throw new AggregateError(cleanupErrors, "Unable to remove coverage generator temporary files");
}
