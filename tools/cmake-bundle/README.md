# CMake Bundle 准备工具

本目录负责在构建或 CI 准备阶段取得并校验固定的 CMake runtime。产品运行时、普通 `pnpm test` 和 `pnpm verify` 都不会下载 CMake。

## 固定来源与信任锚

当前只接受 CMake `4.3.4` 的两个官方 x64 archive：

- Windows：`https://cmake.org/files/v4.3/cmake-4.3.4-windows-x86_64.zip`
- Linux：`https://cmake.org/files/v4.3/cmake-4.3.4-linux-x86_64.tar.gz`

`manifest.json` 同时固定：

- archive SHA-256；
- archive 顶层目录；
- CMake executable 相对路径和 SHA-256；
- `LICENSE.rst` 相对路径和 SHA-256；
- SPDX license identifier `BSD-3-Clause`。

CMake 官方下载页列出了 `4.3.4` 的 Windows x64 ZIP、Linux x86_64 tar.gz 与 release SHA-256 文件：
<https://cmake.org/download/>。CMake 官方许可页说明 CMake 使用 OSI-approved BSD 3-clause License：
<https://cmake.org/licensing/>。

## 显式准备

在 repository root 执行：

```powershell
pnpm prepare:cmake-bundle
```

工具默认选择当前平台，也可以显式指定：

```powershell
pnpm prepare:cmake-bundle -- --key win32-x64
pnpm prepare:cmake-bundle -- --key linux-x64
```

默认输出：

```text
.bundled-tools/
└── cmake/
    ├── manifest.json
    └── 4.3.4/
        └── <platform-key>/
            ├── bundle-state.json
            ├── bin/
            ├── doc/
            └── share/
```

`.bundled-tools/` 不进入 Git。CI 如需把 `--output` 指向 repository 之外，必须显式设置绝对路径
`UNIT_TEST_IDE_CMAKE_BUNDLE_ALLOWED_ROOT`，且输出仍必须位于该根目录内。

## 安全与离线边界

准备命令执行以下检查后才发布 bundle：

1. 严格验证 closed manifest，拒绝 `latest`、额外 platform、额外字段与可覆盖 redirect target 的字段；
2. 只允许 `https://cmake.org/files/v4.3/` 下 manifest 固定的 URL，并对 redirect 后的 URL 重复验证；
3. 在解压前流式校验 archive SHA-256；
4. 用参数数组调用系统 `tar`，先审计 entry，拒绝 absolute path、`..`、Windows ADS、其他顶层目录、symlink、hardlink 和 device；
5. 解压后递归拒绝 symlink、junction/reparse point 和非 regular file/directory；
6. 校验 executable 与 `LICENSE.rst` SHA-256；
7. 执行 bundle 内的 `cmake -E capabilities`，要求 version 精确等于 `4.3.4`；
8. 在 output root 同 volume 的随机 staging 中完成验证，通过 rename 发布不可变 platform 目录；
9. 已存在的目标只验证并复用，绝不覆盖；失败会删除 staging，并保留已存在的有效 bundle。

`test:cmake-bundle` 全部使用临时目录和本地 byte fixtures，不访问网络：

```powershell
pnpm test:cmake-bundle
```

## 升级与摘要更新

CMake version 或任何 digest 只能通过单独 review 的 manifest 变更升级：

1. 从 CMake 官方下载页取得明确版本的 release SHA-256 文件及其 PGP signature；
2. 核对签名身份和 archive 文件名，再更新 `archiveSha256`；
3. 在隔离目录解压已验证 archive，分别计算 executable 与 `LICENSE.rst` SHA-256；
4. 核对 archive 顶层目录、executable、license path 和 `cmake -E capabilities` version；
5. 同一提交更新 manifest、离线测试、供应链说明与第三方声明证据；
6. 禁止使用 `latest`、未固定 mirror、调用方 URL、调用方 header、proxy credential 或调用方 executable。

## BSD 3-Clause 分发责任

随产品分发 CMake binary 时，必须保留 CMake archive 中的 `LICENSE.rst`，并在 binary distribution 的文档或其他材料中保留适用的 copyright notice、license 条款和 warranty disclaimer；不得使用原作者或贡献者名称为衍生产品背书。CMake source tree 还包含兼容许可的第三方组件，因此 Phase 8 必须完成第三方声明复核、安装包内许可展示和最终法律审查。

本说明记录工程门禁，不替代正式法律意见。Phase 8 还负责安装包签名、发布摘要、升级与回滚验证。
