# Phase 3B：Workspace、CMake 与 Toolchain Discovery 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**目标：** 建立不暴露 Protocol 的 Go 内部 discovery 层，安全解析单 workspace、CMake Presets/File API 和 Windows/Linux 编译器，并产出稳定 Build Profile 与 Diagnostic。

**架构：** `workspace` 负责根目录与配置，`probe` 负责固定参数的有界能力探测，`cmake` 与 `toolchain` 分别实现 Adapter，`diagnostic` 规范化工具输出，`discovery.Inspector` 组合并生成稳定 snapshot。所有 package 通过小接口注入 probe runner 和文件系统，以便 unit tests 不依赖本机安装。

**技术栈：** Go 1.26.5、JSON Schema Draft 2020-12、CMake Presets、CMake File API、MSVC/vswhere/VsDevCmd、GCC、Clang、Go build tags。

## 全局约束

- 一个 Service 实例只绑定一个规范化 workspace root。
- workspace 配置固定为 `.unit-test-ide/workspace.json`，version 固定为 `1`。
- 配置最大 256 KiB、最多 64 个 project、最多 64 个手动 toolchain。
- 无配置时只自动识别 workspace 根目录的 `CMakeLists.txt`，不递归扫描 `third_party`。
- Preset include 默认不得逃逸 workspace root。
- 产品默认使用 bundle CMake；受信任配置可使用绝对路径覆盖；产品模式不搜索 `PATH`。
- 产品不内置或下载 compiler。
- 自动发现 Windows MSVC/clang-cl 与 Linux GCC/Clang。
- discovery probe 使用固定 executable/args、有界 timeout/output，不接受 Protocol 输入。
- 所有 stable ID 和 generation 使用确定排序、规范路径、规范 JSON 与 SHA-256。
- Phase 3B 不修改 Protocol Schema 或 TypeScript Client。
- 所有 Markdown 使用中文，English technical terms 保持 English 格式。

---

### Task 1：Workspace Root 与跨平台路径边界

**文件：**
- 创建： `apps/test-service/internal/workspace/root.go`
- 创建： `apps/test-service/internal/workspace/root_unix.go`
- 创建： `apps/test-service/internal/workspace/root_windows.go`
- 创建： `apps/test-service/internal/workspace/root_test.go`
- 创建： `apps/test-service/internal/workspace/root_unix_test.go`
- 创建： `apps/test-service/internal/workspace/root_windows_test.go`

**接口：**
- 输入： OS 文件系统。
- 输出：

```go
type Root struct {
	NativePath string
	URI        string
	ID         string
}

func OpenRoot(path string) (Root, error)
func (r Root) ResolveRelative(relative string) (string, error)
func (r Root) Contains(path string) bool
```

- [ ] **Step 1：写出正常路径、symlink/junction escape 和大小写测试**

公共测试覆盖绝对化、`..`、绝对输入、空路径和 stable ID。Unix 创建指向 root 外部的 symlink；Windows 创建 directory junction，并覆盖 drive letter 大小写：

```go
func TestResolveRelativeRejectsEscape(t *testing.T) {
	rootPath := t.TempDir()
	root, err := OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.ResolveRelative("../outside"); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("escape error = %v", err)
	}
}
```

- [ ] **Step 2：运行平台测试并确认失败**

运行：

```powershell
go test ./apps/test-service/internal/workspace -count=1
```

预期： FAIL，原因是 `Root` 和 platform path resolver 尚不存在。

- [ ] **Step 3：实现规范化与 containment**

`OpenRoot` 必须：

- 拒绝不存在或非目录；
- 使用 `filepath.Abs`、`filepath.Clean` 和平台 final-path resolver；
- URI 使用 `net/url` 构造，不手工拼接；
- ID 为规范 platform、volume identity 和 final path 的 SHA-256。

`ResolveRelative` 必须先拒绝绝对输入，再 join、解析现有链接组件并用 `filepath.Rel` 判断是否越界。Windows 比较使用 case-insensitive volume-aware 规则；Unix 使用 case-sensitive 规则。不存在的末尾路径通过“最近存在祖先的 final path + 剩余组件”判断。

