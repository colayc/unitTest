# Phase 7：Linux GCC/gcovr Native Coverage Execution 设计

**日期：** 2026-09-06

**状态：** 已确认，实施计划待编写

**目标分支：** `master`

**基础提交：** `1ad11ba`

## 1. 目标

本批次补齐 Phase 7 的第一项缺口：在 Linux x64 production Service 中执行真实 GCC/gcovr 原生覆盖率链路。实现必须复用现有 durable queued `CoverageRun`、共享 Coverage Execution Coordinator、Protocol v1.4、Coverage JSON v1、报告生成与 artifact publish 事务，不得复制第二套 Coordinator，也不得用假 profile、空 collector 或测试替身宣称 Linux coverage 可用。

本批次完成后，Linux GCC + CppUTest 与 Linux GCC + Unity 都能通过真实 Unix Socket Service 完成 configure、build、serial test、gcov data collection、bundled gcovr aggregation、normalization、report 和 publish。Linux Clang coverage 仍明确 unavailable，留给 Phase 7 Batch B。

## 2. 当前实现基线与缺口

仓库已经具备：

- durable CoverageRun/TestRun 聚合与 Task Engine continuation；
- Windows `clang-cl`/`llvm-profdata`/`llvm-cov` production coverage；
- Coverage JSON v1、JUnit XML、单文件 HTML 和 Code-OSS coverage controller；
- product-owned Python 3.14.6 + gcovr 8.6 offline bundle；
- bundle manifest、checksum、license、closed layout、pinned runner 和 owned descriptor；
- Linux GCC/Clang toolchain discovery 与真实 Unix Socket Service smoke；
- `coveragerun` 对 Linux GCC/gcov/gcovr 的逻辑 routing model。

当前仍存在以下 production 缺口：

1. `coverageexec.PreparedAdapter`、Build Boundary、test profile allocation 和 normalizer 直接依赖 `coveragellvm` 类型，实际并未形成工具无关的 Adapter 边界。
2. Linux GCC discovery 尚未固定 `gcov`，也没有将 GCC、G++ 与 gcov 绑定到同一 coverage toolset identity。
3. Runtime 在非 Windows 平台直接把 coverage execution 标记为 unsupported，未按 retained toolchain family 分派 Adapter。
4. GCC instrumentation、`.gcno/.gcda` manifest、清理和收集尚未实现。
5. Coordinator 只会解析 LLVM stdout export，尚不能消费 gcovr 的 pinned JSON output。
6. Linux GCC CoverageRun 的 collector version 当前错误地沿用 compiler version，而不是 bundle manifest 中的 gcovr 版本。

因此本批次首先泛化既有边界，再新增 GCC 实现；Windows LLVM 的可观察行为必须保持不变。

## 3. 已确认决策

1. 采用单一共享 Coordinator + 平台 Adapter Registry，不新增 Linux 专用 Coordinator。
2. 不让 GCC 伪装成 LLVM，不生成 `.profraw`，不执行空的 `llvm-profdata` merge。
3. `coverageexec`、Build Boundary 和 embedded test runner 使用工具无关的 coverage capability；LLVM/GCC concrete types 留在各自 Adapter package。
4. Linux GCC 首批只支持 x64，与现有 `linux-x64` coverage bundle 和 `coveragerun.ResolveCollector` 一致。
5. GCC coverage test invocation 固定串行，`MaxConcurrency=1`。
6. GCC、G++、gcov 必须通过固定 probe、版本校验、file identity 和 SHA-256 绑定到同一 toolset identity。
7. Python、gcovr 和 Python dependency 只来自 product-owned bundle；产品运行时不使用系统 Python、pip、site-packages 或网络。
8. `.gcno/.gcda` 只在 Service-owned coverage build root 内按 closed manifest 管理；不递归删除任意文件。
9. CppUTest 与 Unity 复用同一个 embedded test execution path，不建立框架专用 coverage pipeline。
10. Protocol v1.4、Coverage JSON v1、三种 report artifact 和 Code-OSS UX contract 不改变。
11. 本批次不启用签名、不发布 GitHub Release，也不替代最终第三方 license/legal 人工审批。

