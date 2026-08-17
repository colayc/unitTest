# Phase 5D：LLVM/GCC collection 与 Coverage JSON normalization 实施计划

> 实施时逐 Task 使用 red-green-refactor TDD；每个 Step 完成后更新 checkbox。

**目标：** 将当前 CoverageRun 的 LLVM/GCC profile 安全转换为 bounded internal model 和 deterministic Coverage JSON v1；本计划不生成 JUnit/HTML，也不开放 Protocol handler。

**架构：** `coveragecollect` 拥有 profile lifecycle 和 fixed tool process；`coverageparser/llvm` 与 `coverageparser/gcovr` 只解析 bounded stream；`coveragenormalize` 负责真实路径边界、glob、source digest、common metric 和 canonical serialization。Tool-specific parser 不依赖 Workspace/Protocol。

**依赖：** Phase 5A contract/domain、5B fixed runner、5C instrumented build/pin。

## 全局约束

- collector 只枚举 current-run manifest 中的 profile，不做 workspace-wide recursive search。
- third-party JSON 在进入 parser 前已 close、size/digest、owner/path 校验。
- parser 设置 input bytes、depth、files、functions、lines、branches、string 和 safe integer 上限。
- native path 必须先解析为真实文件 identity，再转换 relative URI；外部/生成/data/.git 文件不可由 glob 重新纳入。
- LLVM region/template/MC/DC 与 GCC call/path coverage 不进入 common metric。
- canonical Coverage JSON body 不含 run/artifact ID、time、duration、absolute path 或 percentage。

---

### Task 1：Bounded parse primitives、path normalizer 与 coverage glob

**文件：**

- 创建：`apps/test-service/internal/coveragenormalize/limits.go`
- 创建：`apps/test-service/internal/coveragenormalize/limits_test.go`
- 创建：`apps/test-service/internal/coveragenormalize/path.go`
- 创建：`apps/test-service/internal/coveragenormalize/path_test.go`
- 创建：`apps/test-service/internal/coveragenormalize/path_windows_test.go`
- 创建：`apps/test-service/internal/coveragenormalize/path_unix_test.go`
- 创建：`apps/test-service/internal/coveragenormalize/glob.go`
- 创建：`apps/test-service/internal/coveragenormalize/glob_test.go`
- 创建：`apps/test-service/internal/coveragenormalize/source.go`
- 创建：`apps/test-service/internal/coveragenormalize/source_test.go`

**接口：**

```go
type SourceBinding struct {
    URI, SHA256 string
    NativePath string // internal-only
}

type Matcher interface { Include(relativeURI string) bool }
```

- [ ] **Step 1：写出 Windows/Linux path、glob、digest 失败测试**

覆盖：

- drive/UNC/case/separator/extended path；
- Linux case sensitivity/symlink；
- junction/reparse/symlink escape；
- hardlink/duplicate spelling merge；
- Unicode NFC、space、percent encoding、invalid UTF-8；
- Workspace/data/build/.git boundary；
- `* ? **` semantics 与 matcher state bound；
- include 后 exclude、mandatory exclusion precedence；
- source file replacement during digest；
- directory replacement 与 TOCTOU；
- empty/large/non-regular source；
- no native path in public binding clone。

- [ ] **Step 2：运行 normalizer primitives tests 并确认失败**

```powershell
go test ./apps/test-service/internal/coveragenormalize -run 'Path|Glob|Source|Limit' -count=1
```

- [ ] **Step 3：实现 identity-first normalization**

`NativePath` 只能在 collector/normalizer internal object 生命周期存在；写 Coverage JSON 前必须转换 URI 并清除。Digest 使用 opened file snapshot，前后复验 identity。

- [ ] **Step 4：运行全套/race**

```powershell
go test ./apps/test-service/internal/coveragenormalize -count=1
go test -race ./apps/test-service/internal/coveragenormalize -count=1
```

- [ ] **Step 5：提交 path/glob foundation**

```powershell
git add apps/test-service/internal/coveragenormalize
git commit -m "feat: normalize coverage source paths"
```

### Task 2：LLVM export JSON bounded parser

**文件：**

