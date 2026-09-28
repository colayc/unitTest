import { createHash } from "node:crypto";
import { once } from "node:events";
import { createRequire } from "node:module";
import net from "node:net";
import type { Duplex } from "node:stream";
import { Ajv2020, type ValidateFunction } from "ajv/dist/2020.js";
import * as formatsModule from "ajv-formats";
import type {
  ArtifactMetadata,
  ArtifactMetadataV12,
  ArtifactMetadataV13,
  ArtifactMetadataV14,
  ArtifactMetadataV15,
  Capabilities,
  CapabilitiesV11,
  CapabilitiesV12,
  CapabilitiesV13,
  CapabilitiesV14,
  CapabilitiesV15,
  CapabilitiesV16,
  CoverageProjectV16,
  CoverageFilePageV16,
  CoverageFunctionPageV16,
  CoverageLinePageV16,
  ManagedTestRecordPageV16,
  ManagedReviewV16,
  ManagedReviewIDRequestV16,
  ManagedReviewApplyRequestV16,
  ManagedReviewApplyResultV16,
  CoverageReport,
  CoverageRun,
  CoverageRunPage,
  CoverageRunStartRequest,
  TargetList,
  TaskSnapshot,
  TaskSnapshotV12,
  TaskSnapshotV13,
  TaskSnapshotV14,
  TaskSnapshotV15,
  TestCatalog,
  TestCatalogV14,
  TestRun,
  TestRunPageV14,
  TestRunV14,
  TestRunPage,
  TestSelection,
  TestGenerationAcceptRequestV15,
  TestGenerationCandidateListRequestV15,
  TestGenerationCandidatePageV15,
  TestGenerationEventPageV15,
  TestGenerationEventReplayRequestV15,
  TestGenerationRunV15,
  TestGenerationStartRequestV15,
  TestGenerationTargetListRequestV15,
  TestGenerationTargetListV15,
  WorkspaceSnapshot
} from "@unit-test-ide/protocol-models";
import { Connection, MAX_MESSAGE_BYTES } from "./connection.js";
import {
  decodeArtifactMetadata,
  decodeArtifactMetadataV12,
  decodeArtifactMetadataV13,
  decodeArtifactMetadataV14,
  decodeArtifactMetadataV15,
  decodeCoverageReport,
  decodeCoverageRun,
  decodeCoverageRunPage,
  decodeCapabilitiesV15,
  decodeCapabilitiesV16,
  decodeCoverageProjectV16,
  decodeCoverageFilePageV16,
  decodeCoverageFunctionPageV16,
  decodeCoverageLinePageV16,
  decodeManagedTestRecordPageV16,
  decodeManagedReviewV16,
  decodeManagedReviewApplyResultV16,
  decodeTargetList,
  decodeTaskSnapshot,
  decodeTaskSnapshotV12,
  decodeTaskSnapshotV13,
  decodeTaskSnapshotV14,
  decodeTaskSnapshotV15,
  decodeTestCatalog,
  decodeTestCatalogV14,
  decodeTestRun,
  decodeTestRunPage,
  decodeTestRunPageV14,
  decodeTestRunV14,
  decodeTestGenerationCandidatePage,
  decodeTestGenerationEventPage,
  decodeTestGenerationRun,
  decodeTestGenerationTargetList,
  decodeWorkspaceSnapshot
} from "./decoders.js";
import type { Method, ProtocolTaskEvent, ProtocolVersion } from "./envelopes.js";
import { ProtocolError } from "./envelopes.js";
import { EventSubscription } from "./subscription.js";

export { LEGACY_MAX_MESSAGE_BYTES, MAX_MESSAGE_BYTES } from "./connection.js";
/** Maximum artifact size materialized by readArtifact(). */
export const MAX_ARTIFACT_BYTES = 64 * 1024 * 1024;

export type SimulationScenario = "success" | "exit-nonzero" | "hang" | "spawn-child" | "emit-output";
export interface StartTaskInput {
  idempotencyKey: string;
  scenario: SimulationScenario;
  timeoutMs: number;
}
export interface CMakeBuildInput {
  idempotencyKey: string;
  workspaceGeneration: string;
  projectId: string;
  buildProfileId: string;
  toolchainId?: string;
  targetIds: string[];
  jobs: number;
  timeoutMs: number;
}
export interface CMakeTargetsInput {
  workspaceGeneration: string;
  projectId: string;
  buildProfileId: string;
}
export interface TestDiscoveryInput {
  idempotencyKey: string;
  projectId: string;
  profileId: string;
}
export interface TestRunInput {
  idempotencyKey: string;
  projectId: string;
  profileId: string;
  catalogRevision: string;
  selection: TestSelection;
  repeatCount: number;
}
export interface CatalogGetInput {
  projectId: string;
  profileId: string;
  cursor?: string;
  limit?: number;
}
export interface TestRunListInput {
  projectId?: string;
  profileId?: string;
  cursor?: string;
  limit?: number;
}
export type CoverageRunInput = CoverageRunStartRequest;
export type TestGenerationStartInput = TestGenerationStartRequestV15;
export type TestGenerationTargetListInput = TestGenerationTargetListRequestV15;
export type TestGenerationCandidateListInput = TestGenerationCandidateListRequestV15;
export type TestGenerationAcceptInput = TestGenerationAcceptRequestV15;
export type TestGenerationEventReplayInput = TestGenerationEventReplayRequestV15;
export interface CoverageRunListInput {
  projectId?: string;
  coverageProfileId?: string;
  cursor?: string;
  limit?: number;
}
export interface PageInput { cursor?: string; limit?: number }
export interface CoverageProjectInputV16 { workspaceGeneration: string; coverageReportId: string; projectId: string }
export interface CoverageFileListInputV16 extends CoverageProjectInputV16 { cursor?: string; limit?: number }
export interface CoverageFunctionListInputV16 { workspaceGeneration: string; coverageReportId: string; fileId: string; cursor?: string; limit?: number }
export type CoverageLineListInputV16 = { workspaceGeneration: string; coverageReportId: string; cursor?: string; limit?: number } &
  ({ fileId: string; functionId?: never } | { functionId: string; fileId?: never });
export interface ManagedTestListInputV16 extends CoverageProjectInputV16 { fileId?: string; status?: "current" | "stale" | "conflicted" | "orphaned" | "invalid"; cursor?: string; limit?: number }
export type ManagedReviewGetInputV16 = ManagedReviewIDRequestV16;
export type ManagedReviewApplyInputV16 = ManagedReviewApplyRequestV16;
export type ProtocolTaskSnapshot = TaskSnapshot | TaskSnapshotV12 | TaskSnapshotV13 | TaskSnapshotV14 | TaskSnapshotV15;
export type ProtocolArtifactMetadata = ArtifactMetadata | ArtifactMetadataV12 | ArtifactMetadataV13 | ArtifactMetadataV14 | ArtifactMetadataV15;
export type ProtocolTestCatalog = TestCatalog | TestCatalogV14;
export type ProtocolTestRun = TestRun | TestRunV14;
export type ProtocolTestRunPage = TestRunPage | TestRunPageV14;
export interface TaskPage { items: ProtocolTaskSnapshot[]; nextCursor?: string }
export interface ArtifactPage { items: ProtocolArtifactMetadata[]; nextCursor?: string }
export interface HandshakeResult { negotiatedProtocolVersion: ProtocolVersion; serviceVersion: string }
export type ConnectionConnector = () => Duplex | Promise<Duplex>;

interface Credentials {
  token: string;
  clientName: string;
  clientVersion: string;
}

interface ArtifactChunk {
  data: string;
  nextOffset: number;
  eof: boolean;
  sizeBytes: number;
  sha256: string;
}
interface SubscriptionAcknowledgement { afterSequence: number }

