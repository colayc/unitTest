# Phase 9 Batch F1：真实框架输入与可复现 CMock fixture 设计

**状态：** 设计已确认
**日期：** 2026-09-16
**范围：** Phase 9 Batch F1
**实施基线：** 本设计文档提交时的本地候选链；实施开始前重新确认精确 HEAD

## 1. 目标

Batch F1 为 Phase 9 的原生框架矩阵建立可信、跨平台、可复现的输入层：固定 CppUTest、Unity、CMock 三项上游依赖；安全准备只含源码的 framework bundle；提交由固定 CMock generator 预生成的 mock 源码；提供可在 Windows 原生工具链编译、运行的真实 CppUTest/CppUMock 与 Unity/CMock fixture。

F1 只解决“测试什么、依赖从哪里来、生成文件如何证明、fixture 是否真实可运行”。四工具链 Service 驱动执行、每框架 17 个场景、双平台报告、GitHub hosted evidence 与 Phase 9 回执属于 Batch F2。

## 2. 范围与边界

### 2.1 F1 包含

- 将 `tools/framework-bundle/manifest.json` 从 Linux-only schema v1 升级为跨平台 schema v2；
- 精确固定 CppUTest 4.0、Unity 2.6.1 和 CMock 2.7.0 的 tag、revision、归档、展开树与许可证身份；
- 将 `tools/framework-bundle/prepare.mjs` 改为 Windows/Linux 均可使用的安全、确定性源码准备器；
- 新增普通校验入口 `tools/framework-bundle/check.mjs`；
- 新增仅供维护者显式调用的 `tools/framework-bundle/update-cmock-fixture.mjs`；
- 新增真实 `testdata/frameworks/cpputest/`、`testdata/frameworks/unity/` fixture 和负向安全 fixture；
- 提交 CMock 预生成 `.c/.h` 文件和闭集 provenance；
- 在 Windows 上用 MSVC 与 clang-cl 编译并运行两套真实 fixture；
- 将 CMock MIT 许可证加入第三方 license inventory，但不冒充人工 legal 审批。

### 2.2 F1 不包含

- 不实现 Linux GCC/Clang 与 Windows MSVC/clang-cl 的四工具链报告 producer；
- 不生成或发布每框架 17 个场景的 P4 platform report；
- 不修改 Phase 9 receipt、candidate baseline 或 gate matrix 状态；
- 不修改产品 Service、Extension、协议、打包、producer、foundation 或发布行为；
- 不运行正式 Windows 签名，不完成第三方 license/legal 人工审批；
- 不创建 tag、GitHub Release 或 Gitee Release；
- 不让普通 CI、产品运行时、Service 或 fixture 执行路径依赖 Ruby、Ceedling、Docker 或 CMock generator。

## 3. 固定依赖身份

schema v2 的三项依赖采用闭集记录。任何字段缺失、额外字段、大小写漂移或 digest 不一致均失败。

| ID | 版本 / tag | revision | 归档 SHA-256 | 展开树 SHA-256 | 许可证 |
|---|---|---|---|---|---|
| `cpputest` | `4.0` / `v4.0` | `b9b841c56c524a10ccd40e88c3acaf9d5ec751c2` | `21c692105db15299b5529af81a11a7ad80397f92c122bd7bf1e4a4b0e85654f7` | `c564fb5e4e32836dc66f46efb86edb6f1f2fa6afa255a57052031aa00fc56f04` | `BSD-3-Clause`; `COPYING` SHA-256 `d8fe282e4047197e1fbd6ef2527bde832a1514be6bd82fac7d1296ce184285c8` |
| `unity` | `2.6.1` / `v2.6.1` | `cbcd08fa7de711053a3deec6339ee89cad5d2697` | `b41a66d45a6b99758fb3202ace6178177014d52fc524bf1f72687d93e9867292` | `abfb7b2b7aec36739a7b138490d2e9dd178cc4f00e806ed372cbb8cfe98f73ae` | `MIT`; `LICENSE.txt` SHA-256 `907d9e859c6433703c0c183de3ddeaaf4baf3d517382f8f368b2c190fd2581d1` |
| `cmock` | `2.7.0` / `v2.7.0` | `6ea503340b1d3fdc0f2bcaf69273ba0160ec83af` | `d96282cf0286682f7628afc31cf2e3ed6ecb66944d63e098824d98196904f04c` | `19e013d70a3f032decb2e836b875a997d085ca72b34c92c91b528e6ad2e49ac3` | `MIT`; `LICENSE.txt` SHA-256 `f19bba29498b9405a86ab5fdc6bc58654fffb197603834e6d1423d583649b35c` |

所有提交到 manifest 的 digest 必须使用全小写规范表示；校验器不得接受大小写不同的第二种表示。

CMock 源归档固定为：

