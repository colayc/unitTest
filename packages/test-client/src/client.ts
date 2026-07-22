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
import type { ProtocolVersion } from "./envelopes.js";
import { ProtocolError } from "./envelopes.js";
import { EventSubscription } from "./subscription.js";

export { MAX_MESSAGE_BYTES } from "./connection.js";

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

const require = createRequire(import.meta.url);
const payloadAjv = new Ajv2020({ allErrors: true, strict: true, strictRequired: false });
const addFormats = formatsModule.default as unknown as (instance: Ajv2020) => void;
addFormats(payloadAjv);
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.1/task"));
payloadAjv.addSchema(require("@unit-test-ide/protocol-schema/v1.1/artifact"));

const validateHandshake = payloadAjv.compile({
  type: "object",
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

function validatePayload<T>(method: string, validator: ValidateFunction, payload: Record<string, unknown>): T {
  if (!validator(payload)) {
    throw new Error(`invalid ${method} response: ${payloadAjv.errorsText(validator.errors)}`);
  }
  return payload as T;
}

function decodeBase64Url(value: string): Buffer {
  if (!/^(?:[A-Za-z0-9_-]{4})*(?:[A-Za-z0-9_-]{2}|[A-Za-z0-9_-]{3})?$/.test(value)) {
    throw new Error("invalid artifact chunk Base64URL data");
  }
  const decoded = Buffer.from(value, "base64url");
  if (decoded.toString("base64url") !== value) throw new Error("invalid artifact chunk Base64URL data");
  return decoded;
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
  #reconnecting = false;
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

  async getCapabilities(): Promise<Capabilities | CapabilitiesV11> {
    const version = this.#requireAuthentication();
    const payload = await this.#connection.request(version, "capabilities/get", {});
    if (version === "1.1") {
      return validatePayload<CapabilitiesV11>("capabilities/get", validateCapabilitiesV11, payload);
    }
    return validatePayload<Capabilities>("capabilities/get", validateCapabilitiesV10, payload);
  }

  async shutdown(): Promise<void> {
    const version = this.#requireAuthentication();
    const payload = await this.#connection.request(version, "shutdown", {});
    validatePayload("shutdown", validateShutdown, payload);
  }

  async startTask(input: StartTaskInput): Promise<TaskSnapshot> {
    const payload = await this.#requestV11("tasks/start", { ...input });
    return validatePayload<TaskSnapshot>("tasks/start", validateTask, payload);
  }

  async getTask(taskId: string): Promise<TaskSnapshot> {
    const payload = await this.#requestV11("tasks/get", { taskId });
    return validatePayload<TaskSnapshot>("tasks/get", validateTask, payload);
  }

  async listTasks(input: PageInput = {}): Promise<TaskPage> {
    const payload = await this.#requestV11("tasks/list", { ...input });
    return validatePayload<TaskPage>("tasks/list", validateTaskPage, payload);
  }

  async cancelTask(taskId: string): Promise<TaskSnapshot> {
    const payload = await this.#requestV11("tasks/cancel", { taskId });
    return validatePayload<TaskSnapshot>("tasks/cancel", validateTask, payload);
  }

  async subscribeEvents(afterSequence: number): Promise<EventSubscription> {
    const subscription = new EventSubscription(afterSequence);
    const previous = this.#activeSubscription;
    this.#activeSubscription = subscription;
    try {
      const payload = await this.#requestV11("events/subscribe", { afterSequence });
      validatePayload("events/subscribe", validateSubscription, payload);
      previous?.close();
      return subscription;
    } catch (error) {
      if (this.#activeSubscription === subscription) this.#activeSubscription = previous;
      subscription.close();
      throw error;
    }
  }

  async listArtifacts(taskId: string, input: PageInput = {}): Promise<ArtifactPage> {
    const payload = await this.#requestV11("artifacts/list", { taskId, ...input });
    return validatePayload<ArtifactPage>("artifacts/list", validateArtifactPage, payload);
  }

  async readArtifact(artifactId: string): Promise<Uint8Array> {
    this.#requireV11();
    const chunks: Buffer[] = [];
    let offset = 0;
    let expectedSize: number | undefined;
    let expectedDigest: string | undefined;
    for (;;) {
      const payload = await this.#requestV11("artifacts/read", { artifactId, offset, length: 65_536 });
      const chunk = validatePayload<ArtifactChunk>("artifacts/read", validateArtifactChunk, payload);
      const data = decodeBase64Url(chunk.data);
      if (data.byteLength > 65_536 || chunk.nextOffset !== offset + data.byteLength) {
        throw new Error("invalid artifact chunk offset or length");
      }
      if (!chunk.eof && chunk.nextOffset <= offset) throw new Error("invalid artifact chunk: offset did not advance");
      if (expectedSize === undefined) {
        expectedSize = chunk.sizeBytes;
        expectedDigest = chunk.sha256;
      } else if (chunk.sizeBytes !== expectedSize || chunk.sha256 !== expectedDigest) {
        throw new Error("artifact chunk metadata changed during read");
      }
      if (chunk.nextOffset > expectedSize) throw new Error("invalid artifact chunk: offset exceeds declared size");
      chunks.push(data);
      offset = chunk.nextOffset;
      if (!chunk.eof) continue;
      if (offset !== expectedSize) throw new Error("artifact size does not match the completed read");
      const result = Buffer.concat(chunks);
      if (result.byteLength !== expectedSize) throw new Error("artifact size does not match the completed read");
      const actualDigest = createHash("sha256").update(result).digest("hex");
      if (actualDigest !== expectedDigest) throw new Error("artifact SHA-256 does not match metadata");
      return new Uint8Array(result);
    }
  }

  async reconnect(): Promise<void> {
    if (!this.#connector) throw new Error("connection connector is not available; attach() clients cannot reconnect");
    if (!this.#credentials) throw new Error("handshake has not completed");
    if (this.#closed) throw new Error("protocol client is closed");
    if (this.#reconnecting) throw new Error("reconnect is already in progress");
    this.#reconnecting = true;
    const credentials = this.#credentials;
    const subscription = this.#activeSubscription;
    this.#removeConnectionListeners();
    this.#connection.close();
    let candidate: Connection | undefined;
    let candidateEventUnsubscribe: (() => void) | undefined;
    try {
      candidate = new Connection(await this.#connector());
      const negotiated = await this.#authenticate(candidate, credentials);
      if (subscription && negotiated.negotiatedProtocolVersion !== "1.1") {
        throw new ProtocolError("PROTOCOL_FEATURE_UNAVAILABLE", "protocol 1.1 was not negotiated", false);
      }
      if (subscription) {
        candidateEventUnsubscribe = candidate.onEvent((event) => subscription.push(event));
        const payload = await candidate.request("1.1", "events/subscribe", { afterSequence: subscription.lastSequence });
        validatePayload("events/subscribe", validateSubscription, payload);
      }
      this.#connection = candidate;
      this.#negotiatedVersion = negotiated.negotiatedProtocolVersion;
      this.#installConnectionListeners(candidate, candidateEventUnsubscribe);
      candidateEventUnsubscribe = undefined;
    } catch (error) {
      candidateEventUnsubscribe?.();
      candidate?.close();
      throw error;
    } finally {
      this.#reconnecting = false;
    }
  }

  close(): void {
    if (this.#closed) return;
    this.#closed = true;
    this.#removeConnectionListeners();
    this.#activeSubscription?.close();
    this.#connection.close();
  }

  async #authenticate(connection: Connection, credentials: Credentials): Promise<HandshakeResult> {
    let payload: Record<string, unknown>;
    try {
      payload = await connection.request("1.1", "handshake", {
        ...credentials,
        supportedProtocolVersions: ["1.1", "1.0"]
      });
    } catch (error) {
      if (!(error instanceof ProtocolError) || error.code !== "UNSUPPORTED_PROTOCOL") throw error;
      payload = await connection.request("1.0", "handshake", { ...credentials });
    }
    return validatePayload<HandshakeResult>("handshake", validateHandshake, payload);
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
    this.#unsubscribeEvent = eventUnsubscribe ?? connection.onEvent((event: TaskEvent) => this.#activeSubscription?.push(event));
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
}