- 创建：`apps/test-service/internal/coverageparser/llvm/parser.go`
- 创建：`apps/test-service/internal/coverageparser/llvm/parser_test.go`
- 创建：`apps/test-service/internal/coverageparser/llvm/model.go`
- 创建：`apps/test-service/internal/coverageparser/llvm/testdata/simple.json`
- 创建：`apps/test-service/internal/coverageparser/llvm/testdata/branches.json`
- 创建：`apps/test-service/internal/coverageparser/llvm/testdata/template-macro.json`
- 创建：`apps/test-service/internal/coverageparser/llvm/testdata/windows-path.json`
- 创建：`apps/test-service/internal/coverageparser/llvm/testdata/malformed.json`
- 创建：`apps/test-service/internal/coverageparser/llvm/testdata/unsupported-major.json`

- [ ] **Step 1：写出 Golden/chunk/malformed/limit 失败测试**

覆盖：

- supported JSON major/minor/patch；
- data/object/files/functions/segments/branches/totals shape；
- line/branch/function counts；
- gap/region/template/macro 不重复计算 common line；
- Windows/Linux path 原样保留在 internal object；
- chunk size 1..N、CRLF/whitespace；
- unknown additive minor field 忽略；
- unknown major、duplicate field、invalid number、negative/float/overflow、NaN、deep nesting 拒绝；
- file/line/branch/string/input limit；
- parse error 不返回 partial object。

- [ ] **Step 2：运行 LLVM parser tests 并确认失败**

```powershell
go test ./apps/test-service/internal/coverageparser/llvm -count=1
```

- [ ] **Step 3：实现 streaming/bounded parser**

Parser 不调用 filesystem，不解析 URI，不运行 tool；只把受支持 LLVM export shape 转换为 tool-specific object，并保留 export format version。

- [ ] **Step 4：运行 parser fuzz/race**

```powershell
go test ./apps/test-service/internal/coverageparser/llvm -count=1
go test -race ./apps/test-service/internal/coverageparser/llvm -count=1
```

- [ ] **Step 5：提交 LLVM parser**

```powershell
git add apps/test-service/internal/coverageparser/llvm
git commit -m "feat: parse llvm coverage exports"
```

### Task 3：LLVM profile allocation、merge/export collector

**文件：**

- 创建：`apps/test-service/internal/coveragecollect/llvm.go`
- 创建：`apps/test-service/internal/coveragecollect/llvm_test.go`
- 创建：`apps/test-service/internal/coveragecollect/profile_manifest.go`
- 创建：`apps/test-service/internal/coveragecollect/profile_manifest_test.go`
- 创建：`apps/test-service/internal/coveragecollect/cleanup.go`
- 创建：`apps/test-service/internal/coveragecollect/cleanup_test.go`
- 修改：`apps/test-service/internal/testrun/coordinator.go`
- 修改：`apps/test-service/internal/testrun/coordinator_test.go`
- 修改：`apps/test-service/internal/build/boundary.go`
- 修改：`apps/test-service/internal/task/plan_test.go`

- [ ] **Step 1：写出 profile ownership/plan/cleanup 失败测试**

覆盖：

- `LLVM_PROFILE_FILE` 使用 owned invocation/iteration 与 `%p-%m`；
- user env 不能覆盖；
- parallel invocation path unique；
- 多个 CTest container/instrumented binary 按 stable ID 排序，全部通过 boundary 与 build manifest 校验；
- manifest 只接收 current-run regular `.profraw`；
- symlink/reparse/hardlink duplicate/unknown file 拒绝；
- merge argv stable order 与 `-sparse`；
- export 使用 pinned profdata/cov、一个 primary binary 和 stable ordered additional object set；
- tool/binary replacement before/between/after step 拒绝；
- crash/timeout missing profile → partial reason；
- normal exit missing profile → unavailable；
- cancel 不启动 merge；
- success/failure/restart cleanup only owned intermediates。

- [ ] **Step 2：运行 collector tests 并确认失败**

```powershell
go test ./apps/test-service/internal/coveragecollect ./apps/test-service/internal/testrun ./apps/test-service/internal/build ./apps/test-service/internal/task -run 'LLVM|Profile|Coverage' -count=1
```

- [ ] **Step 3：实现 Service-owned LLVM process plan**

Collector 返回固定 Task continuation steps：manifest seal → profdata merge → llvm-cov export。多个 binary 使用固定 primary/additional object argv，不按 filesystem enumeration order 决定。Export 输出先进入 bounded staging file，再交 LLVM parser；stderr diagnostic 脱敏。

- [ ] **Step 4：运行 fake/real LLVM fixture**

