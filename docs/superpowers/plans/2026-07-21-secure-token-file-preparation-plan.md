# Secure Token File Preparation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create the authentication token file with owner-only platform permissions before TypeScript writes the secret, then use the existing validated service startup path.

**Architecture:** The Go executable gains a mutually exclusive `--prepare-token-file` mode. Platform files create the empty destination atomically with a Windows protected owner-only DACL or Linux mode `0600`; the TypeScript probe invokes that mode, writes to the existing file, and then launches normal service mode.

**Tech Stack:** Node.js 24.18.0, pnpm 11.4.0, TypeScript 6.0.3, Go 1.26.5, `golang.org/x/sys/windows`, and `golang.org/x/sys/unix`.

## Global Constraints

- Do not add dependencies or change protocol schema version `1.0`.
- Support both Windows and Linux through build-tagged Go files.
- The token file must be owned by the current user and grant access only to that user.
- Apply restrictive permissions atomically before any token bytes are written.
- Never pass the token in process arguments or include it in stdout, stderr, or errors.
- Keep normal startup validation, 4096-byte limit, identity-checked deletion, handshake, capabilities, and shutdown behavior unchanged.
- Reject an existing preparation destination instead of truncating or replacing it.
- Follow test-driven development: run every specified red test before its implementation step.

---

## File Map

- `apps/test-service/cmd/unit-test-service/token_file_prepare.go`: common preparation validation, close, and failure cleanup orchestration.
- `apps/test-service/cmd/unit-test-service/token_file_prepare_windows.go`: atomic Windows creation with a protected owner-only security descriptor.
- `apps/test-service/cmd/unit-test-service/token_file_prepare_unix.go`: exclusive Linux creation with mode `0600`.
- `apps/test-service/cmd/unit-test-service/token_file_prepare_test.go`: cross-platform preparation behavior.
- `apps/test-service/cmd/unit-test-service/main.go`: mutually exclusive preparation and service CLI modes.
- `apps/test-service/cmd/unit-test-service/main_test.go`: CLI dispatch and invalid-combination tests.
- `tools/service-probe/src/probe.ts`: invoke preparation mode before writing the token.
- `tools/service-probe/src/probe.test.ts`: real-binary preparation and end-to-end tests.
- `tools/workspace-smoke/workspace-smoke.test.mjs`: documentation contract test.
- `README.md`: launcher behavior for contributors.
- `docs/decisions/0001-local-ipc-and-protocol-v1.md`: binding security decision.

---

### Task 1: Create an empty owner-only token file atomically

**Files:**
- Create: `apps/test-service/cmd/unit-test-service/token_file_prepare.go`
- Create: `apps/test-service/cmd/unit-test-service/token_file_prepare_windows.go`
- Create: `apps/test-service/cmd/unit-test-service/token_file_prepare_unix.go`
- Create: `apps/test-service/cmd/unit-test-service/token_file_prepare_test.go`

**Interfaces:**
- Consumes: existing `validateTokenFile(file *os.File, info os.FileInfo) error` and `removeSameTokenFile(path string, expected os.FileInfo) error`.
- Produces: `prepareTokenFile(path string) error` for CLI preparation mode and build-tagged `createTokenFile(path string) (*os.File, error)` implementations.

- [ ] **Step 1: Write the failing cross-platform preparation tests**

Create `token_file_prepare_test.go`:

```go
package main

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPrepareTokenFileCreatesEmptyValidatedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := prepareTokenFile(path); err != nil {
		t.Fatal(err)
	}

	info, err := inspectTokenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("prepared token file size = %d, want 0", info.Size())
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("prepared token file mode = %o, want 600", info.Mode().Perm())
	}

	file, err := openTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := validateTokenFile(file, info); err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 0 {
		t.Fatalf("prepared token contents = %q, want empty", raw)
	}
}

func TestPrepareTokenFileRejectsExistingPathWithoutChangingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareTokenFile(path); err == nil {
		t.Fatal("expected an existing token path to be rejected")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "original" {
		t.Fatalf("existing token contents = %q, want original", contents)
	}
}
```

- [ ] **Step 2: Run the focused test and verify RED**

Run from `apps/test-service`:

```powershell
go test ./cmd/unit-test-service -run 'TestPrepareTokenFile' -count=1
```

Expected: build failure with `undefined: prepareTokenFile`.

- [ ] **Step 3: Implement common preparation orchestration**

Create `token_file_prepare.go`:

