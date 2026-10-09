# Windows CMake 覆盖率受控兼容 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补齐 Windows 安全构建的有限 CMake 声明和真实框架配方，使首份覆盖率、生成用例、执行全部测试和文件/函数覆盖率验收能够继续收口。

**Architecture:** 扩展现有闭合解析器，不接入通用 CMake 求值器。共享固定 SDK 配方使用未经修改的锁定 Unity/CppUTest 源码；唯一新增系统库由服务从已选 Windows SDK 捕获并绑定到实际链接证据。新增三个修复任务完成后恢复原任务 4，不重开发生成器或重做已完成任务。

**Tech Stack:** Go 1.26.6、CMake 4.3.4 File API、Windows MSVC/clang-cl、LLVM、TypeScript、Node 24.19.0、pnpm 11.4.0、Unity 2.6.1、CppUTest 4.0、现有 Go 本地服务和 Code-OSS 扩展。

## Global Constraints

- 唯一设计依据：[已确认设计](../specs/2026-10-08-windows-cmake-coverage-compatibility-design.md)，用户于本轮确认书面设计。
- 工作区：`C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest`；分支：`codex/phase10-production-generation`；设计提交：`6356dee49de95683d8362e93bbd6cf077a7cbd10`。执行前读最新 HEAD，不把设计 SHA 当成未来验收 SHA。
- 完全离线；不引入 Mock/CMock、CppUTestExt Mock 扩展、云模型、新服务端、协议版本或另一套生成算法。
- 保留 APP_ID 进程闭包、输入快照/身份/摘要、受管输出、报告来源、分页预算及候选覆盖率提升且不回退要求。
- 不自动改写用户 CMake；显式采用静态兼容集成方式，不宣称任意上游工程兼容。
- 框架归档、源码树、license 和原 `sdk/cmake/UnitTestIDE.cmake` 的锁保持不变；新配方不运行上游配置、探测、安装、GLOB 或动态 helper。
- 普通路径变量仅 `${CMAKE_SOURCE_DIR}`、`${CMAKE_CURRENT_SOURCE_DIR}`、`${CMAKE_BINARY_DIR}`、`${CMAKE_CURRENT_BINARY_DIR}`、`${CMAKE_CURRENT_LIST_DIR}`；单个基变量前缀后接字面量路径。SDK 库变量只允许作为完整链接参数。
- 宏名为 ASCII C 标识符；可选值仅带可选正负号的十进制整数或 ASCII C 标识符；不接受 `-D`、空值、引号、分号、反斜杠或 `$`。
- `include` 仅一个存在的 `.cmake` 文件参数；`add_subdirectory` 仅 `source [binary] [EXCLUDE_FROM_ALL]`，无 SYSTEM/多余参数。
- 保留 128 个 CMake 文件、单文件 512 KiB、总计 4 MiB、8192 条命令、65536 个参数和 64 个启动计划条目；不放大既有 wire/File API 预算。
- 未知命令/变量、环境展开、生成器表达式、输入变化、越界/别名路径、目标或证据不一致均失败关闭。不得删除或静默跳过安全测试，不提交临时诊断代码。
- Linux 行为保持不变；本轮 Windows SDK 配方不替换 Linux fixture 的原集成方式。
- 2026-10-09 有界修订待书面审阅：[已选工具链 ID 契约修正设计](../specs/2026-10-09-selected-toolchain-identity-contract-design.md)。确认后，仅共享生成 runtime 的实例 ID 词法/精确选择修正及测试覆盖例外于上一条；Linux fixture/集成及其他约束不变。空 profile ID 的生成拒绝、普通构建不变；该修正须按补充实施计划执行，不由旧 W4 推定授权。
- Task 1–3 的历史 RED/GREEN、审查与原 Task 4 的现有现场继续有效；本轮计划完成不等于 Phase 10 验收完成。
- 本地实施者不推送。已有分支推送/更新 PR #39/hosted 授权仅在本地关键门槛与审查通过后由控制器使用；不合并、不操作 master、不发布 Release、不签名、不强推。

---

## 现场、文件职责与执行准备

现有未提交文件属于原 Task 4，必须保留：

```text
tools/service-probe/src/native-test-generation.ts
tools/service-probe/src/native-test-generation.test.ts
tools/service-probe/src/test-generation-e2e.test.ts
apps/code-oss-extension/test/coverage-first-report-native.test.mjs
```

前两项还会由新增 Task W3 修改，形成有意重叠。W1/W2 不写这些文件；W3 结束时提交其累计变更，包含已保留的原 Task 4 helper 和测试，原 Task 4 最终提交不得再次声称它们是新实现。控制器保存执行前 diff 摘要，核对累计改动没有丢失。禁止通过 stash/reset/checkout 清理现场。

