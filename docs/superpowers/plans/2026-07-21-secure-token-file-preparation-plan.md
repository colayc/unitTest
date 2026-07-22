# 安全令牌文件准备实施计划

> **面向智能体工作者：** 必需的子技能：superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans，逐项实施本计划。各步骤使用复选框（`- [ ]`）语法跟踪。

**目标：** 在 TypeScript 写入秘密之前，以平台原生的仅所有者权限创建身份验证令牌文件，随后沿用现有且经过验证的服务启动路径。

**架构：** Go 可执行文件新增互斥的 `--prepare-token-file` 模式。平台专用文件以 Windows 受保护的仅所有者 DACL 或 Linux mode `0600` 原子创建空目标文件；TypeScript 探针调用该模式，写入现有文件，然后启动正常服务模式。

**技术栈：** Node.js 24.18.0、pnpm 11.4.0、TypeScript 6.0.3、Go 1.26.5、`golang.org/x/sys/windows` 和 `golang.org/x/sys/unix`。

## 全局约束

- 不得新增依赖，也不得更改协议 schema 版本 `1.0`。
- 通过带 build tag 的 Go 文件同时支持 Windows 和 Linux。
- 令牌文件必须归当前用户所有，并且只允许该用户访问。
- 必须在写入任何令牌字节之前，原子应用限制性权限。
- 绝不能通过进程参数传递令牌，也不能将其写入 stdout、stderr 或错误信息。
- 正常启动验证、4096 字节上限、经过身份校验的删除、handshake、capabilities 和 shutdown 行为必须保持不变。
- 如果准备目标已存在，必须拒绝操作，而不是截断或替换该目标。
- 遵循测试驱动开发：每个指定的红灯测试都必须在相应实现步骤之前运行。

---

## 文件映射

- `apps/test-service/cmd/unit-test-service/token_file_prepare.go`：通用的准备验证、关闭和失败清理编排。
- `apps/test-service/cmd/unit-test-service/token_file_prepare_windows.go`：使用受保护的仅所有者安全描述符在 Windows 上原子创建文件。
- `apps/test-service/cmd/unit-test-service/token_file_prepare_unix.go`：使用 mode `0600` 在 Linux 上排他创建文件。
- `apps/test-service/cmd/unit-test-service/token_file_prepare_test.go`：跨平台准备行为。
- `apps/test-service/cmd/unit-test-service/main.go`：互斥的准备模式和服务 CLI 模式。
- `apps/test-service/cmd/unit-test-service/main_test.go`：CLI 分派和无效组合测试。
- `tools/service-probe/src/probe.ts`：在写入令牌之前调用准备模式。
- `tools/service-probe/src/probe.test.ts`：使用真实二进制文件的准备测试和端到端测试。
- `tools/workspace-smoke/workspace-smoke.test.mjs`：文档契约测试。
- `README.md`：面向贡献者的启动器行为说明。
- `docs/decisions/0001-local-ipc-and-protocol-v1.md`：具有约束力的安全决策。

---

### Task 1：原子创建仅所有者可访问的空令牌文件

**文件：**
- 创建：`apps/test-service/cmd/unit-test-service/token_file_prepare.go`
- 创建：`apps/test-service/cmd/unit-test-service/token_file_prepare_windows.go`
- 创建：`apps/test-service/cmd/unit-test-service/token_file_prepare_unix.go`
- 创建：`apps/test-service/cmd/unit-test-service/token_file_prepare_test.go`

**接口：**
- 输入：现有的 `validateTokenFile(file *os.File, info os.FileInfo) error` 和 `removeSameTokenFile(path string, expected os.FileInfo) error`。
- 产出：供 CLI 准备模式使用的 `prepareTokenFile(path string) error`，以及带 build tag 的 `createTokenFile(path string) (*os.File, error)` 实现。

- [ ] **Step 1：编写失败的跨平台准备测试**

创建 `token_file_prepare_test.go`：

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

- [ ] **Step 2：运行聚焦测试并确认红灯**

从 `apps/test-service` 运行：

```powershell
go test ./cmd/unit-test-service -run 'TestPrepareTokenFile' -count=1
```

预期：构建失败，并出现 `undefined: prepareTokenFile`。

- [ ] **Step 3：实现通用准备编排**

创建 `token_file_prepare.go`：

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

- [ ] **Step 4：实现 Windows 原子创建**

创建 `token_file_prepare_windows.go`：

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

- [ ] **Step 5：实现 Linux 排他创建**

创建 `token_file_prepare_unix.go`：

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

- [ ] **Step 6：格式化并运行聚焦测试以确认绿灯**

运行：

