# CMake bundle

## 目标

仓库固定 CMake 4.3.4 runtime，以保证开发、CI 和最终桌面产品使用相同的 CMake 行为。bundle 只提供 CMake，不包含 MSVC、Windows SDK、LLVM、GCC、Clang、Ninja 或 Make。

tracked manifest 位于 `tools/cmake-bundle/manifest.json`。每个平台条目固定：

- archive URL 与 SHA-256；
- archive root directory；
- CMake executable 相对路径；
- license 相对路径；
- 安装后必须存在的文件及其 SHA-256。

当前支持 `win32-x64` 与 `linux-x64`。

## 显式准备

```sh
pnpm prepare:cmake-bundle
```

该命令是 CI 中唯一允许下载 CMake 的步骤。准备流程：

1. 选择与 host platform/architecture 精确匹配的 manifest 条目。
2. 下载固定 archive，并在解包前验证 archive SHA-256。
3. 拒绝绝对路径、父目录穿越、链接和异常 archive entry。
4. 解包到临时目录。
5. 验证 root layout、CMake executable、license 和所有 tracked installed-file SHA-256。
6. 原子发布到 `.bundled-tools/cmake/<version>/<platform-key>`。
7. 写入用于运行前复验的固定 state。

`.bundled-tools` 不提交到 Git；Phase 8 打包时会把同一 manifest 合同接入签名安装包。

## 运行时复验

Go Service 和 native E2E 不信任“目录存在”这一条件。启动 CMake 前会重新验证：

- bundle root 位于允许的根目录内；
- manifest 与 state 的 platform、version 和 archive identity 一致；
- executable、license 和 installed files 仍是普通文件；
- 路径没有通过 symlink/junction 离开 bundle；
- 文件摘要仍与 tracked manifest 一致。

任何不一致都在 Service 启动 CMake 前失败。

## 自定义 CMake

开发者可在 trusted workspace 场景显式使用自定义 CMake，但必须提供绝对 executable，并通过固定 version/capability probe 和文件身份验证。该入口不是 Protocol 字段，也不是普通用户默认路径。

## 离线使用

bundle 成功准备或随 Phase 8 安装包交付后，产品运行与 `pnpm test:e2e:native` 都不需要访问网络。native E2E 会在加载测试实现前安装 HTTP(S) network guard；如果测试代码尝试发起标准 Node.js HTTP(S) 请求，会立即失败。