```go
package main

import (
	"errors"
	"os"
)

func prepareTokenFile(path string) error {
	file, err := createTokenFile(path)
	if err != nil {
		return err
	}
	info, statErr := file.Stat()
	if statErr != nil {
		return errors.Join(statErr, file.Close())
	}
	validationErr := validateTokenFile(file, info)
	closeErr := file.Close()
	if validationErr == nil && closeErr == nil {
		return nil
	}
	cleanupErr := removeSameTokenFile(path, info)
	return errors.Join(validationErr, closeErr, cleanupErr)
}
```

- [ ] **Step 4: Implement atomic Windows creation**

Create `token_file_prepare_windows.go`:

```go
//go:build windows

package main

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func createTokenFile(path string) (*os.File, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	sid := user.User.Sid.String()
	descriptor, err := windows.SecurityDescriptorFromString(
		fmt.Sprintf("O:%sD:P(A;;GA;;;%s)", sid, sid),
	)
	if err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	attributes := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL,
		0,
		attributes,
		windows.CREATE_NEW,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}
```

- [ ] **Step 5: Implement exclusive Linux creation**

Create `token_file_prepare_unix.go`:

```go
//go:build !windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func createTokenFile(path string) (*os.File, error) {
	fd, err := unix.Open(
		path,
		unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0o600,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if err := file.Chmod(0o600); err != nil {
		info, statErr := file.Stat()
		closeErr := file.Close()
		var cleanupErr error
		if statErr == nil {
			cleanupErr = removeSameTokenFile(path, info)
		}
		return nil, errors.Join(err, statErr, closeErr, cleanupErr)
	}
	return file, nil
}
```

- [ ] **Step 6: Format and run the focused tests to verify GREEN**

Run:

```powershell
gofmt -w cmd/unit-test-service/token_file_prepare.go cmd/unit-test-service/token_file_prepare_windows.go cmd/unit-test-service/token_file_prepare_unix.go cmd/unit-test-service/token_file_prepare_test.go
go test ./cmd/unit-test-service -run 'TestPrepareTokenFile' -count=1
```

Expected: both preparation tests pass. On Windows the first test exercises `validateOwnerOnlyDACL`, so any retained `SY`, `WD`, or other allow ACE fails the test.

- [ ] **Step 7: Run the complete Go test package**

Run:

```powershell
go test ./cmd/unit-test-service -count=1
```

Expected: all token consumption and preparation tests pass.

- [ ] **Step 8: Commit Task 1**

```powershell
git add apps/test-service/cmd/unit-test-service/token_file_prepare.go apps/test-service/cmd/unit-test-service/token_file_prepare_windows.go apps/test-service/cmd/unit-test-service/token_file_prepare_unix.go apps/test-service/cmd/unit-test-service/token_file_prepare_test.go
git commit -m "feat: create restricted token files atomically"
```

---

### Task 2: Add the preparation command mode

**Files:**
- Create: `apps/test-service/cmd/unit-test-service/main_test.go`
- Modify: `apps/test-service/cmd/unit-test-service/main.go`

**Interfaces:**
- Consumes: `prepareTokenFile(path string) error` from Task 1.
- Produces: `unit-test-service --prepare-token-file <path>` with success exit code `0`, operational failure `1`, and usage failure `2`.

- [ ] **Step 1: Write failing CLI mode tests**

Create `main_test.go`:

```go
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPrepareTokenFileModeCreatesEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--prepare-token-file", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("run code = %d, stderr = %q", code, stderr.String())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("prepared file size = %d, want 0", info.Size())
	}
}

func TestRunRejectsMixedPreparationAndServiceModes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--prepare-token-file", path,
		"--endpoint", "unused-endpoint",
		"--token-file", "unused-token",
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "cannot be combined") {
		t.Fatalf("stderr = %q, want combination error", stderr.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("mixed mode created token path: %v", err)
	}
}

func TestRunRejectsPositionalArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"unexpected"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "positional arguments") {
		t.Fatalf("stderr = %q, want positional argument error", stderr.String())
	}
}
```

- [ ] **Step 2: Run CLI tests and verify RED**

Run from `apps/test-service`:

```powershell
go test ./cmd/unit-test-service -run 'TestRun' -count=1
```

Expected: `flag provided but not defined: -prepare-token-file`; the positional-argument test also exposes the current unvalidated positional input.

- [ ] **Step 3: Implement exclusive CLI dispatch**

In `main.go`, add the third flag and dispatch immediately after parsing:

