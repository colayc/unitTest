import test from "node:test";
import assert from "node:assert/strict";
import type { ArtifactMetadata, Capabilities, TaskEvent, TaskSnapshot } from "./index.js";
import { ArtifactKind, MIMEType } from "./generated/artifact.js";
import { Event, EventKind, ProtocolVersion } from "./generated/event.js";
import { Outcome, Scenario, Status, TaskKind } from "./generated/task.js";

test("generated capabilities represent an empty Windows service", () => {
  const value: Capabilities = {
    platform: "windows",
    transports: ["named-pipe"],
    toolchains: [],
    frameworks: [],
    coverageTools: []
  };
  assert.equal(value.platform, "windows");
});

test("generated protocol 1.1 models represent finished tasks, events, and artifacts", () => {
  const task: TaskSnapshot = {
    taskId: "0123456789abcdef0123456789abcdef",
    kind: TaskKind.Simulation,
    scenario: Scenario.Success,
    status: Status.Finished,
    outcome: Outcome.Cancelled,
    createdAt: new Date("2026-07-22T00:00:00Z"),
    finishedAt: new Date("2026-07-22T00:00:01Z"),
    lastSequence: 3
  };
  const event: TaskEvent = {
    protocolVersion: ProtocolVersion.The11,
    kind: EventKind.Event,
    messageId: "fedcba9876543210fedcba9876543210",
    sentAt: new Date("2026-07-22T00:00:01Z"),
    sequence: 3,
    event: Event.TaskFinished,
    taskId: task.taskId,
    payloadVersion: 1,
    payload: {}
  };
  const artifact: ArtifactMetadata = {
    artifactId: "abcdef0123456789abcdef0123456789",
    taskId: task.taskId,
    kind: ArtifactKind.TaskSummary,
    mimeType: MIMEType.ApplicationJSON,
    sizeBytes: 128,
    sha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    createdAt: new Date("2026-07-22T00:00:01Z")
  };

  assert.equal(event.event, Event.TaskFinished);
  assert.equal(artifact.kind, ArtifactKind.TaskSummary);
});
