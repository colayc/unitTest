import { randomUUID } from "node:crypto";
import { createRequire } from "node:module";
import type { Duplex } from "node:stream";
import { Ajv2020, type ValidateFunction } from "ajv/dist/2020.js";
import * as formatsModule from "ajv-formats";
import type { TaskEvent } from "@unit-test-ide/protocol-models";
import { decodeTaskEvent } from "./decoders.js";
import type { ErrorEnvelope, IncomingEnvelope, Method, ProtocolVersion, RequestEnvelope, ResponseEnvelope } from "./envelopes.js";
import { ProtocolError } from "./envelopes.js";

export const MAX_MESSAGE_BYTES = 1024 * 1024;

const require = createRequire(import.meta.url);
const ajv = new Ajv2020({ allErrors: true, strict: true, strictRequired: false });
const addFormats = formatsModule.default as unknown as (instance: Ajv2020) => void;
addFormats(ajv);
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.1/task"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.1/event"));
ajv.addSchema(require("@unit-test-ide/protocol-schema/v1.1/artifact"));
const validators: Record<ProtocolVersion, ValidateFunction> = {
  "1.0": ajv.compile(require("@unit-test-ide/protocol-schema/v1/message")),
  "1.1": ajv.compile(require("@unit-test-ide/protocol-schema/v1.1/message"))
};

type Pending = {
  version: ProtocolVersion;
  method: Method;
  onResponse?: (payload: Record<string, unknown>) => void;
  onError?: (error: ProtocolError) => void;
  resolve: (payload: Record<string, unknown>) => void;
  reject: (error: Error) => void;
};

export class Connection {
  readonly #pending = new Map<string, Pending>();
  readonly #eventListeners = new Set<(event: TaskEvent) => void>();
  readonly #closeListeners = new Set<(error: Error) => void>();
  #buffer = Buffer.alloc(0);
  #closed = false;
  #closeError: Error | undefined;

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
    onResponse?: (payload: Record<string, unknown>) => void,
    onError?: (error: ProtocolError) => void
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
    const validator = validators[version];
    if (!validator(request)) {
      return Promise.reject(new Error(`invalid protocol request: ${ajv.errorsText(validator.errors)}`));
    }
    const encoded = Buffer.from(`${JSON.stringify(request)}\n`, "utf8");
    if (encoded.byteLength - 1 > MAX_MESSAGE_BYTES) {
      const failure = new Error("protocol line exceeds the 1 MiB limit");
      this.#closeWithError(failure);
      return Promise.reject(failure);
    }
    return new Promise((resolve, reject) => {
      this.#pending.set(messageId, { version, method, onResponse, onError, resolve, reject });
      this.stream.write(encoded, (error) => {
        if (!error) return;
        this.#closeWithError(error);
      });
    });
  }

  onEvent(listener: (event: TaskEvent) => void): () => void {
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
      if (line.byteLength > MAX_MESSAGE_BYTES) {
        this.#closeWithError(new Error("protocol line exceeds the 1 MiB limit"));
        return;
      }
      if (!this.#onLine(line.toString("utf8"))) return;
    }
    const bufferedBodyBytes = this.#buffer.at(-1) === 0x0d ? this.#buffer.byteLength - 1 : this.#buffer.byteLength;
    if (bufferedBodyBytes > MAX_MESSAGE_BYTES) {
      this.#closeWithError(new Error("protocol line exceeds the 1 MiB limit"));
    }
  }

  #onLine(line: string): boolean {
    let value: unknown;
    try {
      value = JSON.parse(line);
    } catch {
      this.#closeWithError(new Error("service returned invalid JSON"));
      return false;
    }
    if (!value || typeof value !== "object") {
      this.#closeWithError(new Error("service returned invalid protocol message"));
      return false;
    }
    const version = (value as { protocolVersion?: unknown }).protocolVersion;
    if (version !== "1.0" && version !== "1.1") {
      this.#closeWithError(new Error("service returned an unsupported protocol version"));
      return false;
    }
    const validator = validators[version];
    if (!validator(value)) {
      this.#closeWithError(new Error(`service returned invalid protocol message: ${ajv.errorsText(validator.errors)}`));
      return false;
    }
    const message = value as IncomingEnvelope;
    if (message.kind === "event") {
      let event: TaskEvent;
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
    if (message.protocolVersion !== pending.version) {
      this.#closeWithError(new Error("response protocol version does not match request"));
      return false;
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
    for (const pending of this.#pending.values()) pending.reject(error);
    this.#pending.clear();
    for (const listener of [...this.#closeListeners]) listener(error);
    this.#eventListeners.clear();
    this.#closeListeners.clear();
    this.stream.destroy();
  }
}
