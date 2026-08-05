# Offline Python/gcovr bundle 合同

本目录固定产品拥有的 Python `3.14.6` 与 gcovr `8.6` 的输入供应链。产品运行时不调用系统 `python`、`pip` 或 `gcovr`，也不从网络、用户 site-packages 或 workspace 导入 package。

## 固定来源

- Windows x64 使用 Python 官方 CPython embedded archive；Linux x64 使用同一版本的官方 source archive。
- gcovr 与每一个直接/传递 dependency 只使用 PyPI 已发布的 wheel；lock 不含 version range、marker、Git URL、editable package 或 sdist fallback。
- Linux builder 为 `quay.io/pypa/manylinux_2_28_x86_64@sha256:c7123a4aebb153c1e45b8152f07a64bd950d65e630cfb633a029cc45ee21897c`，ABI 最低基线为 glibc `2.28`；musl 明确不支持。
- `manifest.json` 只保存 prepare 前可验证的 source/archive/wheel 输入和 SHA-256。Task 2 的 `manifest.resolved.json` 才保存 extracted/generated output 的逐文件 SHA-256；其闭合 record shape 已由本 schema 的 `resolvedOutput` 定义并由测试覆盖。

Python `3.14.6` 的官方 release page 列出 source 与 Windows embeddable archive 的 SHA-256：<https://www.python.org/downloads/release/python-3146/>。gcovr 及依赖的 URL、wheel hash、Requires-Python 和 metadata 来自相应的 PyPI JSON release metadata，例如：<https://pypi.org/pypi/gcovr/8.6/json>。`manylinux_2_28` 的 glibc compatibility 和官方 image 名称由 PyPA manylinux 项目说明：<https://github.com/pypa/manylinux>。

## 使用和门禁

```powershell
pnpm test:coverage-bundle
pnpm prepare:coverage-bundle
pnpm check:coverage-bundle
```

`prepare:coverage-bundle` 和 `check:coverage-bundle` 是 Task 2 将提供的显式 prepare command，因此不会加入默认 `verify`。默认 `test` 只运行可离线的 manifest/license contract tests；真实 download/build 由平台 CI 显式调用。

## 升级规则

Python、gcovr、任何 wheel、builder image 或 license 变更必须在同一 review 中更新 manifest、license contract、Golden tests 和相关 source evidence。禁止 `latest`、branch、未锁定 transitive dependency、未审阅 mirror、用户 URL 或下载后解析依赖。

本说明是工程供应链约束，不替代发布前的法律审查。分发时必须保留 `licenses/` 记录的适用 license 和 NOTICE。