const require = createRequire(import.meta.url);
const payloadAjv = new Ajv2020({ allErrors: true, strict: true, strictRequired: false });
const addFormats = formatsModule.default as unknown as (instance: Ajv2020) => void;
addFormats(payloadAjv);
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.1/task"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.1/artifact"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.2/capabilities"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.2/diagnostic"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.2/workspace"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.2/task"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.2/artifact"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.3/capabilities"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.3/diagnostic"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.3/test"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.3/task"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.3/artifact"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/capabilities"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/diagnostic"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/test"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/coverage"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/task"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/event"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/artifact"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/message"));
for (const name of ["capabilities", "diagnostic", "test", "coverage", "test-generation", "task", "event", "artifact", "message"]) {
  payloadAjv.addSchema(require(`@unit-test-ide/protocol-schema/v1.5/${name}`));
}
for (const name of ["capabilities", "diagnostic", "test", "coverage", "test-generation", "task", "event", "artifact", "message"]) {
  payloadAjv.addSchema(require(`@unit-test-ide/protocol-schema/v1.6/${name}`));
}

const validateHandshakeV10 = payloadAjv.compile({
  type: "object",
  additionalProperties: false,
  required: ["negotiatedProtocolVersion", "serviceVersion"],
  properties: {
    negotiatedProtocolVersion: { const: "1.0" },
    serviceVersion: { type: "string", minLength: 1 }
  }
});
const validateHandshakeModern = payloadAjv.compile({
  type: "object",
  additionalProperties: false,
  required: ["negotiatedProtocolVersion", "serviceVersion"],
  properties: {
    negotiatedProtocolVersion: { enum: ["1.0", "1.1", "1.2", "1.3", "1.4", "1.5", "1.6"] },
    serviceVersion: { type: "string", minLength: 1 }
  }
});
const validateCapabilitiesV10 = payloadAjv.compile(require("@unit-test-ide/protocol-schema/v1/capabilities"));
const validateCapabilitiesV11 = payloadAjv.compile(require("@unit-test-ide/protocol-schema/v1.1/capabilities"));
const validateCapabilitiesV12 = payloadAjv.getSchema("urn:unit-test-ide:protocol:v1.2:capabilities") as ValidateFunction;
const validateCapabilitiesV13 = payloadAjv.getSchema("urn:unit-test-ide:protocol:v1.3:capabilities") as ValidateFunction;
const validateCapabilitiesV14 = payloadAjv.getSchema("urn:unit-test-ide:protocol:v1.4:capabilities") as ValidateFunction;
const validateCapabilitiesV15 = payloadAjv.getSchema("urn:unit-test-ide:protocol:v1.5:capabilities") as ValidateFunction;
const validateCapabilitiesV16 = payloadAjv.getSchema("urn:unit-test-ide:protocol:v1.6:capabilities") as ValidateFunction;
const validateTask = payloadAjv.getSchema("urn:unit-test-ide:protocol:v1.1:task") as ValidateFunction;
const validateTaskV12 = payloadAjv.getSchema("urn:unit-test-ide:protocol:v1.2:task") as ValidateFunction;
const validateTaskV13 = payloadAjv.getSchema("urn:unit-test-ide:protocol:v1.3:task") as ValidateFunction;
const validateTaskV14 = payloadAjv.getSchema("urn:unit-test-ide:protocol:v1.4:task") as ValidateFunction;
const validateTaskV15 = payloadAjv.getSchema("urn:unit-test-ide:protocol:v1.5:task") as ValidateFunction;
const validateWorkspaceV12 = payloadAjv.getSchema("urn:unit-test-ide:protocol:v1.2:workspace") as ValidateFunction;
const validateTargetListV12 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.2:workspace#/$defs/targetList"
});
const validateTaskPage = payloadAjv.compile({
  type: "object",
  additionalProperties: false,
  required: ["items"],
  properties: {
    items: { type: "array", items: { $ref: "urn:unit-test-ide:protocol:v1.1:task" } },
    nextCursor: { type: "string", minLength: 1 }
  }
});
const validateTaskPageV12 = payloadAjv.compile({
  type: "object",
  additionalProperties: false,
  required: ["items"],
  properties: {
    items: { type: "array", items: { $ref: "urn:unit-test-ide:protocol:v1.2:task" } },
    nextCursor: { type: "string", minLength: 1 }
  }
});
const validateTaskPageV13 = payloadAjv.compile({
  type: "object",
  additionalProperties: false,
  required: ["items"],
  properties: {
    items: { type: "array", items: { $ref: "urn:unit-test-ide:protocol:v1.3:task" } },
    nextCursor: { type: "string", minLength: 1 }
  }
});
const validateTaskPageV14 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.4:message#/$defs/tasksListResponse/allOf/1/properties/payload"
});
const validateTaskPageV15 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.5:message#/$defs/tasksListResponse/allOf/1/properties/payload"
});
const validateArtifactPage = payloadAjv.compile({
  type: "object",
  additionalProperties: false,
  required: ["items"],
  properties: {
    items: { type: "array", items: { $ref: "urn:unit-test-ide:protocol:v1.1:artifact" } },
    nextCursor: { type: "string", minLength: 1 }
  }
});
const validateArtifactPageV12 = payloadAjv.compile({
  type: "object",
  additionalProperties: false,
  required: ["items"],
  properties: {
    items: { type: "array", items: { $ref: "urn:unit-test-ide:protocol:v1.2:artifact" } },
    nextCursor: { type: "string", minLength: 1 }
  }
});
const validateArtifactPageV13 = payloadAjv.compile({
  type: "object",
  additionalProperties: false,
  required: ["items"],
  properties: {
    items: { type: "array", items: { $ref: "urn:unit-test-ide:protocol:v1.3:artifact" } },
    nextCursor: { type: "string", minLength: 1 }
  }
});
const validateArtifactPageV14 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.4:message#/$defs/artifactsListResponse/allOf/1/properties/payload"
});
const validateArtifactPageV15 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.5:message#/$defs/artifactsListResponse/allOf/1/properties/payload"
});
const validateTestCatalogV13 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.3:test#/$defs/testCatalog"
});
const validateTestRunV13 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.3:test#/$defs/testRun"
});
const validateTestRunPageV13 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.3:test#/$defs/testRunPage"
});
const validateTestCatalogV14 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.4:test#/$defs/testCatalog"
});
const validateTestRunV14 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.4:test#/$defs/testRun"
});
const validateTestRunPageV14 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.4:test#/$defs/testRunPage"
});
const validateCoverageRunStartV14 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.4:coverage#/$defs/coverageRunStartRequest"
});
const validateCoverageRunV14 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.4:coverage#/$defs/coverageRun"
});
const validateCoverageRunPageV14 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.4:coverage#/$defs/coverageRunPage"
});
const validateCoverageReportV14 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.4:coverage#/$defs/coverageReport"
});
const validateCoverageRunIdPayloadV14 = payloadAjv.getSchema(
  "urn:unit-test-ide:protocol:v1.4:message#/$defs/coverageRunIdPayload"
) as ValidateFunction;
const validateCoverageReportIdPayloadV14 = payloadAjv.getSchema(
  "urn:unit-test-ide:protocol:v1.4:message#/$defs/coverageReportIdPayload"
) as ValidateFunction;
const validateCoverageRunsListPayloadV14 = payloadAjv.getSchema(
  "urn:unit-test-ide:protocol:v1.4:message#/$defs/coverageRunsListPayload"
) as ValidateFunction;
const generationSchema = "urn:unit-test-ide:protocol:v1.5:test-generation#/$defs/";
const validateGenerationTargetsRequest = payloadAjv.getSchema(`${generationSchema}targetListRequest`) as ValidateFunction;
const validateGenerationTargets = payloadAjv.getSchema(`${generationSchema}targetList`) as ValidateFunction;
const validateGenerationStart = payloadAjv.getSchema(`${generationSchema}startRequest`) as ValidateFunction;
const validateGenerationRunId = payloadAjv.getSchema(`${generationSchema}runIdRequest`) as ValidateFunction;
const validateGenerationRun = payloadAjv.getSchema(`${generationSchema}run`) as ValidateFunction;
const validateGenerationCandidateListRequest = payloadAjv.getSchema(`${generationSchema}candidateListRequest`) as ValidateFunction;
const validateGenerationCandidates = payloadAjv.getSchema(`${generationSchema}candidatePage`) as ValidateFunction;
const validateGenerationAccept = payloadAjv.getSchema(`${generationSchema}acceptRequest`) as ValidateFunction;
const validateGenerationEventReplay = payloadAjv.getSchema(`${generationSchema}eventReplayRequest`) as ValidateFunction;
const validateGenerationEventPage = payloadAjv.getSchema(`${generationSchema}eventPage`) as ValidateFunction;
const coverageDetailSchema = "urn:unit-test-ide:protocol:v1.6:coverage#/$defs/";
const managedSchema = "urn:unit-test-ide:protocol:v1.6:test-generation#/$defs/";
const validateCoverageProjectRequestV16 = payloadAjv.getSchema(`${coverageDetailSchema}detailProjectRequest`) as ValidateFunction;
const validateCoverageFilesRequestV16 = payloadAjv.getSchema(`${coverageDetailSchema}detailFilesRequest`) as ValidateFunction;
const validateCoverageFunctionsRequestV16 = payloadAjv.getSchema(`${coverageDetailSchema}detailFunctionsRequest`) as ValidateFunction;
const validateCoverageLinesRequestV16 = payloadAjv.getSchema(`${coverageDetailSchema}detailLinesRequest`) as ValidateFunction;
const validateCoverageProjectV16 = payloadAjv.getSchema(`${coverageDetailSchema}detailProject`) as ValidateFunction;
const validateCoverageFilePageV16 = payloadAjv.getSchema(`${coverageDetailSchema}filePage`) as ValidateFunction;
const validateCoverageFunctionPageV16 = payloadAjv.getSchema(`${coverageDetailSchema}functionPage`) as ValidateFunction;
const validateCoverageLinePageV16 = payloadAjv.getSchema(`${coverageDetailSchema}linePage`) as ValidateFunction;
const validateManagedRecordsRequestV16 = payloadAjv.getSchema(`${managedSchema}managedRecordsRequest`) as ValidateFunction;
const validateManagedRecordPageV16 = payloadAjv.getSchema(`${managedSchema}managedRecordPage`) as ValidateFunction;
const validateManagedReviewGetRequestV16 = payloadAjv.getSchema(`${managedSchema}reviewIdRequest`) as ValidateFunction;
const validateManagedReviewV16 = payloadAjv.getSchema(`${managedSchema}managedReview`) as ValidateFunction;
const validateManagedReviewApplyRequestV16 = payloadAjv.getSchema(`${managedSchema}reviewApplyRequest`) as ValidateFunction;
const validateManagedReviewApplyResultV16 = payloadAjv.getSchema(`${managedSchema}reviewApplyResult`) as ValidateFunction;
const validateSubscription = payloadAjv.compile({
  type: "object",
  additionalProperties: false,
  required: ["afterSequence"],
  properties: { afterSequence: { type: "integer", minimum: 0 } }
});
const validateShutdown = payloadAjv.compile({
  type: "object",
  additionalProperties: false,
  required: ["accepted"],
  properties: { accepted: { const: true } }
});
const validateArtifactChunk = payloadAjv.compile({
  type: "object",
  additionalProperties: false,
  required: ["data", "nextOffset", "eof", "sizeBytes", "sha256"],
  properties: {
    data: { type: "string" },
    nextOffset: { type: "integer", minimum: 0 },
    eof: { type: "boolean" },
    sizeBytes: { type: "integer", minimum: 0 },
    sha256: { type: "string", pattern: "^[0-9a-f]{64}$" }
  }
});