## 4. 范围

### 4.1 本批次包含

- 泛化 Coverage Adapter、Build Boundary、test decoration、evidence manifest、collector 和 normalizer 接口；
- 保持 Windows LLVM production behavior 和门禁；
- Linux GCC/gcov capability discovery 与 identity；
- GCC CMake instrumentation；
- `.gcno/.gcda` 生命周期和 completeness evidence；
- product-owned gcovr bundle runtime 接线；
- bounded gcovr JSON parser 与 GCC normalizer；
- Linux x64 CppUTest/Unity native coverage E2E；
- Linux native offline evidence、deterministic rerun 和 CI required check。

### 4.2 本批次不包含

- Linux Clang/llvm-cov；
- macOS；
- MSVC `cl.exe` coverage；
- coverage threshold 或增量覆盖率；
- Mock/Stub UX、完整 run configuration UX 或 history/artifact browsing；
- Windows 签名、公开 Release 或 legal 人工批准；
- Workspace 提供的 gcovr config、plugin、Python script、collector argv 或 executable override。

## 5. 总体架构

```text
Protocol v1.4 coverage/runs/start
              │
              ▼
      Runtime Coverage Backend
              │ retained toolchain + persisted snapshot
              ▼
      Coverage Adapter Registry
       ├─ Windows clang-cl ─► LLVM Adapter
       ├─ Linux GCC ───────► GCC Adapter
       └─ Linux Clang ─────► unavailable（Batch B）
              │
              ▼
   Shared coverageexec.Coordinator
       ├─ Generic Build Attachment
       ├─ Adapter-owned Test Decoration
       ├─ Adapter-owned Evidence Manifest
       ├─ Controlled Collector Plan
       └─ Adapter Parser/Normalizer
              │
              ▼
 Coverage JSON v1 + JUnit + HTML
              │
              ▼
      Existing Artifact Store
```

Runtime 不再用单个 `native := platform == "windows"` 布尔值决定能力。它构造 closed Adapter Registry，并用当前 Inspector 重新验证后保留的 `toolchain.Instance` 与 queued run snapshot 一起解析唯一 Adapter。持久化 snapshot 只用于一致性比较，绝不能反向重建 executable 或 native path。

普通 build/test/discovery 不依赖 coverage bundle。bundle 缺失或 GCC coverage capability 不完整时，只有对应 CoverageRun 以稳定 unavailable 语义结束，Service 其余能力继续工作。

## 6. 工具无关的共享执行合约

### 6.1 Adapter Registry

Registry 的 key 是 closed platform/family combination，不接受字符串插件名或 Workspace 扩展：

- `windows + clang-cl` → LLVM Adapter；
- `linux + gcc` → GCC Adapter；
- 其他组合 → unsupported。

Registry 在 `Prepare` 时同时验证 retained `toolchain.Instance` 与 persisted `coveragedomain.ToolchainSnapshot` 的 platform、architecture、family、version、driver、collector、normalizer 和 instrumentation fingerprint。任何不一致都在运行 native process 前失败。

### 6.2 Prepared Adapter

共享 Coordinator 只依赖逻辑能力：

- 返回 exact instrumentation contract；
- 返回可转移所有权的 verified build toolset；
- 为每个 test invocation 装饰并验证 `ProcessSpec`；
- 在 test 前准备并清理 Adapter-owned evidence；
- 在 test 后封存 bounded evidence manifest；
- 基于 trusted binaries、build root 和 evidence 产生 closed collector plan；
- 验证 collector completion，并从 pinned output 解析、规范化；
- 幂等关闭 pins、temporary data 和 native resources。