- [ ] **Step 4：运行测试和 race**

运行：

```powershell
go test ./apps/test-service/internal/workspace -count=1
go test -race ./apps/test-service/internal/workspace -count=1
```

预期： PASS。

- [ ] **Step 5：提交 Workspace Root**

```powershell
git add apps/test-service/internal/workspace/root*
git commit -m "feat: bind service paths to a workspace root"
```

### Task 2：Workspace Config Schema 与严格 decoder

**文件：**
- 创建： `apps/test-service/internal/workspace/workspace.schema.json`
- 创建： `apps/test-service/internal/workspace/config.go`
- 创建： `apps/test-service/internal/workspace/config_test.go`
- 创建： `apps/test-service/internal/workspace/testdata/minimal.valid.json`
- 创建： `apps/test-service/internal/workspace/testdata/manual-toolchains.valid.json`
- 创建： `apps/test-service/internal/workspace/testdata/shell.invalid.json`
- 创建： `tools/workspace-smoke/workspace-config-schema.test.mjs`
- 修改： `package.json`

**接口：**
- 输入： Task 1 `Root.ResolveRelative`。
- 输出：

```go
type Config struct {
	Version    int
	CMake      CMakeConfig
	Projects   []ProjectConfig
	Toolchains []ToolchainConfig
}

type ProjectConfig struct {
	ID        string
	SourceDir string
	Fallback  FallbackConfig
}

type LoadResult struct {
	Config Config
	Issues []Issue
}

func LoadConfig(root Root) (LoadResult, error)
```

- [ ] **Step 1：写出 Schema 和 Go decoder 失败测试**

JSON Schema 必须拒绝 unknown property、command、args、env、绝对 `sourceDir`、完全相同的重复数组项、超限数组和错误 version。不同对象使用相同 ID 的情况由 Go semantic validator 拒绝。Go 测试额外覆盖 256 KiB 限制、缺失配置的 root project fallback，以及嵌套项目显式声明。

Node contract 核心：

```js
const valid = ajv.compile(schema);
assert.equal(valid(await load("minimal.valid.json")), true);
assert.equal(valid(await load("shell.invalid.json")), false);
assert.match(JSON.stringify(valid.errors), /additionalProperties/);
```

- [ ] **Step 2：运行测试并确认失败**

运行：

```powershell
go test ./apps/test-service/internal/workspace -run 'TestLoadConfig' -count=1
pnpm test:workspace
```

预期： 至少一条 FAIL，因为 Schema/decoder 尚未实现。

- [ ] **Step 3：实现 Schema、strict decode 与语义校验**

使用 `json.Decoder.DisallowUnknownFields`、第二次 Decode 必须为 `io.EOF`。手动 toolchain 使用 family-discriminated shape：

```json
{
  "id": "linux-clang",
  "family": "clang",
  "cCompiler": "/usr/bin/clang",
  "cppCompiler": "/usr/bin/clang++"
}
```

MSVC shape 只允许 `installationId`、`toolsetVersion`、`hostArchitecture` 和 `targetArchitecture`。fallback 只允许 configurations 与 preferredGenerator。配置不存在且 root 有 `CMakeLists.txt` 时生成 `ProjectConfig{ID: "root", SourceDir: "."}`；根目录没有项目时返回 non-blocking issue。

修改 `test:workspace`，显式运行两个 Node test 文件，避免 shell glob 的平台差异。

- [ ] **Step 4：运行 Go/Node contract**

运行：

```powershell
go test ./apps/test-service/internal/workspace -count=1
pnpm test:workspace
```

预期： PASS。

- [ ] **Step 5：提交 Config Schema**

```powershell
git add apps/test-service/internal/workspace package.json tools/workspace-smoke/workspace-config-schema.test.mjs
git commit -m "feat: validate structured workspace configuration"
```

### Task 3：有界 Probe Runner 与 CMake Resolver