function endpointConnector(endpoint: string): ConnectionConnector {
  return async () => {
    const socket = net.createConnection(endpoint);
    await once(socket, "connect");
    return socket;
  };
}

function validatePayload(method: string, validator: ValidateFunction, payload: Record<string, unknown>): void {
  if (!validator(payload)) {
    throw new Error(`invalid ${method} response: ${payloadAjv.errorsText(validator.errors)}`);
  }
}

function validateRequestPayload(method: string, validator: ValidateFunction, payload: unknown): void {
  if (!validator(payload)) {
    throw new Error(`invalid protocol request for ${method}: ${payloadAjv.errorsText(validator.errors)}`);
  }
}

function validateDetailBinding(
  method: string,
  payload: { workspaceGeneration: string; coverageReportId: string },
  request: { workspaceGeneration: string; coverageReportId: string }
): void {
  if (payload.workspaceGeneration !== request.workspaceGeneration || payload.coverageReportId !== request.coverageReportId) {
    throw new Error(`${method} response does not match the requested workspace generation and coverage report`);
  }
}

function snapshotRequestPayload(method: string, payload: unknown): unknown {
  try {
    const encoded = JSON.stringify(payload);
    if (encoded === undefined) throw new Error("payload is not JSON-serializable");
    return JSON.parse(encoded) as unknown;
  } catch (error) {
    const detail = error instanceof Error ? error.message : String(error);
    throw new Error(`invalid protocol request for ${method}: payload snapshot failed: ${detail}`);
  }
}

function decodeBase64Url(value: string): Buffer {
  if (!/^(?:[A-Za-z0-9_-]{4})*(?:[A-Za-z0-9_-]{2}|[A-Za-z0-9_-]{3})?$/.test(value)) {
    throw new Error("invalid artifact chunk Base64URL data");
  }
  const decoded = Buffer.from(value, "base64url");
  if (decoded.toString("base64url") !== value) throw new Error("invalid artifact chunk Base64URL data");
  return decoded;
}

function validateSubscriptionAcknowledgement(payload: Record<string, unknown>, expected: number): void {
  validatePayload("events/subscribe", validateSubscription, payload);
  if ((payload as unknown as SubscriptionAcknowledgement).afterSequence !== expected) {
    throw new Error("events/subscribe acknowledgement afterSequence does not match the requested cursor");
  }
}

type TaskProtocolVersion = Exclude<ProtocolVersion, "1.0">;

function decodeTaskResponse(
  method: string,
  version: TaskProtocolVersion,
  payload: Record<string, unknown>
): ProtocolTaskSnapshot {
  if (version === "1.5" || version === "1.6") {
    validatePayload(method, validateTaskV15, payload);
    return decodeTaskSnapshotV15(payload);
  }
  if (version === "1.4") {
    validatePayload(method, validateTaskV14, payload);
    return decodeTaskSnapshotV14(payload);
  }
  if (version === "1.3") {
    validatePayload(method, validateTaskV13, payload);
    return decodeTaskSnapshotV13(payload);
  }
  if (version === "1.2") {
    validatePayload(method, validateTaskV12, payload);
    return decodeTaskSnapshotV12(payload);
  }
  validatePayload(method, validateTask, payload);
  return decodeTaskSnapshot(payload);
}

function validateCMakeContext(input: CMakeTargetsInput): void {
  if (!/^[0-9a-f]{64}$/.test(input.workspaceGeneration)) {
    throw new Error("workspaceGeneration must be a 64-character lowercase hexadecimal value");
  }
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(input.projectId)) {
    throw new Error("projectId is invalid");
  }
  if (!/^[0-9a-f]{64}$/.test(input.buildProfileId)) {
    throw new Error("buildProfileId must be a 64-character lowercase hexadecimal value");
  }
}

