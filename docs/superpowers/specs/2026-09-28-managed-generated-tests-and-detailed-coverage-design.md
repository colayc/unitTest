# 受管生成用例与文件/函数级覆盖率设计

**日期：**2026-09-28  
**状态：**已确认  
**前置设计：**`docs/superpowers/specs/2026-09-27-offline-coverage-guided-test-generation-design.md`

## 1. 目标

在现有完全离线、无 Mock/Stub 的 C/C++ 测试生成能力上补齐两项首版必需能力：

1. 生成用例可长期维护：重复生成不重复追加，用户手写内容不丢失，源码变化可识别过期用例，用户修改生成区后必须通过冲突审查。
2. 覆盖率可逐级查看：Code-OSS 同时提供项目、文件、函数级函数/行/分支覆盖率，以及源码行和分支标记。

该能力不引入云服务、Web 后端、本地 LLM 或新的独立服务端应用。它扩展现有 `apps/test-service` Go 本地服务、内置 Code-OSS 扩展和本地 IPC 协议。完整产品仍打包为 Code-OSS 桌面程序、内置扩展、`unit-test-service`、固定工具包及许可文件。

## 2. 非目标

- 不生成或使用 Mock、Stub、CppUMock、CMock。
- 不自动覆盖用户修改过的受管代码。
- 不自动删除失去源函数的测试。
- 不把未知或无法归属的覆盖率当作 0% 或 100%。
- 不允许扩展自行解析非权威 coverage artifact 后授权写入。
- 不以本地静态测试替代真实 Windows/Linux native evidence。
- 不改变正式 Windows 签名、第三方 license/legal 审批和最终发布资格门禁。

## 3. 架构

### 3.1 ManagedTestRegistry

`unit-test-service` 新增受管用例注册表，记录：

- 稳定用例 ID；
- 项目、源文件、源函数和语义身份；
- 测试文件的 workspace-relative path；
- 受管代码块当前接受摘要；
- 生成器、framework、toolchain 和 coverage provenance；
- 最近一次成功编译、执行和覆盖率验证证据；
- `current | stale | conflicted | orphaned | invalid` 状态。

稳定用例 ID 由项目、规范化源文件路径、稳定函数身份和场景身份派生，不依赖源码行号。SQLite 增量迁移持久化注册表；迁移失败时保留旧数据并禁止广告新能力。

### 3.2 CoverageDetailIndex

服务端建立权威覆盖率明细索引，包含：

- 项目汇总；
- 每个文件的函数、行、分支 `covered / total / percent / delta`；
- 每个函数的规范化名称、源位置和函数/行/分支明细；
- 未覆盖行、部分覆盖分支和服务端签发的 coverage-gap ID；
- `current | stale | incomplete` 状态及稳定原因码；
- coverage toolchain、collector 和 workspace snapshot 身份。

inline、template、宏展开和重复实例必须依据规范化源位置与稳定函数身份去重。无法可靠归属的记录标记为 `incomplete`，不得进入精确百分比汇总。

### 3.3 ManagedTestReconciler

协调器执行三方比较：

1. 上次接受并持久化的受管代码块；
2. 当前 workspace 中的受管代码块；
3. 本次重新生成并验证通过的候选代码块。

当前块与上次接受块一致时，可生成普通更新预览；存在用户修改时必须进入 `conflicted`，展示三方差异并要求用户选择：

- 保留当前内容；
- 采用新生成版本；
- 将当前块转为非受管手写用例。

冲突未解决时不得调用 publisher。所有正式写入继续由现有原子 publisher 独占执行。

### 3.4 Code-OSS

内置扩展新增：

- 项目 → 文件 → 函数覆盖率树；
- 源码行和分支覆盖率标记；
- `stale/conflicted/orphaned/incomplete` 状态展示和筛选；
- 受管用例普通差异和三方冲突审查；
- 基于服务端权威函数或 coverage-gap ID 的生成入口。

扩展只负责显示和收集明确确认，不得自行认定 coverage、用例身份或可写状态。

## 4. Protocol v1.6

新增 Protocol v1.6，而不修改已发布的 v1.0–v1.5 语义。v1.0–v1.4 继续保持 1 MiB wire 边界；v1.5 保持既有 2 MiB 行为。v1.6 使用分页和按需明细，避免以提高单消息上限替代数据建模。

v1.6 至少提供以下闭集能力：

- 获取 coverage project summary；
- 分页列出 file coverage；
- 按文件分页列出 function coverage；
- 按文件/函数读取 line 和 branch detail；
- 分页列出 managed test records；
- 获取 managed test 三方 review；
- 将冲突选择绑定到最新 review digest；
- 将函数或 coverage gap 的服务端签发 ID 用于 generation request。

每个列表响应包含稳定 cursor、workspace generation、coverage report identity 和有界 page size。所有接受请求继续只携带闭集 ID、确认信息和服务端签发摘要；源字节、绝对路径、argv、环境变量和任意输出路径均不进入客户端请求。

老服务未广告 v1.6 时，新 UI 隐藏受管维护和函数覆盖率树，v1.5 生成流程维持现状，不进行不安全降级。

## 5. 受管测试文件格式

一个源文件对应一个生成测试文件：

```text
src/math.cpp
tests/generated/src/math_test.cpp
```

文件内按函数分区。受管区使用可验证、不可嵌套的成对标记：

```cpp
// unit-test-ide:managed-begin case=utc_7f2... symbol=calculateTotal
TEST_GROUP(CalculateTotalGenerated) {
};

TEST(CalculateTotalGenerated, ReturnsZeroForEmptyInput) {
    // Arrange
    Order order{};

    // Act
    const auto result = calculateTotal(order);

    // Assert
    CHECK_EQUAL(0, result);
}
// unit-test-ide:managed-end case=utc_7f2...
```

