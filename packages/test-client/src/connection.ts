import { randomUUID } from "node:crypto";
import { createRequire } from "node:module";
import type { Duplex } from "node:stream";
import { Ajv2020, type ValidateFunction } from "ajv/dist/2020.js";
import * as formatsModule from "ajv-formats";
import { decodeTaskEvent } from "./decoders.js";
import type { ErrorEnvelope, IncomingEnvelope, Method, ProtocolTaskEvent, ProtocolVersion, RequestEnvelope, ResponseEnvelope } from "./envelopes.js";
import { ProtocolError } from "./envelopes.js";

export const LEGACY_MAX_MESSAGE_BYTES = 1024 * 1024;
// JSON may escape every byte of a 262144-byte v1.5 preview diff sixfold.
export const MAX_MESSAGE_BYTES = 2 * 1024 * 1024;

const require = createRequire(import.meta.url);
const ajv = new Ajv2020({ allErrors: true, strict: true, strictRequired: false });
const addFormats = formatsModule.default as unknown as (instance: Ajv2020) => void;
addFormats(ajv);
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.1/task"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.1/event"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.1/artifact"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.2/capabilities"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.2/diagnostic"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.2/workspace"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.2/task"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.2/event"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.2/artifact"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.3/capabilities"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.3/diagnostic"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.3/test"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.3/task"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.3/event"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.3/artifact"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/capabilities"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/diagnostic"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/test"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/coverage"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/task"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/event"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.4/artifact"));
for (const name of ["capabilities", "diagnostic", "test", "coverage", "test-generation", "task", "event", "artifact"]) {
  ajv.addSchema(require(`@unit-test-ide/protocol-schema/v1.5/${name}`));
}
for (const name of ["capabilities", "diagnostic", "test", "coverage", "test-generation", "task", "event", "artifact"]) {
  ajv.addSchema(require(`@unit-test-ide/protocol-schema/v1.6/${name}`));
}
const validators: Record<ProtocolVersion, ValidateFunction> = {
  "1.0": ajv.compile(require("@unit-test-ide/protocol-schema/v1/message")),
  "1.1": ajv.compile(require("@unit-test-ide/protocol-schema/v1.1/message")),
  "1.2": ajv.compile(require("@unit-test-ide/protocol-schema/v1.2/message")),
  "1.3": ajv.compile(require("@unit-test-ide/protocol-schema/v1.3/message")),
  "1.4": ajv.compile(require("@unit-test-ide/protocol-schema/v1.4/message")),
  "1.5": ajv.compile(require("@unit-test-ide/protocol-schema/v1.5/message")),
  "1.6": ajv.compile(require("@unit-test-ide/protocol-schema/v1.6/message"))
};

type Pending = {
  version: ProtocolVersion;
  method: Method;
  acceptLegacyUnsupportedProtocol: boolean;
  offeredProtocolVersions: ProtocolVersion[];
  onResponse?: (payload: Record<string, unknown>) => void;
  onError?: (error: ProtocolError) => void;
  resolve: (payload: Record<string, unknown>) => void;
  reject: (error: Error) => void;
};

export interface RequestOptions {
  onResponse?: (payload: Record<string, unknown>) => void;
  onError?: (error: ProtocolError) => void;
}

export class Connection {
  readonly #pending = new Map<string, Pending>();
  readonly #eventListeners = new Set<(event: ProtocolTaskEvent) => void>();
  readonly #closeListeners = new Set<(error: Error) => void>();
  #buffer = Buffer.alloc(0);
  #closed = false;
  #closeError: Error | undefined;
  #legacyHandshakeCeiling = Number.POSITIVE_INFINITY;
  #negotiatedVersion: ProtocolVersion | undefined;