共享接口不再出现 `coveragellvm.Toolset`、`coveragellvm.Manifest` 或 LLVM-specific environment name。

### 6.3 Build Boundary

Build Boundary 改为持有通用 coverage attachment：

- verified compiler/toolset capability；
- instrumentation file、parent directory、digest 和 fingerprint；
- coverage binary directory capability；
- 可选的 collector execution capability。

attachment 必须保留当前 claim/commit/rollback ownership 语义。失败 attach 不转移所有权；成功 attach 后只有 Boundary 负责关闭。Boundary 在 configure、build、test、collector 启动前后重新验证相关 identity。

LLVM attachment 继续固定 compiler、profdata、cov。GCC attachment 固定 C compiler、C++ compiler 与 gcov；gcovr collector execution 在 `.gcno/.gcda` manifest 封存后再附加。

### 6.4 Embedded test decoration

embedded runner 不再生成 LLVM `.profraw` 文件名，也不再硬编码“必须恰好一个 `LLVM_PROFILE_FILE`”。Adapter 对每个 invocation 返回：

- 不变的 executable、argv 和 working directory；
- Adapter 允许的 environment delta；
- stable invocation/iteration evidence expectation；
- 用于验证 decoration 的 closed policy。

LLVM policy 仍要求唯一 Service-owned `LLVM_PROFILE_FILE`。GCC policy 不添加 profile path，并删除或拒绝能改变 gcov 输出位置或行为的 hostile environment，例如 `GCOV_PREFIX`、`GCOV_PREFIX_STRIP` 和相关 gcov control variables。

### 6.5 Collector plan 与状态机

现有顶层 Task 和 public step vocabulary 保持兼容。LLVM 的 merge step 继续执行真实 `llvm-profdata`，normalize step 继续执行 `llvm-cov export` 并规范化 stdout。

GCC 的 aggregate step 执行真实 bundled gcovr combined collection；它不是空 merge，也不调用 `llvm-profdata`。随后 normalize 是 Service-owned action：验证并读取 pinned gcovr JSON、调用 GCC parser/normalizer，再进入现有 report/publish。

`coveragerun.Plan` 决定 concrete collector 是否需要独立 native merge tool。Coordinator 不再假设所有 Adapter 都返回两个 process specs；它消费有界、closed 的 collector step 列表和 service action。现有 completion reason 保持：

- test 后缺失或不可信 raw evidence → `profile_collection_failed`；
- native aggregate/collector process 失败 → `merge_failed`；
- pinned output、parser 或 normalizer 失败 → `normalization_failed`。

## 7. GCC/gcov Toolset Identity

### 7.1 discovery

`NewUnixAdapters` 继续发现 GCC/G++ pair。GCC Adapter 增加固定 probe，让当前 C compiler 定位 gcov：

1. 使用固定 GCC program-discovery argument；
2. 接受规范绝对结果，或只在已固定 compiler directory 内解析确定性 basename；
3. 不在 coverage execution 时搜索 `PATH`；
4. 固定 gcov executable 后运行 bounded `gcov --version` probe；
5. 在每次 probe 前后重新验证 compiler pair 与 gcov snapshots。

如果 gcov 不存在、路径不安全、版本无法解析或版本与 GCC/G++ 不匹配，toolchain 本身仍可用于普通 build，但 coverage capability 为空。

### 7.2 evidence model

`toolchain.CoverageCapability` 增加 GCC 所需的明确 evidence，而不复用 LLVM 字段制造歧义：

- gcov path 与 normalized version；
- C compiler evidence；
- C++ compiler evidence；
- gcov evidence；
- GCC coverage toolset identity。

Unix evidence 至少绑定 canonical path、device/inode、mode 与 SHA-256。`GCCToolsetIdentity` 使用带版本标签的 deterministic hash，覆盖 family、compiler version、target triple、三个 executable path/evidence 和相关 SDK/sysroot identity。identity 进入内部 build/configuration fingerprint；native path 不进入 Protocol、Coverage JSON 或公开 diagnostic。

