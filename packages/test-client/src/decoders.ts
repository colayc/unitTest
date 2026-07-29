import type {
  ArtifactMetadata,
  ArtifactMetadataV12,
  TargetList,
  TaskEvent,
  TaskEventV12,
  TaskSnapshot,
  TaskSnapshotV12,
  WorkspaceSnapshot
} from "@unit-test-ide/protocol-models";
import type { ProtocolTaskEvent } from "./envelopes.js";

function record(value: unknown, name: string): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error(`invalid ${name} object`);
  return value as Record<string, unknown>;
}

function date(value: unknown, name: string): Date {
  if (typeof value !== "string") throw new Error(`invalid ${name} date-time`);
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) throw new Error(`invalid ${name} date-time`);
  return parsed;
}

function optionalDate(value: unknown, name: string): Date | undefined {
  return value === undefined ? undefined : date(value, name);
}

function safeInteger(value: unknown, name: string): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value)) throw new Error(`${name} must be a safe integer`);
  return value;
}

function optionalSafeInteger(value: unknown, name: string): number | undefined {
  return value === undefined ? undefined : safeInteger(value, name);
}

export function decodeTaskSnapshot(value: unknown): TaskSnapshot {
  const wire = record(value, "task snapshot");
  return {
    taskId: wire.taskId as string,
    kind: wire.kind as TaskSnapshot["kind"],
    scenario: wire.scenario as TaskSnapshot["scenario"],
    status: wire.status as TaskSnapshot["status"],
    outcome: wire.outcome as TaskSnapshot["outcome"],
    createdAt: date(wire.createdAt, "task createdAt"),
    startedAt: optionalDate(wire.startedAt, "task startedAt"),
    finishedAt: optionalDate(wire.finishedAt, "task finishedAt"),
    timeoutMs: optionalSafeInteger(wire.timeoutMs, "task timeoutMs"),
    lastSequence: safeInteger(wire.lastSequence, "task lastSequence"),
    errorCode: wire.errorCode as string | undefined,
    errorMessage: wire.errorMessage as string | undefined
  };
}

export function decodeTaskSnapshotV12(value: unknown): TaskSnapshotV12 {
  const wire = record(value, "protocol 1.2 task snapshot");
  const common = {
    taskId: wire.taskId as string,
    status: wire.status,
    outcome: wire.outcome,
    createdAt: date(wire.createdAt, "task createdAt"),
    startedAt: optionalDate(wire.startedAt, "task startedAt"),
    finishedAt: optionalDate(wire.finishedAt, "task finishedAt"),
    lastSequence: safeInteger(wire.lastSequence, "task lastSequence"),
    errorCode: wire.errorCode as string | undefined,
    errorMessage: wire.errorMessage as string | undefined
  };
  switch (wire.kind) {
    case "cmakeBuild":
      return {
        ...common,
        kind: "cmakeBuild",
        workspaceGeneration: wire.workspaceGeneration,
        projectId: wire.projectId,
        buildProfileId: wire.buildProfileId,
        targetIds: [...(wire.targetIds as string[])],
        jobs: safeInteger(wire.jobs, "task jobs"),
        timeoutMs: safeInteger(wire.timeoutMs, "task timeoutMs")
      } as unknown as TaskSnapshotV12;
    case "simulation":
      return {
        ...common,
        kind: "simulation",
        scenario: wire.scenario,
        timeoutMs: optionalSafeInteger(wire.timeoutMs, "task timeoutMs")
      } as unknown as TaskSnapshotV12;
    default:
      throw new Error("invalid protocol 1.2 task kind");
  }
}

export function decodeTaskEvent(value: unknown): ProtocolTaskEvent {
  const wire = record(value, "task event");
  if (wire.protocolVersion === "1.2") return decodeTaskEventV12(wire);
  return {
    protocolVersion: wire.protocolVersion as TaskEvent["protocolVersion"],
    kind: wire.kind as TaskEvent["kind"],
    messageId: wire.messageId as string,
    sentAt: date(wire.sentAt, "event sentAt"),
    sequence: safeInteger(wire.sequence, "event sequence"),
    event: wire.event as TaskEvent["event"],
    taskId: wire.taskId as string,
    payloadVersion: safeInteger(wire.payloadVersion, "event payloadVersion"),
    payload: record(wire.payload, "event payload")
  };
}

