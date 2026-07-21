export type Method = "handshake" | "capabilities/get" | "shutdown";
export interface RequestEnvelope { protocolVersion: "1.0"; kind: "request"; messageId: string; method: Method; sentAt: string; payload: Record<string, unknown>; }
export interface ResponseEnvelope { protocolVersion: "1.0"; kind: "response"; messageId: string; requestId: string; method: Method; sentAt: string; payload: Record<string, unknown>; }
export interface ErrorEnvelope { protocolVersion: "1.0"; kind: "error"; messageId: string; requestId: string; sentAt: string; error: { code: string; message: string; retryable: boolean }; }
export type IncomingEnvelope = ResponseEnvelope | ErrorEnvelope;
export class ProtocolError extends Error { constructor(readonly code: string, message: string, readonly retryable: boolean) { super(message); } }