```go
endpoint := flags.String("endpoint", "", "local IPC endpoint")
tokenFile := flags.String("token-file", "", "authentication token file")
prepareTokenFilePath := flags.String("prepare-token-file", "", "create an empty owner-only authentication token file")
if err := flags.Parse(args); err != nil {
	return 2
}
if flags.NArg() != 0 {
	fmt.Fprintln(stderr, "positional arguments are not supported")
	return 2
}
if *prepareTokenFilePath != "" {
	if *endpoint != "" || *tokenFile != "" {
		fmt.Fprintln(stderr, "--prepare-token-file cannot be combined with --endpoint or --token-file")
		return 2
	}
	if err := prepareTokenFile(*prepareTokenFilePath); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
if *endpoint == "" || *tokenFile == "" {
	fmt.Fprintln(stderr, "--endpoint and --token-file are required")
	return 2
}
```

Leave the existing service startup below this block unchanged.

- [ ] **Step 4: Format and verify GREEN**

Run:

```powershell
gofmt -w cmd/unit-test-service/main.go cmd/unit-test-service/main_test.go
go test ./cmd/unit-test-service -run 'TestRun' -count=1
go test ./cmd/unit-test-service -count=1
```

Expected: all CLI tests and the complete command package pass.

- [ ] **Step 5: Commit Task 2**

```powershell
git add apps/test-service/cmd/unit-test-service/main.go apps/test-service/cmd/unit-test-service/main_test.go
git commit -m "feat: add token file preparation command"
```

---

### Task 3: Prepare the file before TypeScript writes the token

**Files:**
- Modify: `tools/service-probe/src/probe.ts`
- Modify: `tools/service-probe/src/probe.test.ts`

**Interfaces:**
- Consumes: `unit-test-service --prepare-token-file <path>` from Task 2.
- Produces: `prepareTokenFile(serviceBinary: string, tokenFile: string, token: string): Promise<void>` and the existing `runProbe(serviceBinary: string): Promise<Capabilities>`.

- [ ] **Step 1: Write the failing real-binary preparation test**

Replace `probe.test.ts` with:

```ts
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";
import { prepareTokenFile, runProbe } from "./probe.js";

const root = resolve(import.meta.dirname, "../../..");
const binary = join(root, "build", process.platform === "win32" ? "unit-test-service.exe" : "unit-test-service");

test("prepares the token file before writing the secret", async () => {
  const directory = await mkdtemp(join(tmpdir(), "unit-test-ide-token-"));
  const tokenFile = join(directory, "token");
  const token = "0123456789abcdef0123456789abcdef";
  try {
    await prepareTokenFile(binary, tokenFile, token);
    assert.equal(await readFile(tokenFile, "utf8"), token);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("probe authenticates, reads capabilities, and shuts the service down", async () => {
  const capabilities = await runProbe(binary);
  assert.equal(capabilities.platform, process.platform === "win32" ? "windows" : "linux");
  assert.deepEqual(capabilities.transports, [process.platform === "win32" ? "named-pipe" : "unix-socket"]);
});
```

- [ ] **Step 2: Run E2E and verify RED**

Run from the repository root:

```powershell
pnpm --filter @unit-test-ide/service-probe test:e2e
```

Expected: TypeScript build failure `Module './probe.js' has no exported member 'prepareTokenFile'`.

- [ ] **Step 3: Replace post-write ACL mutation with pre-write preparation**

In `probe.ts`, keep the promisified `execFile`, delete `restrictTokenFile`, and add:

```ts
export async function prepareTokenFile(serviceBinary: string, tokenFile: string, token: string): Promise<void> {
  await execFile(serviceBinary, ["--prepare-token-file", tokenFile], { windowsHide: true });
  await writeFile(tokenFile, token, { flag: "r+" });
}
```

Inside `runProbe`, replace:

```ts
await writeFile(tokenFile, token, { mode: 0o600, flag: "wx" });
await restrictTokenFile(tokenFile);
```

with:

```ts
await prepareTokenFile(serviceBinary, tokenFile, token);
```

Remove the `whoami` and `icacls` calls completely. Do not pass `token` to `execFile` or `spawn`.

- [ ] **Step 4: Build and verify GREEN with the real service**

Run:

```powershell
pnpm --filter @unit-test-ide/service-probe test:e2e
```

Expected: both the preparation test and the handshake/capabilities/shutdown E2E test pass on the current platform.

- [ ] **Step 5: Run TypeScript and Go regression tests**

Run:

```powershell
pnpm build
pnpm test
```

Expected: all workspace, TypeScript, and Go tests pass.

- [ ] **Step 6: Commit Task 3**