function validateCMakeBuildInput(input: CMakeBuildInput): void {
  validateCMakeContext(input);
  if (input.toolchainId !== undefined &&
    !/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(input.toolchainId)) {
    throw new Error("toolchainId is invalid");
  }
  if (!/^[0-9a-f]{32}$/.test(input.idempotencyKey)) {
    throw new Error("idempotencyKey must be a 32-character lowercase hexadecimal value");
  }
  if (!Array.isArray(input.targetIds) || input.targetIds.length > 128 ||
    input.targetIds.some((targetId) => !/^[0-9a-f]{64}$/.test(targetId)) ||
    new Set(input.targetIds).size !== input.targetIds.length) {
    throw new Error("targetIds must contain at most 128 unique 64-character lowercase hexadecimal values");
  }
  if (!Number.isSafeInteger(input.jobs) || input.jobs < 1 || input.jobs > 256) {
    throw new Error("jobs must be a safe integer between 1 and 256");
  }
  if (!Number.isSafeInteger(input.timeoutMs) || input.timeoutMs < 1 || input.timeoutMs > 86_400_000) {
    throw new Error("timeoutMs must be a safe integer between 1 and 86400000");
  }
}

export class ProtocolClient {
  static attach(stream: Duplex): ProtocolClient {
    return new ProtocolClient(new Connection(stream));
  }

  static async connect(endpoint: string | ConnectionConnector): Promise<ProtocolClient> {
    const connector = typeof endpoint === "string" ? endpointConnector(endpoint) : endpoint;
    return new ProtocolClient(new Connection(await connector()), connector);
  }

  #connection: Connection;
  readonly #connector: ConnectionConnector | undefined;
  #credentials: Credentials | undefined;
  #negotiatedVersion: ProtocolVersion | undefined;
  #activeSubscription: EventSubscription | undefined;
  #unsubscribeEvent: (() => void) | undefined;
  #unsubscribeClose: (() => void) | undefined;
  #lifecycleOperation: "subscribe" | "reconnect" | undefined;
  #reconnectGeneration = 0;
  #reconnectCandidate: Connection | undefined;
  #closed = false;
  readonly #reviewBindings = new Map<string, { reviewDigest: string; workspaceGeneration: string; coverageReportId: string }>();

  private constructor(connection: Connection, connector?: ConnectionConnector) {
    this.#connection = connection;
    this.#connector = connector;
    this.#installConnectionListeners(connection);
  }

