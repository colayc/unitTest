# Phase 3D：Bundled CMake 与 Native E2E 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**目标：** 固定并校验 Windows/Linux x64 CMake 4.3.4 runtime，用真实 MSVC、clang-cl、GCC、Clang 完成 Protocol v1.2 从 TypeScript Client 到 Go Service 的 native build E2E，并把 Phase 3 门禁固定到明确的 GitHub Hosted Runner。

**架构：** `tools/cmake-bundle` 是显式联网的构建准备工具，普通产品运行和 `pnpm verify` 均不下载依赖。`tools/service-probe` 复制 sample workspace 到隔离目录，通过 Protocol v1.2 驱动真实 configure/build 并生成路径无关的工具链报告。CI 在准备阶段取得并校验 CMake bundle，随后分别在 Windows 与 Ubuntu 上强制执行对应的两个 compiler family。

**技术栈：** TypeScript 6.0.3、Node.js 24.18.0、pnpm 11.4.0、Go 1.26.5、CMake 4.3.4、MSVC、clang-cl/LLVM、GCC、Clang、GitHub Actions。

## 全局约束

- CMake 固定为 `4.3.4`，只能通过 review 后的 manifest 变更升级，禁止 `latest`。
- 支持的 bundle key 固定为 `win32-x64` 和 `linux-x64`。
- archive 只从 `https://cmake.org/files/v4.3/` 获取，并在解压前校验 SHA-256。
- 解压后再次校验 executable 与 `LICENSE.rst` SHA-256，再执行 `cmake -E capabilities` 验证 version。
- bundle 输出位于 `.bundled-tools/cmake/<cmake-version>/<platform-key>/`，不提交二进制归档或解压内容。
- `pnpm verify`、Service 产品模式和所有 native test 的执行阶段均不联网。
- compiler 不进入 bundle；由 Phase 3B Adapter 探测本机 MSVC、clang-cl、GCC 和 Clang。
- local native E2E 可报告缺失的非必需 toolchain；CI 通过 required-family 环境变量把缺失变成失败。
- golden diagnostics 必须规范化 workspace、build root、drive letter、路径分隔符和 Hosted Runner 安装路径。
- Windows CI 固定 `windows-2025-vs2026`，Linux CI 固定 `ubuntu-24.04`。
- Phase 3 不执行 coverage；Windows clang-cl 只验证 Phase 5 所需的 LLVM toolchain capability。
- 所有 Markdown 文档使用中文，English technical terms 保留 English 格式。

---

### Task 1：固定 CMake Bundle Manifest 与准备工具

**文件：**
- 创建： `tools/cmake-bundle/manifest.json`
- 创建： `tools/cmake-bundle/prepare.mjs`
- 创建： `tools/cmake-bundle/prepare.test.mjs`
- 创建： `tools/cmake-bundle/README.md`
- 修改： `.gitignore`
- 修改： `package.json`

**接口：**
- 输入： 显式命令、固定 manifest 和 CMake 官方 archive。
- 输出：

```js
export function platformKey(platform = process.platform, arch = process.arch)
export function validateManifest(manifest)
export function sha256File(filePath)
export function verifyInstalledFiles(root, files)
export async function prepareBundle({ key, outputRoot, download })
```

- [x] **Step 1：写 manifest 解析、平台选择和摘要失败测试**

`prepare.test.mjs` 使用 `node:test`、临时目录和本地 byte fixtures，不访问网络。至少覆盖：

- 当前支持的两个 platform key；
- 未支持 architecture；
- 非 HTTPS、非 `cmake.org`、含 redirect target override 的 URL；
- version、archive、root directory、license 字段缺失；
- archive digest 不匹配时不创建输出目录；
- executable 或 license digest 不匹配时删除 staging；
- 已存在且完整的 bundle 可幂等复用；
- 目标目录替换失败时保留旧的有效 bundle。

核心断言：

```js
test("digest mismatch never publishes staging", async () => {
  const outputRoot = await mkdtemp(join(tmpdir(), "cmake-bundle-"));
  await assert.rejects(
    prepareBundle({
      key: "linux-x64",
      outputRoot,
      download: async (destination) => writeFile(destination, "tampered"),
    }),
    /archive SHA-256 mismatch/,
  );
  assert.equal(await exists(join(outputRoot, "4.3.4", "linux-x64")), false);
});
```

- [x] **Step 2：运行准备工具测试并确认失败**

运行：

```powershell
node --test tools/cmake-bundle/prepare.test.mjs
```

预期： FAIL，原因是 manifest 与 `prepare.mjs` 尚不存在。

