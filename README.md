# C/C++ Unit Test IDE

Phase 1 provides the versioned protocol, reusable TypeScript client, and local Go service skeleton. It does not execute workspace code, CMake, compilers, or tests.

## Prerequisites

- Node.js 24.18.0
- pnpm 11.4.0 through Corepack
- Go 1.26.5

## Setup

```sh
corepack enable
corepack prepare pnpm@11.4.0 --activate
pnpm install --frozen-lockfile
```

## Verification

```sh
pnpm check:protocol-generated
pnpm build
pnpm test
pnpm test:e2e
```

Protocol models are generated from `packages/protocol-schema/schema/v1`. Generated TypeScript and Go files are committed; edit the Schema and run `pnpm generate:protocol` instead of editing generated files.

The service listens on a random per-user Windows Named Pipe or a Linux Unix Socket with mode `0600`. Every connection must complete the token handshake before using another method. Authentication token files must be owned by the current user and grant access only to that user: Unix uses owner-only mode bits, while Windows uses a protected owner-only DACL. Before writing the token, the launcher runs `unit-test-service --prepare-token-file <path>` so the Go binary creates the empty file with platform-native owner-only permissions. The service validates the file independently and deletes it after consuming the token.
