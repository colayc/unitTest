import { randomUUID } from "node:crypto";
import { once } from "node:events";
import { createRequire } from "node:module";
import net from "node:net";
import type { Duplex } from "node:stream";
import { Ajv2020 } from "ajv/dist/2020.js";
import * as formatsModule from "ajv-formats";
import type { Capabilities } from "@unit-test-ide/protocol-models";
import type { ErrorEnvelope, IncomingEnvelope, Method, RequestEnvelope, ResponseEnvelope } from "./envelopes.js";
import { ProtocolError } from "./envelopes.js";

export const MAX_MESSAGE_BYTES = 1024 * 1024;
const require = createRequire(import.meta.url);
const ajv = new Ajv2020({ allErrors: true, strict: true });
const addFormats = formatsModule.default as unknown as (instance: Ajv2020) => void;
addFormats(ajv);
const validateMessage = ajv.compile(require("@unit-test-ide/protocol-schema/v1/message"));
const validateCapabilities = ajv.compile(require("@unit-test-ide/protocol-schema/v1/capabilities"));
type Pending = { method: Method; resolve: (payload: Record<string, unknown>) => void; reject: (error: Error) => void };

export class ProtocolClient {
  static attach(stream: Duplex): ProtocolClient { return new ProtocolClient(stream); }
  static async connect(endpoint: string): Promise<ProtocolClient> {
    const socket = net.createConnection(endpoint);
    await once(socket, "connect");
    return new ProtocolClient(socket);
  }

  readonly #pending = new Map<string, Pending>();
  #buffer = Buffer.alloc(0);
  #authenticated = false;
  #closed = false;

  private constructor(private readonly stream: Duplex) {
    stream.on("data", (chunk: Buffer | string) => this.#onData(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk)));
    stream.on("error", (error) => this.#failAll(error));
    stream.on("close", () => this.#failAll(new Error("service connection closed")));
  }

  async handshake(token: string, clientName: string, clientVersion: string): Promise<{ negotiatedProtocolVersion: "1.0"; serviceVersion: string }> {
    const payload = await this.#request("handshake", { token, clientName, clientVersion });
    if (payload.negotiatedProtocolVersion !== "1.0" || typeof payload.serviceVersion !== "string") throw new Error("invalid handshake response");
    this.#authenticated = true;
    return payload as { negotiatedProtocolVersion: "1.0"; serviceVersion: string };
  }

  async getCapabilities(): Promise<Capabilities> {
    this.#requireAuthentication();
    const payload = await this.#request("capabilities/get", {});
    if (!validateCapabilities(payload)) throw new Error(`invalid capabilities response: ${ajv.errorsText(validateCapabilities.errors)}`);
    return payload as unknown as Capabilities;
  }

  async shutdown(): Promise<void> { this.#requireAuthentication(); await this.#request("shutdown", {}); }
  close(): void { if (!this.#closed) { this.#closed = true; this.stream.destroy(); } }

  #requireAuthentication(): void { if (!this.#authenticated) throw new Error("handshake has not completed"); }

  #request(method: Method, payload: Record<string, unknown>): Promise<Record<string, unknown>> {
    if (this.#closed) return Promise.reject(new Error("service connection is closed"));
    const messageId = randomUUID().replaceAll("-", "");
    const request: RequestEnvelope = { protocolVersion: "1.0", kind: "request", messageId, method, sentAt: new Date().toISOString(), payload };
    return new Promise((resolve, reject) => {
      this.#pending.set(messageId, { method, resolve, reject });
      this.stream.write(`${JSON.stringify(request)}\n`, (error) => { if (error) { this.#pending.delete(messageId); reject(error); } });
    });
  }

  #onData(chunk: Buffer): void {
    this.#buffer = Buffer.concat([this.#buffer, chunk]);
    for (;;) {
      const newline = this.#buffer.indexOf(0x0a);
      if (newline < 0) break;
      const line = this.#buffer.subarray(0, newline);
      this.#buffer = this.#buffer.subarray(newline + 1);
      if (line.byteLength > MAX_MESSAGE_BYTES) { this.#failAll(new Error("protocol line exceeds the 1 MiB limit")); this.close(); return; }
      this.#onLine(line.toString("utf8"));
    }
    if (this.#buffer.byteLength > MAX_MESSAGE_BYTES) { this.#failAll(new Error("protocol line exceeds the 1 MiB limit")); this.close(); }
  }

  #onLine(line: string): void {
    let value: unknown;
    try { value = JSON.parse(line); } catch { this.#failAll(new Error("service returned invalid JSON")); this.close(); return; }
    if (!validateMessage(value)) { this.#failAll(new Error(`service returned invalid protocol message: ${ajv.errorsText(validateMessage.errors)}`)); this.close(); return; }
    const message = value as IncomingEnvelope;
    const pending = this.#pending.get(message.requestId);
    if (!pending) return;
    this.#pending.delete(message.requestId);
    if (message.kind === "error") {
      const failure = message as ErrorEnvelope;
      pending.reject(new ProtocolError(failure.error.code, failure.error.message, failure.error.retryable));
      return;
    }
    const response = message as ResponseEnvelope;
    if (response.method !== pending.method) { pending.reject(new Error("response method does not match request")); return; }
    pending.resolve(response.payload);
  }

  #failAll(error: Error): void {
    for (const pending of this.#pending.values()) pending.reject(error);
    this.#pending.clear();
  }
}
