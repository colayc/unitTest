# Native E2E

## 验证目标

Native E2E 不是直接调用 CMake 的 smoke script，而是使用真实 TypeScript Client、Named Pipe/Unix Socket、Protocol v1.2 和 Go Service lifecycle 验证：

- workspace inspect、Toolchain、generated Build Profile 与 CMake Preset Build Profile；
- CMake configure、无变化复用和 input 变化失效；
- 每个 required compiler family 都以受信任 Preset 自己的 compiler/toolset 语义完成额外真实构建，并核对 CMake 报告的 compiler family/version；
- 默认 target 与指定 target；
- compiler、linker 和 configure Diagnostic；
- 空格与 Unicode workspace；
- 父目录穿越和外部 Preset include 拒绝；
- cancel、timeout 与无残留进程树；
- 断线重连、Service restart 与任务恢复。

## 平台矩阵

| 平台 | required family | 主要 generator |
| --- | --- | --- |
| Windows | MSVC、clang-cl | Visual Studio 18 2026；clang-cl 使用经过验证的独立 Ninja，或已验证 Visual Studio 实例随附的 Ninja |
| Linux | GCC、Clang | 经过验证的 Ninja；不可用时为 Unix Makefiles |

产品提供 CMake runtime，不提供 compiler。缺少 compiler 时应安装平台工具链，而不是把 compiler 下载逻辑加入产品运行时。

## 本地运行

先准备固定 bundle：

```sh
pnpm prepare:cmake-bundle
```

默认运行允许本机没有安装的 family 记录为 `skipped`：

```sh
pnpm test:e2e:native
```

Windows 不使用用户 profile temp 作为 Service data/build 根。普通 clone 使用 checkout 内的 `.native-e2e/work`；位于 `.worktrees/<name>` 的 managed worktree 使用主 checkout 的 `.native-e2e/work`。这样既控制 MSBuild 路径长度，也允许 Service 以不共享 delete 的目录句柄固定全部祖先，不需要放宽 owner-only/TOCTOU 安全策略。Linux 继续使用系统 temp 下的 `uti-native`。

Preset 场景不会通过 Protocol 传入 compiler executable、args 或 environment。测试只在隔离的受信任 fixture 中写入 CMake 原生 compiler 语义，再把 Service 返回的 Preset profile ID 交回 Protocol。Windows MSVC Preset 使用已验证 Visual Studio generator，clang-cl Preset 使用已验证 Ninja 和 `clang-cl` compiler semantic。configure 后的 File API 只把 `C`/`CXX` compiler descriptor 纳入 toolchain identity；Visual Studio 附带的 `RC` 等辅助描述不会成为执行或文件读取权限。

Windows PowerShell 强制完整矩阵：

```powershell
$env:UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS='msvc,clang-cl'
pnpm test:e2e:native
```

Linux 强制完整矩阵：

```sh
UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS=gcc,clang pnpm test:e2e:native
```

环境变量只能包含当前平台允许的 family；重复、未知或跨平台 family 会在 Service 启动前失败。CI 总是设置完整 required family，因此不能把缺失工具链降级为 `skipped`。

## Hosted CI

`.github/workflows/foundation.yml` 使用两个固定 job：

- `verify-windows`：`windows-2025-vs2026`，要求 `msvc,clang-cl`。
- `verify-linux`：`ubuntu-24.04`，要求 `gcc,clang`。

Linux job 固定执行完整 native 矩阵；Windows 公共 hosted runner 默认跳过原生矩阵，因为 `clang-cl` linker diagnostic 场景可能耗尽 named-pipe liveness reconnect。为在稳定的自托管/专用 runner 上启用 Windows 矩阵，设置仓库变量 `UNIT_TEST_IDE_WINDOWS_NATIVE_E2E_REQUIRED=1`；启用后缺少 toolchain report 会使 job 失败。

每个 job 的共同步骤为：