  async handshake(token: string, clientName: string, clientVersion: string): Promise<HandshakeResult> {
    if (this.#closed) throw new Error("protocol client is closed");
    const credentials = { token, clientName, clientVersion };
    const result = await this.#authenticate(this.#connection, credentials);
    this.#credentials = credentials;
    this.#negotiatedVersion = result.negotiatedProtocolVersion;
    this.#reviewBindings.clear();
    return result;
  }

  async getCapabilities(): Promise<Capabilities | CapabilitiesV11 | CapabilitiesV12 | CapabilitiesV13 | CapabilitiesV14 | CapabilitiesV15 | CapabilitiesV16> {
    const version = this.#requireAuthentication();
    const payload = await this.#connection.request(version, "capabilities/get", {});
    if (version === "1.6") {
      return this.#decodeV14InboundResponse(version, () => {
        validatePayload("capabilities/get", validateCapabilitiesV16, payload);
        return decodeCapabilitiesV16(payload);
      });
    }
    if (version === "1.5") {
      return this.#decodeV14InboundResponse(version, () => {
        validatePayload("capabilities/get", validateCapabilitiesV15, payload);
        return decodeCapabilitiesV15(payload);
      });
    }
    if (version === "1.4") {
      return this.#decodeV14InboundResponse(version, () => {
        validatePayload("capabilities/get", validateCapabilitiesV14, payload);
        return {
          ...payload,
          frameworkAdapters: (payload.frameworkAdapters as Record<string, unknown>[]).map((adapter) => ({ ...adapter }))
        } as unknown as CapabilitiesV14;
      });
    }
    if (version === "1.3") {
      validatePayload("capabilities/get", validateCapabilitiesV13, payload);
      return payload as unknown as CapabilitiesV13;
    }
    if (version === "1.2") {
      validatePayload("capabilities/get", validateCapabilitiesV12, payload);
      return payload as unknown as CapabilitiesV12;
    }
    if (version === "1.1") {
      validatePayload("capabilities/get", validateCapabilitiesV11, payload);
      return payload as unknown as CapabilitiesV11;
    }
    validatePayload("capabilities/get", validateCapabilitiesV10, payload);
    return payload as unknown as Capabilities;
  }

  async shutdown(): Promise<void> {
    const version = this.#requireAuthentication();
    const payload = await this.#connection.request(version, "shutdown", {});
    this.#decodeV14InboundResponse(version, () => validatePayload("shutdown", validateShutdown, payload));
  }

  async inspectWorkspace(): Promise<WorkspaceSnapshot> {
    const version = this.#requireV12();
    const payload = await this.#connection.request(version, "workspace/inspect", {});
    return this.#decodeV14InboundResponse(version, () => {
      validatePayload("workspace/inspect", validateWorkspaceV12, payload);
      return decodeWorkspaceSnapshot(payload);
    });
  }

  async listCMakeTargets(input: CMakeTargetsInput): Promise<TargetList> {
    const version = this.#requireV12();
    validateCMakeContext(input);
    const payload = await this.#connection.request(version, "cmake/targets/list", { ...input });
    return this.#decodeV14InboundResponse(version, () => {
      validatePayload("cmake/targets/list", validateTargetListV12, payload);
      return decodeTargetList(payload);
    });
  }

  async startCMakeBuild(input: CMakeBuildInput): Promise<TaskSnapshotV12 | TaskSnapshotV13 | TaskSnapshotV14 | TaskSnapshotV15> {
    const version = this.#requireV12();
    validateCMakeBuildInput(input);
    const payload = await this.#connection.request(version, "tasks/start", { ...input, kind: "cmakeBuild" });
    return this.#decodeV14InboundResponse(version, () => {
      if (version === "1.5" || version === "1.6") {
        validatePayload("tasks/start", validateTaskV15, payload);
        return decodeTaskSnapshotV15(payload);
      }
      if (version === "1.4") {
        validatePayload("tasks/start", validateTaskV14, payload);
        return decodeTaskSnapshotV14(payload);
      }
      if (version === "1.3") {
        validatePayload("tasks/start", validateTaskV13, payload);
        return decodeTaskSnapshotV13(payload);
      }
      validatePayload("tasks/start", validateTaskV12, payload);
      return decodeTaskSnapshotV12(payload);
    });
  }

  async discoverTests(input: TestDiscoveryInput): Promise<TaskSnapshotV13 | TaskSnapshotV14 | TaskSnapshotV15> {
    const version = this.#requireV13();
    const payload = await this.#connection.request(version, "tasks/start", { ...input, kind: "testDiscovery" });
    return this.#decodeV14InboundResponse(
      version,
      () => decodeTaskResponse("tasks/start", version, payload) as TaskSnapshotV13 | TaskSnapshotV14 | TaskSnapshotV15
    );
  }

  async runTests(input: TestRunInput): Promise<TaskSnapshotV13 | TaskSnapshotV14 | TaskSnapshotV15> {
    const version = this.#requireV13();
    const payload = await this.#connection.request(version, "tasks/start", { ...input, kind: "testRun" });
    return this.#decodeV14InboundResponse(
      version,
      () => decodeTaskResponse("tasks/start", version, payload) as TaskSnapshotV13 | TaskSnapshotV14 | TaskSnapshotV15
    );
  }

  async getTestCatalog(input: CatalogGetInput): Promise<ProtocolTestCatalog> {
    const version = this.#requireV13();
    const payload = await this.#connection.request(version, "tests/catalog/get", { ...input });
    return this.#decodeV14InboundResponse(version, () => {
      validatePayload("tests/catalog/get", version === "1.4" || version === "1.5" || version === "1.6" ? validateTestCatalogV14 : validateTestCatalogV13, payload);
      return version === "1.4" || version === "1.5" || version === "1.6" ? decodeTestCatalogV14(payload) : decodeTestCatalog(payload);
    });
  }

  async getTestRun(runId: string): Promise<ProtocolTestRun> {
    const version = this.#requireV13();
    const payload = await this.#connection.request(version, "tests/runs/get", { runId });
    return this.#decodeV14InboundResponse(version, () => {
      validatePayload("tests/runs/get", version === "1.4" || version === "1.5" || version === "1.6" ? validateTestRunV14 : validateTestRunV13, payload);
      return version === "1.4" || version === "1.5" || version === "1.6" ? decodeTestRunV14(payload) : decodeTestRun(payload);
    });
  }

  async listTestRuns(input: TestRunListInput = {}): Promise<ProtocolTestRunPage> {
    const version = this.#requireV13();
    const payload = await this.#connection.request(version, "tests/runs/list", { ...input });
    return this.#decodeV14InboundResponse(version, () => {
      validatePayload("tests/runs/list", version === "1.4" || version === "1.5" || version === "1.6" ? validateTestRunPageV14 : validateTestRunPageV13, payload);
      return version === "1.4" || version === "1.5" || version === "1.6" ? decodeTestRunPageV14(payload) : decodeTestRunPage(payload);
    });
  }

  async startCoverage(input: CoverageRunInput): Promise<CoverageRun> {
    const version = this.#requireV14();
    const request = snapshotRequestPayload("coverage/runs/start", input);
    validateRequestPayload("coverage/runs/start", validateCoverageRunStartV14, request);
    const payload = await this.#connection.request(
      version,
      "coverage/runs/start",
      request as Record<string, unknown>
    );
    return this.#decodeV14InboundResponse(version, () => {
      validatePayload("coverage/runs/start", validateCoverageRunV14, payload);
      return decodeCoverageRun(payload);
    });
  }

  async getCoverageRun(coverageRunId: string): Promise<CoverageRun> {
    const version = this.#requireV14();
    const request = { coverageRunId };
    validateRequestPayload("coverage/runs/get", validateCoverageRunIdPayloadV14, request);
    const payload = await this.#connection.request(version, "coverage/runs/get", request);
    return this.#decodeV14InboundResponse(version, () => {
      validatePayload("coverage/runs/get", validateCoverageRunV14, payload);
      return decodeCoverageRun(payload);
    });
  }

  async listCoverageRuns(input: CoverageRunListInput = {}): Promise<CoverageRunPage> {
    const version = this.#requireV14();
    const request = snapshotRequestPayload("coverage/runs/list", input);
    validateRequestPayload("coverage/runs/list", validateCoverageRunsListPayloadV14, request);
    const payload = await this.#connection.request(
      version,
      "coverage/runs/list",
      request as Record<string, unknown>
    );
    return this.#decodeV14InboundResponse(version, () => {
      validatePayload("coverage/runs/list", validateCoverageRunPageV14, payload);
      return decodeCoverageRunPage(payload);
    });
  }

  async getCoverageReport(reportId: string): Promise<CoverageReport> {
    const version = this.#requireV14();
    const request = { reportId };
    validateRequestPayload("coverage/reports/get", validateCoverageReportIdPayloadV14, request);
    const payload = await this.#connection.request(version, "coverage/reports/get", request);
    return this.#decodeV14InboundResponse(version, () => {
      validatePayload("coverage/reports/get", validateCoverageReportV14, payload);
      return decodeCoverageReport(payload);
    });
  }

  async getCoverageProject(input: CoverageProjectInputV16): Promise<CoverageProjectV16> {
    return this.#detailV16("coverage/details/project/get", input, validateCoverageProjectRequestV16,
      validateCoverageProjectV16, decodeCoverageProjectV16, true, (result, request) => {
        if (result.projectId !== request.projectId) throw new Error("coverage project response does not match projectId");
      });
  }

  async listCoverageFiles(input: CoverageFileListInputV16): Promise<CoverageFilePageV16> {
    return this.#detailV16("coverage/details/files/list", input, validateCoverageFilesRequestV16,
      validateCoverageFilePageV16, decodeCoverageFilePageV16, true);
  }

  async listCoverageFunctions(input: CoverageFunctionListInputV16): Promise<CoverageFunctionPageV16> {
    return this.#detailV16("coverage/details/functions/list", input, validateCoverageFunctionsRequestV16,
      validateCoverageFunctionPageV16, decodeCoverageFunctionPageV16, true, (result, request) => {
        if (result.items.some((item) => item.fileId !== request.fileId)) throw new Error("coverage function page fileId mismatch");
      });
  }

  async listCoverageLines(input: CoverageLineListInputV16): Promise<CoverageLinePageV16> {
    return this.#detailV16("coverage/details/lines/list", input, validateCoverageLinesRequestV16,
      validateCoverageLinePageV16, decodeCoverageLinePageV16, true);
  }

  async listManagedTests(input: ManagedTestListInputV16): Promise<ManagedTestRecordPageV16> {
    return this.#detailV16("managedTests/records/list", input, validateManagedRecordsRequestV16,
      validateManagedRecordPageV16, decodeManagedTestRecordPageV16, false, (result, request) => {
        if (request.fileId && result.items.some((item) => item.fileId !== request.fileId)) {
          throw new Error("managed test page fileId mismatch");
        }
        if (request.status && result.items.some((item) => item.status !== request.status)) {
          throw new Error("managed test page status mismatch");
        }
      });
  }

  async getManagedReview(input: ManagedReviewGetInputV16): Promise<ManagedReviewV16> {
    this.#requireV16();
    const method = "managedTests/reviews/get";
    const request = snapshotRequestPayload(method, input);
    validateRequestPayload(method, validateManagedReviewGetRequestV16, request);
    await this.#requireV16Capability(false);
    const payload = await this.#connection.request("1.6", method, request as Record<string, unknown>);
    return this.#decodeV14InboundResponse("1.6", () => {
      validatePayload(method, validateManagedReviewV16, payload);
      const result = decodeManagedReviewV16(payload);
      const sent = request as ManagedReviewGetInputV16;
      if (result.reviewId !== sent.reviewId) throw new Error("managed review response reviewId mismatch");
      const binding = sent.cursor === undefined ? undefined : this.#reviewBindings.get(sent.reviewId);
      if (binding && (binding.reviewDigest !== result.reviewDigest ||
        binding.workspaceGeneration !== result.workspaceGeneration || binding.coverageReportId !== result.coverageReportId)) {
        throw new Error("managed review cursor page digest or report binding changed");
      }
      this.#reviewBindings.set(sent.reviewId, {
        reviewDigest: result.reviewDigest, workspaceGeneration: result.workspaceGeneration,
        coverageReportId: result.coverageReportId
      });
      return result;
    });
  }

  async applyManagedReview(input: ManagedReviewApplyInputV16): Promise<ManagedReviewApplyResultV16> {
    this.#requireV16();
    const method = "managedTests/reviews/apply";
    const request = snapshotRequestPayload(method, input);
    validateRequestPayload(method, validateManagedReviewApplyRequestV16, request);
    const resolutions = (request as ManagedReviewApplyInputV16).resolutions;
    if (new Set(resolutions.map((item) => item.caseId)).size !== resolutions.length) {
      throw new Error("duplicate managed review case id");
    }
    await this.#requireV16Capability(false);
    const payload = await this.#connection.request("1.6", method, request as Record<string, unknown>);
    return this.#decodeV14InboundResponse("1.6", () => {
      validatePayload(method, validateManagedReviewApplyResultV16, payload);
      const result = decodeManagedReviewApplyResultV16(payload);
      if (result.reviewId !== (request as ManagedReviewApplyInputV16).reviewId ||
          result.reviewDigest !== (request as ManagedReviewApplyInputV16).reviewDigest) {
        throw new Error("managed review apply response digest or reviewId mismatch");
      }
      return result;
    });
  }

  async #detailV16<I extends { workspaceGeneration: string; coverageReportId: string },
    T extends { workspaceGeneration: string; coverageReportId: string }>(
    method: Method,
    input: I,
    requestValidator: ValidateFunction,
    responseValidator: ValidateFunction,
    decode: (value: unknown) => T,
    coverage: boolean,
    check?: (result: T, request: I) => void
  ): Promise<T> {
    this.#requireV16();
    const request = snapshotRequestPayload(method, input) as I;
    validateRequestPayload(method, requestValidator, request);
    await this.#requireV16Capability(coverage);
    const payload = await this.#connection.request("1.6", method, request as Record<string, unknown>);
    return this.#decodeV14InboundResponse("1.6", () => {
      validatePayload(method, responseValidator, payload);
      const result = decode(payload);
      validateDetailBinding(method, result, request);
      check?.(result, request);
      return result;
    });
  }

  async listTestGenerationTargets(input: TestGenerationTargetListInput): Promise<TestGenerationTargetListV15> {
    const version = this.#requireV15();
    const request = snapshotRequestPayload("testGeneration/targets/list", input);
    validateRequestPayload("testGeneration/targets/list", validateGenerationTargetsRequest, request);
    const payload = await this.#connection.request(version, "testGeneration/targets/list", request as Record<string, unknown>);
    return this.#decodeV14InboundResponse(version, () => {
      validatePayload("testGeneration/targets/list", validateGenerationTargets, payload);
      return decodeTestGenerationTargetList(payload);
    });
  }

  async startTestGeneration(input: TestGenerationStartInput): Promise<TestGenerationRunV15> {
    const version = this.#requireV15();
    const request = snapshotRequestPayload("testGeneration/start", input);
    validateRequestPayload("testGeneration/start", validateGenerationStart, request);
    const payload = await this.#connection.request(version, "testGeneration/start", request as Record<string, unknown>);
    return this.#decodeV14InboundResponse(version, () => {
      validatePayload("testGeneration/start", validateGenerationRun, payload);
      return decodeTestGenerationRun(payload);
    });
  }

  async getTestGenerationRun(runId: string): Promise<TestGenerationRunV15> {
    const version = this.#requireV15();
    const request = { runId };
    validateRequestPayload("testGeneration/runs/get", validateGenerationRunId, request);
    const payload = await this.#connection.request(version, "testGeneration/runs/get", request);
    return this.#decodeV14InboundResponse(version, () => {
      validatePayload("testGeneration/runs/get", validateGenerationRun, payload);
      return decodeTestGenerationRun(payload);
    });
  }

  async cancelTestGeneration(runId: string): Promise<TestGenerationRunV15> {
    const version = this.#requireV15();
    const request = { runId };
    validateRequestPayload("testGeneration/runs/cancel", validateGenerationRunId, request);
    const payload = await this.#connection.request(version, "testGeneration/runs/cancel", request);
    return this.#decodeV14InboundResponse(version, () => {
      validatePayload("testGeneration/runs/cancel", validateGenerationRun, payload);
      return decodeTestGenerationRun(payload);
    });
  }

  async replayTestGenerationEvents(input: TestGenerationEventReplayInput): Promise<TestGenerationEventPageV15> {
    const version = this.#requireV15();
    const request = snapshotRequestPayload("testGeneration/events/replay", input);
    validateRequestPayload("testGeneration/events/replay", validateGenerationEventReplay, request);
    const payload = await this.#connection.request(version, "testGeneration/events/replay", request as Record<string, unknown>);
    return this.#decodeV14InboundResponse(version, () => {
      validatePayload("testGeneration/events/replay", validateGenerationEventPage, payload);
      return decodeTestGenerationEventPage(payload);
    });
  }

  async listTestGenerationCandidates(input: TestGenerationCandidateListInput): Promise<TestGenerationCandidatePageV15> {
    const version = this.#requireV15();
    const request = snapshotRequestPayload("testGeneration/candidates/list", input);
    validateRequestPayload("testGeneration/candidates/list", validateGenerationCandidateListRequest, request);
    const payload = await this.#connection.request(version, "testGeneration/candidates/list", request as Record<string, unknown>);
    return this.#decodeV14InboundResponse(version, () => {
      validatePayload("testGeneration/candidates/list", validateGenerationCandidates, payload);
      return decodeTestGenerationCandidatePage(payload);
    });
  }

  /** The service resolves candidate kind from runId/candidateId and checks explicit characterization consent. */
  async acceptTestGeneration(input: TestGenerationAcceptInput): Promise<TestGenerationRunV15> {
    const version = this.#requireV15();
    const request = snapshotRequestPayload("testGeneration/accept", input);
    validateRequestPayload("testGeneration/accept", validateGenerationAccept, request);
    const payload = await this.#connection.request(version, "testGeneration/accept", request as Record<string, unknown>);
    return this.#decodeV14InboundResponse(version, () => {
      validatePayload("testGeneration/accept", validateGenerationRun, payload);
      return decodeTestGenerationRun(payload);
    });
  }

  async startTask(input: StartTaskInput): Promise<ProtocolTaskSnapshot> {
    const version = this.#requireTaskProtocol();
    const payload = await this.#connection.request(
      version,
      "tasks/start",
      version === "1.1" ? { ...input } : { ...input, kind: "simulation" }
    );
    return this.#decodeV14InboundResponse(version, () => decodeTaskResponse("tasks/start", version, payload));
  }

  async getTask(taskId: string): Promise<ProtocolTaskSnapshot> {
    const { version, payload } = await this.#requestTaskProtocol("tasks/get", { taskId });
    return this.#decodeV14InboundResponse(version, () => decodeTaskResponse("tasks/get", version, payload));
  }

  async listTasks(input: PageInput = {}): Promise<TaskPage> {
    const { version, payload } = await this.#requestTaskProtocol("tasks/list", { ...input });
    return this.#decodeV14InboundResponse(version, () => {
      const validator = version === "1.5" || version === "1.6"
        ? validateTaskPageV15
        : version === "1.4"
        ? validateTaskPageV14
        : version === "1.3" ? validateTaskPageV13 : version === "1.2" ? validateTaskPageV12 : validateTaskPage;
      validatePayload("tasks/list", validator, payload);
      return {
        items: (payload.items as Record<string, unknown>[]).map((item) =>
          version === "1.5" || version === "1.6"
            ? decodeTaskSnapshotV15(item)
            : version === "1.4"
            ? decodeTaskSnapshotV14(item)
            : version === "1.3"
            ? decodeTaskSnapshotV13(item)
            : version === "1.2" ? decodeTaskSnapshotV12(item) : decodeTaskSnapshot(item)),
        ...(typeof payload.nextCursor === "string" ? { nextCursor: payload.nextCursor } : {})
      };
    });
  }

  async cancelTask(taskId: string): Promise<ProtocolTaskSnapshot> {
    const { version, payload } = await this.#requestTaskProtocol("tasks/cancel", { taskId });
    return this.#decodeV14InboundResponse(version, () => decodeTaskResponse("tasks/cancel", version, payload));
  }

  async subscribeEvents(afterSequence: number): Promise<EventSubscription> {
    const version = this.#requireTaskProtocol();
    if (!Number.isSafeInteger(afterSequence) || afterSequence < 0) {
      throw new Error("invalid protocol request: afterSequence must be a non-negative safe integer");
    }
    this.#beginLifecycleOperation("subscribe");
    const connection = this.#connection;
    const subscription = new EventSubscription(afterSequence, () => {
      if (this.#activeSubscription === subscription) this.#activeSubscription = undefined;
    });
    const previous = this.#activeSubscription;
    let committed = false;
    const retireUnacknowledgedSubscriptions = () => {
      subscription.close();
      previous?.close();
      if (this.#activeSubscription === previous || this.#activeSubscription === subscription) {
        this.#activeSubscription = undefined;
      }
    };
    try {
      await connection.request(version, "events/subscribe", { afterSequence }, {
        onResponse: (payload) => {
          try {
            this.#decodeV14InboundResponse(version, () => {
              validateSubscriptionAcknowledgement(payload, afterSequence);
              if (this.#closed || connection !== this.#connection) {
                throw new Error("event subscription connection is no longer active");
              }
            });
          } catch (error) {
            retireUnacknowledgedSubscriptions();
            throw error;
          }
          previous?.close();
          this.#activeSubscription = subscription;
          committed = true;
        },
        onError: () => retireUnacknowledgedSubscriptions()
      });
      return subscription;
    } catch (error) {
      if (!committed) {
        retireUnacknowledgedSubscriptions();
      } else if (this.#activeSubscription === subscription) {
        this.#activeSubscription = undefined;
      }
      throw error;
    } finally {
      this.#endLifecycleOperation("subscribe");
    }
  }

  async listArtifacts(taskId: string, input: PageInput = {}): Promise<ArtifactPage> {
    const { version, payload } = await this.#requestTaskProtocol("artifacts/list", { taskId, ...input });
    return this.#decodeV14InboundResponse(version, () => {
      const validator = version === "1.5" || version === "1.6"
        ? validateArtifactPageV15
        : version === "1.4"
        ? validateArtifactPageV14
        : version === "1.3" ? validateArtifactPageV13 : version === "1.2" ? validateArtifactPageV12 : validateArtifactPage;
      validatePayload("artifacts/list", validator, payload);
      return {
        items: (payload.items as Record<string, unknown>[]).map((item) =>
          version === "1.5" || version === "1.6"
            ? decodeArtifactMetadataV15(item)
            : version === "1.4"
            ? decodeArtifactMetadataV14(item)
            : version === "1.3"
            ? decodeArtifactMetadataV13(item)
            : version === "1.2" ? decodeArtifactMetadataV12(item) : decodeArtifactMetadata(item)),
        ...(typeof payload.nextCursor === "string" ? { nextCursor: payload.nextCursor } : {})
      };
    });
  }

  async readArtifact(artifactId: string): Promise<Uint8Array> {
    const version = this.#requireTaskProtocol();
    const hash = createHash("sha256");
    let result: Buffer | undefined;
    let offset = 0;
    let expectedSize: number | undefined;
    let expectedDigest: string | undefined;
    for (;;) {
      const payload = await this.#connection.request(version, "artifacts/read", { artifactId, offset, length: 65_536 });
      const completed = this.#decodeV14InboundResponse(version, () => {
        validatePayload("artifacts/read", validateArtifactChunk, payload);
        const chunk: ArtifactChunk = {
          data: payload.data as string,
          nextOffset: payload.nextOffset as number,
          eof: payload.eof as boolean,
          sizeBytes: payload.sizeBytes as number,
          sha256: payload.sha256 as string
        };
        const data = decodeBase64Url(chunk.data);
        if (!Number.isSafeInteger(chunk.sizeBytes) || !Number.isSafeInteger(chunk.nextOffset) || !Number.isSafeInteger(data.byteLength)) {
          throw new Error("artifact size and offsets must be safe integers");
        }
        const computedNextOffset = offset + data.byteLength;
        if (!Number.isSafeInteger(computedNextOffset)) throw new Error("artifact offset overflowed the safe integer range");
        if (data.byteLength > 65_536 || chunk.nextOffset !== computedNextOffset) {
          throw new Error("invalid artifact chunk offset or length");
        }
        if (!chunk.eof && chunk.nextOffset <= offset) throw new Error("invalid artifact chunk: offset did not advance");
        if (expectedSize === undefined) {
          if (chunk.sizeBytes > MAX_ARTIFACT_BYTES) throw new Error("artifact exceeds the client download limit");
          expectedSize = chunk.sizeBytes;
          expectedDigest = chunk.sha256;
          result = Buffer.allocUnsafe(expectedSize);
        } else if (chunk.sizeBytes !== expectedSize || chunk.sha256 !== expectedDigest) {
          throw new Error("artifact chunk metadata changed during read");
        }
        if (computedNextOffset > MAX_ARTIFACT_BYTES) throw new Error("artifact exceeds the client download limit");
        if (chunk.nextOffset > expectedSize) throw new Error("invalid artifact chunk: offset exceeds declared size");
        if (!chunk.eof && chunk.nextOffset === expectedSize) {
          throw new Error("artifact chunk reached the declared size without EOF");
        }
        if (!result) throw new Error("artifact buffer was not initialized");
        hash.update(data);
        data.copy(result, offset);
        offset = chunk.nextOffset;
        if (!chunk.eof) return undefined;
        if (offset !== expectedSize) throw new Error("artifact size does not match the completed read");
        if (result.byteLength !== expectedSize) throw new Error("artifact size does not match the completed read");
        const actualDigest = hash.digest("hex");
        if (actualDigest !== expectedDigest) throw new Error("artifact SHA-256 does not match metadata");
        return result;
      });
      if (completed !== undefined) return completed;
    }
  }

  async reconnect(): Promise<void> {
    if (!this.#connector) throw new Error("connection connector is not available; attach() clients cannot reconnect");
    if (!this.#credentials) throw new Error("handshake has not completed");
    if (this.#closed) throw new Error("protocol client is closed");
    this.#beginLifecycleOperation("reconnect");
    const generation = ++this.#reconnectGeneration;
    const credentials = this.#credentials;
    const subscription = this.#activeSubscription;
    this.#removeConnectionListeners();
    this.#connection.close();
    let candidate: Connection | undefined;
    let candidateStream: Duplex | undefined;
    let candidateEventUnsubscribe: (() => void) | undefined;
    try {
      candidateStream = await this.#connector();
      if (!this.#reconnectIsCurrent(generation)) {
        candidateStream.destroy();
        throw new Error("reconnect was cancelled because the protocol client is closed");
      }
      candidate = new Connection(candidateStream);
      candidateStream = undefined;
      this.#reconnectCandidate = candidate;
      const negotiated = await this.#authenticate(candidate, credentials);
      this.#requireCurrentReconnect(generation, candidate);
      if (subscription && negotiated.negotiatedProtocolVersion === "1.0") {
        throw new ProtocolError("PROTOCOL_FEATURE_UNAVAILABLE", "protocol 1.1 or newer was not negotiated", false);
      }
      if (subscription) {
        this.#requireActiveSubscription(subscription);
        const reconnectConnection = candidate;
        const requestedAfterSequence = subscription.lastSequence;
        candidateEventUnsubscribe = reconnectConnection.onEvent((event) => this.#pushEvent(reconnectConnection, subscription, event));
        const invalidateCandidate = (error: Error) => {
          candidateEventUnsubscribe?.();
          candidateEventUnsubscribe = undefined;
          reconnectConnection.close(error);
        };
        await reconnectConnection.request(negotiated.negotiatedProtocolVersion, "events/subscribe", { afterSequence: requestedAfterSequence }, {
          onResponse: (payload) => {
            try {
              this.#requireCurrentReconnect(generation, reconnectConnection);
              validateSubscriptionAcknowledgement(payload, requestedAfterSequence);
            } catch (error) {
              const failure = error instanceof Error ? error : new Error(String(error));
              invalidateCandidate(failure);
              throw failure;
            }
          },
          onError: (error) => invalidateCandidate(error)
        });
        this.#requireCurrentReconnect(generation, reconnectConnection);
        this.#requireActiveSubscription(subscription);
      }
      this.#requireCurrentReconnect(generation, candidate);
      this.#connection = candidate;
      this.#negotiatedVersion = negotiated.negotiatedProtocolVersion;
      this.#reviewBindings.clear();
      this.#installConnectionListeners(candidate, candidateEventUnsubscribe);
      candidateEventUnsubscribe = undefined;
    } catch (error) {
      candidateEventUnsubscribe?.();
      candidateStream?.destroy();
      candidate?.close();
      throw error;
    } finally {
      if (this.#reconnectCandidate === candidate) this.#reconnectCandidate = undefined;
      this.#endLifecycleOperation("reconnect");
    }
  }

  close(): void {
    if (this.#closed) return;
    this.#closed = true;
    this.#reviewBindings.clear();
    this.#reconnectGeneration++;
    this.#reconnectCandidate?.close();
    this.#reconnectCandidate = undefined;
    this.#removeConnectionListeners();
    this.#activeSubscription?.close();
    this.#connection.close();
  }

  async #authenticate(connection: Connection, credentials: Credentials): Promise<HandshakeResult> {
    const attempts: ReadonlyArray<{
      version: "1.6" | "1.5" | "1.4" | "1.3" | "1.2" | "1.1";
      offered: ProtocolVersion[];
    }> = [
      { version: "1.6", offered: ["1.6", "1.5", "1.4", "1.3", "1.2", "1.1", "1.0"] },
      { version: "1.5", offered: ["1.5", "1.4", "1.3", "1.2", "1.1", "1.0"] },
      { version: "1.4", offered: ["1.4", "1.3", "1.2", "1.1", "1.0"] },
      { version: "1.3", offered: ["1.3", "1.2", "1.1", "1.0"] },
      { version: "1.2", offered: ["1.2", "1.1", "1.0"] },
      { version: "1.1", offered: ["1.1", "1.0"] }
    ];
    for (const attempt of attempts) {
      try {
        const payload = await connection.request(attempt.version, "handshake", {
          ...credentials,
          supportedProtocolVersions: attempt.offered
        });
        validatePayload("handshake", validateHandshakeModern, payload);
        return {
          negotiatedProtocolVersion: payload.negotiatedProtocolVersion as ProtocolVersion,
          serviceVersion: payload.serviceVersion as string
        };
      } catch (error) {
        if (!(error instanceof ProtocolError) || error.code !== "UNSUPPORTED_PROTOCOL") throw error;
      }
    }
    const payload = await connection.request("1.0", "handshake", { ...credentials });
    validatePayload("handshake", validateHandshakeV10, payload);
    return {
      negotiatedProtocolVersion: payload.negotiatedProtocolVersion as ProtocolVersion,
      serviceVersion: payload.serviceVersion as string
    };
  }

  #requireAuthentication(): ProtocolVersion {
    if (!this.#negotiatedVersion) throw new Error("handshake has not completed");
    return this.#negotiatedVersion;
  }

  #requireTaskProtocol(): TaskProtocolVersion {
    const version = this.#requireAuthentication();
    if (version === "1.0") {
      throw new ProtocolError("PROTOCOL_FEATURE_UNAVAILABLE", "protocol 1.1 or newer was not negotiated", false);
    }
    return version;
  }

  #requireV12(): "1.2" | "1.3" | "1.4" | "1.5" | "1.6" {
    const version = this.#requireAuthentication();
    if (version !== "1.2" && version !== "1.3" && version !== "1.4" && version !== "1.5" && version !== "1.6") {
      throw new ProtocolError("PROTOCOL_FEATURE_UNAVAILABLE", "protocol 1.2 or newer was not negotiated", false);
    }
    return version;
  }

  #requireV13(): "1.3" | "1.4" | "1.5" | "1.6" {
    const version = this.#requireAuthentication();
    if (version !== "1.3" && version !== "1.4" && version !== "1.5" && version !== "1.6") {
      throw new ProtocolError("PROTOCOL_FEATURE_UNAVAILABLE", "protocol 1.3 or newer was not negotiated", false);
    }
    return version;
  }

  #requireV14(): "1.4" | "1.5" | "1.6" {
    const version = this.#requireAuthentication();
    if (version !== "1.4" && version !== "1.5" && version !== "1.6") {
      throw new ProtocolError("PROTOCOL_FEATURE_UNAVAILABLE", "protocol 1.4 was not negotiated", false);
    }
    return version;
  }

  #requireV15(): "1.5" | "1.6" {
    const version = this.#requireAuthentication();
    if (version !== "1.5" && version !== "1.6") {
      throw new ProtocolError("PROTOCOL_FEATURE_UNAVAILABLE", "protocol 1.5 was not negotiated", false);
    }
    return version;
  }

  #requireV16(): "1.6" {
    const version = this.#requireAuthentication();
    if (version !== "1.6") {
      throw new ProtocolError("PROTOCOL_FEATURE_UNAVAILABLE", "protocol 1.6 was not negotiated", false);
    }
    return version;
  }

  async #requireV16Capability(coverage: boolean): Promise<void> {
    this.#requireV16();
    const capabilities = await this.getCapabilities() as CapabilitiesV16;
    if (!(coverage ? capabilities.coverageDetails : capabilities.managedTests)) {
      throw new ProtocolError("PROTOCOL_FEATURE_UNAVAILABLE", coverage ? "coverage details are unavailable" : "managed tests are unavailable", false);
    }
  }

  #decodeV14InboundResponse<T>(version: ProtocolVersion, decode: () => T): T {
    if (version !== "1.4" && version !== "1.5" && version !== "1.6") return decode();
    try {
      return decode();
    } catch (error) {
      const failure = error instanceof Error ? error : new Error(String(error));
      this.#connection.close(failure);
      throw failure;
    }
  }

  async #requestTaskProtocol(
    method: Method,
    payload: Record<string, unknown>
  ): Promise<{ version: TaskProtocolVersion; payload: Record<string, unknown> }> {
    const version = this.#requireTaskProtocol();
    return { version, payload: await this.#connection.request(version, method, payload) };
  }

  #installConnectionListeners(connection: Connection, eventUnsubscribe?: () => void): void {
    this.#unsubscribeEvent = eventUnsubscribe ?? connection.onEvent((event: ProtocolTaskEvent) => {
      const subscription = this.#activeSubscription;
      if (subscription) this.#pushEvent(connection, subscription, event);
    });
    this.#unsubscribeClose = connection.onClose(() => {
      if (!this.#connector) this.#activeSubscription?.close();
    });
  }

  #removeConnectionListeners(): void {
    this.#unsubscribeEvent?.();
    this.#unsubscribeClose?.();
    this.#unsubscribeEvent = undefined;
    this.#unsubscribeClose = undefined;
  }

  #reconnectIsCurrent(generation: number): boolean {
    return !this.#closed && generation === this.#reconnectGeneration;
  }

  #requireCurrentReconnect(generation: number, candidate: Connection): void {
    if (this.#reconnectIsCurrent(generation) && !candidate.closed) return;
    candidate.close();
    throw new Error("reconnect was cancelled because the protocol client is closed");
  }

  #beginLifecycleOperation(operation: "subscribe" | "reconnect"): void {
    if (this.#lifecycleOperation) throw new Error("client lifecycle operation is already in progress");
    this.#lifecycleOperation = operation;
  }

  #endLifecycleOperation(operation: "subscribe" | "reconnect"): void {
    if (this.#lifecycleOperation === operation) this.#lifecycleOperation = undefined;
  }

  #requireActiveSubscription(subscription: EventSubscription): void {
    if (this.#activeSubscription === subscription && !subscription.closed) return;
    throw new Error("active event subscription changed during reconnect");
  }

  #pushEvent(connection: Connection, subscription: EventSubscription, event: ProtocolTaskEvent): void {
    if (subscription.push(event)) return;
    connection.close(new Error(`event sequence gap after ${subscription.lastSequence}`));
  }
}