| 任务 | 文件 | 单一职责 |
| --- | --- | --- |
| W1 | 修改 `apps/test-service/internal/build/cmake_launch_plan.go`；新建 `cmake_declarative_context.go`、`cmake_target_declarations.go` 及各自 `_test.go`；修改 `planner_test.go` | 路径上下文、声明子集、输入/目标绑定；只拆新增职责，不重排整套安全解析器 |
| W2 | 新建 `apps/test-service/internal/build/windows_sdk_library.go`、`windows_sdk_library_test.go`；修改 `planner.go`、`cmake_launch_plan.go`、`coordinator.go`、对应测试 | SDK 单库捕获、保留变量、普通/插桩计划与执行前验证 |
| W2 | 修改 `apps/test-service/internal/cmake/fileapi.go`、`fileapi_test.go`、`fingerprint.go`、`fingerprint_test.go` | 有界实际链接片段、独立 clone、库身份进入配置指纹 |
| W3 | 新建 `sdk/cmake/framework-recipes/windows-cpputest-4.0.cmake`、`windows-unity-2.6.1.cmake`、`manifest.json`、`README.md`；新建 `tools/framework-bundle/framework-recipes.mjs`、同名 `.test.mjs` | 固定 SDK 资源、闭合库存和摘要检查/复制 |
| W3 | 修改 `tools/service-probe/src/native-test-generation.ts`、同名 `.test.ts`；新建四个 `fixtures/test-generation/{cpputest,unity}/windows/{CMakeLists.txt,tests/CMakeLists.txt}` | Windows 夹具采用同一配方；Linux 现有文件保持原样 |
| W4 | 恢复原 Task 4 的 UI native 与 managed fixture；新建 `docs/superpowers/evidence/phase10/2026-10-08-windows-cmake-coverage-compatibility.md`；完成原证据文档 | 正式 IPC、原生执行、维护复跑、SHA 绑定与审查收口 |

所有 Go 文件路径在上表包目录内。W2 的 `windows_sdk_library.go` 是普通 Go 文件名，不是 `_windows.go` 平台后缀；入口按平台和工具链家族拒绝不适用调用，不令 Linux 链接缺少符号。

- [ ] 只读核对工作分支、HEAD、index 和现场；index 非空时先明确归属，不能提交他人的暂存修改。

```powershell
Set-Location 'C:\Users\DELL\.codex\worktrees\phase10-production-generation\unitTest'
git branch --show-current
git rev-parse HEAD
git status --short
git diff --cached --name-only
git diff --check
```

- [ ] 设置已存在的运行环境，不安装或切换依赖；每条原生命令后立即检查 `$LASTEXITCODE`。

```powershell
$ErrorActionPreference = 'Stop'
$env:PATH = 'C:\Users\DELL\AppData\Local\Temp\utide-pnpm-11.4.0;C:\Users\DELL\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin;D:\Program Files\Go\bin;' + $env:PATH
$env:CMAKE = 'C:\codex_project\unitTest\.bundled-tools\cmake\4.3.4\win32-x64\cmake-4.3.4-windows-x86_64\bin\cmake.exe'
node --version
pnpm --version
go version
Test-Path -LiteralPath $env:CMAKE -PathType Leaf
```

已准备的框架和 bundle 复用正常 `prepare/check/stage` 产物；只有具体缺失才运行对应准备，不伪造 READY，不再跑依赖下载排查。当前原生 LLVM 选择是 `C:/Program Files/LLVM/bin/clang-cl.exe` 22.1.8；不能拿 Swift clang-cl 或 PATH 中另一个编译器替代实际选择证据。

## Task 1 (W1)：闭合声明、目录上下文和源码绑定

**Files:** 文件职责表 W1 的六个文件；沿用现有 `newPlannerFixture`、`activatePlannerWFPRegistration`、`canonicalCMakeLaunchPath`、`SnapshotLaunchInput` 与预算。

**Interfaces:** 保留 `validateCMakeLaunchPlan(input PlanInput, sourceDir string, launchPlan []string) ([]string, []cmake.FingerprintFile, error)`。新增的上下文和方法定义如下；旧 `validateFile(path,binaryDir)` 改为入口适配器，递归调用必须走有上下文的方法。

```go
type cmakeDirectoryContext struct {
    SourceRoot string
    BinaryRoot string
    SourceDir  string
    BinaryDir  string
    ListDir    string
}
type cmakeScopedArguments struct {
    Scope  string
    Values []cmakeArgument
}
// Each declaration is validated against the current lexical directory context.
// New method signatures:
// (*cmakeLaunchValidator).validateFileContext(path string, context cmakeDirectoryContext) error
// (*cmakeLaunchValidator).resolveDeclarativePath(value string, context cmakeDirectoryContext) (string, error)
// (*cmakeLaunchValidator).validateTargetIncludes(invocation cmakeInvocation, context cmakeDirectoryContext) error
// (*cmakeLaunchValidator).validateTargetDefinitions(invocation cmakeInvocation) error
// (*cmakeLaunchValidator).validateTargetSources(invocation cmakeInvocation, context cmakeDirectoryContext) error
// parseScopedCMakeArguments(arguments []cmakeArgument) ([]cmakeScopedArguments, error)
// validClosedCompileDefinition(value string) bool
```

- [ ] **Step 1：写生产 Plan 的行为红测，不先引入尚不存在的 helper 造成编译失败。** 在 `planner_test.go` 使用真实现有 fixture，创建 `main.cpp`、`extra.cpp`、`include/value.h` 和目录，然后写下面 CMake；Windows 上用既有 registration fixture 进入正式闭合校验。