Registry 对 GCC family 执行 family-specific completeness 和 identity recomputation。Windows LLVM validation 保持独立，不因新增字段放宽。

### 7.3 CoverageRun provenance

Linux GCC snapshot 记录：

- compiler：GCC 的真实 normalized version；
- driver：gcov 的真实 normalized version；
- collector：bundle manifest 的 gcovr version，例如 `8.6`；
- normalizer：产品 GCC normalizer contract version；
- instrumentation fingerprint：exact GCC CMake include contract fingerprint。

compiler/driver 必须满足支持的兼容关系；collector 不要求等于 compiler。当前把 GCC version 写入 collector version 的行为必须删除。

## 8. GCC instrumentation

GCC Adapter 在 Task-owned、owner-only instrumentation root 原子发布只读 CMake include。固定 contract：

- C/C++ compile 和 link 使用 `--coverage`；
- C/C++ compile 使用 `-fprofile-abs-path`；
- 只接受 CMake 识别出的 GNU compiler；
- 不覆盖 base profile optimization；
- 不读取 Workspace coverage flags、gcovr config 或 environment override。

instrumentation bytes、SHA-256 和 versioned fingerprint 由 `coveragegcc` 唯一维护。Build planner 的 instrumentation validation 改为比较 Adapter 提供的 retained contract，不再调用 LLVM global constant。

configure 后继续使用 CMake File API 验证 compile/link fragments。build 后验证 test executable digest、coverage build identity 和 `.gcno` capability；目标撤销 instrumentation、compiler 忽略 flag 或没有有效 `.gcno` 时在测试前失败。

优化构建不自动降级或改写。若 base profile 启用了会影响 source mapping 的 optimization，report metadata 记录配置并生成既有 bounded warning，但仍以实际工具输出为准。

## 9. `.gcno/.gcda` 生命周期

### 9.1 build 后封存 `.gcno`

在成功 coverage build 后，GCC Adapter 在 verified coverage binary root 内有界遍历 `.gcno`：

- 只接受 direct regular file；
- 拒绝 symlink、special file、异常 hard link、越界路径和重复 identity；
- 保存 root-relative path、size、file identity 和 digest；
- 对目录数量、深度、文件数量和总 hash bytes 设置固定 budget；
- 遍历前后验证 root 与所有 retained parent directory identity。

没有有效 `.gcno` 视为 coverage build 无效，映射 `build_failed`，不得生成空报告。

### 9.2 test 前清理

根据 `.gcno` manifest 计算唯一允许的 `.gcda` path set。对上一轮遗留数据：

- 只使用 pinned parent directory 下的 handle-relative operation；
- 只删除 expected set 中的 direct regular `.gcda`；
- 未知 `.gcda`、symlink、hard link、删除失败或目录替换均 fail closed；
- 不递归删除 coverage build root 或 Workspace 文件。

同一 coverage build identity 继续由既有 exclusive lease 保护；GCC test invocation 固定串行。

### 9.3 test 后封存 `.gcda`

测试结束后再次验证 `.gcno` manifest，并枚举 expected `.gcda`：

- 正常完成且全部 expected data 缺失 → `profile_collection_failed`；
- crash/timeout 后部分 data 缺失 → 允许 `partial`，复用现有 completeness reason；
- 正常 assertion failure 但 profile 完整 → CoverageRun 可为 `available`，TestRun 仍为 `failed`；
- 未知、越界或身份不可信的 data → `profile_collection_failed`，不调用 gcovr。

GCC counter 是 build-object scoped，不声称能够把每个 `.gcda` 精确归因到单个 test invocation。partial 判断只使用真实 TestRun outcome 与 object manifest evidence，不制造不存在的 per-test profile mapping。

### 9.4 cleanup

