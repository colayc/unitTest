# C/C++ Unit Test IDE

当前仓库已推进到 Phase 3D：在 Phase 2 的版本化协议、TypeScript 客户端和本地 Go Service 基础上，加入 Protocol v1.2、CMake workspace 检查、MSVC/clang-cl/GCC/Clang 工具链发现、固定 CMake runtime，以及通过真实 Service lifecycle 执行的 native configure/build E2E。Phase 2 的受控 simulation 仍保留；协议仍不接受客户端 executable、Shell 字符串、任意参数、任意环境变量或任意工作目录。

最终产品是 Code-OSS desktop 客户端：TypeScript 负责桌面 UI 与 IDE 集成，Go Service 在用户本机完成 workspace、构建、测试和覆盖率工作。它不是依赖 GitHub 的浏览器服务；GitHub 只用于源码托管、PR、CI 和发布准备，最终用户运行产品不必连接 GitHub。当前阶段尚未实现 Code-OSS UI。

## 前置条件

- Node.js 24.18.0
- 通过 Corepack 使用 pnpm 11.4.0
- Go 1.26.6
- Windows native 验证：MSVC 与 Windows SDK；clang-cl 场景还需要 LLVM
- Linux native 验证：GCC 或 Clang；推荐 Ninja，缺少 Ninja 时可使用经过验证的 Unix Makefiles

## 安装与验证

```sh
corepack enable
corepack prepare pnpm@11.4.0 --activate
pnpm install --frozen-lockfile
pnpm verify
```

`pnpm verify` 会依次检查生成协议、构建全部包和 Go 服务、运行单元与契约测试、运行 Go race 测试，并通过真实 Named Pipe 或 Unix Socket 执行端到端测试。

需要逐项定位失败时，可按同样顺序展开完整门禁：

```sh
pnpm check:protocol-generated
pnpm build
pnpm test
pnpm test:go:race
pnpm test:e2e
```

## CMake runtime 与 native E2E

产品固定使用 CMake 4.3.4 runtime，但不捆绑 compiler。开发环境或 CI 必须显式准备经过 manifest、archive、executable、installed-file 和 license 摘要验证的 bundle：

```sh
pnpm prepare:cmake-bundle
pnpm test:e2e:native
```

本地运行允许记录未安装的非必需工具链为 `skipped`。要求完整平台矩阵时，Windows PowerShell 使用：

```powershell
$env:UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS='msvc,clang-cl'
pnpm test:e2e:native
```

Linux 使用：

```sh
UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS=gcc,clang pnpm test:e2e:native
```

报告写入 `.native-e2e/artifacts/<platform>/toolchain-report.json`，只包含 compiler family/version、generator、CMake version 和场景状态，不记录 token、环境变量或主机绝对路径。native E2E 启动时会安装 HTTP(S) network guard；bundle 下载只允许发生在显式的 `prepare:cmake-bundle` 阶段。

Windows `clang-cl` coverage smoke 另有一个 required gate：它先运行独立 Go `coverage-toolset-preflight`，只有拿到 verified toolchain 后才建立 Windows Filtering Platform (WFP) guardian，再启动真实 Service/native coverage。non-required 本机若缺 verified LLVM 只能在 boundary 前精确 `SKIP`，required CI 则直接失败；成功 evidence 仅写入 `.native-e2e/artifacts/windows/coverage-execution-report.json`，且 JSON 只保留 `schemaVersion/outcome/reason/toolchainDigest/guardianOutcome/filterAuditOutcome/startedAt/finishedAt` 这些闭集字段。旧 PowerShell 脚本现在只负责一次性 legacy residue cleanup，不再承担生产 boundary。

更多说明见 [开发指南](docs/development.md)、[安全边界](docs/security.md)、[CMake bundle](docs/cmake-bundle.md) 和 [native E2E](docs/native-e2e.md)。

## Release staging

发布 staging tree 不会在默认测试或运行脚本里下载任何依赖；它只消费已经准备好的输入。`CODE_OSS_EXECUTABLE` 是必需的 build input，必须显式指向本地 Code-OSS 可执行文件。运行前还需要：

