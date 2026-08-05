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
  Capabilities,
  CapabilitiesV11,
  CapabilitiesV12,
  CapabilitiesV13,
  TargetList,
  TaskSnapshot,
  TaskSnapshotV12,
  TaskSnapshotV13,
  TestCatalog,
  TestRun,
  TestRunPage,
  TestSelection,
  WorkspaceSnapshot
} from "@unit-test-ide/protocol-models";
import { Connection, MAX_MESSAGE_BYTES } from "./connection.js";
import {
  decodeArtifactMetadata,
  decodeArtifactMetadataV12,
  decodeArtifactMetadataV13,
  decodeTargetList,
  decodeTaskSnapshot,
  decodeTaskSnapshotV12,
  decodeTaskSnapshotV13,
  decodeTestCatalog,
  decodeTestRun,
  decodeTestRunPage,
  decodeWorkspaceSnapshot
} from "./decoders.js";
import type { Method, ProtocolTaskEvent, ProtocolVersion } from "./envelopes.js";
import { ProtocolError } from "./envelopes.js";
import { EventSubscription } from "./subscription.js";

export { MAX_MESSAGE_BYTES } from "./connection.js";
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
export interface PageInput { cursor?: string; limit?: number }
export type ProtocolTaskSnapshot = TaskSnapshot | TaskSnapshotV12 | TaskSnapshotV13;
export type ProtocolArtifactMetadata = ArtifactMetadata | ArtifactMetadataV12 | ArtifactMetadataV13;
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
    negotiatedProtocolVersion: { enum: ["1.0", "1.1", "1.2", "1.3", "1.4"] },
    serviceVersion: { type: "string", minLength: 1 }
  }
});
const validateCapabilitiesV10 = payloadAjv.compile(require("@unit-test-ide/protocol-schema/v1/capabilities"));
const validateCapabilitiesV11 = payloadAjv.compile(require("@unit-test-ide/protocol-schema/v1.1/capabilities"));
const validateCapabilitiesV12 = payloadAjv.getSchema("urn:unit-test-ide:protocol:v1.2:capabilities") as ValidateFunction;
const validateCapabilitiesV13 = payloadAjv.getSchema("urn:unit-test-ide:protocol:v1.3:capabilities") as ValidateFunction;
const validateTask = payloadAjv.getSchema("urn:unit-test-ide:protocol:v1.1:task") as ValidateFunction;
const validateTaskV12 = payloadAjv.getSchema("urn:unit-test-ide:protocol:v1.2:task") as ValidateFunction;
const validateTaskV13 = payloadAjv.getSchema("urn:unit-test-ide:protocol:v1.3:task") as ValidateFunction;
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
const validateTestCatalogV13 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.3:test#/$defs/testCatalog"
});
const validateTestRunV13 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.3:test#/$defs/testRun"
});
const validateTestRunPageV13 = payloadAjv.compile({
  $ref: "urn:unit-test-ide:protocol:v1.3:test#/$defs/testRunPage"
});
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
    return result;
  }

  async getCapabilities(): Promise<Capabilities | CapabilitiesV11 | CapabilitiesV12 | CapabilitiesV13> {
    const version = this.#requireAuthentication();
    const payload = await this.#connection.request(version, "capabilities/get", {});
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
    validatePayload("shutdown", validateShutdown, payload);
  }

  async inspectWorkspace(): Promise<WorkspaceSnapshot> {
    const payload = await this.#requestV12("workspace/inspect", {});
    validatePayload("workspace/inspect", validateWorkspaceV12, payload);
    return decodeWorkspaceSnapshot(payload);
  }

  async listCMakeTargets(input: CMakeTargetsInput): Promise<TargetList> {
    this.#requireV12();
    validateCMakeContext(input);
    const payload = await this.#requestV12("cmake/targets/list", { ...input });
    validatePayload("cmake/targets/list", validateTargetListV12, payload);
    return decodeTargetList(payload);
  }

  async startCMakeBuild(input: CMakeBuildInput): Promise<TaskSnapshotV12 | TaskSnapshotV13> {
    const version = this.#requireV12();
    validateCMakeBuildInput(input);
    const payload = await this.#connection.request(version, "tasks/start", { ...input, kind: "cmakeBuild" });
    if (version === "1.3") {
      validatePayload("tasks/start", validateTaskV13, payload);
      return decodeTaskSnapshotV13(payload);
    }
    validatePayload("tasks/start", validateTaskV12, payload);
    return decodeTaskSnapshotV12(payload);
  }

  async discoverTests(input: TestDiscoveryInput): Promise<TaskSnapshotV13> {
    this.#requireV13();
    const payload = await this.#connection.request("1.3", "tasks/start", {
      ...input,
      kind: "testDiscovery"
    });
    validatePayload("tasks/start", validateTaskV13, payload);
    return decodeTaskSnapshotV13(payload);
  }

  async runTests(input: TestRunInput): Promise<TaskSnapshotV13> {
    this.#requireV13();
    const payload = await this.#connection.request("1.3", "tasks/start", {
      ...input,
      kind: "testRun"
    });
    validatePayload("tasks/start", validateTaskV13, payload);
    return decodeTaskSnapshotV13(payload);
  }

  async getTestCatalog(input: CatalogGetInput): Promise<TestCatalog> {
    this.#requireV13();
    const payload = await this.#connection.request("1.3", "tests/catalog/get", { ...input });
    validatePayload("tests/catalog/get", validateTestCatalogV13, payload);
    return decodeTestCatalog(payload);
  }

  async getTestRun(runId: string): Promise<TestRun> {
    this.#requireV13();
    const payload = await this.#connection.request("1.3", "tests/runs/get", { runId });
    validatePayload("tests/runs/get", validateTestRunV13, payload);
    return decodeTestRun(payload);
  }

  async listTestRuns(input: TestRunListInput = {}): Promise<TestRunPage> {
    this.#requireV13();
    const payload = await this.#connection.request("1.3", "tests/runs/list", { ...input });
    validatePayload("tests/runs/list", validateTestRunPageV13, payload);
    return decodeTestRunPage(payload);
  }

  async startTask(input: StartTaskInput): Promise<ProtocolTaskSnapshot> {
    const version = this.#requireTaskProtocol();
    const payload = await this.#connection.request(
      version,
      "tasks/start",
      version === "1.1" ? { ...input } : { ...input, kind: "simulation" }
    );
    return decodeTaskResponse("tasks/start", version, payload);
  }

  async getTask(taskId: string): Promise<ProtocolTaskSnapshot> {
    const { version, payload } = await this.#requestTaskProtocol("tasks/get", { taskId });
    return decodeTaskResponse("tasks/get", version, payload);
  }

  async listTasks(input: PageInput = {}): Promise<TaskPage> {
    const { version, payload } = await this.#requestTaskProtocol("tasks/list", { ...input });
    const validator = version === "1.3"
      ? validateTaskPageV13
      : version === "1.2" ? validateTaskPageV12 : validateTaskPage;
    validatePayload("tasks/list", validator, payload);
    return {
      items: (payload.items as Record<string, unknown>[]).map((item) =>
        version === "1.3"
          ? decodeTaskSnapshotV13(item)
          : version === "1.2" ? decodeTaskSnapshotV12(item) : decodeTaskSnapshot(item)),
      ...(typeof payload.nextCursor === "string" ? { nextCursor: payload.nextCursor } : {})
    };
  }

  async cancelTask(taskId: string): Promise<ProtocolTaskSnapshot> {
    const { version, payload } = await this.#requestTaskProtocol("tasks/cancel", { taskId });
    return decodeTaskResponse("tasks/cancel", version, payload);
  }

  async subscribeEvents(afterSequence: number): Promise<EventSubscription> {
    const version = this.#requireTaskProtocol();
    if (!Number.isSafeInteger(afterSequence) || afterSequence < 0) {
      throw new Error("invalid protocol request: afterSequence must be a non-negative safe integer");
    }
    this.#beginLifecycleOperation("subscribe");
    const connection = this.#connection;
    const subscription = new EventSubscription(afterSequence);
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
            validateSubscriptionAcknowledgement(payload, afterSequence);
            if (this.#closed || connection !== this.#connection) throw new Error("event subscription connection is no longer active");
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
    const validator = version === "1.3"
      ? validateArtifactPageV13
      : version === "1.2" ? validateArtifactPageV12 : validateArtifactPage;
    validatePayload("artifacts/list", validator, payload);
    return {
      items: (payload.items as Record<string, unknown>[]).map((item) =>
        version === "1.3"
          ? decodeArtifactMetadataV13(item)
          : version === "1.2" ? decodeArtifactMetadataV12(item) : decodeArtifactMetadata(item)),
      ...(typeof payload.nextCursor === "string" ? { nextCursor: payload.nextCursor } : {})
    };
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
      if (!chunk.eof) continue;
      if (offset !== expectedSize) throw new Error("artifact size does not match the completed read");
      if (!result || result.byteLength !== expectedSize) throw new Error("artifact size does not match the completed read");
      const actualDigest = hash.digest("hex");
      if (actualDigest !== expectedDigest) throw new Error("artifact SHA-256 does not match metadata");
      return result;
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
    this.#reconnectGeneration++;
    this.#reconnectCandidate?.close();
    this.#reconnectCandidate = undefined;
    this.#removeConnectionListeners();
    this.#activeSubscription?.close();
    this.#connection.close();
  }

  async #authenticate(connection: Connection, credentials: Credentials): Promise<HandshakeResult> {
    const attempts: ReadonlyArray<{
      version: "1.4" | "1.3" | "1.2" | "1.1";
      offered: ProtocolVersion[];
    }> = [
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

  #requireV12(): "1.2" | "1.3" {
    const version = this.#requireAuthentication();
    if (version !== "1.2" && version !== "1.3") {
      throw new ProtocolError("PROTOCOL_FEATURE_UNAVAILABLE", "protocol 1.2 or newer was not negotiated", false);
    }
    return version;
  }

  #requireV13(): void {
    const version = this.#requireAuthentication();
    if (version !== "1.3") {
      throw new ProtocolError("PROTOCOL_FEATURE_UNAVAILABLE", "protocol 1.3 was not negotiated", false);
    }
  }

  async #requestTaskProtocol(
    method: Method,
    payload: Record<string, unknown>
  ): Promise<{ version: TaskProtocolVersion; payload: Record<string, unknown> }> {
    const version = this.#requireTaskProtocol();
    return { version, payload: await this.#connection.request(version, method, payload) };
  }

  async #requestV12(method: Method, payload: Record<string, unknown>): Promise<Record<string, unknown>> {
    const version = this.#requireV12();
    return this.#connection.request(version, method, payload);
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
