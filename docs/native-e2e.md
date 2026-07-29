# Native E2E

## 验证目标

Native E2E 不是直接调用 CMake 的 smoke script，而是使用真实 TypeScript Client、Named Pipe/Unix Socket、Protocol v1.2 和 Go Service lifecycle 验证：

- workspace inspect、Toolchain 与 generated Build Profile；
- CMake configure、无变化复用和 input 变化失效；
- 默认 target 与指定 target；
- compiler、linker 和 configure Diagnostic；
- 空格与 Unicode workspace；
- 父目录穿越和外部 Preset include 拒绝；
- cancel、timeout 与无残留进程树；
- 断线重连、Service restart 与任务恢复。

## 平台矩阵

| 平台 | required family | 主要 generator |
| --- | --- | --- |
| Windows | MSVC、clang-cl | Visual Studio 18 2026 或经过验证的 Ninja |
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

每个 job 固定执行：

1. `pnpm install --frozen-lockfile`
2. `pnpm verify`
3. `pnpm prepare:cmake-bundle`
4. `pnpm test:e2e:native`
5. 使用 `actions/upload-artifact@v7` 只上传对应平台的 `toolchain-report.json`
6. `git diff --exit-code`

工作流不使用 `windows-latest` 或 `ubuntu-latest`，避免 Runner 工具链更新在未评审时改变验收矩阵。

## Network guard

`prepare:cmake-bundle` 是 native 验证前唯一允许的 CMake 网络准备步骤。`native-run` 会先安装 HTTP(S) network guard，再动态加载矩阵实现；guard 覆盖 Node.js `http`、`https`、`http2` 和全局 `fetch`，同时保留 Named Pipe/Unix Socket 所需的 `net`。

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
