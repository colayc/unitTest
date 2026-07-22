import { createHash } from "node:crypto";
import { once } from "node:events";
import { createRequire } from "node:module";
import net from "node:net";
import type { Duplex } from "node:stream";
import { Ajv2020, type ValidateFunction } from "ajv/dist/2020.js";
import * as formatsModule from "ajv-formats";
import type {
  ArtifactMetadata,
  Capabilities,
  CapabilitiesV11,
  TaskEvent,
  TaskSnapshot
} from "@unit-test-ide/protocol-models";
import { Connection, MAX_MESSAGE_BYTES } from "./connection.js";
import { decodeArtifactMetadata, decodeTaskSnapshot } from "./decoders.js";
import type { ProtocolVersion } from "./envelopes.js";
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
export interface PageInput { cursor?: string; limit?: number }
export interface TaskPage { items: TaskSnapshot[]; nextCursor?: string }
export interface ArtifactPage { items: ArtifactMetadata[]; nextCursor?: string }
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

const validateHandshakeV10 = payloadAjv.compile({
  type: "object",
  additionalProperties: false,
  required: ["negotiatedProtocolVersion", "serviceVersion"],
  properties: {
    negotiatedProtocolVersion: { const: "1.0" },
    serviceVersion: { type: "string", minLength: 1 }
  }
});
const validateHandshakeV11 = payloadAjv.compile({
  type: "object",
  additionalProperties: false,
  required: ["negotiatedProtocolVersion", "serviceVersion"],
  properties: {
    negotiatedProtocolVersion: { enum: ["1.0", "1.1"] },
    serviceVersion: { type: "string", minLength: 1 }
  }
});
const validateCapabilitiesV10 = payloadAjv.compile(require("@unit-test-ide/protocol-schema/v1/capabilities"));
const validateCapabilitiesV11 = payloadAjv.compile(require("@unit-test-ide/protocol-schema/v1.1/capabilities"));
const validateTask = payloadAjv.getSchema("urn:unit-test-ide:protocol:v1.1:task") as ValidateFunction;
const validateTaskPage = payloadAjv.compile({
  type: "object",
  additionalProperties: false,
  required: ["items"],
  properties: {
    items: { type: "array", items: { $ref: "urn:unit-test-ide:protocol:v1.1:task" } },
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
    const result = await this.#authenticate(this.#connection, credentials, this.#negotiatedVersion === undefined);
    this.#credentials = credentials;
    this.#negotiatedVersion = result.negotiatedProtocolVersion;
    return result;
  }

  async getCapabilities(): Promise<Capabilities | CapabilitiesV11> {
    const version = this.#requireAuthentication();
    const payload = await this.#connection.request(version, "capabilities/get", {});
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

  async startTask(input: StartTaskInput): Promise<TaskSnapshot> {
    const payload = await this.#requestV11("tasks/start", { ...input });
    validatePayload("tasks/start", validateTask, payload);
    return decodeTaskSnapshot(payload);
  }

  async getTask(taskId: string): Promise<TaskSnapshot> {
    const payload = await this.#requestV11("tasks/get", { taskId });
    validatePayload("tasks/get", validateTask, payload);
    return decodeTaskSnapshot(payload);
  }

  async listTasks(input: PageInput = {}): Promise<TaskPage> {
    const payload = await this.#requestV11("tasks/list", { ...input });
    validatePayload("tasks/list", validateTaskPage, payload);
    return {
      items: (payload.items as Record<string, unknown>[]).map(decodeTaskSnapshot),
      ...(typeof payload.nextCursor === "string" ? { nextCursor: payload.nextCursor } : {})
    };
  }

  async cancelTask(taskId: string): Promise<TaskSnapshot> {
    const payload = await this.#requestV11("tasks/cancel", { taskId });
    validatePayload("tasks/cancel", validateTask, payload);
    return decodeTaskSnapshot(payload);
  }

  async subscribeEvents(afterSequence: number): Promise<EventSubscription> {
    this.#requireV11();
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
      await connection.request("1.1", "events/subscribe", { afterSequence }, {
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
    const payload = await this.#requestV11("artifacts/list", { taskId, ...input });
    validatePayload("artifacts/list", validateArtifactPage, payload);
    return {
      items: (payload.items as Record<string, unknown>[]).map(decodeArtifactMetadata),
      ...(typeof payload.nextCursor === "string" ? { nextCursor: payload.nextCursor } : {})
    };
  }

  async readArtifact(artifactId: string): Promise<Uint8Array> {
    this.#requireV11();
    const hash = createHash("sha256");
    let result: Buffer | undefined;
    let offset = 0;
    let expectedSize: number | undefined;
    let expectedDigest: string | undefined;
    for (;;) {
      const payload = await this.#requestV11("artifacts/read", { artifactId, offset, length: 65_536 });
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
      if (subscription && negotiated.negotiatedProtocolVersion !== "1.1") {
        throw new ProtocolError("PROTOCOL_FEATURE_UNAVAILABLE", "protocol 1.1 was not negotiated", false);
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
        await reconnectConnection.request("1.1", "events/subscribe", { afterSequence: requestedAfterSequence }, {
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

  async #authenticate(
    connection: Connection,
    credentials: Credentials,
    allowLegacyHandshakeError = true
  ): Promise<HandshakeResult> {
    let payload: Record<string, unknown>;
    try {
      payload = await connection.request("1.1", "handshake", {
        ...credentials,
        supportedProtocolVersions: ["1.1", "1.0"]
      }, { allowLegacyHandshakeError });
    } catch (error) {
      if (!(error instanceof ProtocolError) || error.code !== "UNSUPPORTED_PROTOCOL") throw error;
      payload = await connection.request("1.0", "handshake", { ...credentials });
      validatePayload("handshake", validateHandshakeV10, payload);
      return {
        negotiatedProtocolVersion: payload.negotiatedProtocolVersion as ProtocolVersion,
        serviceVersion: payload.serviceVersion as string
      };
    }
    validatePayload("handshake", validateHandshakeV11, payload);
    return {
      negotiatedProtocolVersion: payload.negotiatedProtocolVersion as ProtocolVersion,
      serviceVersion: payload.serviceVersion as string
    };
  }

  #requireAuthentication(): ProtocolVersion {
    if (!this.#negotiatedVersion) throw new Error("handshake has not completed");
    return this.#negotiatedVersion;
  }

  #requireV11(): void {
    const version = this.#requireAuthentication();
    if (version !== "1.1") {
      throw new ProtocolError("PROTOCOL_FEATURE_UNAVAILABLE", "protocol 1.1 was not negotiated", false);
    }
  }

  async #requestV11(method: Parameters<Connection["request"]>[1], payload: Record<string, unknown>): Promise<Record<string, unknown>> {
    this.#requireV11();
    return this.#connection.request("1.1", method, payload);
  }

  #installConnectionListeners(connection: Connection, eventUnsubscribe?: () => void): void {
    this.#unsubscribeEvent = eventUnsubscribe ?? connection.onEvent((event: TaskEvent) => {
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

  #pushEvent(connection: Connection, subscription: EventSubscription, event: TaskEvent): void {
    if (subscription.push(event)) return;
    connection.close(new Error(`event sequence gap after ${subscription.lastSequence}`));
  }
}