```text
https://github.com/ThrowTheSwitch/CMock/archive/refs/tags/v2.7.0.tar.gz
```

CMock 生成环境固定为官方 Linux amd64 Ruby 镜像的具体 manifest，而不是可漂移 tag：

```text
docker.io/library/ruby:3.3.6-bookworm@sha256:7184e67a2927ea0749093abd199f38c1da5f371ab4bf7056b6fff50669031556
platform=linux/amd64
```

tag `ruby:3.3.6-bookworm` 只用于人类可读说明；脚本执行必须使用上面的 digest。

## 4. framework bundle schema v2

`manifest.json` 继续是三项上游输入的唯一 reviewed lock。根对象采用闭集字段：

```json
{
  "schemaVersion": 2,
  "platforms": ["linux-x64", "windows-x64"],
  "fixtureTools": {
    "cmakeHelper": {},
    "unityRunnerGenerator": {},
    "cmockGenerator": {}
  },
  "frameworks": []
}
```

每个 framework 项必须包含且只包含：

```text
id
version
tag
revision
source { filename, url, sha256 }
license { spdx, path, sha256 }
sourceDirectory
treeSha256
```

`frameworks` 必须按 `cpputest`、`unity`、`cmock` 排序且恰好出现一次。`platforms` 必须按 `linux-x64`、`windows-x64` 排序。所有 revision 为 40 位小写十六进制，所有 SHA-256 为 64 位小写十六进制，所有 URL 必须是清单中精确允许的 HTTPS URL。

`fixtureTools.cmockGenerator` 必须包含且只包含：

```text
frameworkId=cmock
version=2.7.0
entrypoint=lib/cmock.rb
containerImage=docker.io/library/ruby
containerTag=3.3.6-bookworm
containerPlatform=linux/amd64
containerDigest=sha256:7184e67a2927ea0749093abd199f38c1da5f371ab4bf7056b6fff50669031556
generatedAtRuntime=false
```

现有 CMake helper 和 Unity runner generator 身份继续保留并校验。schema v2 不增加运行时下载例外：只有显式 bootstrap 可以联网填充不可变 archive cache，普通验证和产品运行均离线。

## 5. 安全准备器

### 5.1 `prepare.mjs`

准备器在 Windows 和 Linux 上执行同一组检查：

1. 以闭集 schema 校验 reviewed manifest；
2. 从固定 HTTPS URL 下载到专用 partial 文件，限制响应体大小与总时长；
3. 只接受 GitHub 预期的最终下载 host；
4. 在发布 cache 前验证归档 SHA-256；
5. 在专用 staging 目录检查 tar entry，再使用参数数组调用系统 tar；
6. 拒绝 symlink、junction、hard link、设备、绝对路径、盘符路径、反斜杠别名、空段、`.`、`..`、深度/条目数/展开尺寸超限；
7. 验证精确 source root、marker、展开树 SHA-256、许可证路径和许可证 SHA-256；
8. 写入确定性 `manifest.resolved.json` 与 `READY`；
9. 仅在全部检查成功后原子发布 prepared source tree。

archive cache 继续使用归档 digest 作为文件名身份。prepared source tree 使用 schema v2 manifest 原始字节 SHA-256 作为不可变目录身份。若目标已存在，准备器只验证并复用；不得先递归删除受信目标再覆盖。失败只清理本次随机 staging/partial 文件，不能修改已发布 cache、已提交 fixture 或其他工作区文件。

prepare 输出只包含上游源码和 resolved metadata，不包含编译产物、用户路径、时间、PID 或机器名。目录树摘要算法继续按排序后的相对 POSIX 路径和原始文件字节计算，Windows/Linux 必须得到相同结果。

### 5.2 `check.mjs`

普通 CI 和本地 `pnpm verify` 调用 `check.mjs`。它必须完全离线，并验证：

- reviewed manifest 自身与三个依赖的精确身份；
- cache/prepared tree 已存在时的 archive、tree、license 和 resolved metadata；
- CMock 预生成 input/output 闭集及所有 digest；
- provenance 不含本地绝对路径、用户名、时间戳或未知字段；
- fixture CMake 只消费 locked source tree 和已提交 generated files；
- 普通脚本、workflow、Service 和产品代码不存在 Ruby/Ceedling/CMock generator 调用。

cache 缺失不能触发隐式下载；需要下载时必须由显式 prepare 命令完成。

## 6. CMock 维护者生成流程

`update-cmock-fixture.mjs` 是唯一允许执行 CMock generator 的仓库入口，而且必须由维护者显式调用。它不属于 `pnpm verify`、workspace smoke、Phase 9 workflow、Service 或产品运行时。

生成流程如下：