- `apps/code-oss-extension/dist/` 已经构建完成；
- Go service 二进制已经构建完成；
- CMake bundle root 和 coverage bundle root 已经通过各自的 prepare/check gate。

Windows PowerShell 示例：

```powershell
$env:CODE_OSS_EXECUTABLE='C:\path\to\CodeOSS.exe'
pnpm release:stage -- --platform windows --architecture x64 --version 1.2.3 --code-oss $env:CODE_OSS_EXECUTABLE --service .\bin\unit-test-service.exe --cmake-root .\.bundled-tools\cmake\windows-x64 --coverage-root .\.superpowers\runtime\coverage-bundle\windows-x64 --out .\dist
```

Linux shell 示例：

```sh
CODE_OSS_EXECUTABLE=/path/to/code-oss \
pnpm release:stage -- --platform linux --architecture x64 --version 1.2.3 --code-oss "$CODE_OSS_EXECUTABLE" --service ./bin/unit-test-service --cmake-root ./.bundled-tools/cmake/linux-x64 --coverage-root ./.superpowers/runtime/coverage-bundle/linux-x64 --out ./dist
```

命令会生成 `dist/staging/<version>/<platform>-<architecture>/`，其中包含 Code-OSS runtime、扩展 `dist`、service、bundle、聚合后的 license notices，以及闭集 `release-manifest.json`。

## 协议与安全边界

协议模型由 `packages/protocol-schema/schema` 生成。生成的 TypeScript 和 Go 文件已提交；请编辑 Schema 并运行 `pnpm generate:protocol`，不要直接编辑生成文件。消息继续使用 UTF-8 NDJSON，每行编码后上限为 1 MiB。

服务会监听随机的 per-user Windows Named Pipe，或权限模式为 `0600` 的 Linux Unix Socket。每个连接在使用其他方法前都必须完成 token handshake。身份验证 token 文件必须归当前用户所有，且只能由该用户访问：Unix 使用仅所有者可用的权限位，Windows 使用受保护的仅所有者 DACL。写入 token 前，启动器运行 `unit-test-service --prepare-token-file <path>`，使 Go 二进制程序以平台原生的仅所有者权限创建空文件。服务独立验证该文件，并在使用 token 后将其删除。

协议 `1.0` 保留 Phase 1 的严格响应形状，可完成 handshake、查询旧能力和关闭服务。协议 `1.1` 新增受控任务、事件重放、持久化与制品能力。协议 `1.2` 新增 workspace、Build Profile、Toolchain、Target、CMake build 与结构化 Diagnostic。客户端只调用封闭的语义方法；`1.0`/`1.1` compatibility gate 继续保留。

## 任务生命周期

任务状态仅为 `queued`、`running`、`cancelling`、`finished`。终止结果仅为 `succeeded`、`command_failed`、`cancelled`、`timed_out`、`interrupted`、`infrastructure_failed`；Phase 2 不产生 `test_failed`，因为本阶段尚未执行测试框架。

任务、事件和制品属于服务实例，而不属于某条客户端连接。同一实例内，断线重连会从最后应用的全局 `sequence` 重放事件；交付语义为至少一次，TypeScript 客户端按 `sequence` 去重。服务重启不会恢复或重新附着原进程，所有未终止任务都会恢复为 `finished/interrupted`，已完成快照、已提交事件和已引用制品继续保留。

Windows 使用 Job Object 终止完整进程树，Linux 使用 Process Group/Session 并在宽限期后从 `SIGTERM` 升级到 `SIGKILL`。取消、超时、主进程退出和服务关闭都不得遗留后代进程。

## 数据目录与制品

服务模式必须传入 `--endpoint`、`--token-file` 和 `--data-dir`。数据目录只允许当前用户访问，并包含：

- `history.sqlite3`：任务快照、追加事件、运行租约和 Artifact metadata。
- `artifacts/`：与 SQLite 分离的制品字节。
- `service.lock`：阻止同一数据目录被多个服务实例同时打开的锁文件。

客户端只能按服务生成的 `artifactId` 读取制品。服务会校验规范化路径、文件大小和 SHA-256，TypeScript 客户端在分块读取完成后再次校验大小与 SHA-256。启动清理只删除临时文件和数据库未引用的孤立文件，不删除已完成任务、已提交事件或已引用制品。
