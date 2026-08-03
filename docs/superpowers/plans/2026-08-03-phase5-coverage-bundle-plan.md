# Phase 5B：Offline Python/gcovr bundle 实施计划

> 实施时逐 Task 使用 red-green-refactor TDD；每个 Step 完成后更新 checkbox。

**目标：** 交付 manifest-bound、可离线运行、不能导入用户 package 或执行 workspace script 的 Python 3.14.6/gcovr 8.6 Windows/Linux x64 bundle。

**架构：** 下载/构建只发生在 `tools/coverage-bundle` prepare 阶段。产品运行时由 Go `coveragebundle` 读取安装后的 manifest、复验 file identity/SHA-256，并只允许固定 runner 生成 gcovr JSON。Windows 使用 CPython embedded distribution；Linux 从 CPython 3.14.6 官方 source 在固定 `manylinux_2_28` compatible build image/sysroot 中构建 product-owned runtime，运行基线为 x64 glibc 2.28，不承诺 musl。最终 bundle 不含 pip、compiler、test suite 或 download client。

**依赖：** Phase 5A Coverage domain/contract。

## 全局约束

- Python 固定为 3.14.6，gcovr 固定为 8.6；升级必须修改 manifest、lock、checksum、license 和 Golden tests。
- manifest 中每个 source/archive/output file 都有 SHA-256，禁止 `latest` URL 或 floating dependency。
- 产品运行时不得调用 PATH 中的 `python`、`python3`、`pip`、`gcovr` 或 shell。
- 产品运行时不得联网、安装 package、读取 registry/user site/current directory 或执行 workspace Python。
- Windows/Linux bundle 分开构建和验证，不把一个平台 archive 当作另一平台 fallback。
- Linux builder image 必须以 digest 固定，产物记录 build image/sysroot provenance；Hosted runner 的发行版不得隐式改变运行时 ABI。
- 本计划不执行 GCC coverage collection；只验证固定 runner 能在 owned fixture 上调用 fake/real gcov 并输出 bounded JSON。

---

### Task 1：Bundle manifest、source lock 与 license contract

**文件：**

- 创建：`tools/coverage-bundle/manifest.json`
- 创建：`tools/coverage-bundle/manifest.schema.json`
- 创建：`tools/coverage-bundle/manifest.test.mjs`
- 创建：`tools/coverage-bundle/README.md`
- 创建：`tools/coverage-bundle/licenses/README.md`
- 创建：`tools/coverage-bundle/licenses/Python-3.14.6.txt`
- 创建：`tools/coverage-bundle/licenses/gcovr-8.6.txt`
- 创建：`tools/coverage-bundle/licenses/dependencies.json`
- 修改：`package.json`

- [ ] **Step 1：写出 exact-version/checksum/license 失败测试**

覆盖：

- Python version `3.14.6`、gcovr version `8.6`；
- Windows x64 embedded artifact 与 Linux x64 source artifact；
- Linux `manylinux_2_28` builder image digest、glibc 2.28 baseline 与 unsupported musl policy；
- source URL 使用 HTTPS allowlist；
- 每个 archive/wheel/output 有 lowercase SHA-256；
- wheel lock 无 range、marker ambiguity 或 duplicate project；
- dependency license/NOTICE 完整；
- 禁止 `latest`、branch、Git URL、editable package、sdist fallback 和未固定 transitive dependency；
- manifest `additionalProperties: false`；
- platform/architecture enum closed。

- [ ] **Step 2：运行 manifest tests 并确认失败**

```powershell
node --test tools/coverage-bundle/manifest.test.mjs
```

- [ ] **Step 3：实现 manifest 与 root scripts**

新增：

```json
{
  "prepare:coverage-bundle": "node tools/coverage-bundle/prepare.mjs",
  "check:coverage-bundle": "node tools/coverage-bundle/prepare.mjs --check",
  "test:coverage-bundle": "node --test tools/coverage-bundle/*.test.mjs"
}
```

Manifest 使用 PyPI wheel published hash 和 Python 官方 release metadata；测试拒绝空值/占位 hash。

把 `test:coverage-bundle` 接入 root `test`，但不把需要下载的 `prepare:coverage-bundle` 接入默认 `verify`；默认门禁只验证 manifest、license、prepare implementation 和可离线 fixture，真实 prepare 由平台 CI 显式执行。

- [ ] **Step 4：运行 contract/license tests**

```powershell
pnpm test:coverage-bundle
git diff --check
```

- [ ] **Step 5：提交 bundle contract**

```powershell
git add package.json tools/coverage-bundle
git commit -m "build: lock coverage runtime dependencies"
```

### Task 2：Deterministic Windows/Linux bundle preparation

**文件：**

