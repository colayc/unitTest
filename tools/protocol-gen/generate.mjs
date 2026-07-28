import { spawnSync } from "node:child_process";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";

const check = process.argv.includes("--check");
const root = resolve(import.meta.dirname, "../..");
const quicktype = join(root, "node_modules", "quicktype", "dist", "index.js");
const models = [
  { directory: "v1", schema: "capabilities.schema.json", top: "Capabilities", ts: "capabilities.ts", go: "generated.go" },
  { directory: "v1.1", schema: "capabilities.schema.json", top: "CapabilitiesV11", ts: "capabilities-v1-1.ts", go: "capabilities_v11_generated.go" },
  { directory: "v1.1", schema: "task.schema.json", top: "TaskSnapshot", ts: "task.ts", go: "task_generated.go" },
  { directory: "v1.1", schema: "event.schema.json", top: "TaskEvent", ts: "event.ts", go: "event_generated.go" },
  { directory: "v1.1", schema: "artifact.schema.json", top: "ArtifactMetadata", ts: "artifact.ts", go: "artifact_generated.go" },
  { directory: "v1.2", schema: "capabilities.schema.json", top: "CapabilitiesV12", ts: "capabilities-v1-2.ts", go: "capabilities_v12_generated.go" },
  { directory: "v1.2", schema: "diagnostic.schema.json", top: "Diagnostic", ts: "diagnostic.ts", go: "diagnostic_generated.go" },
  { directory: "v1.2", schema: "workspace.schema.json", top: "WorkspaceSnapshot", ts: "workspace.ts", go: "workspace_generated.go" },
  { directory: "v1.2", schema: "workspace.schema.json", definition: "targetList", top: "TargetList", ts: "target-list.ts", go: "target_list_generated.go" },
  { directory: "v1.2", schema: "task.schema.json", top: "TaskSnapshotV12", ts: "task-v1-2.ts", go: "task_v12_generated.go" },
  { directory: "v1.2", schema: "event.schema.json", top: "TaskEventV12", ts: "event-v1-2.ts", go: "event_v12_generated.go" },
  { directory: "v1.2", schema: "artifact.schema.json", top: "ArtifactMetadataV12", ts: "artifact-v1-2.ts", go: "artifact_v12_generated.go" }
];
const temp = await mkdtemp(join(tmpdir(), "unit-test-ide-protocol-"));

try {
  let targetIndex = 0;
  for (const model of models) {
    const schema = join(root, "packages/protocol-schema/schema", model.directory, model.schema);
    let source = schema;
    if (model.definition) {
      const document = JSON.parse(await readFile(schema, "utf8"));
      const definition = document.$defs?.[model.definition];
      if (!definition) throw new Error(`Missing schema definition ${model.definition} in ${schema}`);
      const generatedSchema = { $schema: document.$schema, ...definition, title: model.top, $defs: { target: document.$defs.target } };
      source = join(temp, `${model.top}.schema.json`);
      await writeFile(source, `${JSON.stringify(generatedSchema, null, 2)}\n`);
    }
    const targets = [
      {
        output: join(root, "packages/protocol-models/src/generated", model.ts),
        args: ["--lang", "typescript", "--just-types", "--top-level", model.top]
      },
      {
        output: join(root, "apps/test-service/internal/protocolmodel", model.go),
        args: ["--lang", "go", "--just-types", "--package", "protocolmodel", "--top-level", model.top],
        packageName: "protocolmodel"
      }
    ];

    for (const target of targets) {
      const output = check ? join(temp, String(targetIndex++)) : target.output;
      await mkdir(dirname(output), { recursive: true });
      const result = spawnSync(process.execPath, [quicktype, "--quiet", "--src-lang", "schema", "--src", source, ...target.args, "--out", output], { cwd: root, stdio: "inherit" });
      if (result.status !== 0) throw new Error(`quicktype failed for ${model.top} with status ${result.status ?? 1}`);
      if (target.packageName) {
        await writeFile(output, `package ${target.packageName}\n\n${await readFile(output, "utf8")}`);
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
  }
} finally {
  await rm(temp, { recursive: true, force: true });
}