  get closed(): boolean { return this.#closed; }

  constructor(private readonly stream: Duplex) {
    stream.on("data", (chunk: Buffer | string) => this.#onData(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk)));
    stream.on("end", () => this.#closeWithError(new Error("service connection ended")));
    stream.on("error", (error) => this.#closeWithError(error));
    stream.on("close", () => this.#closeWithError(new Error("service connection closed")));
  }

  request(
    version: ProtocolVersion,
    method: Method,
    payload: Record<string, unknown>,
    options: RequestOptions = {}
  ): Promise<Record<string, unknown>> {
    if (this.#closed) return Promise.reject(new Error("service connection is closed"));
    const messageId = randomUUID().replaceAll("-", "");
    const request: RequestEnvelope = {
      protocolVersion: version,
      kind: "request",
      messageId,
      method,
      sentAt: new Date().toISOString(),
      payload
    };
    const encoded = Buffer.from(`${JSON.stringify(request)}\n`, "utf8");
    const limit = Math.min(messageLimitForVersion(version), messageLimitForVersion(this.#negotiatedVersion ?? version));
    if (encoded.byteLength - 1 > limit) {
      return Promise.reject(protocolLineLimitError(limit));
    }
    const validator = validators[version];
    if (!validator(request)) {
      return Promise.reject(new Error(`invalid protocol request: ${ajv.errorsText(validator.errors)}`));
    }
    const handshakeAttempt = method === "handshake";
    const acceptLegacyUnsupportedProtocol = handshakeAttempt
      && version !== "1.0"
      && protocolRank(version) < this.#legacyHandshakeCeiling;
    if (handshakeAttempt) this.#legacyHandshakeCeiling = Number.NEGATIVE_INFINITY;
    const offeredProtocolVersions = method === "handshake" && Array.isArray(payload.supportedProtocolVersions)
      ? payload.supportedProtocolVersions.filter(isProtocolVersion)
      : [];
    return new Promise((resolve, reject) => {
      this.#pending.set(messageId, {
        version,
        method,
        acceptLegacyUnsupportedProtocol,
        offeredProtocolVersions,
        onResponse: options.onResponse,
        onError: options.onError,
        resolve,
        reject
      });
      this.stream.write(encoded, (error) => {
        if (!error) return;
        this.#closeWithError(error);
      });
    });
  }

  onEvent(listener: (event: ProtocolTaskEvent) => void): () => void {
    if (this.#closed) return () => {};
    this.#eventListeners.add(listener);
    return () => this.#eventListeners.delete(listener);
  }

  onClose(listener: (error: Error) => void): () => void {
    if (this.#closed) {
      listener(this.#closeError ?? new Error("service connection is closed"));
      return () => {};
    }
    this.#closeListeners.add(listener);
    return () => this.#closeListeners.delete(listener);
  }

  close(error = new Error("service connection is closed")): void {
    this.#closeWithError(error);
  }

  #onData(chunk: Buffer): void {
    if (this.#closed) return;
    this.#buffer = Buffer.concat([this.#buffer, chunk]);
    for (;;) {
      const newline = this.#buffer.indexOf(0x0a);
      if (newline < 0) break;
      let line = this.#buffer.subarray(0, newline);
      this.#buffer = this.#buffer.subarray(newline + 1);
      if (line.at(-1) === 0x0d) line = line.subarray(0, -1);
      const bufferedLimit = this.#bufferLimit();
      if (line.byteLength > bufferedLimit) {
        this.#closeWithError(protocolLineLimitError(bufferedLimit));
        return;
      }
      if (!this.#onLine(line)) return;
    }
    const bufferedBodyBytes = this.#buffer.at(-1) === 0x0d ? this.#buffer.byteLength - 1 : this.#buffer.byteLength;
    const bufferedLimit = this.#bufferLimit();
    if (bufferedBodyBytes > bufferedLimit) {
      this.#closeWithError(protocolLineLimitError(bufferedLimit));
    }
  }

  #bufferLimit(): number {
    if (this.#negotiatedVersion !== undefined) return messageLimitForVersion(this.#negotiatedVersion);
    for (const pending of this.#pending.values()) {
      if (pending.version === "1.5" || pending.version === "1.6") return MAX_MESSAGE_BYTES;
    }
    return LEGACY_MAX_MESSAGE_BYTES;
  }

  #onLine(line: Buffer): boolean {
    let value: unknown;
    try {
      value = JSON.parse(line.toString("utf8"));
    } catch {
      this.#closeWithError(new Error("service returned invalid JSON"));
      return false;
    }
    if (!value || typeof value !== "object") {
      this.#closeWithError(new Error("service returned invalid protocol message"));
      return false;
    }
    const version = (value as { protocolVersion?: unknown }).protocolVersion;
    if (!isProtocolVersion(version)) {
      this.#closeWithError(new Error("service returned an unsupported protocol version"));
      return false;
    }
    const requestId = (value as { requestId?: unknown }).requestId;
    const pendingVersion = typeof requestId === "string" ? this.#pending.get(requestId)?.version : undefined;
    const contextVersion = this.#negotiatedVersion ?? pendingVersion ?? version;
    const limit = Math.min(messageLimitForVersion(version), messageLimitForVersion(contextVersion));
    if (line.byteLength > limit) {
      this.#closeWithError(protocolLineLimitError(limit));
      return false;
    }
    const validator = validators[version];
    if (!validator(value)) {
      const eventName = isSafeProtocolToken((value as { event?: unknown }).event)
        ? (value as { event: string }).event
        : "unknown";
      if (process.env.UT_DEBUG_PROCESS_HOST_FAILURES === "1" && eventName === "coverage.run.finished") {
        const payload = (value as { payload?: unknown }).payload;
        const payloadRecord = payload && typeof payload === "object" ? payload as Record<string, unknown> : undefined;
        process.stderr.write(`protocol invalid coverage event keys[${payloadRecord ? Object.keys(payloadRecord).sort().join(",") : "none"}] outcome[${String(payloadRecord?.outcome ?? "missing")}]\n`);
      }
      const keywords = [...new Set((validator.errors ?? [])
        .map((error) => error.keyword)
        .map((keyword) => keyword.toLowerCase())
        .filter(isSafeProtocolToken))]
        .sort()
        .join(",") || "unknown";
      const properties = [...new Set((validator.errors ?? [])
        .flatMap((error) => {
          const params = error.params as Record<string, unknown> | undefined;
          return [params?.missingProperty, params?.additionalProperty];
        })
        .filter(isSafeProtocolToken)
        .map((property) => property.toLowerCase()))]
        .sort()
        .join(",") || "unknown";
      const paths = [...new Set((validator.errors ?? [])
        .map((error) => error.instancePath)
        .filter((path): path is string => /^\/?[a-zA-Z0-9_./-]*$/u.test(path)))]
        .sort()
        .join(",") || "unknown";
      this.#closeWithError(new Error(
        `service returned invalid protocol message [event=${eventName};keywords=${keywords};properties=${properties};paths=${paths}]: ${ajv.errorsText(validator.errors)}`,
      ));
      return false;
    }
    const message = value as IncomingEnvelope;
    if (message.kind === "event") {
      if (this.#negotiatedVersion === undefined) {
        this.#closeWithError(new Error("service returned an event before protocol negotiation"));
        return false;
      }
      if (message.protocolVersion !== this.#negotiatedVersion) {
        this.#closeWithError(new Error("event protocol version does not match the negotiated session"));
        return false;
      }
      let event: ProtocolTaskEvent;
      try {
        event = decodeTaskEvent(message);
      } catch (error) {
        this.#closeWithError(error instanceof Error ? error : new Error(String(error)));
        return false;
      }
      for (const listener of [...this.#eventListeners]) listener(event);
      return true;
    }
    const pending = this.#pending.get(message.requestId);
    if (!pending) return true;
    const isAllowedLegacyHandshakeError = pending.acceptLegacyUnsupportedProtocol
      && pending.method === "handshake"
      && message.kind === "error"
      && message.protocolVersion === "1.0"
      && message.error.code === "UNSUPPORTED_PROTOCOL";
    const isAllowedNegotiatedHandshakeResponse = pending.method === "handshake"
      && message.kind === "response"
      && pending.offeredProtocolVersions.includes(message.protocolVersion)
      && message.payload.negotiatedProtocolVersion === message.protocolVersion
      && protocolRank(message.protocolVersion) <= protocolRank(pending.version);
    if (message.protocolVersion !== pending.version && !isAllowedLegacyHandshakeError && !isAllowedNegotiatedHandshakeResponse) {
      this.#closeWithError(new Error("response protocol version does not match request"));
      return false;
    }
    if (isAllowedLegacyHandshakeError) {
      this.#legacyHandshakeCeiling = protocolRank(pending.version);
    }
    this.#pending.delete(message.requestId);
    if (message.kind === "error") {
      const failure = message as ErrorEnvelope;
      const protocolError = new ProtocolError(failure.error.code, failure.error.message, failure.error.retryable);
      try {
        pending.onError?.(protocolError);
      } catch (error) {
        pending.reject(error instanceof Error ? error : new Error(String(error)));
        return true;
      }
      pending.reject(protocolError);
      return true;
    }
    const response = message as ResponseEnvelope;
    if (response.method !== pending.method) {
      pending.reject(new Error("response method does not match request"));
      return true;
    }
    if (pending.method === "handshake") this.#negotiatedVersion = response.protocolVersion;
    try {
      pending.onResponse?.(response.payload);
    } catch (error) {
      pending.reject(error instanceof Error ? error : new Error(String(error)));
      return true;
    }
    pending.resolve(response.payload);
    return true;
  }

  #closeWithError(error: Error): void {
    if (this.#closed) return;
    this.#closed = true;
    this.#closeError = error;
    if (process.env.UT_DEBUG_PROCESS_HOST_FAILURES === "1") {
      process.stderr.write(`protocol client close: ${error.message}\n`);
    }
    for (const pending of this.#pending.values()) pending.reject(error);
    this.#pending.clear();
    for (const listener of [...this.#closeListeners]) listener(error);
    this.#eventListeners.clear();
    this.#closeListeners.clear();
    // Preserve the initiating protocol error so callers can distinguish a
    // deliberate fail-closed shutdown (for example an event sequence gap)
    // from an unannounced peer disconnect.
    this.stream.destroy(error);
  }
}

function isProtocolVersion(value: unknown): value is ProtocolVersion {
  return value === "1.0" || value === "1.1" || value === "1.2" || value === "1.3" || value === "1.4" || value === "1.5" || value === "1.6";
}

function protocolRank(version: ProtocolVersion): number {
  return { "1.0": 0, "1.1": 1, "1.2": 2, "1.3": 3, "1.4": 4, "1.5": 5, "1.6": 6 }[version];
}

function messageLimitForVersion(version: ProtocolVersion): number {
  return version === "1.5" || version === "1.6" ? MAX_MESSAGE_BYTES : LEGACY_MAX_MESSAGE_BYTES;
}

function protocolLineLimitError(limit: number): Error {
  return new Error(`protocol line exceeds the ${limit === MAX_MESSAGE_BYTES ? "2" : "1"} MiB limit`);
}

function isSafeProtocolToken(value: unknown): value is string {
  return typeof value === "string" && /^[a-z0-9._-]+$/u.test(value);
}