```powershell
gofmt -w cmd/unit-test-service/token_file_prepare.go cmd/unit-test-service/token_file_prepare_windows.go cmd/unit-test-service/token_file_prepare_unix.go cmd/unit-test-service/token_file_prepare_test.go
go test ./cmd/unit-test-service -run 'TestPrepareTokenFile' -count=1
```

预期：两个准备测试均通过。在 Windows 上，第一个测试会执行 `validateOwnerOnlyDACL`，因此只要保留了 `SY`、`WD` 或任何其他允许 ACE，测试就会失败。

- [ ] **Step 7：运行完整的 Go 测试包**

运行：

```powershell
go test ./cmd/unit-test-service -count=1
```

预期：所有令牌消费和准备测试均通过。

- [ ] **Step 8：提交 Task 1**

```powershell
git add apps/test-service/cmd/unit-test-service/token_file_prepare.go apps/test-service/cmd/unit-test-service/token_file_prepare_windows.go apps/test-service/cmd/unit-test-service/token_file_prepare_unix.go apps/test-service/cmd/unit-test-service/token_file_prepare_test.go
git commit -m "feat: create restricted token files atomically"
```

---

### Task 2：添加准备命令模式

**文件：**
- 创建：`apps/test-service/cmd/unit-test-service/main_test.go`
- 修改：`apps/test-service/cmd/unit-test-service/main.go`

**接口：**
- 输入：Task 1 提供的 `prepareTokenFile(path string) error`。
- 产出：`unit-test-service --prepare-token-file <path>`，成功退出码为 `0`、操作失败退出码为 `1`、用法错误退出码为 `2`。

- [ ] **Step 1：编写失败的 CLI 模式测试**

创建 `main_test.go`：

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

- [ ] **Step 2：运行 CLI 测试并确认红灯**

从 `apps/test-service` 运行：

```powershell
go test ./cmd/unit-test-service -run 'TestRun' -count=1
```

预期：出现 `flag provided but not defined: -prepare-token-file`；位置参数测试还会暴露当前未验证的位置输入。

- [ ] **Step 3：实现互斥的 CLI 分派**

在 `main.go` 中添加第三个 flag，并在解析后立即分派：

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

此代码块下方现有的服务启动逻辑保持不变。

- [ ] **Step 4：格式化并确认绿灯**

运行：

```powershell
gofmt -w cmd/unit-test-service/main.go cmd/unit-test-service/main_test.go
go test ./cmd/unit-test-service -run 'TestRun' -count=1
go test ./cmd/unit-test-service -count=1
```

预期：所有 CLI 测试和完整命令包测试均通过。

- [ ] **Step 5：提交 Task 2**

```powershell
git add apps/test-service/cmd/unit-test-service/main.go apps/test-service/cmd/unit-test-service/main_test.go
git commit -m "feat: add token file preparation command"
```

---

### Task 3：在 TypeScript 写入令牌之前准备文件

**文件：**
- 修改：`tools/service-probe/src/probe.ts`
- 修改：`tools/service-probe/src/probe.test.ts`

**接口：**
- 输入：Task 2 提供的 `unit-test-service --prepare-token-file <path>`。
- 产出：`prepareTokenFile(serviceBinary: string, tokenFile: string, token: string): Promise<void>` 和现有的 `runProbe(serviceBinary: string): Promise<Capabilities>`。

- [ ] **Step 1：编写使用真实二进制文件的失败准备测试**

将 `probe.test.ts` 替换为：

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

- [ ] **Step 2：运行 E2E 并确认红灯**

从仓库根目录运行：

```powershell
pnpm --filter @unit-test-ide/service-probe test:e2e
```

预期：TypeScript 构建失败，并出现 `Module './probe.js' has no exported member 'prepareTokenFile'`。

- [ ] **Step 3：用写入前准备替代写入后 ACL 修改**

在 `probe.ts` 中保留 Promise 化的 `execFile`，删除 `restrictTokenFile`，并添加：

```ts
export async function prepareTokenFile(serviceBinary: string, tokenFile: string, token: string): Promise<void> {
  await execFile(serviceBinary, ["--prepare-token-file", tokenFile], { windowsHide: true });
  await writeFile(tokenFile, token, { flag: "r+" });
}
```

在 `runProbe` 内，将：

```ts
await writeFile(tokenFile, token, { mode: 0o600, flag: "wx" });
await restrictTokenFile(tokenFile);
```

替换为：

```ts
await prepareTokenFile(serviceBinary, tokenFile, token);
```

彻底移除对 `whoami` 和 `icacls` 的调用。不得将 `token` 传给 `execFile` 或 `spawn`。

- [ ] **Step 4：使用真实服务构建并确认绿灯**

运行：

```powershell
pnpm --filter @unit-test-ide/service-probe test:e2e
```

预期：准备测试和 handshake/capabilities/shutdown E2E 测试在当前平台上均通过。