成功规范化后、publish 前删除当前 run 的 `.gcda`、gcovr third-party JSON 和 owned descriptor。取消、失败和 shutdown 也通过幂等 `Close` 尝试清理。

cleanup 失败会阻止当前结果 publish，并让下一次 pre-test cleanup 再次 fail closed。`.gcno` 与独立 coverage build tree 可以保留供同一 immutable build identity 复用。

## 10. Bundled gcovr execution

### 10.1 bundle root

正式安装布局中的 platform bundle 位于 verified Service executable 的产品树内 `bundles/coverage`。Runtime 从安装布局推导 exact bundle root；Workspace、Protocol 和 Coverage Profile 均不能提供路径。

开发和 CI 可通过内部 dependency/config seam 注入 `.superpowers/runtime/coverage-bundle/linux-x64`，但该 seam 不暴露为生产 Protocol 字段或普通 Workspace setting。resolver 必须验证 absolute canonical path、expected platform、closed tree、manifest、READY、file identity、mode 和 digest。

这与现有 release staging 一致：packager 已把所选 platform bundle 复制到 `bundles/coverage`。本批次负责让 Linux runtime 真正消费该资源；不改变签名和 Release 决策。

### 10.2 owned descriptor

GCC Adapter 调用现有 `coveragebundle.PrepareRunner`，descriptor 只包含：

- verified Workspace root；
- verified coverage object directory；
- verified gcov executable；
- Task-owned gcovr JSON output path。

四项均通过 `serviceanchor`/verified directory/executable capabilities 关联到同一 authority。descriptor 在 owner-only root 中原子创建，大小有界，字段 closed，执行前后验证。

### 10.3 fixed process

唯一允许的 collector process 形式为：

```text
<bundled-python> -I -S <gcovr-runner.pyz> <owned-descriptor>
```

runner 只生成固定 gcovr argv：root、object-directory、gcov-executable、JSON output 和 pretty JSON。它不接受 `-c`、`-m`、arbitrary script、plugin、config、filter、working-directory override 或追加 argv。

环境 policy 忽略/移除 `PYTHON*`、user site、import path、gcov/gcovr control、dynamic loader injection 和代理变量。bundle 下载只允许在开发/CI bootstrap，native coverage runtime window 内不联网。

### 10.4 pinned output

collector 成功退出后，Boundary 先调用 `VerifyCoverageExecutionAfter`，再通过 retained handle 取得 `PinnedCoverageOutput`。normalizer 只从 pinned handle 读取，不能根据 mutable path 重新打开文件。output size、identity、digest 和 tree shape 在 parser 前后验证。

## 11. gcovr parser 与 GCC normalizer

### 11.1 bounded parser

新增 `internal/coverageparser/gcovr`。parser 使用 `json.Decoder.Token` 流式读取 gcovr 8.6 实际输出的受支持 format version：

- 验证 root shape、required fields、nested types 和 exact version policy；
- 拒绝 duplicate field、unexpected critical shape、trailing JSON、invalid UTF-8；
- 拒绝 negative/non-integral count、non-finite number、overflow 和 non-canonical path；
- 执行 `coveragenormalize.DefaultLimits` 的 input、depth、file、line、branch、function 和 string budgets；
- 输出 GCC-specific internal export model，不直接生成 public document。

gcovr 在 fixed `--root` contract 下产生的 source file 必须是规范的 Workspace-relative forward-slash path。absolute path、`.`、`..`、separator alias、NUL 和 root escape 全部拒绝。

### 11.2 NormalizeGCC

新增 `NormalizeGCC`，并从 `NormalizeLLVM` 提取共享 helper：

- include/exclude glob；
- physical source identity 与 symlink policy；
- source SHA-256 和 URI；
- canonical file/line ordering；
- lines、branches、functions summary；
- completeness 和 provenance；
- safe integer、aggregate overflow 和 Coverage JSON v1 validation。