- 创建：`tools/coverage-bundle/prepare.mjs`
- 创建：`tools/coverage-bundle/prepare.test.mjs`
- 创建：`tools/coverage-bundle/build-linux.sh`
- 创建：`tools/coverage-bundle/layout.mjs`
- 创建：`tools/coverage-bundle/runner/__main__.py`
- 创建：`tools/coverage-bundle/runner/contract.py`
- 创建：`tools/coverage-bundle/runner/NOTICE.txt`
- 修改：`.gitignore`

**输出布局：**

```text
.superpowers/runtime/coverage-bundle/<platform>-<arch>/
├─ manifest.resolved.json
├─ python/
├─ app/gcovr-runner.pyz
├─ licenses/
└─ READY
```

- [ ] **Step 1：写出 cache/layout/tamper/partial download 失败测试**

覆盖：

- cold prepare 只取 manifest allowlist URL；
- archive 在 extraction 前校验；
- path traversal、symlink、duplicate archive entry 拒绝；
- temp directory 构建，READY 最后原子发布；
- interrupted prepare 不留下可消费 bundle；
- cache hit 仍复验 resolved manifest；
- Windows `_pth` 不含 `import site`；
- final layout 不含 pip、ensurepip、test、idle、tk、header、static library 或 build tool；
- Linux build 使用固定 configure option 与 `SOURCE_DATE_EPOCH`；
- 输出 file list/digest deterministic。

- [ ] **Step 2：运行 prepare tests 并确认失败**

```powershell
pnpm test:coverage-bundle
```

- [ ] **Step 3：实现 platform preparation**

Windows 解包官方 embedded ZIP，再把 exact wheel lock 安装到 bundle-private application archive。Linux 在 digest-pinned `manylinux_2_28` compatible build image/sysroot 中从官方 source 构建最小 CPython，并用相同 wheel lock 生成 application archive；Hosted Ubuntu 只负责执行 builder，不能成为隐式 ABI 输入。Prepare 禁止调用项目外 `pip install`；dependency resolution 已由 manifest 固定。

Runner 接受一个 Service-owned JSON descriptor path；descriptor schema 只包含 verified root/object/gcov/output path。include/exclude 由 Go Normalizer 在 workspace-relative URI 上执行，不传给 gcovr。Runner 把 descriptor 转换成固定 gcovr API/args，不转发 unknown field。

- [ ] **Step 4：运行当前平台 real prepare/check**

```powershell
pnpm prepare:coverage-bundle
pnpm check:coverage-bundle
pnpm test:coverage-bundle
git diff --check
```

- [ ] **Step 5：提交 deterministic preparation**

```powershell
git add .gitignore tools/coverage-bundle
git commit -m "build: prepare offline coverage runtime"
```

### Task 3：Go bundle resolver、manifest verification 与 pin lifecycle

**文件：**

- 创建：`apps/test-service/internal/coveragebundle/manifest.go`
- 创建：`apps/test-service/internal/coveragebundle/manifest_test.go`
- 创建：`apps/test-service/internal/coveragebundle/resolver.go`
- 创建：`apps/test-service/internal/coveragebundle/resolver_test.go`
- 创建：`apps/test-service/internal/coveragebundle/pin.go`
- 创建：`apps/test-service/internal/coveragebundle/pin_windows.go`
- 创建：`apps/test-service/internal/coveragebundle/pin_unix.go`
- 创建：`apps/test-service/internal/coveragebundle/pin_test.go`
- 修改：`apps/test-service/internal/runtime/data_dir.go`
- 修改：`apps/test-service/internal/runtime/data_dir_test.go`

**接口：**

```go
type Installation struct {
    Root, Python, Runner string
    PythonVersion, GcovrVersion string
    ManifestSHA256 string
}

type Pin interface {
    Installation() Installation
    Verify() error
    Close() error
}
```

- [ ] **Step 1：写出 resolver/tamper/path identity 失败测试**

覆盖 missing READY、unknown schema/platform、wrong arch、duplicate file、digest mismatch、file replacement、directory replacement、junction/symlink escape、case alias、TOCTOU、double close 和 immutable clone。

- [ ] **Step 2：运行 Go tests 并确认失败**

```powershell
go test ./apps/test-service/internal/coveragebundle ./apps/test-service/internal/runtime -run 'Bundle|Pin|CoverageData' -count=1
```

- [ ] **Step 3：实现 manifest-bound resolver/pin**

Resolver 只接收产品安装 root；不搜索 PATH、registry、home 或 workspace。Pin 保持 Python executable、runner archive、stdlib archive 和 dependency archive 的 opened identity，所有执行前后复验 digest/identity。

- [ ] **Step 4：运行全套/race**

```powershell
go test ./apps/test-service/internal/coveragebundle ./apps/test-service/internal/runtime -count=1
go test -race ./apps/test-service/internal/coveragebundle -count=1
```

- [ ] **Step 5：提交 Go bundle resolver**

