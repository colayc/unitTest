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
  { directory: "v1.2", schema: "capabilities.schema.json", top: "CapabilitiesV12", ts: "capabilities-v1-2.ts", go: "v1_2/capabilities/generated.go", goPackage: "protocolmodelv12capabilities" },
  { directory: "v1.2", schema: "diagnostic.schema.json", top: "Diagnostic", ts: "diagnostic.ts", go: "v1_2/diagnostic/generated.go", goPackage: "protocolmodelv12diagnostic" },
  { directory: "v1.2", schema: "workspace.schema.json", top: "WorkspaceSnapshot", ts: "workspace.ts", go: "v1_2/workspace/generated.go", goPackage: "protocolmodelv12workspace" },
  { directory: "v1.2", schema: "workspace.schema.json", definition: "targetList", top: "TargetList", ts: "target-list.ts", go: "v1_2/targetlist/generated.go", goPackage: "protocolmodelv12targetlist" },
  { directory: "v1.2", schema: "task.schema.json", top: "TaskSnapshotV12", template: "task", ts: "task-v1-2.ts", go: "v1_2/task/generated.go", goPackage: "protocolmodelv12task" },
  { directory: "v1.2", schema: "event.schema.json", top: "TaskEventV12", template: "event", ts: "event-v1-2.ts", go: "v1_2/event/generated.go", goPackage: "protocolmodelv12event" },
  { directory: "v1.2", schema: "artifact.schema.json", top: "ArtifactMetadataV12", ts: "artifact-v1-2.ts", go: "v1_2/artifact/generated.go", goPackage: "protocolmodelv12artifact" }
];
const typescriptTemplates = {
  task: `export interface TaskSnapshotBaseV12 { taskId: string; status: TaskStatusV12; createdAt: Date; lastSequence: number; outcome?: TaskOutcomeV12; startedAt?: Date; finishedAt?: Date; errorCode?: string; errorMessage?: string; }\nexport interface CmakeBuildTaskSnapshotV12 extends TaskSnapshotBaseV12 { kind: TaskKindV12.CmakeBuild; workspaceGeneration: string; projectId: string; buildProfileId: string; targetIds: string[]; jobs: number; timeoutMs: number; scenario?: never; }\nexport interface SimulationTaskSnapshotV12 extends TaskSnapshotBaseV12 { kind: TaskKindV12.Simulation; scenario: SimulationScenarioV12; timeoutMs?: number; workspaceGeneration?: never; projectId?: never; buildProfileId?: never; targetIds?: never; jobs?: never; }\nexport type TaskSnapshotV12 = CmakeBuildTaskSnapshotV12 | SimulationTaskSnapshotV12;\nexport enum TaskKindV12 { CmakeBuild = "cmakeBuild", Simulation = "simulation" }\nexport enum TaskStatusV12 { Queued = "queued", Running = "running", Cancelling = "cancelling", Finished = "finished" }\nexport enum TaskOutcomeV12 { Succeeded = "succeeded", CommandFailed = "command_failed", Cancelled = "cancelled", TimedOut = "timed_out", Interrupted = "interrupted", InfrastructureFailed = "infrastructure_failed" }\nexport enum SimulationScenarioV12 { Success = "success", ExitNonzero = "exit-nonzero", Hang = "hang", SpawnChild = "spawn-child", EmitOutput = "emit-output" }\n`,
  event: `export interface TaskEventBaseV12 { protocolVersion: EventProtocolVersionV12; kind: EventKindV12.Event; messageId: string; sentAt: Date; sequence: number; taskId: string; payloadVersion: 1; }
export interface TaskCreatedEventV12 extends TaskEventBaseV12 { event: TaskEventNameV12.TaskCreated; payload: { status: "queued" }; }
export interface TaskStartedEventV12 extends TaskEventBaseV12 { event: TaskEventNameV12.TaskStarted; payload: { status: "running" }; }
export interface TaskStepStartedEventV12 extends TaskEventBaseV12 { event: TaskEventNameV12.TaskStepStarted; payload: TaskStepStartedPayloadV12; }
export interface TaskOutputEventV12 extends TaskEventBaseV12 { event: TaskEventNameV12.TaskOutput; payload: TaskOutputPayloadV12; }
export interface TaskStepFinishedEventV12 extends TaskEventBaseV12 { event: TaskEventNameV12.TaskStepFinished; payload: TaskStepFinishedPayloadV12; }
export interface TaskCancellationRequestedEventV12 extends TaskEventBaseV12 { event: TaskEventNameV12.TaskCancellationRequested; payload: { status: "cancelling" }; }
export interface ArtifactCreatedEventV12 extends TaskEventBaseV12 { event: TaskEventNameV12.ArtifactCreated; payload: ArtifactCreatedPayloadV12; }
export interface TaskFinishedEventV12 extends TaskEventBaseV12 { event: TaskEventNameV12.TaskFinished; payload: { outcome: TaskOutcomeV12 }; }
export interface TaskDiagnosticEventV12 extends TaskEventBaseV12 { event: TaskEventNameV12.TaskDiagnostic; payload: TaskDiagnosticPayloadV12; }
export type TaskEventV12 = TaskCreatedEventV12 | TaskStartedEventV12 | TaskStepStartedEventV12 | TaskOutputEventV12 | TaskStepFinishedEventV12 | TaskCancellationRequestedEventV12 | ArtifactCreatedEventV12 | TaskFinishedEventV12 | TaskDiagnosticEventV12;
export interface TaskStepStartedPayloadV12 { stepId: string; kind: TaskStepKindV12; status: TaskStepStatusV12.Running; }
export interface TaskOutputPayloadV12 { stepId: string; stream: TaskOutputStreamV12; text: string; truncated: boolean; }
export interface TaskStepFinishedPayloadV12 { stepId: string; kind: TaskStepKindV12; status: TaskStepStatusV12.Succeeded | TaskStepStatusV12.Failed | TaskStepStatusV12.Skipped; exitCode?: number; errorCode?: string; }
export interface ArtifactCreatedPayloadV12 { artifactId: string; kind: string; }
export interface TaskDiagnosticPayloadV12 { diagnostic: TaskEventDiagnosticV12; }
export interface TaskEventDiagnosticV12 { severity: TaskEventDiagnosticSeverityV12; code: string; message: string; sourceUri?: string; line?: number; column?: number; }
export enum EventProtocolVersionV12 { The12 = "1.2" }
export enum EventKindV12 { Event = "event" }
export enum TaskEventNameV12 { TaskCreated = "task.created", TaskStarted = "task.started", TaskStepStarted = "task.step_started", TaskOutput = "task.output", TaskStepFinished = "task.step_finished", TaskCancellationRequested = "task.cancellation_requested", ArtifactCreated = "artifact.created", TaskFinished = "task.finished", TaskDiagnostic = "task.diagnostic" }
export enum TaskStepKindV12 { Simulation = "simulation", Configure = "configure", Build = "build" }
export enum TaskStepStatusV12 { Running = "running", Succeeded = "succeeded", Failed = "failed", Skipped = "skipped" }
export enum TaskOutputStreamV12 { Stdout = "stdout", Stderr = "stderr", Combined = "combined" }
export enum TaskOutcomeV12 { Succeeded = "succeeded", CommandFailed = "command_failed", Cancelled = "cancelled", TimedOut = "timed_out", Interrupted = "interrupted", InfrastructureFailed = "infrastructure_failed" }
export enum TaskEventDiagnosticSeverityV12 { Error = "error", Warning = "warning", Info = "info" }
`
};
const goUnionBodies = {
  task: `type TaskSnapshotV12 interface{ isTaskSnapshotV12() }
type CmakeBuildTaskSnapshotV12 struct { TaskID string \`json:"taskId"\`; Kind TaskKindV12 \`json:"kind"\`; WorkspaceGeneration string \`json:"workspaceGeneration"\`; ProjectID string \`json:"projectId"\`; BuildProfileID string \`json:"buildProfileId"\`; TargetIDs []string \`json:"targetIds"\`; Jobs int64 \`json:"jobs"\`; TimeoutMS int64 \`json:"timeoutMs"\`; Status TaskStatusV12 \`json:"status"\`; CreatedAt time.Time \`json:"createdAt"\`; LastSequence int64 \`json:"lastSequence"\`; Outcome *TaskOutcomeV12 \`json:"outcome,omitempty"\`; StartedAt *time.Time \`json:"startedAt,omitempty"\`; FinishedAt *time.Time \`json:"finishedAt,omitempty"\`; ErrorCode *string \`json:"errorCode,omitempty"\`; ErrorMessage *string \`json:"errorMessage,omitempty"\` }
func (CmakeBuildTaskSnapshotV12) isTaskSnapshotV12() {}
type SimulationTaskSnapshotV12 struct { TaskID string \`json:"taskId"\`; Kind TaskKindV12 \`json:"kind"\`; Scenario SimulationScenarioV12 \`json:"scenario"\`; Status TaskStatusV12 \`json:"status"\`; CreatedAt time.Time \`json:"createdAt"\`; LastSequence int64 \`json:"lastSequence"\`; TimeoutMS *int64 \`json:"timeoutMs,omitempty"\`; Outcome *TaskOutcomeV12 \`json:"outcome,omitempty"\`; StartedAt *time.Time \`json:"startedAt,omitempty"\`; FinishedAt *time.Time \`json:"finishedAt,omitempty"\`; ErrorCode *string \`json:"errorCode,omitempty"\`; ErrorMessage *string \`json:"errorMessage,omitempty"\` }
func (SimulationTaskSnapshotV12) isTaskSnapshotV12() {}

`,
  event: `type TaskEventV12 interface{ isTaskEventV12() }
type TaskEventBaseV12 struct { ProtocolVersion EventProtocolVersionV12 \`json:"protocolVersion"\`; Kind EventKindV12 \`json:"kind"\`; MessageID string \`json:"messageId"\`; SentAt time.Time \`json:"sentAt"\`; Sequence int64 \`json:"sequence"\`; TaskID string \`json:"taskId"\`; PayloadVersion float64 \`json:"payloadVersion"\` }
type TaskCreatedEventV12 struct { TaskEventBaseV12; Event TaskEventNameV12 \`json:"event"\`; Payload struct { Status string \`json:"status"\` } \`json:"payload"\` }
func (TaskCreatedEventV12) isTaskEventV12() {}
type TaskStartedEventV12 struct { TaskEventBaseV12; Event TaskEventNameV12 \`json:"event"\`; Payload struct { Status string \`json:"status"\` } \`json:"payload"\` }
func (TaskStartedEventV12) isTaskEventV12() {}
type TaskStepStartedEventV12 struct { TaskEventBaseV12; Event TaskEventNameV12 \`json:"event"\`; Payload struct { StepID string \`json:"stepId"\`; Kind TaskStepKindV12 \`json:"kind"\`; Status TaskStepStatusV12 \`json:"status"\` } \`json:"payload"\` }
func (TaskStepStartedEventV12) isTaskEventV12() {}
type TaskOutputEventV12 struct { TaskEventBaseV12; Event TaskEventNameV12 \`json:"event"\`; Payload struct { StepID string \`json:"stepId"\`; Stream TaskOutputStreamV12 \`json:"stream"\`; Text string \`json:"text"\`; Truncated bool \`json:"truncated"\` } \`json:"payload"\` }
func (TaskOutputEventV12) isTaskEventV12() {}
type TaskStepFinishedEventV12 struct { TaskEventBaseV12; Event TaskEventNameV12 \`json:"event"\`; Payload struct { StepID string \`json:"stepId"\`; Kind TaskStepKindV12 \`json:"kind"\`; Status TaskStepStatusV12 \`json:"status"\`; ExitCode *int64 \`json:"exitCode,omitempty"\`; ErrorCode *string \`json:"errorCode,omitempty"\` } \`json:"payload"\` }
func (TaskStepFinishedEventV12) isTaskEventV12() {}
type TaskCancellationRequestedEventV12 struct { TaskEventBaseV12; Event TaskEventNameV12 \`json:"event"\`; Payload struct { Status string \`json:"status"\` } \`json:"payload"\` }
func (TaskCancellationRequestedEventV12) isTaskEventV12() {}
type ArtifactCreatedEventV12 struct { TaskEventBaseV12; Event TaskEventNameV12 \`json:"event"\`; Payload struct { ArtifactID string \`json:"artifactId"\`; Kind string \`json:"kind"\` } \`json:"payload"\` }
func (ArtifactCreatedEventV12) isTaskEventV12() {}
type TaskFinishedEventV12 struct { TaskEventBaseV12; Event TaskEventNameV12 \`json:"event"\`; Payload struct { Outcome TaskOutcomeV12 \`json:"outcome"\` } \`json:"payload"\` }
func (TaskFinishedEventV12) isTaskEventV12() {}
type TaskDiagnosticEventV12 struct { TaskEventBaseV12; Event TaskEventNameV12 \`json:"event"\`; Payload struct { Diagnostic TaskEventDiagnosticV12 \`json:"diagnostic"\` } \`json:"payload"\` }
func (TaskDiagnosticEventV12) isTaskEventV12() {}
type TaskStepKindV12 string
const ( StepSimulation TaskStepKindV12 = "simulation"; StepConfigure TaskStepKindV12 = "configure"; StepBuild TaskStepKindV12 = "build" )
type TaskStepStatusV12 string
const ( StepRunning TaskStepStatusV12 = "running"; StepSucceeded TaskStepStatusV12 = "succeeded"; StepFailed TaskStepStatusV12 = "failed"; StepSkipped TaskStepStatusV12 = "skipped" )
type TaskEventDiagnosticV12 struct { Code string \`json:"code"\`; Column *int64 \`json:"column,omitempty"\`; Line *int64 \`json:"line,omitempty"\`; Message string \`json:"message"\`; Severity TaskEventDiagnosticSeverityV12 \`json:"severity"\`; SourceURI *string \`json:"sourceUri,omitempty"\` }
type TaskEventNameV12 string
const ( ArtifactCreated TaskEventNameV12 = "artifact.created"; TaskCancellationRequested TaskEventNameV12 = "task.cancellation_requested"; TaskCreated TaskEventNameV12 = "task.created"; TaskDiagnostic TaskEventNameV12 = "task.diagnostic"; TaskFinished TaskEventNameV12 = "task.finished"; TaskOutput TaskEventNameV12 = "task.output"; TaskStarted TaskEventNameV12 = "task.started"; TaskStepFinished TaskEventNameV12 = "task.step_finished"; TaskStepStarted TaskEventNameV12 = "task.step_started" )
type EventKindV12 string
const Event EventKindV12 = "event"
type TaskEventDiagnosticSeverityV12 string
const ( Error TaskEventDiagnosticSeverityV12 = "error"; Info TaskEventDiagnosticSeverityV12 = "info"; Warning TaskEventDiagnosticSeverityV12 = "warning" )
type TaskOutcomeV12 string
const ( OutcomeCancelled TaskOutcomeV12 = "cancelled"; OutcomeCommandFailed TaskOutcomeV12 = "command_failed"; OutcomeInfrastructureFailed TaskOutcomeV12 = "infrastructure_failed"; OutcomeInterrupted TaskOutcomeV12 = "interrupted"; OutcomeSucceeded TaskOutcomeV12 = "succeeded"; OutcomeTimedOut TaskOutcomeV12 = "timed_out" )
type TaskOutputStreamV12 string
const ( OutputCombined TaskOutputStreamV12 = "combined"; OutputStderr TaskOutputStreamV12 = "stderr"; OutputStdout TaskOutputStreamV12 = "stdout" )
type EventProtocolVersionV12 string
const The12 EventProtocolVersionV12 = "1.2"

`
};
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
        args: ["--lang", "go", "--just-types", "--package", model.goPackage ?? "protocolmodel", "--top-level", model.top],
        packageName: model.goPackage ?? "protocolmodel"
      }
    ];

    for (const target of targets) {
      const output = check ? join(temp, String(targetIndex++)) : target.output;
      await mkdir(dirname(output), { recursive: true });
      const result = spawnSync(process.execPath, [quicktype, "--quiet", "--src-lang", "schema", "--src", source, ...target.args, "--out", output], { cwd: root, stdio: "inherit" });
      if (result.status !== 0) throw new Error(`quicktype failed for ${model.top} with status ${result.status ?? 1}`);
      if (!target.packageName && model.template) await writeFile(output, typescriptTemplates[model.template]);
      if (target.packageName && model.template) {
        if (model.template === "event") {
          await writeFile(output, `import "time"\n\n${goUnionBodies.event}`);
        } else {
          const generated = await readFile(output, "utf8");
          const replaced = generated.replace(new RegExp(`type ${model.top} struct \\{[\\s\\S]*?\\n\\}\\n\\n`), goUnionBodies[model.template]);
          if (replaced === generated) throw new Error(`Unable to create Go union for ${model.top}`);
          await writeFile(output, replaced);
        }
      }
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
