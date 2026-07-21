# Secure Token File Preparation Design

**Date:** 2026-07-21
**Status:** Awaiting written review

## Context

The TypeScript service probe currently writes the authentication token and then invokes `icacls` on Windows. This has two problems:

1. The token exists before its DACL is restricted.
2. `icacls /grant:r` replaces entries for one trustee but does not guarantee that every other explicit allow ACE is removed. GitHub's Windows runner therefore retained a Local System (`SY`) ACE and the Go service correctly rejected the file.

The documented security contract remains unchanged: the token file is a regular, non-symlink file owned by the current user and accessible only by that user. The service must validate this contract independently before reading and deleting the token.

## Goals

- Create the token file with restrictive permissions before any secret is written.
- Use one launcher flow on Windows and Linux while keeping platform-specific permission code inside Go.
- Preserve the service's existing owner, permission, identity, size, and deletion checks.
- Remove the launcher's dependency on `whoami`, `icacls`, PowerShell, or another shell.
- Fail closed without logging or passing the token through process arguments.

## Non-goals

- Changing the handshake protocol or token format.
- Allowing Local System, Administrators, group, or other-user access to the token file.
- Building a general-purpose file-permission command.
- Moving service lifecycle ownership into the Go process.

## Approaches Considered

### 1. Go-owned secure file creation (selected)

Add a mutually exclusive service command mode:

```text
unit-test-service --prepare-token-file <path>
```

The Go binary creates a new empty file with platform-native owner-only permissions and exits. TypeScript then opens that existing file without create semantics and writes the token before launching the normal service mode.

This keeps native permission logic in Go, creates the file securely before the secret exists, and avoids shell-specific ACL behavior.

### 2. Replace the DACL through PowerShell

PowerShell could construct a new ACL and remove all existing rules. This is smaller initially, but it adds quoting and host-policy dependencies and still requires careful sequencing to avoid writing the secret before hardening the file.

### 3. Permit the `SY` ACE

Allowing Local System would make the GitHub runner pass, but it would weaken and redefine the existing owner-only contract instead of fixing token creation. It is rejected for this phase.

## Command Interface

The executable has two exclusive modes:

- Preparation mode: `--prepare-token-file <path>` only.
- Service mode: both `--endpoint <endpoint>` and `--token-file <path>`.

Preparation mode rejects additional positional arguments, an existing destination, a missing parent directory, or any creation/permission error. It prints no token data. On partial failure it removes only the file instance it created.

Service mode retains its current behavior and error codes. Supplying preparation and service flags together is a usage error with exit code `2`.

## Secure Creation

### Windows

The Go process obtains the current process token SID and builds a protected security descriptor whose DACL contains one allow ACE granting that SID full access. It passes this descriptor to `CreateFile` with `CREATE_NEW`, so the restrictive DACL applies atomically when the empty file is created. The current user is the file owner. The handle is closed before preparation mode exits.

The implementation validates the resulting owner and protected owner-only DACL using the same semantic SID comparison used during service startup. Any mismatch is an error and triggers identity-checked cleanup.

### Linux

The Go process creates the file exclusively with mode `0600`. It verifies that the result is a regular file owned by the effective user with no group or other permission bits, then closes the handle.

## Launcher Data Flow

1. TypeScript creates the temporary working directory and generates the token in memory.
2. It executes the service binary with `--prepare-token-file <path>` and waits for exit code `0`.
3. It opens the existing file without create semantics and writes the token. The open operation must not replace the file or its ACL.
4. It launches normal service mode with `--endpoint` and `--token-file`.
5. The Go service reopens without following symlinks, checks file identity and permissions, reads at most 4096 bytes, and deletes the same file before accepting connections.
6. Existing handshake, capabilities, shutdown, and cleanup behavior continues unchanged.

If any launcher step fails, TypeScript removes its temporary directory and reports process stdout/stderr without including the in-memory token.

## Testing

- A cross-platform Go CLI test proves preparation mode creates an empty file and rejects an existing path.
- Windows tests prove the created file is owned by the current SID, has a protected DACL with only that SID, and does not grant `SY` or `WD` access.
- Linux tests prove mode `0600` and current effective-user ownership.
- CLI tests prove preparation mode is mutually exclusive with service mode.
- The TypeScript E2E test exercises the real preparation command, writes the token to the existing file, authenticates, reads capabilities, and shuts down.
- The full Windows/Ubuntu Actions matrix remains the acceptance gate.

## Documentation Impact

The local IPC decision record and README will state that the launcher asks the Go binary to create the restricted token file before writing the token. The owner-only validation contract does not change.