```go
if runtime.GOOS != "windows" { t.Skip("Windows production launch declaration") }
activatePlannerWFPRegistration(t)
fixture := newPlannerFixture(t)
if err := os.MkdirAll(filepath.Join(fixture.sourceDir, "include"), 0700); err != nil { t.Fatal(err) }
for relative, content := range map[string]string{
    "main.cpp": "int main() { return 0; }\n",
    "extra.cpp": "int extra() { return 1; }\n",
    "include/value.h": "#define VALUE 1\n",
} {
    if err := os.WriteFile(filepath.Join(fixture.sourceDir, relative), []byte(content), 0600); err != nil { t.Fatal(err) }
}
contents := `cmake_minimum_required(VERSION 3.28)
project(closed LANGUAGES CXX)
add_library(core STATIC main.cpp)
target_include_directories(core PUBLIC include)
target_compile_definitions(core PUBLIC FEATURE_ON=1)
target_sources(core PRIVATE extra.cpp)
add_executable(closed-tests main.cpp)
target_link_libraries(closed-tests PRIVATE core)
add_test(NAME closed-tests COMMAND closed-tests)
`
if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakeLists.txt"), []byte(contents), 0600); err != nil {
    t.Fatal(err)
}
_, err := Plan(PlanInput{Installation: fixture.installation, WorkspaceRoot: fixture.root,
    Project: fixture.project, Profile: fixture.profile, Toolchain: fixture.toolchain,
    Jobs: 1, Configure: true})
if err != nil { t.Fatalf("closed declarations rejected: %v", err) }
```

测试命名 `TestPlannerWindowsDeclarativeTargets`。不能省略输入准备来掩盖实际拒绝阶段。第一轮必须失败于原生产解析拒绝，而非环境、编译或 WFP 安装；其他平台的明确 skip 不计为 Windows 通过。

- [ ] **Step 2：只运行该红测。**

```powershell
go test ./apps/test-service/internal/build -run '^TestPlannerWindowsDeclarativeTargets$' -count=1 -v
```

期望 exit 1，记录拒绝输入；未复现行为红测不进入实现。

- [ ] **Step 3：添加上下文实现和声明分派。** `include` 保留 SourceDir/BinaryDir、替换 ListDir；`add_subdirectory` 更新 SourceDir/BinaryDir/ListDir，函数返回后恢复。已知变量只绑定上下文，禁止重写；路径展开后先规范化/归属验证再读文件。递归 active 集合拒绝循环；visited 绑定完整上下文，不同上下文再次进入同文件拒绝。严格消费 include/subdirectory 的全部参数。

```go
var closedCompileDefinition = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(=([+-]?[0-9]+|[A-Za-z_][A-Za-z0-9_]*))?$`)
func validClosedCompileDefinition(value string) bool {
    return closedCompileDefinition.MatchString(value)
}
// Add these branches to the existing invocation switch; retain its default reject.
case "target_include_directories":
    err = validator.validateTargetIncludes(invocation, context)
case "target_compile_definitions":
    err = validator.validateTargetDefinitions(invocation)
case "target_sources":
    err = validator.validateTargetSources(invocation, context)
```

作用域解析要求首项为合法目标名、至少一个明确 scope 段且每段非空；目标必须实际已定义，不只出现在 File API。STATIC 库/可执行文件的源码参数和新增 target_sources 都解析到同一归属规则，记录源码快照；不把 IMPORTED/ALIAS 当目标定义。include 目录确认真实目录及归属，声明源码确认真实文件并复用快照，不能对目录伪造文件摘要。命令数量/参数预算跨文件累计，保持可信插桩脚本的原精确认证例外。

- [ ] **Step 4：补具体上下文与负例红绿。** `TestCMakeDeclarativeContext` 验证 include 文件在 `sdk/` 时 `include` 相对路径仍基于调用 SourceDir；`${CMAKE_CURRENT_LIST_DIR}` 指向 `sdk/`；子目录 SourceDir 和 BinaryDir 不同且返回后恢复。`TestCMakeClosedMacro` 表格必须包含：

```go
for _, tt := range []struct { value string; want bool }{
    {"FEATURE", true}, {"FEATURE=1", true}, {"FEATURE=-1", true}, {"FEATURE=VALUE", true},
    {"FEATURE=", false}, {"-DFEATURE=1", false}, {"FEATURE=\"x\"", false},
    {"FEATURE=1;BAD", false}, {"FEATURE=${OTHER}", false}, {"FEATURE=$<BOOL:1>", false},
    {"FEATURE=1\nBAD", false}, {"FEATURE=foo\\bar", false},
} {
    if got := validClosedCompileDefinition(tt.value); got != tt.want {
        t.Fatalf("definition %q = %v, want %v", tt.value, got, tt.want)
    }
}
```

`TestPlannerWindowsDeclarativeRejects` 逐项测试未知变量、`$ENV`、嵌套/多个变量、genexpr、分号路径、未声明/ALIAS/IMPORTED 目标、文件集/GLOB、不存在和越界源码、include 额外参数、subdirectory SYSTEM/多余参数、不同上下文重复文件、循环、受控变量重写和原有预算超限。输入替换测试用既有 `VerifyLaunchInput` 验证先捕获后替换拒绝；不能只断言计划含某字符串。

- [ ] **Step 5：定向绿测与本地提交。**

```powershell
go test ./apps/test-service/internal/build -run 'TestCMakeDeclarativeContext|TestCMakeClosedMacro|TestPlannerWindowsDeclarative|TestPlannerRejects|TestPlannerDerivesFresh|TestExecutionBoundaryRejects' -count=1
git diff --check
git add -- apps/test-service/internal/build/cmake_launch_plan.go apps/test-service/internal/build/cmake_declarative_context.go apps/test-service/internal/build/cmake_declarative_context_test.go apps/test-service/internal/build/cmake_target_declarations.go apps/test-service/internal/build/cmake_target_declarations_test.go apps/test-service/internal/build/planner_test.go
git commit -m 'fix: validate bounded Windows CMake target declarations'
```

每条命令 exit 0 后才继续。独立审查目录语义、目标真实性、快照和原有安全反例；Critical/Important 未闭合不进入 W2。W1 不宣称真实 CppUTest 原生链路通过。

## Task 2 (W2)：服务拥有的 SDK 库绑定与实际链接证据

**Files:** 文件职责表 W2。`cmake.Target` 当前没有 link fragments；必须补有界读取与 clone，不能假设已有该字段。官方定义：link fragments 存在于真正链接的可执行/共享库，而非 STATIC 库；格式是生成系统本机 shell 格式。[CMake 4.3 File API](https://cmake.org/cmake/help/v4.3/manual/cmake-file-api.7.html#codemodel-version-2-target-object)

**Consumes:** W1 的上下文、目标/源码图和快照入口；现有已选 `toolchain.Instance.Environment`、TargetArchitecture 与已验证 SDK 环境；`cmake.FingerprintFile`。

**Produces:** 以下包内接口；所有调用者使用同一拼写和类型。`consumerNames` 来自已验证目标链接关系中真正消费该 PUBLIC 库的可执行目标，不通过名字是否叫 CppUTest 猜测。

```go
const windowsWinMMVariable = "UNIT_TEST_IDE_WINMM_LIBRARY"
const maxWindowsLinkLibraryBytes = 4 * 1024 * 1024
type windowsLinkLibraryBinding struct {
    SDKRoot            string
    SDKIdentity        string
    SDKVersion         string
    TargetArchitecture string
    File               cmake.FingerprintFile
}
// captureWindowsWinMMLibrary(instance toolchain.Instance) (*windowsLinkLibraryBinding, error)
// verifyWindowsWinMMLibrary(binding *windowsLinkLibraryBinding) error
// windowsLinkFragmentTokens(value string) ([]string, error)
// verifyWindowsLinkLibraryEvidence(binding *windowsLinkLibraryBinding, targets []cmake.Target, consumerNames []string) error