- [x] **Step 3：提交固定 manifest**

`manifest.json` 写入已由 CMake 官方 SHA-256 清单和官方归档复核的固定值：

```json
{
  "schemaVersion": 1,
  "cmakeVersion": "4.3.4",
  "license": "BSD-3-Clause",
  "archives": {
    "win32-x64": {
      "url": "https://cmake.org/files/v4.3/cmake-4.3.4-windows-x86_64.zip",
      "archiveSha256": "86e5fcafb38bdf58346a78b187c7b6b4f252ae5242cffe24c463a92bbd2e77d1",
      "rootDirectory": "cmake-4.3.4-windows-x86_64",
      "executable": "bin/cmake.exe",
      "licensePath": "doc/cmake/LICENSE.rst",
      "installedFiles": {
        "bin/cmake.exe": "1aa884bf1f4949327fffcc8ee4a97c2d684bdc1d0a64b71f01dc16321c7fbc64",
        "doc/cmake/LICENSE.rst": "cd944d878806fee998ef3f88ca41ec060ae198bd8ba615e284f7d8d90c25593e"
      }
    },
    "linux-x64": {
      "url": "https://cmake.org/files/v4.3/cmake-4.3.4-linux-x86_64.tar.gz",
      "archiveSha256": "ca6f08ccbd5e6b0a9068d33317d0d1aff7278d08cccaed4529b8fbead7942a68",
      "rootDirectory": "cmake-4.3.4-linux-x86_64",
      "executable": "bin/cmake",
      "licensePath": "doc/cmake/LICENSE.rst",
      "installedFiles": {
        "bin/cmake": "8542b512ac147329e03de375583665a64f02afb65d6c4665099390be103ac2d0",
        "doc/cmake/LICENSE.rst": "4382e7c1879ac90e3f101a395d23846fa4dbcaa1eed7265b43681e348754825d"
      }
    }
  }
}
```

- [x] **Step 4：实现显式下载、校验、解压和原子发布**

`prepare.mjs` 必须：

1. 将 manifest 读为普通 data，并严格拒绝多余字段和不支持的 key；
2. 下载到 output root 同 volume 的随机 staging 目录；
3. `createHash("sha256")` 流式计算 archive 摘要；
4. 只调用受控的系统 `tar -xf <archive> -C <staging>`，不拼接 Shell 字符串；
5. 在解压前列出 archive entries 并拒绝绝对路径、`..`、alternate data stream 和 manifest root 之外的 entry；解压后拒绝 symlink/junction/reparse point；
6. 校验 `installedFiles`；
7. 执行 `<cmake> -E capabilities`，解析 JSON，并要求 `version.string === "4.3.4"`；
8. 要求 license 文件存在且 `license === "BSD-3-Clause"`；
9. 把 `{schemaVersion,key,cmakeVersion,archiveSha256,installedFiles}` 写入 version/platform 目录的 `bundle-state.json`，并把 byte-for-byte 相同的固定 manifest 发布为 output root 的 `manifest.json`；
10. 通过同 volume rename 发布不可变的 platform 目录；目标已存在时只验证并复用，不覆盖。任何失败都删除 staging，不破坏现有有效 bundle。

下载函数只允许 manifest 中的 URL，并在 redirect 后再次要求最终 URL 为 HTTPS `cmake.org` 固定路径；禁止调用方传入 URL、header、proxy credential 或 executable。`--output` 必须位于 repository root 或 CI 显式提供的临时根内。

- [x] **Step 5：加入离线与显式联网脚本**

`.gitignore` 加入：

```gitignore
.bundled-tools/
.native-e2e/
```

`package.json` 加入：

```json
{
  "scripts": {
    "prepare:cmake-bundle": "node tools/cmake-bundle/prepare.mjs",
    "test:cmake-bundle": "node --test tools/cmake-bundle/prepare.test.mjs"
  }
}
```

`test:cmake-bundle` 进入普通 `test` 链；`prepare:cmake-bundle` 不进入 `test` 或 `verify` 链。

- [x] **Step 6：写中文供应链说明并运行测试**

`README.md` 记录固定来源、摘要更新流程、BSD 3-Clause 分发义务、显式准备命令、输出 layout、离线边界和 Phase 8 签名责任。

运行：

```powershell
pnpm test:cmake-bundle
pnpm test
git diff --check
```

预期： PASS；测试过程中没有外网请求，`.bundled-tools` 不出现在 tracked files。

- [x] **Step 7：提交**