```powershell
git add tools/service-probe/src/probe.ts tools/service-probe/src/probe.test.ts
git commit -m "fix: prepare token files before writing secrets"
```

---

### Task 4: Record the secure preparation contract

**Files:**
- Modify: `tools/workspace-smoke/workspace-smoke.test.mjs`
- Modify: `README.md`
- Modify: `docs/decisions/0001-local-ipc-and-protocol-v1.md`

**Interfaces:**
- Consumes: the CLI and launcher behavior from Tasks 1-3.
- Produces: contributor documentation and a smoke test that prevents regression to post-write ACL mutation.

- [ ] **Step 1: Write the failing documentation contract test**

Append to `workspace-smoke.test.mjs`:

```js
test("documentation records pre-write token file preparation", async () => {
  const readme = await readFile("README.md", "utf8");
  const decision = await readFile("docs/decisions/0001-local-ipc-and-protocol-v1.md", "utf8");
  assert.match(readme, /--prepare-token-file/);
  assert.match(decision, /--prepare-token-file/);
  assert.doesNotMatch(readme, /removes inherited Windows permissions after writing/i);
});
```

- [ ] **Step 2: Run the smoke test and verify RED**

Run:

```powershell
pnpm test:workspace
```

Expected: the new test fails because neither document contains `--prepare-token-file`.

- [ ] **Step 3: Update the README**

Replace the final README paragraph with:

```markdown
The service listens on a random per-user Windows Named Pipe or a Linux Unix Socket with mode `0600`. Every connection must complete the token handshake before using another method. Authentication token files must be owned by the current user and grant access only to that user: Unix uses owner-only mode bits, while Windows uses a protected owner-only DACL. Before writing the token, the launcher runs `unit-test-service --prepare-token-file <path>` so the Go binary creates the empty file with platform-native owner-only permissions. The service validates the file independently and deletes it after consuming the token.
```

- [ ] **Step 4: Update the IPC decision**

Replace the token-file paragraph in ADR 0001 with:

```markdown
Each client must send `handshake` first. Its token is supplied through a file owned by the current user. Before writing the token, the launcher invokes `unit-test-service --prepare-token-file <path>`; Go creates the empty file atomically with mode `0600` on Unix or a protected owner-only DACL on Windows. The launcher then writes to that existing file without replacing it. The service independently rejects symbolic links and non-owner access, bounds the file to 4 KiB, and must delete the same file it opened before startup can succeed. The protocol version is `1.0`.
```

- [ ] **Step 5: Verify the documentation contract**

Run:

```powershell
pnpm test:workspace
git diff --check
```

Expected: all workspace smoke tests pass and `git diff --check` prints no errors.

- [ ] **Step 6: Commit Task 4**

```powershell
git add tools/workspace-smoke/workspace-smoke.test.mjs README.md docs/decisions/0001-local-ipc-and-protocol-v1.md
git commit -m "docs: describe secure token preparation"
```

---

### Task 5: Run the complete local and hosted acceptance gates

**Files:**
- Verify only; no source changes are expected.

**Interfaces:**
- Consumes: all deliverables from Tasks 1-4.
- Produces: fresh local evidence and a passing Windows/Ubuntu GitHub Actions run for Draft PR #1.

- [ ] **Step 1: Run the complete local gate**

Run from the repository root with the pinned Node.js and Go runtimes on `PATH`:

```powershell
pnpm install --frozen-lockfile --offline
pnpm check:protocol-generated
pnpm build
pnpm test
pnpm test:e2e
Push-Location apps/test-service
go test -race ./...
Pop-Location
git diff --check
git status --short
```

Expected: every command exits `0`; all tests pass; `git diff --check` is empty; `git status --short` is empty after the four task commits.

- [ ] **Step 2: Push the feature branch**

```powershell
git push github codex/foundation-protocol-service
```

Expected: GitHub advances the Draft PR branch to the local `HEAD`.

- [ ] **Step 3: Find and watch the new Actions run**

```powershell
$runs = & 'C:\Program Files\GitHub CLI\gh.exe' run list --repo colayc/unitTest --branch codex/foundation-protocol-service --event pull_request --limit 3 --json databaseId,headSha,status,conclusion,url | ConvertFrom-Json
$runID = $runs[0].databaseId
& 'C:\Program Files\GitHub CLI\gh.exe' run watch $runID --repo colayc/unitTest --exit-status --interval 10
```

Expected: both `verify (windows-latest)` and `verify (ubuntu-latest)` complete successfully, including `pnpm test:e2e` and `git diff --exit-code`.
