# ADR 0001: Local IPC and protocol v1

## Status

Accepted.

## Decision

The test service runs as a per-user Go service. It listens on a Named Pipe on Windows and a Unix Socket with mode `0600` on Linux. Messages use NDJSON framing with a 1 MiB line limit.

Each client must send `handshake` first. Its token is supplied through a file owned by the current user. Before writing the token, the launcher invokes `unit-test-service --prepare-token-file <path>`; Go creates the empty file atomically with mode `0600` on Unix or a protected owner-only DACL on Windows. The launcher then writes to that existing file without replacing it. The service independently rejects symbolic links and non-owner access, bounds the file to 4 KiB, and must delete the same file it opened before startup can succeed. The protocol version is `1.0`.

The protocol schema defines the error codes `INVALID_MESSAGE`, `UNSUPPORTED_PROTOCOL`, `AUTH_REQUIRED`, `AUTH_FAILED`, and `METHOD_NOT_FOUND`. The supported request methods are `handshake`, `capabilities/get`, and `shutdown`.

A disconnect ends only the disconnected client session; it does not end the service process.
