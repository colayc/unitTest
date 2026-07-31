import type { TaskEvent, TaskEventV12, TaskEventV13 } from "@unit-test-ide/protocol-models";

export type ProtocolVersion = "1.0" | "1.1" | "1.2" | "1.3";
export type Method =
  | "handshake"
  | "capabilities/get"
  | "shutdown"
  | "tasks/start"
  | "tasks/get"
  | "tasks/list"
  | "tasks/cancel"
  | "events/subscribe"
  | "artifacts/list"
  | "artifacts/read"
  | "workspace/inspect"
  | "cmake/targets/list"
  | "tests/catalog/get"
  | "tests/runs/get"
  | "tests/runs/list";

export type ProtocolTaskEvent = TaskEvent | TaskEventV12 | TaskEventV13;

export interface RequestEnvelope {
  protocolVersion: ProtocolVersion;
  kind: "request";
  messageId: string;
  method: Method;
  sentAt: string;
  payload: Record<string, unknown>;
}

export interface ResponseEnvelope {
  protocolVersion: ProtocolVersion;
  kind: "response";
  messageId: string;
  requestId: string;
  method: Method;
  sentAt: string;
  payload: Record<string, unknown>;
}

export interface ErrorEnvelope {
  protocolVersion: ProtocolVersion;
  kind: "error";
  messageId: string;
  requestId: string;
  sentAt: string;
  error: { code: string; message: string; retryable: boolean };
}

export type IncomingEnvelope = ResponseEnvelope | ErrorEnvelope | ProtocolTaskEvent;

export class ProtocolError extends Error {
  constructor(readonly code: string, message: string, readonly retryable: boolean) {
    super(message);
    this.name = "ProtocolError";
  }
}