```powershell
go test ./apps/test-service/internal/coveragecollect ./apps/test-service/internal/coverageparser/llvm ./apps/test-service/internal/testrun -count=1
go test -race ./apps/test-service/internal/coveragecollect ./apps/test-service/internal/testrun -count=1
```

- [ ] **Step 5：提交 LLVM collector**

```powershell
git add apps/test-service/internal/coveragecollect apps/test-service/internal/coverageparser/llvm apps/test-service/internal/testrun apps/test-service/internal/build apps/test-service/internal/task
git commit -m "feat: collect llvm coverage profiles"
```

### Task 4：gcovr JSON bounded parser

**文件：**

- 创建：`apps/test-service/internal/coverageparser/gcovr/parser.go`
- 创建：`apps/test-service/internal/coverageparser/gcovr/parser_test.go`
- 创建：`apps/test-service/internal/coverageparser/gcovr/model.go`
- 创建：`apps/test-service/internal/coverageparser/gcovr/testdata/simple.json`
- 创建：`apps/test-service/internal/coverageparser/gcovr/testdata/branches.json`
- 创建：`apps/test-service/internal/coverageparser/gcovr/testdata/functions.json`
- 创建：`apps/test-service/internal/coverageparser/gcovr/testdata/unicode.json`
- 创建：`apps/test-service/internal/coverageparser/gcovr/testdata/malformed.json`
- 创建：`apps/test-service/internal/coverageparser/gcovr/testdata/unsupported-version.json`

- [ ] **Step 1：写出 gcovr 8.6 Golden/compat/limit 失败测试**

覆盖 format version、file path、line count、branch count、function summary、excluded line、uncovered line、gcovr metadata；unknown additive field 可忽略，unknown incompatible major 拒绝；negative/float/overflow/duplicate/depth/count/string/input 超限拒绝；parse failure 不返回 partial object。

- [ ] **Step 2：运行 gcovr parser tests 并确认失败**

```powershell
go test ./apps/test-service/internal/coverageparser/gcovr -count=1
```

- [ ] **Step 3：实现 streaming/bounded parser**

Parser 只接受 Phase 5B fixed runner 标记的 gcovr 8.6 JSON contract；不接受用户生成的 config/report extension。

- [ ] **Step 4：运行 parser fuzz/race**

```powershell
go test ./apps/test-service/internal/coverageparser/gcovr -count=1
go test -race ./apps/test-service/internal/coverageparser/gcovr -count=1
```

- [ ] **Step 5：提交 gcovr parser**

```powershell
git add apps/test-service/internal/coverageparser/gcovr
git commit -m "feat: parse gcovr coverage exports"
```

### Task 5：GCC `.gcda` lifecycle 与 fixed gcovr collection

**文件：**

- 创建：`apps/test-service/internal/coveragecollect/gcc.go`
- 创建：`apps/test-service/internal/coveragecollect/gcc_test.go`
- 创建：`apps/test-service/internal/coveragecollect/gcda.go`
- 创建：`apps/test-service/internal/coveragecollect/gcda_test.go`
- 修改：`apps/test-service/internal/coveragebundle/runner.go`
- 修改：`apps/test-service/internal/coveragebundle/runner_test.go`
- 修改：`apps/test-service/internal/testrun/coordinator.go`
- 修改：`apps/test-service/internal/testrun/coordinator_test.go`

- [ ] **Step 1：写出 stale cleanup/serial run/runner descriptor 失败测试**

覆盖：

- build manifest 枚举 expected `.gcno`，不做 data-root 外递归；
- run 前只删除 owned stale `.gcda`；
- GCC coverage scheduler concurrency=1；
- crash/timeout 后继续 remaining selection 并标 partial；
- exact pinned gcov path、bundle Python/runner；
- fixed descriptor field/args；
- `GCOV_PREFIX`、gcovr config、PYTHON/proxy/user env 清除；
- `.gcno/.gcda` symlink/reparse/identity mismatch 拒绝；
- normal exit with no expected data unavailable；
- cancel/restart cleanup；
- system Python/gcovr PATH never used。

- [ ] **Step 2：运行 GCC collector tests 并确认失败**

```powershell
go test ./apps/test-service/internal/coveragecollect ./apps/test-service/internal/coveragebundle ./apps/test-service/internal/testrun -run 'GCC|GCov|GCDA|Gcovr' -count=1
```