1. 先验证 schema v2、CMock archive/tree/license 与 generator image digest；
2. 创建两个互不复用的新临时输出目录；
3. 分别启动固定 `linux/amd64` Ruby manifest；
4. 容器使用 `--network none`、只读 root filesystem、无额外 capability、`no-new-privileges`；
5. CMock source、`cmock.yml` 和输入 header 只读挂载，仅本次临时输出目录可写；
6. 通过参数数组运行 `ruby lib/cmock.rb`，不拼接 shell 命令；
7. 每次运行必须只产生闭集文件 `MockDependency.c` 与 `MockDependency.h`；
8. 拒绝 symlink、额外文件、缺失文件、非 UTF-8、CRLF、绝对路径、时间戳或本机身份；
9. 两次输出必须逐字节一致；
10. 完整校验 provenance 后，才原子替换 fixture 内已提交的 generated 文件和 `cmock-generation.json`。

如果 Docker 不可用、不是 Linux container、镜像 digest 不匹配、网络未禁用、generator 退出非零或两次输出不同，命令必须失败，并保持已提交 fixture 不变。

## 7. CMock provenance

`testdata/frameworks/unity/mocks/cmock-generation.json` 使用闭集、确定性结构，至少记录：

- schema version；
- CMock version、tag 与 revision；
- generator entrypoint 与报告的 generator version；
- Ruby image name、platform 与 immutable digest；
- `cmock.yml` SHA-256；
- 输入 header 的仓库相对路径与 SHA-256；
- 固定排序的输出闭集，以及每个输出文件 SHA-256；
- 按相对路径和原始文件字节计算的 aggregate output SHA-256；
- schema v2 framework manifest SHA-256；
- `generatedAtRuntime=false`。

文件不记录生成时间、临时路径、用户、主机、Docker ID 或其他易漂移信息。Phase 9 P4 producer 在 F2 中读取该文件，并按以下规则填充既有 `cMockProvenance`：

- `revision` 来自本文件；
- `generatorVersion` 来自本文件；
- `inputSha256` 是输入 header digest；
- `outputSha256` 是闭集 aggregate output digest；
- `manifestSha256` 是 `cmock-generation.json` 完整规范字节的 SHA-256，由消费者计算，不在文件内部自引用；
- `generatedAtRuntime` 固定为 `false`。

## 8. 真实 fixture

### 8.1 CppUTest/CppUMock

`testdata/frameworks/cpputest/` 使用真实 CppUTest 4.0 headers/sources 和 CMake helper，包含能够稳定表现以下原生行为的测试：

- pass；
- assertion failure；
- ignore/skip；
- CppUMock missing call；
- CppUMock unexpected call；
- CppUMock parameter mismatch；
- process crash；
- timeout。

fixture 的测试名、源文件相对路径和断言位置固定，不能包含构建目录或平台特有绝对路径。F1 只验证原生二进制能产生这些真实行为；F2 再通过 Service 将它们映射到 17 个标准场景。

### 8.2 Unity/CMock

`testdata/frameworks/unity/` 使用真实 Unity 2.6.1、CMock 2.7.0 runtime support 和已提交 `MockDependency.c/.h`，包含：

- pass；
- assertion failure；
- `TEST_IGNORE`；
- CMock expectation failure；
- process crash；
- timeout。

fixture build 不执行 generator。删除 Ruby、Docker 或网络后，locked source 已准备好的情况下仍必须能够编译和运行。

### 8.3 负向 fixture

`testdata/frameworks/failures/` 只保存小型合成输入，用于验证不可信 archive entry、manifest extra/missing field、错误 digest、许可证替换、CMock 缺失/额外输出、provenance 漂移与路径逃逸。负向 fixture 不保存大型上游归档，也不能被产品运行时发现为用户测试工程。

## 9. 数据流

```text
reviewed schema v2 manifest
        │
        ├── explicit prepare ── HTTPS archive ── digest/safe extraction/license/tree
        │                                      │
        │                                      ▼
        │                         immutable prepared source tree
        │                                      │
        └── maintainer-only CMock update ──────┤
                                               ▼
                         isolated generation run A + run B
                                               │
                         closed set + byte equality + provenance
                                               │
                                               ▼
                             committed MockDependency.c/.h
                                               │
                      ┌────────────────────────┴────────────────────────┐
                      ▼                                                 ▼
           CppUTest/CppUMock fixture                          Unity/CMock fixture
                      │                                                 │
                      └──────── Windows MSVC + clang-cl validation ─────┘
                                                                        │
                                                                        ▼
                                                           F2 Service matrix producer
```

## 10. 失败处理

稳定失败类别为：