**文件：**
- 创建： `apps/test-service/internal/probe/probe.go`
- 创建： `apps/test-service/internal/probe/runner.go`
- 创建： `apps/test-service/internal/probe/runner_test.go`
- 创建： `apps/test-service/internal/cmake/manifest.go`
- 创建： `apps/test-service/internal/cmake/manifest_test.go`
- 创建： `apps/test-service/internal/cmake/installation.go`
- 创建： `apps/test-service/internal/cmake/resolver.go`
- 创建： `apps/test-service/internal/cmake/resolver_test.go`
- 创建： `apps/test-service/internal/cmake/testdata/bundle-manifest.valid.json`

**接口：**
- 输入： Workspace Config 的 CMake override。
- 输出：

```go
package probe

type Spec struct {
	Executable string
	Args       []string
	Env        []string
	Dir        string
	Timeout    time.Duration
	MaxOutput  int
}

type Result struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

type Runner interface {
	Run(context.Context, Spec) (Result, error)
}
```

```go
package cmake

type Installation struct {
	Executable string
	Version    string
	Source     string
	Identity   string
	LicensePath string
}

type ResolverConfig struct {
	BundleRoot    string
	Override      string
	DevExecutable string
	Platform      string
	Architecture  string
}

func Resolve(context.Context, probe.Runner, ResolverConfig) (Installation, error)
```

- [ ] **Step 1：写出 timeout、output limit、resolver priority 与 hash 测试**

使用当前 test binary 的 helper mode 模拟成功、hang 和大输出。Resolver fake runner 断言固定调用 `cmake --version=json-v1`，并覆盖 override > bundle > dev、无 PATH fallback、manifest/archive identity、executable/license hash mismatch。

```go
func TestResolverDoesNotSearchPath(t *testing.T) {
	_, err := Resolve(context.Background(), fakeRunner{}, ResolverConfig{
		Platform: "linux", Architecture: "x64",
	})
	if !errors.Is(err, ErrCMakeUnavailable) {
		t.Fatalf("error = %v", err)
	}
}
```

- [ ] **Step 2：运行测试并确认失败**

运行：

```powershell
go test ./apps/test-service/internal/probe ./apps/test-service/internal/cmake -run 'Runner|Resolver' -count=1
```

预期： FAIL，package 尚不存在。

- [ ] **Step 3：实现固定参数 probe 和 resolver**

默认 Runner 使用 `exec.CommandContext`，永不调用 Shell；默认 timeout 5 秒、stdout/stderr 各 256 KiB，超限立即终止并返回 `ErrOutputLimit`。环境从调用者显式传入的清理后列表构造。

Resolver：

- override 必须为绝对普通 executable；
- bundle 从 bundle root 读取 `manifest.json`，其 `schemaVersion`、`cmakeVersion`、`license`、platform-key、`archiveSha256`、`rootDirectory`、`executable`、`licensePath` 和 `installedFiles` 字段与 Phase 3D production manifest 完全一致；
- resolver 根据 platform/architecture 选择唯一 archive entry，并只解析 `<bundle-root>/<cmakeVersion>/<platform-key>/<rootDirectory>/`；要求 version/platform 目录的 `bundle-state.json` archive identity 与固定 manifest 一致，并校验 executable、license 和所有关键 installed file SHA-256；
- dev executable 仅通过 `ResolverConfig.DevExecutable` 注入；
- 使用 CMake 4.3 JSON version output，解析失败时用固定 `--version` fallback；
- `Identity` 为规范路径、文件 identity、version 和 source 的 SHA-256。

- [ ] **Step 4：运行 package tests 与 race**

运行：

```powershell
go test ./apps/test-service/internal/probe ./apps/test-service/internal/cmake -count=1
go test -race ./apps/test-service/internal/probe ./apps/test-service/internal/cmake -count=1
```

预期： PASS。

- [ ] **Step 5：提交 Probe/CMake Resolver**

```powershell
git add apps/test-service/internal/probe apps/test-service/internal/cmake
git commit -m "feat: resolve verified cmake installations"
```

### Task 4：Preset discovery、Build Profile 与 Workspace Generation