```powershell
git add apps/test-service/internal/coveragebundle apps/test-service/internal/runtime/data_dir.go apps/test-service/internal/runtime/data_dir_test.go
git commit -m "feat: verify offline coverage bundles"
```

### Task 4：Fixed isolated gcovr runner 与 ExecutionBoundary

**文件：**

- 创建：`apps/test-service/internal/coveragebundle/runner.go`
- 创建：`apps/test-service/internal/coveragebundle/runner_test.go`
- 创建：`apps/test-service/internal/coveragebundle/descriptor.go`
- 创建：`apps/test-service/internal/coveragebundle/descriptor_test.go`
- 修改：`apps/test-service/internal/build/boundary.go`
- 修改：`apps/test-service/internal/build/planner_test.go`
- 修改：`apps/test-service/internal/task/plan_test.go`

**执行 contract：**

```text
<pinned-python> -I -S <pinned-runner.pyz> <owned-descriptor.json>
```

- [ ] **Step 1：写出 args/env/import/injection 失败测试**

覆盖：

- exact Python/runner/descriptor args；
- `PYTHONPATH`、`PYTHONHOME`、`PYTHONSTARTUP`、user site、proxy、locale injection 清除；
- no current-directory import；
- descriptor unknown field、native path escape、workspace script/module/plugin/config 拒绝；
- runner 只能写 owned output；
- boundary 未 pin/tampered/released 时拒绝；
- batch continuation 不能替换 Python/runner。

- [ ] **Step 2：运行 runner/boundary tests 并确认失败**

```powershell
go test ./apps/test-service/internal/coveragebundle ./apps/test-service/internal/build ./apps/test-service/internal/task -run 'Runner|Python|CoverageBundle|Boundary' -count=1
```

- [ ] **Step 3：实现 fixed runner plan**

Go 只返回内部 `task.ProcessSpec`；Protocol/model 不出现 Python args。Descriptor 先原子写入 data root、close、digest/pin，再启动。Runner stdout/stderr 使用现有 bounded sink。

- [ ] **Step 4：运行 real offline smoke**

```powershell
pnpm prepare:coverage-bundle
go test ./apps/test-service/internal/coveragebundle -run 'Real|Offline|Isolated' -count=1
go test -race ./apps/test-service/internal/coveragebundle ./apps/test-service/internal/build ./apps/test-service/internal/task -count=1
```

- [ ] **Step 5：提交 isolated runner**

```powershell
git add apps/test-service/internal/coveragebundle apps/test-service/internal/build apps/test-service/internal/task
git commit -m "feat: isolate bundled gcovr execution"
```

### Task 5：No-network、license 与跨平台 CI 门禁

**文件：**

- 创建：`tools/service-probe/src/coverage-bundle.test.ts`
- 创建：`tools/service-probe/src/coverage-bundle.ts`
- 修改：`tools/service-probe/package.json`
- 修改：`.github/workflows/foundation.yml`
- 修改：`tools/coverage-bundle/README.md`

- [ ] **Step 1：写出 runtime no-network 与 report 失败测试**

Probe 在清空 network/proxy 环境并阻断 DNS/socket 的测试 harness 中运行 fixed runner，验证不会访问网络、home、registry 或 system Python。CI 产出 `coverage-bundle-report.json`，列出 manifest digest、version、license 和 smoke outcome，不包含安装 path。

- [ ] **Step 2：运行 Probe tests 并确认失败**

```powershell
pnpm --filter @unit-test-ide/service-probe test
```

- [ ] **Step 3：接入 Windows/Linux CI**

两个 Hosted job 分别 prepare/check 本平台 bundle，缓存 key 必须包含 manifest digest；cache restore 后仍执行 full verify。下载只发生在 prepare step，service-probe runtime step 开启 no-network guard。

- [ ] **Step 4：运行 Phase 5B 完整门禁**

```powershell
pnpm test:coverage-bundle
pnpm prepare:coverage-bundle
pnpm check:coverage-bundle
pnpm --filter @unit-test-ide/service-probe test
go test ./apps/test-service/internal/coveragebundle ./apps/test-service/internal/build ./apps/test-service/internal/task -count=1
go test -race ./apps/test-service/internal/coveragebundle ./apps/test-service/internal/build -count=1
pnpm verify
git diff --check
```

- [ ] **Step 5：提交 bundle CI gate**

```powershell
git add tools/service-probe .github/workflows/foundation.yml tools/coverage-bundle/README.md
git commit -m "test: verify offline coverage bundles"
```

## Phase 5B 完成检查

- [ ] Python 3.14.6/gcovr 8.6 exact lock
- [ ] Windows/Linux deterministic bundle preparation
- [ ] manifest/digest/license/READY contract
- [ ] Go resolver/pin lifecycle
- [ ] fixed isolated runner 与 ExecutionBoundary
- [ ] runtime no-network 与 Hosted CI gate
- [ ] 完整 Phase 5B 门禁与独立 security review
