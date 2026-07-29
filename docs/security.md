# 安全边界

## 本地优先与网络边界

产品是本地 Code-OSS desktop 应用，运行时不依赖 GitHub。GitHub 只承担源码托管、PR、CI 和发布准备；用户不需要登录 GitHub，离线环境也能使用已安装的产品和已准备的 CMake runtime。

普通 `pnpm verify`、native E2E 执行阶段和 Go Service 不应建立 HTTP(S) 客户端连接。CMake archive 下载只发生在显式的 `pnpm prepare:cmake-bundle` 阶段。native E2E 进程在加载测试实现前安装 HTTP(S) network guard；workspace smoke 还会审计 Go production import，将网络能力限制在 Named Pipe/Unix Socket 本地 IPC 边界。

## Protocol 权限边界

Protocol request 只表达封闭的语义操作。客户端不能提交：

- executable 或 Shell command；
- raw args；
- environment；
- cwd；
- 任意宿主文件路径。

CMake build 由 Service 根据已验证的 Workspace、Build Profile、Toolchain、Target 和固定策略生成 `ExecutionPlan`。CMake project 与 Presets 属于受信任 workspace 的原生语义，不会变成远程命令入口。

## Workspace Trust 与 CMake

固定 CMake bundle 通过 tracked manifest 绑定版本、archive SHA-256、安装布局、executable、license 和 installed-file SHA-256。运行时不自动下载。

开发者自定义 CMake 仅允许：

- workspace 已显式标记为 trusted；
- executable 为绝对路径；
- 文件和父目录身份在 probe 与执行间保持一致；
- version 与 capability probe 通过；
- 路径不来自 Protocol request。

产品捆绑 CMake runtime，但不捆绑 compiler。MSVC、clang-cl、GCC、Clang 和 build tool 都在固定目录或受限 PATH 内发现并验证；Service 不继承不受信任的用户 PATH 来决定执行目标。

## IPC、token 与数据

Windows 使用 per-user Named Pipe，Linux 使用权限模式 `0600` 的 Unix Socket。每条连接在调用其他方法前必须完成 token handshake。

token 文件在写入 secret 前由 Go Service 的 `--prepare-token-file` 模式以平台原生方式创建：

- Windows：受保护、仅 owner 的 DACL。
- Linux：仅 owner 可读写的权限位。

Service 会再次验证 token 文件，消费后删除。任务数据库、制品和 `service.lock` 位于仅当前用户可访问的数据目录中。

## 诊断与报告

Protocol Diagnostic、错误和 native E2E report 必须进行边界化与脱敏。`.native-e2e/artifacts/<platform>/toolchain-report.json` 只记录稳定的 family、version、generator、CMake version 与场景状态，不包含：

- token 或 secret；
- environment；
- Hosted Runner 或用户主机绝对路径；
- compiler executable 路径；
- workspace 内容。

Phase 5 才会实现 clang-cl/Clang/GCC coverage；Phase 8 才会实现签名安装包、升级和回滚。当前代码不能把 capability 声明误当成这些功能已经交付。
