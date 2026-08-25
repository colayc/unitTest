# 安全边界

## 本地优先与网络边界

产品是本地 Code-OSS desktop 应用，运行时不依赖 GitHub。GitHub 只承担源码托管、PR、CI 和发布准备；用户不需要登录 GitHub，离线环境也能使用已安装的产品和已准备的 CMake runtime。

普通 `pnpm verify`、native E2E 执行阶段和 Go Service 不应建立 HTTP(S) 客户端连接。CMake archive 下载只发生在显式的 `pnpm prepare:cmake-bundle` 阶段。native E2E 进程在加载测试实现前安装 HTTP(S) network guard；workspace smoke 还会审计 Go production import，将网络能力限制在 Named Pipe/Unix Socket 本地 IPC 边界。

Windows LLVM coverage smoke 不把 Node API monkeypatch 当作完整禁网证据。它先用独立 Go preflight 复用 production discovery evidence 与 LLVM retained-identity/digest verifier；unavailable 的本机 non-required 运行在任何 firewall/Service/native side effect 前诚实 SKIP，required 运行则失败，verified 结果才允许继续且不替代后续 Service 再验证。随后 smoke 在隔离 runner 上安装临时、可审计的 Windows Firewall all-program outbound Block/Any rule，使 Service 与全部 native child process 都受 OS filter 约束。一个 command-bound 256-bit nonce 和长生命周期 guardian 共同标识唯一 creator：guardian 在 pre-audit 后核对 nonce、owner、自身 PID 与 closed marker state 才创建，分别从 ActiveStore 与 PersistentStore 获取 rule 对象并逐 store 验证完整 policy 与六类 closed filters，同时显式从 ActiveStore 验证严格 Domain/Private/Public 全启用 profile 集合，随后才发布 ready；ready 后不再创建，并在等待期及 finally cleanup transition 持续复核 closed state，重试删除与双 store 审计直到稳定为空。Node 仅在 canonical removed marker 与 guardian exit 均确认后恢复 HTTP。CI `always()` cleanup 对固定 state root 使用含 nonce 的七种 canonical marker closed schema；root 本身也做 `Get-ChildItem -Force` 闭集枚举，拒绝普通文件、未知 entry 和任意 reparse。cleanup 不信任 PID/removed 文件来发现 creator，而是每轮独立枚举 OS live Windows PowerShell 进程，并以原生只读查询绑定 exact executable/script/Guard action/state root/direct-child state/rule/owner/nonce，发出 release 后等待所有 matching process 真实退出。dead-PID substitution、伪造 removed 或 state directory replacement 都不能留下可迟到创建的 installer；查询权限、进程命令行、guardian 身份/状态、建立或清理不能被证明时 required gate fail-closed，不生成或上传 coverage PASS evidence。

## Protocol 权限边界

Protocol request 只表达封闭的语义操作。客户端不能提交：

- executable 或 Shell command；
- raw args；
- environment；
- cwd；
- 任意宿主文件路径。

CMake build 由 Service 根据已验证的 Workspace、Build Profile、Toolchain、Target 和固定策略生成 `ExecutionPlan`。CMake project 与 Presets 属于受信任 workspace 的原生语义，不会变成远程命令入口。

Preset 在 configure 前可能没有单一 `toolchainId`。此时 File API 对 CMake input 的读取边界只能扩展到同一次 workspace snapshot 中由 Service 已发现并验证的 compiler/sysroot roots，不能采用 Protocol 或 File API 自报的新 root。File API 的 compiler path 仅作为有界的规范绝对路径参与 `C`/`CXX` toolchain identity，不会被打开或取得执行权限；`RC` 等辅助语言 descriptor 被忽略。

每个 build `ExecutionBoundary` 在任务被采用前固定已验证的 CMake executable，并在任务终结时统一释放：

- Linux 使用 `O_NOFOLLOW` 打开并持续持有只读 FD，使已经 unlink 的 inode 在边界存续期间不能被回收复用；每个 Step 启动前仍会重新比较路径与固定 FD 的文件身份。
- Windows 使用 `FILE_FLAG_OPEN_REPARSE_POINT` 且不共享 write/delete 的 handle，拒绝 reparse point，并在边界存续期间阻止 executable 被修改、删除或替换。

构造期探测和 build directory 校验使用短生命周期边界并立即释放；进入 Task Manager 的边界则与任务和目录锁共同释放。边界释放后不能再次用于 executable 或 working directory 校验。

## Workspace Trust 与 CMake

固定 CMake bundle 通过 tracked manifest 绑定版本、archive SHA-256、安装布局、executable、license 和 installed-file SHA-256。运行时不自动下载。

## 安装、更新、回滚与许可边界

发布安装验证只操作调用方显式提供的 package-owned root，不写注册表、不修改系统目录，也不改变用户 profile。首次安装会在该 root 写入封闭的 ownership marker；卸载前必须重新验证 marker，因而不会把任意非空目录认领为产品目录。workspace、任务数据库和用户制品必须位于 package-owned root 之外，卸载只递归删除已验证的产品 root。

每个版本先复制到 `versions/` 下的临时 sibling directory，重新验证封闭 `release-manifest.json`、全部 artifact/license 的 size 与 SHA-256、路径边界以及精确文件集合，fsync manifest 后才通过 rename 发布为 `versions/<version>`。`current` 是单独的 semver pointer file：新 pointer 先写入并在平台支持时 fsync，再用 rename 原子替换；验证或复制失败不会改变已有 pointer。普通安装拒绝 downgrade；显式 rollback 只允许切换到仍在 `versions/` 中且再次通过完整 manifest 验证的版本，重复 rollback 保持幂等，而且更新流程不会删除最后一个已知可用版本。

许可审计以 release manifest 中 digest-bearing `{path,size,sha256}` 记录为唯一顶层闭集，拒绝缺失、额外、篡改或 reparse 的 notice/license 文件。coverage bundle 的固定 wheel lock 必须与 `licenses/dependencies.json` 中的 project/version 闭集一一对应，并保留 Python、gcovr 和每个 transitive dependency 的许可材料；CMake 必须至少保留 release manifest 已固定摘要的 license notice，在完整 CMake manifest 随包存在时还会校验平台对应的 `licensePath`。

Windows PowerShell 与 Linux Bash clean-machine smoke 都使用一次性 root，验证首次安装、launch handshake、升级、模拟启动失败后的强制回滚、重复回滚、卸载、user-data 保留和 package residue 清零。CI 只上传封闭 JSON outcome、source commit 和 digest-bearing license audit；evidence 不包含 token、environment、用户名、runner path 或 workspace 内容。

开发者自定义 CMake 仅允许：

- workspace 已显式标记为 trusted；
- executable 为绝对路径；
- 文件和父目录身份在 probe 与执行间保持一致；
- version 与 capability probe 通过；
- 路径不来自 Protocol request。

产品捆绑 CMake runtime，但不捆绑 compiler。MSVC、clang-cl、GCC、Clang 和 build tool 都在固定目录或受限 PATH 内发现并验证；Service 不继承不受信任的用户 PATH 来决定执行目标。Windows 优先检查固定的独立 CMake/Ninja 位置；该位置不可用时，只回退到已由 `vswhere` 发现并固定 identity 的 Visual Studio 实例内 `Common7/IDE/CommonExtensions/Microsoft/CMake/Ninja/ninja.exe`，并在 probe 前后复验 executable、父目录和安装根 identity。

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