```powershell
git add .gitignore package.json tools/cmake-bundle
git commit -m "build: pin and verify bundled cmake"
```

---

### Task 2：Native Sample Workspaces 与 Golden Diagnostics

**文件：**
- 创建： `testdata/toolchains/preset-project/CMakeLists.txt`
- 创建： `testdata/toolchains/preset-project/CMakePresets.json`
- 创建： `testdata/toolchains/preset-project/src/math.cpp`
- 创建： `testdata/toolchains/preset-project/src/main.cpp`
- 创建： `testdata/toolchains/fallback-project/CMakeLists.txt`
- 创建： `testdata/toolchains/fallback-project/src/main.cpp`
- 创建： `testdata/toolchains/failures/compiler/CMakeLists.txt`
- 创建： `testdata/toolchains/failures/compiler/src/main.cpp`
- 创建： `testdata/toolchains/failures/linker/CMakeLists.txt`
- 创建： `testdata/toolchains/failures/linker/src/main.cpp`
- 创建： `testdata/toolchains/failures/configure/CMakeLists.txt`
- 创建： `testdata/toolchains/golden/compiler-gcc-clang.json`
- 创建： `testdata/toolchains/golden/compiler-msvc-clang-cl.json`
- 创建： `testdata/toolchains/golden/linker-gcc-clang.json`
- 创建： `testdata/toolchains/golden/linker-msvc-clang-cl.json`
- 创建： `testdata/toolchains/golden/configure.json`
- 创建： `tools/service-probe/src/native-fixture.ts`
- 创建： `tools/service-probe/src/native-fixture.test.ts`

**接口：**
- 输入： read-only repository fixtures。
- 输出：

```ts
export type NativeFixtureName =
  | "preset-project"
  | "fallback-project"
  | "compiler-failure"
  | "linker-failure"
  | "configure-failure";

export async function copyNativeFixture(
  fixture: NativeFixtureName,
  destinationParent: string,
  directoryName?: string,
): Promise<string>;

export function normalizeNativeDiagnostic(
  diagnostic: Diagnostic,
  roots: {
    workspace: string;
    build: string;
    external?: readonly string[];
  },
): Diagnostic;

export interface GoldenDiagnosticExpectation {
  kind: "configure" | "compiler" | "linker";
  severity: "warning" | "error";
  file?: string;
  line?: number;
  codePattern?: string;
  messageContains: string;
}
```

- [x] **Step 1：写 fixture copy 与 diagnostic normalization 测试**

测试覆盖：

- 默认复制到新的临时 workspace，不修改仓库 fixture；
- 目录名 `"native 空格 Ω"` 在 Windows/Linux 都能创建；
- symlink、junction 和 absolute path 不从 fixture 复制；
- `C:\a\b.cpp` 与 `/a/b.cpp` 均规范为 `<workspace>/...`；
- build root、drive letter 和 `\` 被稳定规范化；
- diagnostic `code`、`severity`、line/column 不被丢失；
- normalizer 不替换 source message 中与路径无关的普通文本。

- [x] **Step 2：运行 fixture tests 并确认失败**

运行：

```powershell
pnpm --filter @unit-test-ide/service-probe test -- native-fixture.test
```

预期： FAIL，原因是 helper 和 fixture 尚不存在。

- [x] **Step 3：建立 preset 与 generated fallback 正常项目**

`preset-project/CMakePresets.json` 使用 CMake Presets version `10`，包含 `unit-test-ide-debug` configure preset 和同名 build preset；binary directory 固定为 `${sourceDir}/.native-e2e/build/preset`，generator 由该 preset 明确选择 `Ninja`。它验证“Preset 语义由 CMake 保留”的路径：

```json
{
  "version": 10,
  "cmakeMinimumRequired": {"major": 3, "minor": 31, "patch": 0},
  "configurePresets": [
    {
      "name": "unit-test-ide-debug",
      "generator": "Ninja",
      "binaryDir": "${sourceDir}/.native-e2e/build/preset",
      "cacheVariables": {"CMAKE_BUILD_TYPE": "Debug"}
    }
  ],
  "buildPresets": [
    {
      "name": "unit-test-ide-debug",
      "configurePreset": "unit-test-ide-debug"
    }
  ]
}
```

`preset-project/CMakeLists.txt`：

```cmake
cmake_minimum_required(VERSION 3.31)
project(unit_test_ide_preset LANGUAGES CXX)

add_library(math STATIC src/math.cpp)
target_compile_features(math PUBLIC cxx_std_20)