**文件：**
- 创建： `apps/test-service/internal/cmake/presets.go`
- 创建： `apps/test-service/internal/cmake/presets_test.go`
- 创建： `apps/test-service/internal/cmake/profile.go`
- 创建： `apps/test-service/internal/cmake/profile_test.go`
- 创建： `apps/test-service/internal/cmake/generation.go`
- 创建： `apps/test-service/internal/cmake/testdata/presets/CMakePresets.json`
- 创建： `apps/test-service/internal/cmake/testdata/presets/CMakeUserPresets.json`
- 创建： `apps/test-service/internal/cmake/testdata/presets/included.json`

**接口：**
- 输入： `workspace.Root`、`workspace.ProjectConfig`、`cmake.Installation`、`probe.Runner`。
- 输出：

```go
type BuildProfile struct {
	ID              string
	ProjectID       string
	Origin          string
	ConfigurePreset string
	BuildPreset     string
	ToolchainID     string
	Generator       string
	Configuration   string
	BinaryDir       string
}

type PresetDiscovery struct {
	Profiles []BuildProfile
	Inputs   []string
	Issues   []Issue
}

func DiscoverPresets(
	context.Context,
	probe.Runner,
	Installation,
	workspace.Root,
	workspace.ProjectConfig,
) (PresetDiscovery, error)

func WorkspaceGeneration(config workspace.Config, install Installation, profiles []BuildProfile, toolchainIDs []string) string
```

- [ ] **Step 1：写出 include、condition、外部路径和 stable ID 测试**

覆盖：

- `CMakeUserPresets.json` 隐式关联 project presets；
- 允许的 include；
- include cycle 拒绝；
- include 指向 root 外；
- CMake listing 只返回当前平台有效 preset；
- 同一输入不同 map/list 顺序得到相同 generation；
- preset/build 组合得到稳定 Profile ID。

- [ ] **Step 2：运行测试并确认失败**

运行：

```powershell
go test ./apps/test-service/internal/cmake -run 'Preset|Profile|Generation' -count=1
```

预期： FAIL，Preset/Profile API 尚不存在。

- [ ] **Step 3：实现有界 Preset pre-scan 与 CMake-authoritative listing**

先用有界 JSON pre-scan 构建 include graph，并在调用 CMake 前拒绝 escape/cycle/超限。然后固定调用：

```text
cmake --list-presets=configure
cmake --build --list-presets
```

只接受 CMake listing 返回的 machine name；displayName/description 从已经通过边界校验的 JSON 读取。Profile ID 和 generation 对所有集合排序，路径使用 Root 规范形式。

Generated Profile 使用显式 generator/toolchain/configuration；本 Task 只实现纯函数，实际 Toolchain 组合在 Task 6/7 接入。

- [ ] **Step 4：运行 CMake package tests**

运行：

```powershell
go test ./apps/test-service/internal/cmake -count=1
```

预期： PASS。

- [ ] **Step 5：提交 Preset/Profile**

```powershell
git add apps/test-service/internal/cmake
git commit -m "feat: discover cmake presets and build profiles"
```

### Task 5：CMake File API、Target ID 与 Configure Fingerprint

**文件：**
- 创建： `apps/test-service/internal/cmake/fileapi.go`
- 创建： `apps/test-service/internal/cmake/fileapi_test.go`
- 创建： `apps/test-service/internal/cmake/fingerprint.go`
- 创建： `apps/test-service/internal/cmake/fingerprint_test.go`
- 创建： `apps/test-service/internal/cmake/testdata/fileapi/reply/index-2026-07-26.json`
- 创建： `apps/test-service/internal/cmake/testdata/fileapi/reply/codemodel-v2.json`
- 创建： `apps/test-service/internal/cmake/testdata/fileapi/reply/toolchains-v1.json`
- 创建： `apps/test-service/internal/cmake/testdata/fileapi/reply/cmakeFiles-v1.json`

**接口：**
- 输入： `BuildProfile`、build directory。
- 输出：

```go
type Target struct {
	ID           string
	Name         string
	Type         string
	SourceDir    string
	BuildDir     string
	Artifacts    []string
}

type FileAPIReply struct {
	Targets       []Target
	ToolchainIDs  []string
	CMakeInputs   []string
	Configurations []string
}

func WriteQuery(buildDir string) error
func ReadReply(buildDir string, allowedRoots []string) (FileAPIReply, error)
func ConfigureFingerprint(ProfileFingerprintInput) string
func NeedsConfigure(previous BuildConfiguration, current ProfileFingerprintInput) bool
```