```text
FRAMEWORK_MANIFEST_INVALID
FRAMEWORK_ARCHIVE_UNTRUSTED
FRAMEWORK_ARCHIVE_UNSAFE
FRAMEWORK_TREE_MISMATCH
FRAMEWORK_LICENSE_MISMATCH
FRAMEWORK_CACHE_INVALID
CMOCK_GENERATION_ENVIRONMENT_UNTRUSTED
CMOCK_GENERATION_OUTPUT_INVALID
CMOCK_GENERATION_NONDETERMINISTIC
CMOCK_PROVENANCE_INVALID
FRAMEWORK_FIXTURE_VALIDATION_FAILED
```

错误信息只暴露 framework ID、稳定原因和安全的仓库相对路径；不输出用户目录、临时目录、环境变量、token 或完整外部响应。未知字段、未知 platform、未知 framework、重定向到非允许 host、HTTP 错误、超时、工具缺失和任何不确定状态全部 fail-closed。

prepare 失败不能留下 `READY`。CMock update 失败不能留下部分 generated 输出。fixture 编译或运行失败不能生成 F2 可消费的成功报告。

## 11. 测试策略

### 11.1 manifest、archive 与 cache

离线单元测试覆盖：

- schema v2 最小合法输入；
- 三依赖闭集、排序、版本/tag/revision/archive/tree/license 精确身份；
- extra/missing field、重复依赖、未知平台、非法 URL、混合大小写或错误长度 digest；
- archive 超限、非法 entry、symlink/hard link/junction、路径穿越与盘符路径；
- archive、tree、license、resolved metadata 任一替换；
- staging 成功后的原子发布、失败清理和既有受信 cache 不被破坏；
- Windows/Linux 相同输入产生相同目录摘要。

### 11.2 CMock 生成与 provenance

测试覆盖：

- 固定 image/platform/digest 与 `--network none`、只读挂载契约；
- 输入 header、配置和 framework manifest digest；
- 输出缺失、额外输出、symlink、CRLF、时间戳、本地路径、未知字段；
- 两次独立生成逐字节一致；
- 任何失败均不修改已提交 generated files；
- 普通 check 只验文件，不调用 Docker、Ruby、Ceedling 或网络；
- provenance omission、field substitution、output digest substitution 全部失败。

### 11.3 Windows 原生 fixture

在本地 Windows 上分别以 MSVC 和 clang-cl：

- 配置并编译真实 CppUTest/CppUMock fixture；
- 配置并编译真实 Unity/CMock fixture；
- 运行 pass/assertion/skip/mock/crash/timeout 等原生行为并核对进程结果；
- 验证 CMock generated files 只被编译，不被重新生成；
- 验证路径、源位置和测试标识不依赖构建目录。

### 11.4 仓库回归

F1 完成前必须通过 framework bundle 聚焦测试、workspace smoke、Phase 9 validator/renderer check、根 `pnpm verify` 和 `git diff --check`。测试必须确认 F1 未修改证据回执：P4 不得被误标为 `PASS`，`releaseReady` 仍为 `false`，且固定三个 Phase 8 gate 仍为 `DEFERRED`。

## 12. F1 验收标准

F1 只有同时满足以下条件才完成：

1. schema v2 精确固定三项依赖及许可证身份；
2. 同一 prepare 实现在 Windows/Linux 上 fail-closed 地生成确定性 source bundle；
3. 普通 check 完全离线，且不要求 Ruby、Ceedling、Docker 或 generator；
4. CMock generator 只能由维护者显式运行，使用固定 Ruby image digest、禁网和只读输入；
5. 两次独立生成结果逐字节一致，提交的输出闭集和 provenance 全部通过；
6. MSVC 与 clang-cl 均能编译、运行真实 CppUTest/CppUMock 和 Unity/CMock fixture；
7. fixture 覆盖已确认的 pass、assertion、skip、mock、crash、timeout 行为；
8. CMock MIT notice 进入 license inventory，但 Phase 8 legal gate 仍保持延期；
9. 聚焦测试、workspace smoke、Phase 9 校验、renderer check 和完整 `pnpm verify` 通过；
10. 没有修改 receipt、P4 gate 状态、签名、发布或产品运行时行为。

## 13. F2 交接契约

F2 只能消费 F1 已提交并通过 check 的内容：

- schema v2 dependency lock；
- prepared source bundle identity；
- 两套真实 fixture；
- CMock closed generated outputs；
- `cmock-generation.json` 及其计算出的 P4 provenance。

F2 不得重新下载未锁定依赖，不得运行 CMock generator，也不得改写 F1 provenance。F2 负责四工具链 producer、每框架 17 个标准场景、平台报告、跨平台稳定 ID/digest 比较、workflow artifact、在线审计和候选绑定回执。只有 F2 的真实 hosted evidence 成功后，P4 相关 gate 才可以从 `MISSING`/`FAILED` 变为 `PASS`。

即使 F1 和 F2 都完成，只要正式 Windows 签名、第三方 license/legal 人工审批与 Phase 8 文档收口仍延期，`releaseReady` 必须继续为 `false`，不得发布生产 Release。