- [ ] **Step 5：运行 TypeScript 和 Go 回归测试**

运行：

```powershell
pnpm build
pnpm test
```

预期：所有 workspace、TypeScript 和 Go 测试均通过。

- [ ] **Step 6：提交 Task 3**

```powershell
git add tools/service-probe/src/probe.ts tools/service-probe/src/probe.test.ts
git commit -m "fix: prepare token files before writing secrets"
```

---

### Task 4：记录安全准备契约

**文件：**
- 修改：`tools/workspace-smoke/workspace-smoke.test.mjs`
- 修改：`README.md`
- 修改：`docs/decisions/0001-local-ipc-and-protocol-v1.md`

**接口：**
- 输入：Tasks 1-3 提供的 CLI 和启动器行为。
- 产出：贡献者文档，以及防止回退到写入后修改 ACL 的 smoke 测试。

- [ ] **Step 1：编写失败的文档契约测试**

追加到 `workspace-smoke.test.mjs`：

```js
test("documentation records pre-write token file preparation", async () => {
  const readme = await readFile("README.md", "utf8");
  const decision = await readFile("docs/decisions/0001-local-ipc-and-protocol-v1.md", "utf8");
  assert.match(readme, /--prepare-token-file/);
  assert.match(decision, /--prepare-token-file/);
  assert.doesNotMatch(readme, /removes inherited Windows permissions after writing/i);
});
```

- [ ] **Step 2：运行 smoke 测试并确认红灯**

运行：

```powershell
pnpm test:workspace
```

预期：新测试失败，因为两份文档都不包含 `--prepare-token-file`。

- [ ] **Step 3：更新 README**

将 README 的最后一段替换为：

```markdown
The service listens on a random per-user Windows Named Pipe or a Linux Unix Socket with mode `0600`. Every connection must complete the token handshake before using another method. Authentication token files must be owned by the current user and grant access only to that user: Unix uses owner-only mode bits, while Windows uses a protected owner-only DACL. Before writing the token, the launcher runs `unit-test-service --prepare-token-file <path>` so the Go binary creates the empty file with platform-native owner-only permissions. The service validates the file independently and deletes it after consuming the token.
```

- [ ] **Step 4：更新 IPC 决策**

将 ADR 0001 中关于令牌文件的段落替换为：

```markdown
Each client must send `handshake` first. Its token is supplied through a file owned by the current user. Before writing the token, the launcher invokes `unit-test-service --prepare-token-file <path>`; Go creates the empty file atomically with mode `0600` on Unix or a protected owner-only DACL on Windows. The launcher then writes to that existing file without replacing it. The service independently rejects symbolic links and non-owner access, bounds the file to 4 KiB, and must delete the same file it opened before startup can succeed. The protocol version is `1.0`.
```

- [ ] **Step 5：验证文档契约**

运行：

```powershell
pnpm test:workspace
git diff --check
```

预期：所有 workspace smoke 测试均通过，并且 `git diff --check` 不输出任何错误。

- [ ] **Step 6：提交 Task 4**

```powershell
git add tools/workspace-smoke/workspace-smoke.test.mjs README.md docs/decisions/0001-local-ipc-and-protocol-v1.md
git commit -m "docs: describe secure token preparation"
```

---

### Task 5：运行完整的本地与托管验收门禁

**文件：**
- 仅验证；预期不修改源文件。

**接口：**
- 输入：Tasks 1-4 的全部交付物。
- 产出：最新的本地证据，以及 Draft PR #1 中通过的 Windows/Ubuntu GitHub Actions 运行结果。

- [ ] **Step 1：运行完整的本地门禁**

从仓库根目录运行，并确保固定版本的 Node.js 和 Go 运行时位于 `PATH`：

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

预期：每条命令均以 `0` 退出；所有测试均通过；`git diff --check` 无输出；完成四次任务提交后，`git status --short` 无输出。

- [ ] **Step 2：推送功能分支**

```powershell
git push github codex/foundation-protocol-service
```

预期：GitHub 将 Draft PR 分支推进到本地 `HEAD`。

- [ ] **Step 3：查找并监视新的 Actions 运行**

```powershell
$runs = & 'C:\Program Files\GitHub CLI\gh.exe' run list --repo colayc/unitTest --branch codex/foundation-protocol-service --event pull_request --limit 3 --json databaseId,headSha,status,conclusion,url | ConvertFrom-Json
$runID = $runs[0].databaseId
& 'C:\Program Files\GitHub CLI\gh.exe' run watch $runID --repo colayc/unitTest --exit-status --interval 10
```

预期：`verify (windows-latest)` 和 `verify (ubuntu-latest)` 均成功完成，其中包括 `pnpm test:e2e` 和 `git diff --exit-code`。