- [ ] **Step 1：写出 query、reply 边界和 invalidation 测试**

测试必须拒绝 reply symlink escape、未知 object major version、缺失 index、target artifact 越界和 malformed JSON。Fingerprint 覆盖 CMake input 内容/identity 变化、Preset 变化和 toolchain 变化；普通 `.cpp` 内容变化不触发。

- [ ] **Step 2：运行测试并确认失败**

运行：

```powershell
go test ./apps/test-service/internal/cmake -run 'FileAPI|ConfigureFingerprint|NeedsConfigure' -count=1
```

预期： FAIL。

- [ ] **Step 3：实现 File API query/reply 与 fingerprint**

query 固定写入：

```json
{
  "requests": [
    {"kind": "codemodel", "version": {"major": 2}},
    {"kind": "cache", "version": {"major": 2}},
    {"kind": "cmakeFiles", "version": {"major": 1}},
    {"kind": "toolchains", "version": {"major": 1}}
  ]
}
```

写入使用同目录临时文件 + fsync + rename。Reply 只接受已支持 major version，minor version 向前兼容时忽略未知字段。Target ID 由 project/profile/configuration/native target identity 生成，不把 native name 直接作为 Protocol ID。

- [ ] **Step 4：运行 package tests**

运行：

```powershell
go test ./apps/test-service/internal/cmake -count=1
go test -race ./apps/test-service/internal/cmake -count=1
```

预期： PASS。

- [ ] **Step 5：提交 File API**

```powershell
git add apps/test-service/internal/cmake
git commit -m "feat: read cmake file api targets"
```

### Task 6：Toolchain Registry 与 Linux GCC/Clang Adapter

**文件：**
- 创建： `apps/test-service/internal/toolchain/model.go`
- 创建： `apps/test-service/internal/toolchain/registry.go`
- 创建： `apps/test-service/internal/toolchain/registry_test.go`
- 创建： `apps/test-service/internal/toolchain/gnu.go`
- 创建： `apps/test-service/internal/toolchain/gnu_test.go`
- 创建： `apps/test-service/internal/toolchain/discover_unix.go`
- 创建： `apps/test-service/internal/toolchain/discover_unix_test.go`

**接口：**
- 输入： `probe.Runner` 与手动 Toolchain Config。
- 输出：

```go
type Family string
const (
	FamilyMSVC Family = "msvc"
	FamilyClangCL Family = "clang-cl"
	FamilyGCC Family = "gcc"
	FamilyClang Family = "clang"
)

type Instance struct {
	ID                 string
	Family             Family
	CCompiler          string
	CXXCompiler        string
	Version            string
	TargetTriple       string
	HostArchitecture   string
	TargetArchitecture string
	Sysroot            string
	Environment        []string
	Generators         []string
}

type Adapter interface {
	Discover(context.Context) ([]Instance, error)
	Probe(context.Context, Candidate) (Instance, error)
}

type Registry struct
func NewRegistry(adapters ...Adapter) (*Registry, error)
func (r *Registry) Discover(context.Context) ([]Instance, []Issue)
```

- [ ] **Step 1：写出 compiler pair、version、triple、sysroot 与稳定排序测试**

Fake runner 必须断言 GCC/Clang 固定 probe args。覆盖 mismatched C/C++ pair、duplicate PATH/manual candidate、missing build tool、Ninja preference 和 Make fallback。

- [ ] **Step 2：运行测试并确认失败**

在 Linux 或 WSL 上运行：

```bash
go test ./apps/test-service/internal/toolchain -run 'Registry|GCC|Clang|Unix' -count=1
```

预期： FAIL。

- [ ] **Step 3：实现 Registry 与 Unix Adapter**

固定 probe：

```text
gcc --version
gcc -dumpmachine
gcc --print-sysroot
clang --version
clang --print-target-triple
clang --print-resource-dir
```