规则：

- 受管块外的 include、辅助函数和手写测试保持原样。
- 标记缺失、重复、嵌套、ID 不匹配或摘要不匹配时 fail-closed。
- 生成名称必须描述行为，并使用 Arrange–Act–Assert 结构。
- 重复生成同一场景产生相同 ID、顺序和字节，不追加副本。
- 用户修改受管块后只允许进入冲突审查。
- 函数实现或签名变化后关联记录变为 `stale`。
- 函数删除或无法可靠匹配后记录变为 `orphaned`，只能由用户确认保留、转手写或删除。
- 元数据不写入绝对路径、原始源码或主机身份。

## 6. 覆盖率用户体验

覆盖率树显示：

```text
Project  行 82% · 分支 68% · 函数 91%
├─ src/math.cpp  行 18/24 75% +4 · 分支 6/10 60% +2
│  ├─ calculateTotal  行 8/8 100% +3 · 分支 4/4 100% +2
│  └─ applyDiscount  行 10/16 62.5% +1 · 分支 2/6 33.3%
└─ src/parser.cpp  行 42/50 84% · 分支 12/18 66.7%
   └─ parseHeader  行 14/20 70% · 分支 3/7 42.9%
```

每个项目、文件和函数节点显示函数、行、分支的覆盖数/总数、当前百分比和相对本次基线的增量。点击文件或函数跳转到摘要绑定的源码位置。

编辑器标记：

- 已覆盖行；
- 未覆盖行；
- 部分覆盖分支，例如 `2/4 branches`；
- 过期覆盖率；
- 不完整或不可比较数据。

树支持筛选未覆盖函数、覆盖率下降、过期用例、冲突用例和不完整报告。大型项目必须分页加载并对树节点虚拟化，不能一次加载所有函数或明细。

## 7. 数据流

1. 服务运行现有测试并形成权威覆盖率基线。
2. CoverageDetailIndex 建立项目、文件、函数、行和分支索引。
3. 用户选择文件、函数或 coverage gap；扩展提交服务端签发的稳定 ID。
4. 服务在隔离根目录分析、求解和渲染候选。
5. 候选依次通过 schema、静态安全检查、configure、compile/link、单候选执行、现有套件回归、coverage provenance、断言质量和 coverage delta 验证。
6. 只有包含有效断言、测试通过、至少提高一种目标覆盖率且任何覆盖率指标均不回退的候选可以进入 review。
7. ManagedTestReconciler 生成普通差异或三方冲突 review。
8. 用户确认绑定最新 review digest；publisher 原子更新测试文件和 CMake 配置。
9. 服务重新运行全部测试和 coverage；只有成功结果才能更新当前 Coverage 树和 delta。
10. 任一步失败均保留原 workspace 字节和上一份有效 coverage report。

源码变化不会触发自动写入。受影响用例先标记 `stale`，经过重新分析、真实编译执行和 coverage 验证后仅产生更新预览。

## 8. 错误处理与安全

- workspace generation、源文件摘要、函数身份、coverage baseline 或 toolchain identity 漂移时，使 review 过期并写入零字节。
- symlink、reparse point、路径穿越、大小写别名和越界路径一律拒绝。
- 用户取消后必须等待 stage 与 process-owner 终止证明，才能进入 `cancelled`。
- 服务重启只恢复具有持久摘要和 owner identity 的阶段；未确认候选不得自动发布。
- 覆盖率工具或 collector 身份变化时，旧结果标记为不可比较。
- 日志、遥测和协议诊断不得包含源码、测试内容、绝对路径、环境变量、token 或无界原始输出。
- SQLite 迁移、分页 cursor 或记录校验失败时禁用 v1.6 能力，不影响旧协议安全运行。

## 9. 验收标准

### 9.1 可维护性

- 相同源码重复生成时输出字节一致且不产生重复用例。
- 用户修改受管块外内容后重新生成，手写内容完整保留。
- 用户修改受管块内内容后进入冲突状态且磁盘零写入。
- 函数变化使关联用例变为 `stale`，重新验证后才能更新。
- 函数删除或无法匹配使关联用例变为 `orphaned`，不得自动删除。
- 写入任一阶段崩溃后可以恢复或回滚。
- 重复接受请求仅产生一次相同结果。

### 9.2 覆盖率

- 项目汇总与文件汇总一致，文件汇总与归属的函数/行/分支明细一致。
- 每个文件和函数均显示 `covered / total / percent / delta`。
- inline、template、宏展开和重复实例不会重复计数。
- 不可归属记录明确为 `incomplete`。
- 文件/函数跳转与编辑器覆盖率标记指向正确摘要绑定位置。
- coverage 下降、报告漂移或工具身份变化被拒绝或标记不可比较。

### 9.3 集成与性能

- CppUTest 与 Unity 均通过真实 fixture。
- Windows MSVC、Windows clang-cl、Linux GCC、Linux Clang 均产生真实 native block；四种 toolchain × 两种 framework，共八个 required blocks。
- 覆盖服务重启、取消、workspace 切换、信任撤销和协议降级。
- 大型树验证分页、cursor、虚拟化、内存和响应预算。
- hosted evidence 必须绑定真实 coverage、mutation、fault/security 和 performance receipt；本地静态报告不构成 native PASS。

## 10. 发布边界

该设计完成与实施通过不代表可以发布。正式 Windows 签名、最终第三方 license/legal 人工审批、真实四工具链 native evidence、coverage/mutation/abuse receipts 和最终发布资格仍是独立门禁。在这些门禁闭合前，`releaseReady` 必须保持 `false`。