GCC 与 LLVM 可以有不同 native detail，但公共 Coverage JSON v1 仍只暴露 lines、branches、functions。gcovr-specific call/path condition、compiler internals 或 native paths 不进入公共模型。

### 11.3 deterministic output

相同 Workspace、build identity、selection 和 test behavior 连续运行两次，最终 Coverage JSON 必须 byte-identical。run ID、task ID、timestamps、duration、temporary root、descriptor path、compiler path 和 gcov path 不得进入 canonical JSON。

JUnit outcome 继续只来自关联 TestRun；HTML 继续从 canonical Coverage JSON + source bindings 渲染。三个 artifacts 仍由现有 publish transaction 一次提交。

## 12. 安全与失败语义

| 失败点 | Coverage reason | 公开结果 |
|---|---|---|
| GCC/gcov identity、bundle 或 instrumentation 无法验证 | `instrumentation_failed` | unavailable，无 report |
| configure/build instrumentation 无效或没有 `.gcno` | `build_failed` | unavailable，无 report |
| `.gcda` 缺失、未知、越界或不可信 | `profile_collection_failed` | unavailable，或仅在 crash/timeout evidence 下 partial |
| bundled gcovr process 非零退出、timeout 或取消外的 collector failure | `merge_failed` | unavailable，无 report |
| pinned output、gcovr JSON parser、source binding 或 normalizer 失败 | `normalization_failed` | unavailable，无 report |
| report renderer 失败 | `report_generation_failed` | unavailable，无 publish |
| artifact transaction 失败 | `persistence_failed` | unavailable，无部分 artifact 引用 |

错误日志、Task event metadata 和 CI evidence 必须经过现有 redaction/closed schema；不得包含 native path、raw argv、environment、descriptor 内容、token 或 Workspace source text。

## 13. 测试策略

### 13.1 单元与组件测试

必须覆盖：

- Adapter Registry 的 closed routing、typed nil、snapshot mismatch 和 unsupported；
- generic interface 重构后的 Windows LLVM exact behavior；
- GCC/G++/gcov probe args、version compatibility、identity mutation、symlink/path escape；
- GCC instrumentation bytes、digest、fingerprint、atomic publication 和 immutable root；
- `.gcno/.gcda` budgets、closed set、cleanup、unknown file、symlink、hard link、directory replacement；
- GCC test decoration 对 hostile gcov environment 的处理；
- bundle/manifest/READY/Python/runner/descriptor/output mutation；
- gcovr parser 的 simple、branch、function、empty、malformed、duplicate、unsupported version、overflow、depth 和 size fixtures；
- NormalizeGCC 的 filters、Linux case-sensitive paths、symlink escape、source digest、sort 和 canonical writer；
- cancel、timeout、crash、assertion failure、missing data、collector failure、cleanup failure 和 publish rollback。

所有 bugfix/feature implementation 按 red-green-refactor 推进；implementation plan 必须先列 failing test，再列最小 production change。

### 13.2 Linux production native E2E

Linux x64 job 构建真实 `unit-test-service`，通过 Unix Socket 与 Protocol v1.4 分别运行：

1. GCC + CppUTest；
2. GCC + Unity。

每条路径验证：

- production discovery 得到真实 GCC/G++/gcov capability；
- 独立 coverage build tree 和真实 `.gcno/.gcda`；
- serial selected test execution；
- bundled Python/gcovr collector；
- available/partial/unavailable 的正确映射；
- Coverage JSON、JUnit XML、HTML metadata/digest/content；
- include/exclude 和已知 line/branch/function counts；
- Extension coverage controller 消费相同 Protocol/artifact；
- 第二次运行的 canonical Coverage JSON byte-identical；
- protocol wire、stderr、artifact metadata 和 evidence report 不泄漏 path/secret。

E2E 同时包含至少一条 assertion failure 但 coverage available 的路径，以及 crash、timeout/cancel、missing data 和 malformed collector output 的受控故障用例。故障注入只能通过 test-only seams/fixtures，不能成为 production Workspace option。