add_executable(sample_app src/main.cpp)
target_link_libraries(sample_app PRIVATE math)
```

`math.cpp` 与 `main.cpp` 使用无平台依赖的确定性代码：

```cpp
int add(int left, int right) {
  return left + right;
}
```

```cpp
int add(int left, int right);

int main() {
  return add(20, 22) == 42 ? 0 : 1;
}
```

`fallback-project` 不包含 Presets，由 Service 为所选 toolchain 生成 profile、generator 和 Service data-dir 下的 binary directory。其完整 `CMakeLists.txt` 为：

```cmake
cmake_minimum_required(VERSION 3.31)
project(unit_test_ide_fallback LANGUAGES CXX)

add_executable(sample_app src/main.cpp)
add_executable(secondary_app src/main.cpp)
target_compile_features(sample_app PRIVATE cxx_std_20)
target_compile_features(secondary_app PRIVATE cxx_std_20)

add_custom_target(
  slow_target
  COMMAND "${CMAKE_COMMAND}" -E sleep 30
  VERBATIM
)
```

`fallback-project/src/main.cpp`：

```cpp
int main() {
  return 0;
}
```

- [x] **Step 4：建立确定性失败项目与 golden**

Compiler fixture 使用一个明确的未知 identifier，确保所有 compiler 在同一源文件行产生 error：

```cpp
int main() {
  const int baseline = 1;
  return baseline + UNIT_TEST_IDE_UNKNOWN_IDENTIFIER;
}
```

其 `CMakeLists.txt` 只包含：

```cmake
cmake_minimum_required(VERSION 3.31)
project(unit_test_ide_compiler_failure LANGUAGES CXX)
add_executable(compiler_failure src/main.cpp)
```

Linker fixture 声明但不定义 `native_missing_symbol()`，确保产生 linker error：

```cpp
int native_missing_symbol();

int main() {
  return native_missing_symbol();
}
```

其 `CMakeLists.txt` 只包含：

```cmake
cmake_minimum_required(VERSION 3.31)
project(unit_test_ide_linker_failure LANGUAGES CXX)
add_executable(linker_failure src/main.cpp)
```

Configure fixture 的完整 `CMakeLists.txt` 为：

```cmake
cmake_minimum_required(VERSION 3.31)
project(unit_test_ide_configure_failure LANGUAGES CXX)
message(FATAL_ERROR "UNIT_TEST_IDE_CONFIGURE_FAILURE")
```

Golden 只记录跨版本稳定基准：

```json
{
  "minimum": [
    {
      "kind": "compiler",
      "severity": "error",
      "file": "<workspace>/src/main.cpp",
      "line": 3,
      "messageContains": "UNIT_TEST_IDE_UNKNOWN_IDENTIFIER"
    }
  ]
}
```

不同 compiler family 分文件记录允许的 `codePattern`；MSVC/clang-cl 文件使用 `^(C[0-9]+|COMPILER_ERROR)$`，GCC/Clang 文件使用 `^COMPILER_ERROR$`。clang-cl 未输出 numeric code 时由 Service 规范化为 `COMPILER_ERROR`，不能产生违反 Protocol/ArtifactStore 非空 code 不变量的 Diagnostic。Linker golden 的 `messageContains` 固定为 `native_missing_symbol`，configure golden 固定为 `UNIT_TEST_IDE_CONFIGURE_FAILURE`。完整原始 message 只检查 marker，不逐字固定 Hosted Runner 文案。

- [x] **Step 5：实现安全复制和规范化**

复制 helper 使用 `lstat` 后只允许 regular file/directory，按 code point 排序逐项复制；拒绝 symlink、junction/reparse point 和 device。destination 必须是 helper 新建的空目录。

normalizer 在分隔符统一前先使用 Phase 3B path identity 比较；只将已证明位于 workspace/build root 内的路径替换为 `<workspace>`/`<build>`。

- [x] **Step 6：运行 tests 并提交**

运行：

```powershell
pnpm --filter @unit-test-ide/service-probe test
pnpm test
git diff --check
```

预期： PASS。

```powershell
git add testdata/toolchains tools/service-probe/src/native-fixture.ts tools/service-probe/src/native-fixture.test.ts
git commit -m "test: add native cmake fixtures"
```

---

### Task 3：Linux GCC 与 Clang Native E2E

**文件：**
- 创建： `tools/service-probe/src/native-build.ts`
- 创建： `tools/service-probe/src/native-build-linux.test.ts`
- 创建： `tools/service-probe/src/native-report.ts`
- 修改： `tools/service-probe/package.json`
- 修改： `package.json`

**接口：**
- 输入： prepared CMake bundle、Phase 3C Service/Client、isolated fixture workspace。
- 输出：

```ts
export type RequiredToolchainFamily = "gcc" | "clang" | "msvc" | "clang-cl";