PATH discovery 只生成 candidate，最终 executable 必须规范化并 Probe。手动绝对路径优先保留其配置 ID；自动实例 ID 使用 family/path/version/triple/sysroot hash。所有结果按 family、target、version、path 排序。

- [ ] **Step 4：运行 Linux tests 与 race**

运行：

```bash
go test ./apps/test-service/internal/toolchain -count=1
go test -race ./apps/test-service/internal/toolchain -count=1
```

预期： PASS。

- [ ] **Step 5：提交 Linux Toolchain**

```bash
git add apps/test-service/internal/toolchain
git commit -m "feat: discover linux gcc and clang toolchains"
```

### Task 7：Windows MSVC 与 clang-cl Adapter

**文件：**
- 创建： `apps/test-service/internal/toolchain/discover_windows.go`
- 创建： `apps/test-service/internal/toolchain/discover_windows_test.go`
- 创建： `apps/test-service/internal/toolchain/msvc_windows.go`
- 创建： `apps/test-service/internal/toolchain/msvc_windows_test.go`
- 创建： `apps/test-service/internal/toolchain/clangcl_windows.go`
- 创建： `apps/test-service/internal/toolchain/clangcl_windows_test.go`
- 创建： `apps/test-service/internal/toolchain/discover_nonwindows.go`

**接口：**
- 输入： Task 6 `Adapter`、`Instance` 和 `probe.Runner`。
- 输出：

```go
type MSVCConfig struct {
	InstallationID    string
	ToolsetVersion    string
	HostArchitecture  string
	TargetArchitecture string
}

func NewWindowsAdapters(probe.Runner, []workspace.ToolchainConfig) []Adapter
```

- [ ] **Step 1：写出 vswhere JSON、VsDevCmd template 和 environment 清理测试**

测试固定 `vswhere` 参数：

```text
-all -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -format json -utf8
```

测试只允许 `x86`、`x64`、`arm64` enum；路径含空格时仍产生一个正确 `/c` argument；输出中的 `UNIT_TEST_IDE_TOKEN`、`GITHUB_TOKEN` 和 Service control variables 被删除。clang-cl 测试验证 LLVM version、`lld-link`、MSVC SDK 环境和 generator capability。

- [ ] **Step 2：在 Windows 运行测试并确认失败**

运行：

```powershell
go test ./apps/test-service/internal/toolchain -run 'MSVC|ClangCL|Windows' -count=1
```

预期： FAIL。

- [ ] **Step 3：实现固定 Windows discovery**

`vswhere.exe` 只从 Visual Studio Installer 固定位置或已验证 dev injection 获取。`VsDevCmd.bat` command line 由 Adapter 使用 enum 构造：

```text
cmd.exe /d /s /c "call \"<validated VsDevCmd.bat>\" -no_logo -host_arch=x64 -arch=x64 && set"
```

任何 path/config 值在进入模板前单独验证。捕获环境按 case-insensitive key 解析、去重和敏感变量清理。正常 CMake build 不调用 `cmd.exe`。

clang-cl Adapter 使用已验证 executable、MSVC environment 和固定 `--version`/`lld-link --version` probes；本阶段只报告 coverage tool path capability，不运行覆盖率。

- [ ] **Step 4：运行 Windows tests 与 race**

运行：

```powershell
go test ./apps/test-service/internal/toolchain -count=1
go test -race ./apps/test-service/internal/toolchain -count=1
```

预期： PASS。

- [ ] **Step 5：提交 Windows Toolchain**

```powershell
git add apps/test-service/internal/toolchain
git commit -m "feat: discover windows msvc and clang-cl toolchains"
```

### Task 8：Diagnostic parsers 与 Workspace Inspector