// New cmake model:
type LinkCommandFragment struct { Fragment string; Role string }
// cmake.Target.LinkFragments []LinkCommandFragment
// cmake.ProfileFingerprintInput.WindowsLinkLibrary *cmake.FingerprintFile
// build.PlanInput.WindowsLinkLibrary *windowsLinkLibraryBinding
// configureStepState.WindowsLinkLibrary *windowsLinkLibraryBinding (omitempty)
// configureStepState.WindowsLinkConsumers []string (omitempty)

// Extend both existing fingerprint helpers with this exact final parameter:
// windowsLinkLibrary *windowsLinkLibraryBinding
// fingerprintInput retains return type cmake.ProfileFingerprintInput.
// configureFingerprint retains return type string.
```

- [ ] **Step 1：写捕获、配置和真实链接证据红测。** `TestWindowsSDKLibraryBinding` 使用临时 SDK 根，在 `Lib/10.0.26100.0/um/x64/winmm.lib` 创建常规文件，并提供已选环境的 WINDOWSSDKDIR、WINDOWSSDKVERSION、架构；父环境设置不同 SDK 作为反例。断言服务只捕获所选 SDK 文件、SHA 为实际字节摘要，清掉捕获环境后不能回退父环境。没有合法 SDK、缺文件/目录代文件、不同架构、别名、SDK 身份/库字节改变均拒绝。

```go
sdkRoot := t.TempDir()
sdkVersion := "10.0.26100.0"
libraryPath := filepath.Join(sdkRoot, "Lib", sdkVersion, "um", "x64", "winmm.lib")
if err := os.MkdirAll(filepath.Dir(libraryPath), 0700); err != nil { t.Fatal(err) }
libraryBytes := []byte("fixture import library bytes")
if err := os.WriteFile(libraryPath, libraryBytes, 0600); err != nil { t.Fatal(err) }
canonicalLibrary := canonicalPlannerFile(t, libraryPath)
sum := sha256.Sum256(libraryBytes)
wantSHA256 := hex.EncodeToString(sum[:])
instance := toolchain.Instance{Family: toolchain.FamilyClangCL, TargetArchitecture: "x64",
    Environment: []string{"WINDOWSSDKDIR=" + sdkRoot, "WINDOWSSDKVERSION=" + sdkVersion + `\`}}
binding, err := captureWindowsWinMMLibrary(instance)
if err != nil { t.Fatal(err) }
if binding.File.Path != canonicalLibrary || binding.File.SHA256 != wantSHA256 {
    t.Fatalf("library authority not captured: %#v", binding)
}
if err := os.WriteFile(binding.File.Path, []byte("changed"), 0600); err != nil { t.Fatal(err) }
if err := verifyWindowsWinMMLibrary(binding); err == nil { t.Fatal("changed library accepted") }
```

`TestPlannerWindowsSDKLibrary` 用 W1 合法图加 `target_link_libraries(core PUBLIC "${UNIT_TEST_IDE_WINMM_LIBRARY}")`，测试普通和 coverage 两种配置都只有一个服务拥有的 `-DUNIT_TEST_IDE_WINMM_LIBRARY:FILEPATH=<绝对路径>`，每个相应进程的 LaunchInputs 都含同一库身份/摘要；覆盖调用者 cache/preset、set/unset/list/property 写保留名的反例。

`TestFileAPILinkFragments` 用现有完整 File API fixture 添加真实 `link.commandFragments`，断言顺序/role 保留、原 reply 输入仍认证、clone 修改不影响原值；超长/超量/错误类型/NUL 拒绝。`TestWindowsSDKLinkEvidence`：确认绑定路径；裸 winmm、另一个 SDK 的同名库、拼接路径子串、缺少消费目标、仅 STATIC archive 信息、无片段、未检查的 response file 均不能作为证据。增加 `core → middle → executable` 间接消费正例和丢失末端库片段反例；确认配置 state 记录非空末端消费者，随后修改其中一个的片段会在保存成功前拒绝。

- [ ] **Step 2：运行定向红测。**

```powershell
go test ./apps/test-service/internal/build ./apps/test-service/internal/cmake -run 'TestWindowsSDK|TestPlannerWindowsSDK|TestFileAPILinkFragments|TestConfigureFingerprintWindowsSDK' -count=1 -v
```

新接口未实现时编译失败只算契约 red；仍需保留 W1 的行为失败及后续现存 Plan 语义反例，不把未编译称为 native red。

- [ ] **Step 3：实现唯一文件绑定及配置参数。** 只接收选中实例的正规化 SDK 环境；验证家族、SDK 根身份、版本、目标架构，定位唯一 `Lib/<version>/um/<arch>/winmm.lib` 并 `SnapshotLaunchInput`，读取上限 4 MiB、数量只能 1。把原 SDK 目录身份和文件身份一起复核，不允许整个 SDK 成为 allowedRoots。

```go
argument := "-D" + windowsWinMMVariable + ":FILEPATH=" + filepath.ToSlash(binding.File.Path)
args = append(args, argument)
launchInputs = append(launchInputs, binding.File)
```

两行置于正常 configure 参数构造与同一份 LaunchInputs 组装位置，普通/插桩共用；不能仅覆盖率路径设变量。共享 W1 解析确认项目采用了新配方/明确系统库引用再要求库绑定，不对无关 GCC/Linux 项目增加 SDK 依赖，不按夹具目录/环境标志放行。使用新配方的普通构建也必须执行闭合校验；不能因尚未开启 coverage 而允许保留变量重写。保留名若被项目/preset 写入，无论值是否相同均拒绝。

`validateTargetLinkLibraries` 对完整保留变量只认匹配的服务 binding；其他变量和裸库名仍拒绝。声明图记录 target→target 链接关系和 scope，从系统库所属目标沿可传递关系计算真正的可执行消费者，不能要求 STATIC 库具有 link fragments，也不能漏掉间接消费者。先计算经过 W1 验证的绑定/消费关系，再序列化配置 state；现有代码先 configureState 后 Plan 的顺序必须调整这一纯数据准备阶段，不能把空 consumer 列表当验证成功。配置 state 保存绑定和消费目标，API 无新增用户可填的库路径。对普通/coverage/no-configure 路径都验证绑定，缺失 SDK 不生成成功计划。

- [ ] **Step 4：读取并验证实际链接输入及重配置身份。** 新 `LinkFragments` 直接从受快照保护的 target reply 读取，保留有界顺序；最多 4096 个片段、每片段沿用 16 KiB 上限，并仍受 8 MiB 单文件/16 MiB 总 File API 上限约束。角色仅 flags/libraries/libraryPath/frameworkPath；这是数据，不是可执行命令权限。CloneTargets 在处理 CompileUnits 之前独立复制该 slice，避免原函数的 nil continue 漏拷贝。

对 Windows libraries 片段使用有限、本机参数格式 tokenizer，不调用 shell，不用字符串 contains 充当路径相等。测试带空格路径、引号、连续反斜杠/转义引号、空 token、超长、未闭合引号、元字符和 `@response`；不支持的表示拒绝证据。逐个已验证消费目标必须出现绑定的完整绝对路径 token；出现另一个 winmm token/隐式 winmm 搜索则拒绝，不能把平台原有其他默认库误当本轮新增白名单。

在 `Coordinator.Succeeded` 配置成功后读取当前 reply、验证实际链接证据和 SDK/file 身份，然后才保存配置成功；`prepare` 的 no-configure 与 `Targets` 同样重新捕获/复核，旧 cache 不能绕过。SDK 库进入配置指纹：在 `ProfileFingerprintInput`、私有 JSON payload、`validProfileFingerprintInput` 中增加可选文件字段，nil 使用 omitempty 保持不适用平台的指纹行为；所有 `fingerprintInput`/`configureFingerprint`/`configureState` 调用点传同一绑定，不从旧 target reply 猜来源。库变动必须需要重配置或明确拒绝，不能继续沿用旧成功记录。

- [ ] **Step 5：运行定向绿测、生命周期回归并本地提交。**

```powershell
go test ./apps/test-service/internal/build ./apps/test-service/internal/cmake -run 'TestWindowsSDK|TestPlannerWindowsSDK|TestFileAPILinkFragments|TestConfigureFingerprint|TestCoordinator|TestPlannerRejects|TestExecutionBoundary' -count=1
git diff --check
git add -- apps/test-service/internal/build/windows_sdk_library.go apps/test-service/internal/build/windows_sdk_library_test.go apps/test-service/internal/build/planner.go apps/test-service/internal/build/planner_test.go apps/test-service/internal/build/cmake_launch_plan.go apps/test-service/internal/build/coordinator.go apps/test-service/internal/build/coordinator_test.go apps/test-service/internal/cmake/fileapi.go apps/test-service/internal/cmake/fileapi_test.go apps/test-service/internal/cmake/fingerprint.go apps/test-service/internal/cmake/fingerprint_test.go
git commit -m 'fix: bind Windows framework system library to selected SDK'
```

独立审查重点是服务拥有的来源、普通/coverage 相同语义、实际链接 token、配置成功保存前门禁、再配置/重启与 lease/输入替换。若需要超出本设计的整套 Ninja 命令解释或外部库搜索，报告具体 NEEDS_CONTEXT，不私自扩大范围。

## Task 3 (W3)：共享固定配方、闭合库存与 Windows fixture 接线

**Files:** 文件职责表 W3。原 `fixtures/test-generation/{cpputest,unity}/CMakeLists.txt` 与 `tests/CMakeLists.txt` 保留给 Linux。新 Windows 目录镜像只包含 CMake 两个文件，分类器源码、测试和 workspace config 共用原 fixture，不复制第二套业务逻辑。

**Consumes:** W1 的静态声明/路径与 W2 的只读系统库变量；框架已准备清单及 `verifyPreparedFrameworkBundle`。不改变其锁定来源验证。

**Produces:** `tools/framework-bundle/framework-recipes.mjs` 导出 `verifyFrameworkRecipes(repositoryRoot: string): Promise<void>` 与 `copyFrameworkRecipeInputs(repositoryRoot: string, workspaceRoot: string): Promise<void>`；前者严格校验库存/摘要/源码相对路径，后者仅复制两个已认证配方到现有新建 native workspace 的 `.unit-test-ide/inputs/framework-recipes`，wx/no-overwrite，不改 live user 工程。库 SHA-256 由服务拥有，不写入 SDK 静态库存冒充系统库来源。

- [ ] **Step 1：写资源和 staging 红测。** 新 `.test.mjs` 导入上述两个导出，测试缺资源、配方摘要修改、未知库存字段、越界来源、含 Mock/动态命令、重复目标及目标版本不符；正例复制字节与 SDK 原件完全相同；已存在目的文件不可覆盖，Linux native setup 不选择 Windows CMake 镜像。库存包含两个且仅两个 recipe：id、framework、version、target、frameworkRoot、sources、defines、systemLibraryVariable、path、sha256；schemaVersion=1。严格遍历条目键，不把 manifest 变成任意可执行 CMake 入口。

```javascript
const repositoryRoot = resolve(import.meta.dirname, "../..");
const workspaceRoot = await mkdtemp(join(tmpdir(), "utide-framework-recipes-"));
await verifyFrameworkRecipes(repositoryRoot);
await copyFrameworkRecipeInputs(repositoryRoot, workspaceRoot);
assert.deepEqual(
  await readFile(join(workspaceRoot, ".unit-test-ide/inputs/framework-recipes/windows-unity-2.6.1.cmake")),
  await readFile(join(repositoryRoot, "sdk/cmake/framework-recipes/windows-unity-2.6.1.cmake")),
);
await assert.rejects(copyFrameworkRecipeInputs(repositoryRoot, workspaceRoot));
```

使用 node:test、assert、fs/promises、已有临时目录清理规则；Windows 删除只处理已确认在临时 workspace 内的目的目录。

- [ ] **Step 2：跑红测，然后创建固定配方。**

```powershell
node --test tools/framework-bundle/framework-recipes.test.mjs
```

CppUTest 19 个 core 文件以 `${CMAKE_CURRENT_LIST_DIR}/../cpputest/src/CppUTest/` 为前缀，逐项列出：CommandLineArguments.cpp、MemoryLeakWarningPlugin.cpp、TestHarness_c.cpp、TestRegistry.cpp、CommandLineTestRunner.cpp、SimpleString.cpp、SimpleStringInternalCache.cpp、TestMemoryAllocator.cpp、TestResult.cpp、JUnitTestOutput.cpp、TeamCityTestOutput.cpp、TestFailure.cpp、TestOutput.cpp、MemoryLeakDetector.cpp、TestFilter.cpp、TestPlugin.cpp、TestTestingFixture.cpp、SimpleMutex.cpp、Utest.cpp。另列 VisualCpp 平台文件，不 glob、不调用 upstream root。

```cmake
target_include_directories(CppUTest PUBLIC "${CMAKE_CURRENT_LIST_DIR}/../cpputest/include")
target_compile_definitions(CppUTest PUBLIC CPPUTEST_USE_OWN_CONFIGURATION=1 CPPUTEST_MEM_LEAK_DETECTION_DISABLED=1)
target_link_libraries(CppUTest PUBLIC "${UNIT_TEST_IDE_WINMM_LIBRARY}")
```

前面必须有 `add_library(CppUTest STATIC ...)` 的上述 20 条具体源路径。Unity 配方为：

```cmake
add_library(unity STATIC "${CMAKE_CURRENT_LIST_DIR}/../unity/src/unity.c")
target_include_directories(unity PUBLIC "${CMAKE_CURRENT_LIST_DIR}/../unity/src")
```

库存 sha256 必须根据最终配方字节实际计算并写入，不留示例摘要；sources 与固定清单完全一致。原锁不变；检查实际上游配置宏分支无需 CppUTestGeneratedConfig.h。README 给完整输入布局、根 include、目标链接与受管生成命令，声明兼容子集、Windows SDK、no mock、用户显式集成及新候选包资源仍需打包验收。

- [ ] **Step 3：接线 Windows 镜像与正常 staging。** `inspectNativeFirstCoverageReport` 仍先复制原 fixture 和已认证框架树；平台 win32 时再复制两个 SDK 配方及该 framework 的两个 Windows CMake 文件到新建测试 workspace。替换仅发生在测试创建的 workspace、服务启动之前，不在运行产品发现/覆盖率时自动改用户文件。Linux 不执行此替换，不复制 Windows CMake 镜像进实际输入图。

Windows root CMake 在生产 target 声明前显式包含：

```cmake
include("${CMAKE_SOURCE_DIR}/.unit-test-ide/inputs/framework-recipes/windows-cpputest-4.0.cmake")
```

Unity 使用其对应文件；其余 classifier/include/标准/enable_testing/add_subdirectory(tests) 保留。Windows CppUTest tests 的链接只剩 `classifier CppUTest`；删除仅新 Windows 镜像里的 upstream options、动态 UTIDE_CPPUTEST_ROOT 和 CppUTestExt 依赖。Unity tests 不重声明已定义的 unity 库，保留既有测试入口。新资源使用普通生产校验，无额外 test flag 或 project-name 白名单。

- [ ] **Step 4：定向 staging/模型绿测与本地提交累计改动。**

```powershell
node --test tools/framework-bundle/framework-recipes.test.mjs
pnpm exec tsc -b tools/service-probe/tsconfig.json --force
node --test tools/service-probe/dist/native-test-generation.test.js tools/service-probe/dist/test-generation-e2e.test.js
git diff --check
git add -- sdk/cmake/framework-recipes tools/framework-bundle/framework-recipes.mjs tools/framework-bundle/framework-recipes.test.mjs tools/service-probe/src/native-test-generation.ts tools/service-probe/src/native-test-generation.test.ts tools/service-probe/fixtures/test-generation/cpputest/windows tools/service-probe/fixtures/test-generation/unity/windows
git commit -m 'fix: use audited Windows framework recipes for native generation'
```

提交前核对：前两个 TS 文件包含原 Task 4 工作，其余 Task 4 fixture/UI 文件仍保留；原 vendor/manifest/helper 与 Linux root/test CMake 无 diff。W3 资源接口摘要/check 和 root path 审查通过后才恢复原 native。不要为仅静态 staging 测试重复跑整套 framework prepare/tar/download 矩阵。

## Task 4 (W4)：恢复原 Task 4，收口真实执行与覆盖率

**Files/Interfaces:** 使用[原计划 Task 4](2026-10-08-coverage-detail-first-report-contract.md#task-4-正式-ipc客户端ui离线生成验收收口)，保留 `readNativeCoverageLineEvidence`、`withNativeFirstCoverageReport` 和原服务生命周期；W1–W3 是新的前置依赖。原 Task 4 实施者 `/root/first_report_task4` 可恢复时复用该实施者及报告，不重派已完成任务。

- [ ] **Step 1：核对原现场的真实断言并构建本次服务。** 保留 helper 分别对文件/函数分页、limit=200、report/generation 绑定、非空页/重复行/循环 cursor/累计 2,000,000 预算及全 null 历史校验；UI 必须使用同一存活正式服务，无直接 SQLite 捷径。无新增同类单测时消费此前 45/45、service 20/20 和 store/race 记录，不把它们伪称为本次新执行。

```powershell
node tools/service-probe/build-service.mjs
pnpm exec tsc -b tools/service-probe/tsconfig.json apps/code-oss-extension/tsconfig.json --force
```

每条 exit 0。服务实际使用生产 bundle 根；已准备 CMake manifest 的身份为 `5c574826f31d6d6b02dfd620b47dfd567edc934d8d4d22ff872fe7847aa87cf0`，不能写一个新 manifest 掩盖缺失。

- [ ] **Step 2：只跑真正此前失败的 UI native 入口。**

```powershell
node --test apps/code-oss-extension/test/coverage-first-report-native.test.mjs
```

要求 exit 0：首次真实 CppUTest 行 5/5、分支 2/4；文件和函数页面都有实际行命中；UI tree 能展开、历史不可比较，无 +0；行 4/5 显示 1/2 branches。若失败，保存新尝试阶段与工作区，只诊断此根因，不开启 full verify 或 hosted。

- [ ] **Step 3：真实两个框架的生成、确认应用和全部测试复跑。** 运行现有 CLI，候选必须分别通过编译/执行及原增量/no-regression 门禁；应用后 `target_sources`/Unity 新目标再次通过校验。确认生成源码可读，维护修改不被启动覆盖，再执行通过正式命令复跑。

```powershell
$env:GITHUB_SHA = (git rev-parse HEAD).Trim()
pnpm --filter @unit-test-ide/service-probe test:e2e:native:test-generation -- --platform win32
```

原 TS fixture/UI 文件尚脏时，这次产物只作为 dirty-working-tree 检查，必须同时记录 HEAD 和 diff 摘要，不能冒充 clean candidate。CppUTest/Unity 基准分支提升目标 2/4→4/4，现有已覆盖行/分支不回退；每个 framework 的真实 receipt 必须存在，不用 stdout 仅列框架名代替。没有真实生成产物不写 PASS。

- [ ] **Step 4：一次必要回归及审查，完成源提交。** 新 SDK/file lifecycle 改动若涉及共享状态/lease，运行新增 build/cmake 定向 race，不重复未改动 taskstore 的既有 race。已有任务 4 的 store/runtime/session 原绿测只有其来源被改变时才重跑。所有关键局部门槛通过后运行一次本轮尚未执行的 `pnpm verify`，记录实际 exit 和每个平台 skipped 状态，不因预期负例输出误认失败，也不把真实 exit 1 当输出噪声。

```powershell
pnpm verify
git diff --check
git add -- tools/service-probe/src/test-generation-e2e.test.ts apps/code-oss-extension/test/coverage-first-report-native.test.mjs docs/superpowers/evidence/phase10/2026-10-08-first-report-contract.md docs/superpowers/evidence/phase10/2026-10-08-windows-cmake-coverage-compatibility.md
git commit -m 'test: close native first-report coverage and generation acceptance'
```

证据文件必须先实际创建，列命令、退出码、尝试/框架/配方/SDK/工具链身份、已执行与未执行门槛；不能为满足 git add 写虚构结果。Task 4 期间若确有源修复，按审查通过的明确路径额外 stage，绝不 `git add -A`。

- [ ] **Step 5：干净提交身份再验收一次，而非全套重跑。**

```powershell
$Dirty = @(git status --porcelain --untracked-files=normal)
if ($Dirty.Count -ne 0) { throw 'Candidate source worktree is not clean' }
$Candidate = (git rev-parse HEAD).Trim()
$env:GITHUB_SHA = $Candidate
pnpm --filter @unit-test-ide/service-probe test:e2e:native:test-generation -- --platform win32
if ($LASTEXITCODE -ne 0) { throw 'Clean candidate native acceptance failed' }
$Receipt = Get-Content -Raw -LiteralPath '.native-e2e/artifacts/windows/test-generation-report.json' | ConvertFrom-Json
if ($Receipt.candidateCommit -ne $Candidate -or $Receipt.platform -ne 'windows-x64') { throw 'Native receipt candidate identity mismatch' }
git status --short
```

再次读取本次 receipt 的 CppUTest 与 Unity 实际结果/比较来源。候选后证据单独提交时注明被验收源码 SHA 与证据提交 SHA，不能说证据文件已在被验收 SHA 中。

- [ ] **Step 6：任务审查与全分支审查。** 全分支当前本地 master 基准为 `59928bac784fedff614f7d154ef227f15b191eee`；使用执行前只读核对后的真实 merge-base 审查整个 PR #39 差异，而非只审最后三个提交。triage 原 Windows cleanup、Windows 2022 LLVM tar 停滞以及现有输出/换行 minor；独立未闭合项不得因局部 native 通过关闭。所有 Critical/Important 解决后才进入授权外部步骤。

- [ ] **Step 7：控制器消费既有授权，按当前来源运行 hosted。** 先读远端分支头、fast-forward关系、PR #39 head 和 workflow 输入/事件语义；普通推送 feature 分支到 GitHub/Gitee并更新该 PR，不创建重复 PR；push 已自动触发时不重复 dispatch。只跟踪候选对应的新 workflow；准确区分 PR head、实际 checkout 和 event 的合成 merge SHA，不把不同身份的产物互相替代。Linux 无本地 native PASS可用，必须由实际 hosted job 验证；Windows independent gate 失败照实记录，不删 gate。

## 设计覆盖与完成定义

| 设计要求 | 计划归属与证据 |
| --- | --- |
| 静态声明、target_sources、真实目标和源码 | W1 行为 red、上下文/目标/snapshot 正反例；W4 应用后再构建 |
| 当前 SOURCE/LIST/BINARY 语义、变量不可重赋值、预算 | W1 上下文及递归/别名/变量/预算负例 |
| SDK 唯一绝对库、服务参数、实际链接和缓存身份 | W2 捕获/计划/片段/指纹/生命周期测试及 W4 原生链接 |
| 固定无 Mock 的真实框架、vendor 锁不变、同 SDK 资源 | W3 库存/摘要/复制测试和源码 diff；W4 两框架真实执行 |
| 首份真实 IPC、nullable 历史、文件函数 UI | 恢复原 Task 4 UI native；W4 Step 2 |
| 生成可维护用例、真实增量、应用后一键复跑 | W4 Step 3/5 的两框架 receipts 和源码维护复跑 |
| 精确提交/attempt/PR/workflow 身份与授权界限 | W4 Step 4–7；本地与 hosted 分开记录 |
| 包内资源和剩余独立门槛不假称完成 | W3 README、W4 evidence 的明确未执行项 |

实施结束须有 W1–W3 的实际验证、独立审查和原 Task 4 的真实 native 证据；尚未执行的 hosted/Linux、候选打包资格、Windows 独立问题、历史比较完善及 Phase 8 签名/legal 继续明确保留。计划文件的 checkbox 仅在对应实际结果成立后勾选。