export interface NativeScenarioResult {
  platform: NodeJS.Platform;
  toolchainFamily: RequiredToolchainFamily;
  toolchainVersion: string;
  generator: string;
  cmakeVersion: string;
  scenarios: Record<string, "passed" | "skipped">;
}

export async function runNativeMatrix(options: {
  platform: NodeJS.Platform;
  requiredFamilies: readonly RequiredToolchainFamily[];
  artifactDirectory: string;
  workDirectory?: string;
}): Promise<readonly NativeScenarioResult[]>;
```

- [x] **Step 1：写 bundle 缺失与 required family 测试**

使用 dependency injection 的 fake Service launcher/discovery snapshot，先覆盖：

- `.bundled-tools` 不存在时，测试在启动 Service 前失败；
- bundle state/version/digest 不一致时失败；
- local 未声明 required family 时可输出明确 skip；
- `UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS=gcc,clang` 时任一缺失均失败；
- native runner 不读取 `PATH` 中的 CMake；
- native runner 给 Service 传入显式 trusted workspace 和 bundle CMake 路径。

- [ ] **Step 2：运行 Linux native tests 并确认失败**

在 Linux 上运行：

```bash
UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS=gcc,clang pnpm --filter @unit-test-ide/service-probe test:e2e:native:linux
```

预期： FAIL，原因是 native runner 和 scripts 尚不存在。

- [x] **Step 3：实现每个 toolchain 独立的真实 Service 生命周期**

对 GCC、Clang 分别：

1. 复制 fixture；
2. 以显式 `--trusted-workspace-root`、独立 data-dir、prepared bundle 路径启动 Service；
3. Client 协商 Protocol v1.2；
4. `workspace/inspect` 选择 family 匹配的 verified generated profile；
5. 构建 fallback default target；
6. 查询 File API target 并构建 `secondary_app`；
7. 停止 Service，验证进程树与 endpoint/token 清理。

禁止把 compiler executable、args 或 environment 从 test 直接传到 Protocol；test 只能按 discovery 返回的 `toolchainFamily` 选择 profile ID。

- [x] **Step 4：覆盖 configure lifecycle**

每个 family 的同一 profile 连续执行：

- 首次任务包含 `configure` 与 `build` Step；
- 第二次无变化只包含 `build` Step；
- 修改 workspace copy 中的 `CMakeLists.txt` 后第三次重新包含 `configure`；
- build target 不存在时在 Task 创建前返回 typed request error；
- stale workspace generation 在 Task 创建前被拒绝。

断言读取 task steps、event sequence 和 configure fingerprint artifact，不依赖 wall-clock 推断。

- [x] **Step 5：覆盖 diagnostics、Unicode、取消、超时和恢复**

对 GCC 与 Clang 至少覆盖：

- compiler failure、linker failure、configure failure 的 golden minimum；
- workspace 路径包含空格和 Unicode；
- configure 或 build timeout 产生 `timed_out`；
- 运行中 cancel 后 process group 全部退出；
- Service 在 queued task 后重启，恢复为设计定义的终态；
- 断线后以 sequence 重连，不重复或遗漏 Step/Diagnostic event；
- `../`、workspace 外 Preset include 与 symlink escape 在进程创建前拒绝。

取消/超时 fixture 由 CMake project 构建受控的长运行 custom target；native runner 等待 `task.step_started` 后再 cancel，禁止固定 sleep 作为同步机制。

- [x] **Step 6：写稳定工具链报告**

将以下内容写入 `.native-e2e/artifacts/linux/toolchain-report.json`：

- OS/platform/architecture；
- bundle CMake version 与 archive digest；
- compiler family/version/capabilities；
- generator；
- 每个 scenario 状态。

报告不得包含 environment、token、绝对 workspace/build path 或完整 compiler installation path。

- [ ] **Step 7：加入 scripts，运行并提交**

`tools/service-probe/package.json` 加入 `test:e2e:native:linux`；根 `package.json` 加入：

```json
{
  "scripts": {
    "test:e2e:native": "pnpm --filter @unit-test-ide/service-probe test:e2e:native"
  }
}
```

在 Linux 上运行：

```bash
pnpm prepare:cmake-bundle
UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS=gcc,clang pnpm test:e2e:native
pnpm verify
git diff --check
```

预期： GCC 与 Clang 全部 scenario PASS；`pnpm verify` 自身不触发 download。

```bash
git add package.json tools/service-probe
git commit -m "test: cover linux gcc and clang builds"
```

#### 2026-07-29 实施记录

Native matrix runner、离线 bundle preflight、required-family policy、路径无关报告及全部 scenario 已实现。Windows 本机使用 bundled CMake 4.3.4 与 MSVC 19.44 / Visual Studio 17 2022 完成真实验证，所有 scenario PASS；本机未安装符合生产发现策略的 clang-cl，因此按 local policy 记录为 `skipped`。

真实 MSVC 运行同时发现并修复了以下架构缺口：

- bundle prepare 的发布 layout 与 Go Resolver 约定不一致；
- Protocol v1.2 缺少选择 generated profile 所需的安全 Toolchain/Profile 关联与 workspace diagnostics；
- automatic toolchain ID 长度超过 generated profile 的旧上限；
- Windows compiler path 未转换为 CMake-safe `/` 形式；
- File API 错误地拒绝 workspace 外的 compiler identity 和已校验 CMake/toolchain root；
- invalid workspace config 在 Runtime pre-READY 阶段退出，客户端无法读取阻断诊断；
- Native E2E 的 Service data path 过长，触发 MSBuild `MSB3491`/`.tlog` 路径失败。

本地验证证据：

- `pnpm check:protocol-generated`、`pnpm build`、`pnpm test` PASS；
- `go test -race ./apps/test-service/...` PASS；
- 既有 Windows Service E2E 19/19 PASS；
- Windows MSVC native report 位于 ignored `.native-e2e/artifacts/windows/toolchain-report.json`，不包含 token、environment 或绝对路径。

Linux GCC/Clang 与 Windows clang-cl 的真实 required-family 运行仍必须由固定 Hosted CI 完成；在该证据到位前，Task 3 Step 2/7、Task 4 和 Phase 3D 总完成门禁保持未勾选。

---

### Task 4：Windows MSVC 与 clang-cl Native E2E

**文件：**
- 创建： `tools/service-probe/src/native-build-windows.test.ts`
- 修改： `tools/service-probe/src/native-build.ts`
- 修改： `tools/service-probe/src/native-report.ts`
- 修改： `tools/service-probe/package.json`

**接口：**
- 输入： Task 3 native runner 与 Windows Phase 3B toolchain profiles。
- 输出： Windows MSVC/clang-cl scenario results 与无敏感信息的工具链报告。

- [ ] **Step 1：写 MSVC/clang-cl profile 选择与 capability 测试**

fake discovery snapshot 覆盖：

- MSVC 必须选择 generated profile、Visual Studio generator 和明确 architecture；
- clang-cl 必须同时具备 LLVM、MSVC environment、Windows SDK 和可用 generator；
- Windows Ninja discovery 优先使用固定的独立 CMake/Ninja；该文件不存在时，只回退到已验证 Visual Studio instance 内固定布局的随附 Ninja，并把 executable、父目录与安装根 identity 纳入 probe 复验；
- 只安装 `clang.exe` 而没有 `clang-cl.exe` 不算 clang-cl；
- required `msvc,clang-cl` 中任一 family 缺失即失败；
- report 只保留 Visual Studio instance ID/toolset version，不保存安装绝对路径或捕获 environment。

- [ ] **Step 2：运行 Windows native tests 并确认失败**

在 Windows 上运行：

```powershell
$env:UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS='msvc,clang-cl'
pnpm --filter @unit-test-ide/service-probe test:e2e:native:windows
```

预期： FAIL，原因是 Windows matrix 尚未接入。

- [ ] **Step 3：执行 MSVC 真实矩阵**

使用 discovery 返回的 MSVC generated profile 驱动 Task 3 的全部正常 lifecycle 场景，并额外断言：

- generator 属于 Adapter 已验证的 Visual Studio generator；
- `configuration` 明确为 `Debug`；
- environment 仅由 Service 内部 MSVC Adapter 构造；
- build artifact 是当前 architecture 的 PE executable；
- cancel/timeout 后 Job Object 中无 compiler/linker/build-tool 子进程。

Preset 项目使用已安装 Ninja 和 preset 自己的 compiler 语义；configure 后通过 File API 验证实际 compiler，并把它作为额外 preset scenario，不替代 generated MSVC profile 门禁。

- [ ] **Step 4：执行 clang-cl 真实矩阵**

选择 `toolchainFamily === "clang-cl"` 的 generated profile，要求：

- compiler identity 是 `clang-cl`；
- profile capabilities 记录 Phase 5 可读取的 `llvmCoverage`、`llvmProfdata`、`llvmCov` availability；
- configure/build 组合使用已验证的 MSVC/Windows SDK environment；
- compiler/linker diagnostics 通过 clang-cl/MSVC family golden；
- 不运行 coverage instrumentation、`.profraw`、`llvm-profdata` 或 `llvm-cov`。

- [ ] **Step 5：覆盖 Windows 路径与恢复边界**

额外覆盖：

- workspace directory 为 `native 空格 Ω`；
- drive letter 大小写不会产生不同 workspace ID；
- junction/reparse point escape 在 configure 前拒绝；
- long path 能力不足时返回稳定的 preflight diagnostic，而非截断路径；
- Service restart 后不会复用已经变化的 MSVC instance/toolchain generation；
- token/endpoint ACL 测试仍满足已有 Windows E2E 架构修复，不通过 `icacls /grant:r` 放宽 ACL。

- [ ] **Step 6：生成报告，运行并提交**

报告写入 `.native-e2e/artifacts/windows/toolchain-report.json`，字段与 Linux 同构。

在 Windows 上运行：

```powershell
pnpm prepare:cmake-bundle
$env:UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS='msvc,clang-cl'
pnpm test:e2e:native
pnpm verify
git diff --check
```

预期： MSVC 与 clang-cl 全部 scenario PASS；无遗留 Service、compiler、linker 或 build tool。

```powershell
git add tools/service-probe
git commit -m "test: cover windows msvc and clang-cl builds"
```

---

### Task 5：固定 Hosted CI、文档与 Phase 3 完成门禁

**文件：**
- 修改： `.github/workflows/foundation.yml`
- 修改： `README.md`
- 创建： `docs/development.md`
- 创建： `docs/security.md`
- 创建： `docs/cmake-bundle.md`
- 创建： `docs/native-e2e.md`

**接口：**
- 输入： Phase 3A–3D 全部交付。
- 输出： 固定的 Windows/Linux CI、用户/开发者说明、PR readiness evidence。

- [x] **Step 1：先写 CI 结构检查**

扩展 `tools/workspace-smoke/workspace-smoke.test.mjs`，解析 workflow text 并断言：

- 只使用 `windows-2025-vs2026` 和 `ubuntu-24.04`；
- 两个 job 都先 `prepare:cmake-bundle`，再运行 native E2E；
- Windows required families 为 `msvc,clang-cl`；
- Linux required families 为 `gcc,clang`；
- 两个 job 都运行 `pnpm verify`、`git diff --exit-code`；
- 两个 job 都上传 `toolchain-report.json`；
- workflow 不使用 `windows-latest` 或 `ubuntu-latest`。

- [x] **Step 2：运行 workspace smoke 并确认失败**

运行：

```powershell
pnpm test:workspace
```

预期： FAIL，原因是 workflow 仍使用浮动 matrix labels。

- [x] **Step 3：将 workflow 拆成固定平台 job**

保留现有 Node/Go/pnpm 固定版本，把单一 matrix job 改为 `verify-windows` 与 `verify-linux`。两个 job 的核心顺序：

```yaml
- run: pnpm install --frozen-lockfile
- run: pnpm verify
- run: pnpm prepare:cmake-bundle
- run: pnpm test:e2e:native
- uses: actions/upload-artifact@v7
  if: always()
  with:
    name: native-toolchain-report-${{ runner.os }}
    path: .native-e2e/artifacts
    if-no-files-found: error