**文件：**
- 创建： `apps/test-service/internal/diagnostic/model.go`
- 创建： `apps/test-service/internal/diagnostic/parser.go`
- 创建： `apps/test-service/internal/diagnostic/cmake.go`
- 创建： `apps/test-service/internal/diagnostic/msvc.go`
- 创建： `apps/test-service/internal/diagnostic/gnu.go`
- 创建： `apps/test-service/internal/diagnostic/linker.go`
- 创建： `apps/test-service/internal/diagnostic/parser_test.go`
- 创建： `apps/test-service/internal/diagnostic/testdata/cmake.txt`
- 创建： `apps/test-service/internal/diagnostic/testdata/msvc.txt`
- 创建： `apps/test-service/internal/diagnostic/testdata/gcc.txt`
- 创建： `apps/test-service/internal/diagnostic/testdata/clang.txt`
- 创建： `apps/test-service/internal/diagnostic/testdata/linkers.txt`
- 创建： `apps/test-service/internal/discovery/inspector.go`
- 创建： `apps/test-service/internal/discovery/inspector_test.go`

**接口：**
- 输入： Workspace Config/Root、CMake Resolver/Profiles、Toolchain Registry。
- 输出：

```go
type Diagnostic struct {
	ID          string
	TaskID      string
	StepID      string
	Source      string
	ToolchainID string
	Severity    string
	Code        string
	Message     string
	FileURI     string
	Range       *Range
	Related     []Related
	External    bool
}

type Parser interface {
	Feed(stream string, data []byte) []Diagnostic
	Close() []Diagnostic
}
```

```go
type Snapshot struct {
	WorkspaceID string
	WorkspaceURI string
	Generation string
	Projects []workspace.ProjectConfig
	Profiles []cmake.BuildProfile
	Toolchains []toolchain.Instance
	Diagnostics []diagnostic.Diagnostic
}

type Inspector struct
func (i *Inspector) Inspect(context.Context) (Snapshot, error)
```

- [ ] **Step 1：写出 golden parser 和 deterministic Inspector 测试**

Golden 断言零起始 half-open range、MSVC `Cxxxx`、`LNKxxxx`、GCC/Clang warning option、CMake multiline、related notes、分块输入、invalid UTF-8、64 KiB 单行上限和 external path mapping。

Inspector fake adapters 以不同完成顺序返回，最终 snapshot 的 toolchain/profile/diagnostic 排序和 generation 必须一致。

- [ ] **Step 2：运行测试并确认失败**

运行：

```powershell
go test ./apps/test-service/internal/diagnostic ./apps/test-service/internal/discovery -count=1
```

预期： FAIL。

- [ ] **Step 3：实现 parser 与 Inspector 组合**

Parser 每个 stream 独立缓冲，最多 64 KiB/line、1 MiB/diagnostic、32 related records；超限时产生 `DIAGNOSTIC_TRUNCATED` info。无法识别的行不产生假 Diagnostic。

Inspector：

- 读取 Config；
- 解析 CMake；
- 使用有界并发发现 Toolchains；
- discover Presets 与 generated Profiles；
- 规范排序；
- 计算 generation；
- 把局部失败转换为 blocking/non-blocking Diagnostic；
- 只有 root/config 失效等基础错误才返回 Go error。

- [ ] **Step 4：运行 Phase 3B 全套测试**

运行：

```powershell
go test ./apps/test-service/internal/workspace ./apps/test-service/internal/probe ./apps/test-service/internal/cmake ./apps/test-service/internal/toolchain ./apps/test-service/internal/diagnostic ./apps/test-service/internal/discovery -count=1
go test -race ./apps/test-service/internal/... -count=1
pnpm verify
```

预期： 三条命令 PASS；现有 Protocol/E2E 不受影响。

- [ ] **Step 5：提交 Inspector**

```powershell
git add apps/test-service/internal/diagnostic apps/test-service/internal/discovery
git commit -m "feat: inspect workspaces and normalize tool diagnostics"
```

## Phase 3B 完成检查

- [ ] Windows workspace/toolchain tests 通过
- [ ] Linux/WSL workspace/toolchain tests 通过
- [ ] `go test ./apps/test-service/internal/... -count=1`
- [ ] `go test -race ./apps/test-service/internal/... -count=1`
- [ ] `pnpm verify`
- [ ] `git diff --check`
- [ ] `git status --short` 为空
- [ ] 独立评审确认所有 probe 均为固定参数、有界输出且不经过 Protocol