1. `pnpm install --frozen-lockfile`
2. `pnpm verify`
3. `pnpm prepare:cmake-bundle`
4. （Linux 默认执行；Windows 由上述仓库变量启用）`pnpm test:e2e:native`
5. 使用 `actions/upload-artifact@v7` 只上传已执行平台的 `toolchain-report.json`
6. `git diff --exit-code`

工作流不使用 `windows-latest` 或 `ubuntu-latest`，避免 Runner 工具链更新在未评审时改变验收矩阵。

## Network guard

`prepare:cmake-bundle` 是 native 验证前唯一允许的 CMake 网络准备步骤。`native-run` 会先安装 HTTP(S) network guard，再动态加载矩阵实现；guard 覆盖 Node.js `http`、`https`、`http2` 和全局 `fetch`，同时保留 Named Pipe/Unix Socket 所需的 `net`。

Windows LLVM coverage required gate 另有更强的 OS 级边界：独立 Go preflight 先复用 production Windows discovery evidence 与 `coveragellvm.PinToolset/Verify` identity/digest verifier；它不输出工具路径，且必须在 WFP、Service start 和 native execution 前完成。unavailable 在 non-required 本机只产生精确单一 SKIP，在 required CI 则失败；verified 后才进入 guardian，guardian 失败仍是 FAIL。真实 Service 启动前由独立 guardian 打开 Windows Filtering Platform (WFP) dynamic session，并为 Service、CMake、Ninja、compiler/linker/archiver、测试程序与 LLVM 工具的精确 APP_ID 安装 IPv4/IPv6 non-loopback outbound-connect block filters。WFP 没有 PID-tree primitive，因此 build planner 在启动 CMake 前还会有界解析 statically reachable 的 `CMakeLists.txt`、literal include/subdirectory、CMake preset include/inheritance/cacheVariables 与 toolchain file，并校验 `add_custom_command`、`add_custom_target`、`add_test` 和 `execute_process` 的每个 `COMMAND` executable，以及 compiler launcher 与所有 `RULE_LAUNCH_*` setter，必须精确属于已注册 closed LaunchPlan。fresh `add_executable` 的确定性 artifact 会加入 plan；`try_run`、未知/动态 executable/property/preset、configure-time graph write、CTest/Ninja/custom wrapper、越界/缺失文件或会代启动进程的 `cmake -E` wrapper 一律在 processhost/CreateProcess 前失败。planner 将完整解析 graph 的 OS identity/SHA-256 固定到 execution plan，processhost 在启动 CMake 前复核并保持 no-write/no-delete handles 到进程树退出，mutation 必须 fail closed。guardian 只有在 owner identity、ready/release/bye 协议和 filter audit 全部闭合后才算通过；Node teardown 必须等待 release、guardian exit 与 HTTP(S) guard 恢复。CI `always()` 步骤现在只执行一次性 legacy residue cleanup：它从固定 state root 做只读审计，只允许已知 historical rule group 与 canonical residue，移除确认后的旧规则/目录；遇到未知 entry、live legacy creator 或无法证明归属时直接 fail-closed。权限、未知 residue、创建、身份、审计或撤销失败均使 required gate 失败。

Go production import audit 禁止 HTTP/TLS/GitHub/OAuth client stack，并只允许本地 IPC 代码使用受限的 `net` 能力。

## 报告

报告位置：

```text
.native-e2e/artifacts/windows/toolchain-report.json
.native-e2e/artifacts/linux/toolchain-report.json
```

报告只包含：

- schema version；
- platform 与 architecture；
- CMake version 与 archive SHA-256；
- compiler family/version；
- generator；
- 每个场景的 `passed`/`skipped` 状态。

报告写入采用临时文件加原子 rename，并拒绝绝对路径、token、environment 和不受限字符串。CI 即使 native job 失败也会尝试上传已有报告，便于定位失败。

## 后续阶段

Phase 5 才会为 clang-cl/Clang/GCC 加入 coverage instrumentation、`llvm-profdata`、`llvm-cov` 和 `gcovr`。当前 Phase 3D 只验证 clang-cl/LLVM 工具链边界与真实构建，不执行覆盖率命令。

Phase 8 才会提供签名安装包、内置 bundle 发布、升级和回滚。