- [ ] **Step 3：实现 serial GCC collection**

Collector 在 test wave 前 seal `.gcno` manifest，运行后 seal `.gcda`，原子写 descriptor，再启动 fixed runner。Runner output close/digest/size 后交 gcovr parser。

- [ ] **Step 4：运行 fake/real GCC fixture**

```powershell
pnpm prepare:coverage-bundle
go test ./apps/test-service/internal/coveragecollect ./apps/test-service/internal/coveragebundle ./apps/test-service/internal/coverageparser/gcovr ./apps/test-service/internal/testrun -count=1
go test -race ./apps/test-service/internal/coveragecollect ./apps/test-service/internal/testrun -count=1
```

- [ ] **Step 5：提交 GCC collector**

```powershell
git add apps/test-service/internal/coveragecollect apps/test-service/internal/coveragebundle apps/test-service/internal/coverageparser/gcovr apps/test-service/internal/testrun
git commit -m "feat: collect gcc coverage profiles"
```

### Task 6：Cross-tool normalization 与 canonical Coverage JSON writer

**文件：**

- 创建：`apps/test-service/internal/coveragenormalize/normalize.go`
- 创建：`apps/test-service/internal/coveragenormalize/normalize_test.go`
- 创建：`apps/test-service/internal/coveragenormalize/summary.go`
- 创建：`apps/test-service/internal/coveragenormalize/summary_test.go`
- 创建：`apps/test-service/internal/coveragenormalize/writer.go`
- 创建：`apps/test-service/internal/coveragenormalize/writer_test.go`
- 创建：`apps/test-service/internal/coveragenormalize/testdata/common-llvm.json`
- 创建：`apps/test-service/internal/coveragenormalize/testdata/common-gcovr.json`
- 创建：`apps/test-service/internal/coveragenormalize/testdata/coverage-v1.golden.json`
- 修改：`apps/test-service/internal/coveragedomain/summary.go`
- 修改：`apps/test-service/internal/coveragedomain/summary_test.go`

- [ ] **Step 1：写出 common semantics/determinism 失败测试**

覆盖：

- LLVM/gcovr simple fixture → 相同 common line/branch/function summary；
- covered/total aggregation 与 per-line branches；
- duplicate physical file merge；
- include/exclude；
- external/generated/data file exclusion；
- source SHA-256；
- file/line stable sort；
- safe integer add overflow；
- `available|partial` completeness/reason；
- fixed JSON field order、UTF-8、LF、ending newline；
- no float/native path/time/ID/duration/secret；
- repeated input byte-identical output。

- [ ] **Step 2：运行 normalizer tests 并确认失败**

```powershell
go test ./apps/test-service/internal/coveragenormalize ./apps/test-service/internal/coveragedomain ./apps/test-service/internal/coveragemodel/... -run 'Normalize|Summary|Writer|Deterministic' -count=1
```

- [ ] **Step 3：实现 adapter-neutral normalization**

Normalizer 通过小接口读取 LLVM/GCC domain object；不使用 type assertion 访问 raw JSON。Writer 生成后再用 Coverage Schema generated validator/contract test round-trip。

- [ ] **Step 4：运行 Phase 5D 完整门禁**

```powershell
pnpm check:coverage-generated
go test ./apps/test-service/internal/coveragecollect ./apps/test-service/internal/coverageparser/... ./apps/test-service/internal/coveragenormalize ./apps/test-service/internal/coveragedomain ./apps/test-service/internal/testrun -count=1
go test -race ./apps/test-service/internal/coveragecollect ./apps/test-service/internal/coverageparser/... ./apps/test-service/internal/coveragenormalize ./apps/test-service/internal/testrun -count=1
pnpm verify
git diff --check
```

- [ ] **Step 5：提交 canonical Coverage JSON pipeline**

```powershell
git add apps/test-service/internal/coveragenormalize apps/test-service/internal/coveragedomain
git commit -m "feat: normalize coverage reports"
```

## Phase 5D 完成检查

- [ ] bounded path/glob/source binding
- [ ] LLVM JSON parser 与 collector
- [ ] gcovr JSON parser 与 GCC collector
- [ ] partial/missing profile semantics
- [ ] deterministic Coverage JSON writer
- [ ] raw profile/intermediate cleanup
- [ ] 完整 Phase 5D 门禁与独立 parser/path review