- run: git diff --exit-code
```

Windows job：

```yaml
runs-on: windows-2025-vs2026
env:
  UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS: msvc,clang-cl
```

Linux job：

```yaml
runs-on: ubuntu-24.04
env:
  UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS: gcc,clang
```

`prepare:cmake-bundle` 是 CI 唯一允许的 CMake 网络准备步骤；native E2E 开始前设置 test harness network guard，任何 HTTP(S) 请求都使测试失败。

- [x] **Step 4：更新中文文档**

文档必须清楚说明：

- 最终产品是 Code-OSS desktop，不是依赖 GitHub 的浏览器服务；
- GitHub 只承担源码托管、PR、CI 和发布准备，用户运行工具不必连接 GitHub；
- 产品带固定 CMake runtime，但不带 compiler；
- Windows 需要可验证的 MSVC/Windows SDK，clang-cl 场景还需要 LLVM；
- Linux 需要 GCC 或 Clang，Ninja 不可用时 generated profile 可使用经过验证的 Unix Makefiles；
- 自定义 CMake 只允许受信任 workspace 的绝对 executable，并经过固定 probe；
- 如何显式准备 bundle、运行 local native E2E、查看无敏感信息报告；
- Phase 5 才实现 clang-cl/Clang/GCC coverage，Phase 8 才实现签名安装包和升级/回滚。

**2026-07-29 实施记录：**

- workspace smoke 已先在旧 `windows-latest`/`ubuntu-latest` matrix 上按预期失败，再在固定双 job workflow 上通过；
- Windows job 固定 `windows-2025-vs2026` 并强制 `msvc,clang-cl`，Linux job 固定 `ubuntu-24.04` 并强制 `gcc,clang`；
- 每个 job 都按 `verify → prepare:cmake-bundle → test:e2e:native` 顺序执行，并只上传对应平台的 `toolchain-report.json`；
- native runner 在动态加载矩阵实现前安装 Node.js HTTP(S) network guard，覆盖 `http`、`https`、`http2` 与全局 `fetch`，同时保留本地 IPC 所需的 `net`；
- Windows native data/build 根不再使用用户 profile temp：普通 clone 使用 checkout 的 `.native-e2e/work`，managed worktree 使用主 checkout 的同名目录。这保留了 MSBuild 短路径预算，也避免因用户 profile 祖先无法以“不共享 delete”方式固定而错误放宽 Service owner-only/TOCTOU 架构；
- 本地 Windows 已在 guard 启用时通过 MSVC 19.44/Visual Studio 17 2022 全场景；本机 clang-cl 不满足 production discovery 前提，Windows clang-cl 与 Linux GCC/Clang 仍等待 fixed Hosted CI required-family 证据。

- [ ] **Step 5：执行完整本地门禁**

Windows：

```powershell
pnpm install --frozen-lockfile --offline
pnpm verify
pnpm prepare:cmake-bundle
$env:UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS='msvc,clang-cl'
pnpm test:e2e:native
git diff --check
git status --short
```

Linux：

```bash
pnpm install --frozen-lockfile --offline
pnpm verify
pnpm prepare:cmake-bundle
UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS=gcc,clang pnpm test:e2e:native
git diff --check
git status --short
```

预期： 全部 PASS；status 只包含本 Task 预期文档/workflow 改动，bundle 和 native artifacts 均被 ignore。

- [ ] **Step 6：执行独立评审与安全边界检查**

评审必须逐项确认：

- Protocol request 不能到达 executable、raw args、environment 或 cwd；
- 自定义 CMake 与 CMake project/Presets 的受信任原生语义没有被错误当成 Protocol 命令入口；
- bundle 下载只发生在显式准备阶段，archive/installed-file/license 摘要都验证；
- 四个 compiler family 都是真实 configure/build，不是 fake adapter；
- diagnostics golden 不含 Hosted Runner 绝对路径；
- cancel/timeout 无残留进程树；
- v1.0/v1.1 compatibility gates 仍通过；
- Windows token ACL E2E 仍验证架构修复后的安全描述符。

- [ ] **Step 7：提交并等待 Hosted CI**

```powershell
git add .github/workflows/foundation.yml README.md docs tools/workspace-smoke/workspace-smoke.test.mjs
git commit -m "ci: require phase 3 native toolchain matrix"
git status --short
```

预期： worktree clean。

推送后等待 `verify-windows` 与 `verify-linux` 均完成；保存两个 artifact 的 compiler、generator、CMake version 作为 Phase 3 验收证据。任何 job failure 都先使用 `superpowers:systematic-debugging` 定位 root cause，再修改实现。

---

## Phase 3D 完成检查

- [x] CMake 4.3.4 archive、executable 和 license digest 均由 manifest 固定并验证。
- [x] 普通 `pnpm verify` 与产品运行不联网。
- [ ] Preset 与 generated fallback 都通过真实构建。
- [ ] Windows/MSVC、Windows/clang-cl、Linux/GCC、Linux/Clang 全部通过。
- [ ] configure 首次执行、无变化跳过、CMake input 变化后重新执行。
- [ ] 默认 target、指定 target、compiler/linker/configure diagnostics 通过。
- [ ] 空格/Unicode、escape、取消、超时、断线重连和 restart recovery 通过。
- [ ] Protocol v1.0/v1.1 回归和 Protocol v1.2 contract 通过。
- [ ] Windows/Ubuntu 固定 Hosted Runner CI 全绿并上传工具链报告。
- [ ] `pnpm verify`、`git diff --check`、`git diff --exit-code` 全绿。
- [ ] 工作树 clean，独立评审无未解决问题。