function decodeTaskEventV12(wire: Record<string, unknown>): TaskEventV12 {
  let payload = { ...record(wire.payload, "event payload") };
  if (wire.event === "task.step_finished") {
    const exitCode = optionalSafeInteger(payload.exitCode, "event step exitCode");
    if (exitCode === undefined) delete payload.exitCode;
    else payload.exitCode = exitCode;
  }
  if (wire.event === "task.diagnostic") {
    const diagnostic = { ...record(payload.diagnostic, "event diagnostic") };
    const line = optionalSafeInteger(diagnostic.line, "event diagnostic line");
    const column = optionalSafeInteger(diagnostic.column, "event diagnostic column");
    if (line === undefined) delete diagnostic.line;
    else diagnostic.line = line;
    if (column === undefined) delete diagnostic.column;
    else diagnostic.column = column;
    payload = { diagnostic };
  }
  return {
    protocolVersion: "1.2",
    kind: "event",
    messageId: wire.messageId,
    sentAt: date(wire.sentAt, "event sentAt"),
    sequence: safeInteger(wire.sequence, "event sequence"),
    event: wire.event,
    taskId: wire.taskId,
    payloadVersion: safeInteger(wire.payloadVersion, "event payloadVersion"),
    payload
  } as unknown as TaskEventV12;
}

export function decodeArtifactMetadata(value: unknown): ArtifactMetadata {
  const wire = record(value, "artifact metadata");
  return {
    artifactId: wire.artifactId as string,
    taskId: wire.taskId as string,
    kind: wire.kind as ArtifactMetadata["kind"],
    mimeType: wire.mimeType as ArtifactMetadata["mimeType"],
    sizeBytes: safeInteger(wire.sizeBytes, "artifact sizeBytes"),
    sha256: wire.sha256 as string,
    createdAt: date(wire.createdAt, "artifact createdAt")
  };
}

export function decodeArtifactMetadataV12(value: unknown): ArtifactMetadataV12 {
  const wire = record(value, "protocol 1.2 artifact metadata");
  return {
    artifactId: wire.artifactId,
    taskId: wire.taskId,
    kind: wire.kind,
    mimeType: wire.mimeType,
    sizeBytes: safeInteger(wire.sizeBytes, "artifact sizeBytes"),
    sha256: wire.sha256,
    createdAt: date(wire.createdAt, "artifact createdAt"),
    uri: wire.uri
  } as unknown as ArtifactMetadataV12;
}

export function decodeWorkspaceSnapshot(value: unknown): WorkspaceSnapshot {
  const wire = record(value, "workspace snapshot");
  return {
    workspaceUri: wire.workspaceUri,
    workspaceGeneration: wire.workspaceGeneration,
    capabilities: { ...record(wire.capabilities, "workspace capabilities") },
    diagnostics: (wire.diagnostics as unknown[]).map((diagnostic) => ({
      ...record(diagnostic, "workspace diagnostic")
    })),
    toolchains: (wire.toolchains as unknown[]).map((toolchainValue) => {
      const toolchain = record(toolchainValue, "workspace toolchain");
      const capabilities = record(toolchain.capabilities, "workspace toolchain capabilities");
      return {
        ...toolchain,
        generators: [...(toolchain.generators as unknown[])],
        capabilities: {
          ...capabilities,
          coverageDrivers: [...(capabilities.coverageDrivers as unknown[])]
        }
      };
    }),
    projects: (wire.projects as unknown[]).map((projectValue) => {
      const project = record(projectValue, "workspace project");
      return {
        projectId: project.projectId,
        sourceUri: project.sourceUri,
        buildProfiles: (project.buildProfiles as unknown[]).map((profileValue) => ({
          ...record(profileValue, "workspace build profile")
        }))
      };
    })
  } as unknown as WorkspaceSnapshot;
}

export function decodeTargetList(value: unknown): TargetList {
  const wire = record(value, "CMake target list");
  return {
    workspaceGeneration: wire.workspaceGeneration,
    projectId: wire.projectId,
    buildProfileId: wire.buildProfileId,
    targets: (wire.targets as unknown[]).map((target) => ({ ...record(target, "CMake target") }))
  } as unknown as TargetList;
}
