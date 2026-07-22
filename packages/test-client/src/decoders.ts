import type { ArtifactMetadata, TaskEvent, TaskSnapshot } from "@unit-test-ide/protocol-models";

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
    timeoutMs: wire.timeoutMs as number | undefined,
    lastSequence: safeInteger(wire.lastSequence, "task lastSequence"),
    errorCode: wire.errorCode as string | undefined,
    errorMessage: wire.errorMessage as string | undefined
  };
}

export function decodeTaskEvent(value: unknown): TaskEvent {
  const wire = record(value, "task event");
  return {
    protocolVersion: wire.protocolVersion as TaskEvent["protocolVersion"],
    kind: wire.kind as TaskEvent["kind"],
    messageId: wire.messageId as string,
    sentAt: date(wire.sentAt, "event sentAt"),
    sequence: safeInteger(wire.sequence, "event sequence"),
    event: wire.event as TaskEvent["event"],
    taskId: wire.taskId as string,
    payloadVersion: wire.payloadVersion as number,
    payload: record(wire.payload, "event payload")
  };
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