### 13.3 Linux native offline boundary

依赖、CMake bundle 和 coverage bundle 在 bootstrap 阶段准备完成后，真实 native coverage smoke 在独立 Linux network namespace 内执行：

- namespace 无外部 interface/route；
- Unix domain socket 可用；
- Service、CMake、GCC/G++、fixture、gcov、bundled Python/gcovr 全部在同一隔离进程树；
- 边界建立失败即 FAIL，不允许将 SKIP 计为 native coverage PASS；
- teardown 后输出 closed、path-free offline evidence。

Node HTTP guard 和 bundle unit test 仍保留，但不能替代 native process tree 的 network namespace evidence。

## 14. CI 与门禁

新增独立 GitHub Actions required check：`coverage-linux-gcc`。它至少执行：

- Go unit/integration tests、`go test -race` 和 `go vet`；
- coverage bundle manifest/checksum/license/self-check；
- GCC instrumentation/parser/normalizer Golden tests；
- CppUTest 与 Unity production native coverage E2E；
- Linux network namespace offline run；
- deterministic second run；
- path/secret leakage audit。

job 始终上传 closed schema `linux-gcc-coverage-report.json`，但 artifact 本身不作为成功的替代；只有 required check 成功才算 PASS。报告至少记录 schema version、outcome、无路径 toolchain/bundle identities、两个 framework case outcome、determinism、offline boundary 和 timestamps。

现有 Windows LLVM、Windows/Linux foundation、package 和 unsigned qualification checks 必须继续成功。新增 check 稳定后加入 `master` 分支保护；在加入前不得把 Batch A 标记为完整。

Gitee 同步继续遵守现有流程：只有 GitHub PR 合并并验证后才把 GitHub `master` 普通同步到 Gitee `master`，不强制覆盖。

## 15. 分阶段实施顺序

1. 泛化 shared coverage contracts，并用 Windows LLVM characterization tests 保证无行为漂移。
2. 增加 GCC/gcov discovery evidence、identity 与正确的 CoverageRun provenance。
3. 实现 GCC instrumentation、test decoration 和 `.gcno/.gcda` lifecycle。
4. 将 existing coverage bundle execution 接到 generic Boundary 与 Coordinator。
5. 实现 gcovr bounded parser、NormalizeGCC 和 canonical report path。
6. 注册 Linux GCC Adapter，并保持 Linux Clang explicit unavailable。
7. 增加 CppUTest/Unity native E2E、offline boundary、evidence artifact 和 required CI check。
8. 运行全量回归，更新 Phase 7 roadmap evidence；随后才进入 Phase 7 Batch B。

## 16. 验收标准

Batch A 只有同时满足以下条件才完成：

1. Linux x64 production Service 对 GCC CoverageRun 不再返回 unsupported。
2. GCC、G++、gcov 和 gcovr bundle 全部有真实、可重复验证的 provenance。
3. CppUTest 与 Unity 都产生准确的 Coverage JSON v1、JUnit XML 和 HTML。
4. `.gcda` stale/unknown/symlink/hard-link/escape 不会被静默读取或任意删除。
5. crash/timeout/assertion/collector/parser/persistence failure 的 outcome/reason 符合本设计。
6. 相同输入连续两次的 Coverage JSON byte-identical。
7. native offline boundary 覆盖 Service 完整子进程树且为 required PASS。
8. `coverage-linux-gcc` 成功并进入 `master` 分支保护。
9. 现有 Windows LLVM 与所有 foundation/unsigned qualification 回归成功。
10. 工作区、日志、Protocol 和 CI evidence 不泄漏 native path、environment 或秘密。
11. Linux Clang 仍以精确 unsupported 结束，没有假报告。
12. 不发布 Release、不启用签名；最终 legal/signing gate 继续保留到正式公开发布前。
