# Windows Protected Tool Digest Reuse Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在一次 Windows 当前覆盖率索引验证中减少已保护工具文件的重复完整哈希，不减少任何来源、身份或权限校验。

**Architecture:** 服务内部的查询作用域持有独立的禁止写入／删除句柄，首次从消费者独立句柄流式计算 SHA-256，后续仅在保护仍有效且路径／对象一致时复用。CMake/CTest 与 Windows 工具链显式接入，其他文件继续完整哈希。Runtime 每次 ReadCurrentCoverageIndex 创建新作用域，前后两次发现与所有来源校验照常执行，关闭失败不得返回有效索引。

**Tech Stack:** 现有 Go1.26、golang.org/x/sys/windows、os.File/ReadAt、SHA-256、context、sync；不新增依赖。当前本地 Go 为1.26.6，使用缓存和离线模块配置。

**Status:** 2026-10-09 用户“继续”作为书面设计审阅通过、进入计划阶段；本文件尚未执行。第二版扩展保持暂停。选择执行方式后才进入实现，native／外部操作另需明确授权。

## Global Constraints

以下英文约束逐句复制自批准的设计 `docs/superpowers/specs/2026-10-09-windows-protected-tool-digest-reuse-design.md`；各任务均受约束。

- Enable the optimization only inside one actual Runtime.ReadCurrentCoverageIndex invocation on Windows.
- A later index attestation, even within the same higher-level start request, creates a new scope and recomputes first-use hashes.
- Non-Windows and non-scoped consumers retain their current full-hash behavior.
- Existing snapshot consumers keep independent read handles and Close ownership.
- User source, editable test files, workspace configuration, preset/declaration inputs, database files, reports, license/manifest/state files, environment scripts, and the separately authenticated selected SDK library are outside digest reuse.
- First use must acquire a direct regular-file Windows read handle that denies write/delete sharing before calculating the complete SHA-256.
- Recheck path/handle identities at each reuse.
- Retain at most 128 distinct tool-file entries.
- Store metadata/digests only; stream file hashes rather than buffering complete executables.
- Reuse eligibility is bounded to 512MiB per tool; all existing stricter consumer limits still apply.
- Noneligible files, capacity exhaustion or inability to establish optional protection use the existing complete validation path.
- Once evidence is bound, detected file/identity drift, invalidated protection, digest mismatch, cancellation or internal guard errors must fail closed through the existing error mapping rather than become harmless cache misses.
- Do not hold a scope mutex across external probe execution.
- Cleanup failures cannot be disguised as successful current-index attestation.
- No removal of full discoveries or fresh index reads; no start-stage deduplication; no process/request/poll/diagnostic deadline changes.
- Any real latency comparison or native attempt requires separately bounded authorization and complete cleanup/evidence handling.
- No push, PR update, master synchronization, merge, force operation, Release, signing, dependency download or installation is authorized by this design.

## Workspace, ownership and verification policy

Actual linked worktree: `C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest`; branch `codex/phase10-production-generation`; planning BASE `90cbdfaea88760d0baae94a452d6ac09f79876b1`. Do not implement in the other checkout `C:/codex_project/unitTest`, check out master, create another checkout unnecessarily, or reset existing work. Recheck HEAD/index/ownership at execution start.

冻结既有 W4：四个 Windows Unity/CppUTest CMakeLists.txt、`tools/service-probe/src/native-test-generation.ts`、其 `.test.ts`、`test-generation-e2e.test.ts`、`apps/code-oss-extension/test/coverage-first-report-native.test.mjs`，以及既有 evidence。第二版 Task2 的 taskstore/sqlite.go、migrations.go、022 SQL、case_definitions、binding_status、import_commit 及新 go.work.sum 也保留、不编辑、不暂存。仅明确列出的任务文件可进入本计划提交；禁止 git add .。

写测试→实际可编译的行为 RED→实现→同一测试 GREEN→窄提交→独立审查。新 API 缺失导致编译失败只记录为接口缺失，不算行为 RED；可在测试写好后补不工作的声明接缝，再证明行为失败。统计选中测试、子测试、SKIP、退出码和实际输出；所有新 Windows 目标测试不得跳过。测试中的真实文件／句柄断言不是 native C/C++ 验收。

每个测试命令从 `apps/test-service` 执行，Go 可执行文件 `D:/Program Files/Go/bin/go.exe`，下文短写 go 时也调用该绝对路径；Git 命令从 worktree 根目录执行。进程内设置 `$env:GOPROXY='off'; $env:GOSUMDB='off'`，不全局更改环境。只运行本任务新测试与指定旧安全回归；末任务再运行一次合并定向／race 门禁。不默认整仓 suite、hosted、打包、原 native 或高 count 循环。race 缺工具时只读取既有编译器配置，禁止自动下载安装；记录未执行、保留门禁，不能当作通过。如果冻结的第二版局部代码导致编译失败，记录明确归因并停止受影响任务，不借机修复、删除或提交第二版代码。

| Task | Owned production files | Independently reviewable deliverable |
| --- | --- | --- |
| 1 | new internal/toolhash/scope.go, scope_windows.go, scope_nonwindows.go | bounded guard/evidence lifecycle, no consumers enabled |
| 2 | cmake/file_snapshot.go, resolver.go; new cmake/tool_digest_reuse.go | only CMake/CTest snapshots reuse; ordinary snapshots unchanged |
| 3 | toolchain/gnu.go; new toolchain/tool_digest_reuse.go | Windows binary snapshots opt in; scripts/SDK/Linux remain full hash |
| 4 | runtime/coverage_detail_backend.go; new runtime/coverage_tool_digest_scope.go | actual current-index entry creates/releases one scope per call, with original authority gates |

New tests below live alongside their owners. No public IPC/protocol/configuration/DTO change. The internal package's exported Go names are package boundaries, not client-controllable capabilities.

## Task 1: bounded Windows guard and nontransferable digest evidence

**Files:**
- Create `apps/test-service/internal/toolhash/scope.go`, `scope_windows.go`, `scope_nonwindows.go`.
- Create `scope_windows_test.go`, `scope_nonwindows_test.go`; build tags respectively windows / !windows.

**Interfaces:**

```go
const MaxEntries = 128
const MaxEligibleBytes int64 = 512 * 1024 * 1024
type Scope struct {
    mu sync.Mutex
    entries map[string]*entry
    parent, work context.Context
    stop context.CancelFunc
    closed bool
    failed error
    closeOnce sync.Once
    closeErr error
    stats Stats
    ops scopeOps
    maxEntries int
    maxBytes int64
}
type Evidence struct { scope *Scope; entry *entry }
type Result struct { SHA256 string; Size int64; Evidence *Evidence; Reused bool }
type Stats struct { FullHashCalls, Reuses, Retained int; HashedBytes int64 }
func Begin(context.Context) (context.Context, *Scope, error)
func Digest(context.Context, string, *os.File, int64) (Result, bool, error)
func (*Evidence) Verify(context.Context, string, *os.File, int64) (string, error)
func (*Scope) Stats() Stats
func (*Scope) Err() error
func (*Scope) Close() error
```

The boolean from Digest means guarded evidence is usable. false,nil means optional reuse unavailable: caller performs its original full hashing. Any nonnil error is fatal, never fallback. Begin always creates a fresh scope on Windows even if the incoming context carries another scope; a private context-key type binds it. !windows Begin returns unchanged ctx,nil,nil and Digest returns false,nil after cancellation checks. Nil/cancelled ctx is an error. Close is nil-safe and idempotent; its result includes sticky failures and real guard-close errors. Err checks external-parent cancellation and sticky failures; normal Close's internal cancellation must not fabricate an external cancellation error.

**Internal types and operation contract:**

```go
type identity struct { info os.FileInfo; windowsID string }
type entry struct {
    mu sync.Mutex
    path string
    guard *os.File
    original identity
    size int64
    digest string
    evidence *Evidence
    ready chan struct{}
    finishOnce sync.Once
    complete bool
}
type scopeOps struct {
    openGuard func(string) (*os.File,error)
    checkTriple func(string,*os.File,*os.File) (identity,error)
    closeGuard func(*os.File) error
    beforeRead func(context.Context,*os.File,int64) error
}
func newScope(ctx context.Context,ops scopeOps,maxEntries int,maxBytes int64) *Scope
```

Production ops use real Windows APIs; beforeRead is nil. Private newScope supports smaller boundary values and failure injection in this package's tests only; Begin fixes production limits. beforeRead may observe/gate actual reads, but cannot supply bytes, hashes or invented identity. No exported caller-supplied hasher/opener/options.

- Reserve entries, including in-flight ones, under a short map mutex. Key by cleaned validated canonical path without merging hard-link names or lowercasing distinct case-sensitive entries. Duplicate first users wait on entry completion or scope/context cancellation; only one hashes. Waits select caller cancellation, scope work cancellation and ready; never wait while holding a mutex.
- Open and register the guard under admission serialization before unlocking for hashing; Close cannot miss a guard opened after its enumeration. Do not acquire entry.mu while holding Scope.mu or perform a Scope.mu acquisition while holding entry.mu. Close marks closed/cancels and snapshots entries under Scope.mu, then releases it before entry identity/close locks. No lock inversion or untracked late guard is permitted.
- Scope.mu owns membership, failure/closed state, entry completion/digest/evidence publication and stats; entry.mu owns only guard access/identity/close. After a short identity check, release entry.mu, acquire Scope.mu, and require live/nonfailed scope before publishing the immutable result and closing ready. Close sets closed under that same publication lock before guard release, so a finishing worker cannot publish after closure. Reads of published metadata also use Scope.mu; no dual-lock operation is needed.
- Open failure when optional protection is unavailable releases reservation and returns optional miss. First validate path/consumer identity independently, so disappearance, cancellation, forbidden alias or mismatched consumer cannot be relabeled as a sharing miss. A path/consumer/guard identity mismatch poisons the scope, including failures found before first publication. Complete the ready channel exactly once on miss, error, success or close.
- `checkTriple` re-resolves the direct tool path using the existing direct-file/canonical-path restrictions, rejects reparse/forbidden aliases, compares the canonical path and os.SameFile across fresh path/consumer/guard stats, and checks nonzero Windows object identity and unchanged size. CMake/toolchain callers keep their existing publication/root authority checks; the scope neither substitutes for them nor incorrectly requires installed tools to lie inside the source workspace. Consumer maximum0 means its original unlimited contract; negative is invalid, positive limits are never relaxed. Optional512MiB excess is a miss, not a new input-rejection rule.
- Hash **consumer.ReadAt**, not the retained guard. Consumer keeps its own lifetime/offset; guard is used only for protection and short identity checks. Use the lesser of positive consumer maximum and maxBytes; maximum0 uses maxBytes for eligible hashing only, not as a new fallback acceptance rule. After hashing, recheck identity, exact byte count, cancellation and live scope before publishing evidence. First-use errors close/remove their guard and never publish partial digest; no worker publishes after closed was set.
- Close atomically closes admission and cancels internal work. It may close guards while independent consumer hashing unwinds, because a closed scope cannot publish/use evidence. Coordinate the short guard-identity critical section with close; never raw-close a handle concurrently with its identity call. Do not wait for an entire hash, external probe, or discovery goroutine; no WaitGroup/global lock held across hashing. Consumers remain responsible for their separate handles.
- Bound-evidence Verify rechecks the above identity/size/liveness under short entry protection, returns the stored digest without reading bytes, and never changes evidence authority into a benign miss. Any sticky failure makes Close fail even if a best-effort discovery branch swallowed an individual tool error.

- [ ] **Step 1: add actual first-use/reuse and write/delete tests.** Include this complete test body in package toolhash, Windows-tagged; imports context,os,path/filepath,testing,crypto/sha256,encoding/hex as used.

```go
func TestToolHashScopeReusesProtectedFile(t *testing.T) {
    path := filepath.Join(t.TempDir(), "tool.exe")
    data := []byte("verified tool bytes")
    if err := os.WriteFile(path, data, 0600); err != nil { t.Fatal(err) }
    consumer, err := os.Open(path); if err != nil { t.Fatal(err) }
    defer consumer.Close()
    ctx, s, err := Begin(context.Background()); if err != nil || s == nil { t.Fatalf("begin: %v",err) }
    defer s.Close()
    first, ok, err := Digest(ctx,path,consumer,1024)
    if err != nil || !ok || first.Evidence == nil || first.Reused { t.Fatalf("first: %+v %v %v",first,ok,err) }
    sum := sha256.Sum256(data)
    if first.SHA256 != hex.EncodeToString(sum[:]) { t.Fatal("not complete file hash") }
    for i:=0;i<3;i++ {
        next, ok, err := Digest(ctx,path,consumer,1024)
        if err != nil || !ok || !next.Reused || next.Evidence != first.Evidence { t.Fatalf("reuse: %+v %v",next,err) }
        got, err := first.Evidence.Verify(ctx,path,consumer,1024)
        if err != nil || got != first.SHA256 { t.Fatal("verify lost binding",err) }
    }
    if got:=s.Stats(); got.FullHashCalls!=1 || got.HashedBytes!=int64(len(data)) || got.Retained!=1 { t.Fatalf("counters: %+v",got) }
    if err:=os.WriteFile(path,[]byte("changed"),0600); err==nil { t.Fatal("write allowed while guarded") }
    if err:=os.Remove(path); err==nil { t.Fatal("delete allowed while guarded") }
    if err:=s.Close(); err!=nil { t.Fatal(err) }
    if err:=os.WriteFile(path,[]byte("changed"),0600); err!=nil { t.Fatal("guard not released",err) }
    if _,err:=first.Evidence.Verify(ctx,path,consumer,1024); err==nil { t.Fatal("closed evidence reused") }
}
```

Add `TestToolHashNewScopeRehashesChangedFile`: close first scope, edit bytes, Begin again using previous scoped ctx's uncancelled parent, reopen consumer, require a different actual SHA and FullHashCalls1; also Begin twice under the same live parent to prove distinct scopes. Do not close the parent context as part of scope cleanup.

- [ ] **Step 2: behavioral RED.** Run `go test ./internal/toolhash -run 'TestToolHashScope|TestToolHashNewScope' -count=1 -v`; after declarations compile, optional-miss/no-guard seam must fail protection/reuse assertions rather than count compile errors.
- [ ] **Step 3: implement guard open and streaming first hash.** Windows open uses existing x/sys dependency; this is the required open mode, not a grant to accept arbitrary input paths:

```go
func openGuard(path string) (*os.File,error) {
    name,err:=windows.UTF16PtrFromString(path); if err!=nil { return nil,err }
    h,err:=windows.CreateFile(name,windows.GENERIC_READ,windows.FILE_SHARE_READ,nil,
        windows.OPEN_EXISTING,windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_SEQUENTIAL_SCAN,0)
    if err!=nil { return nil,err }
    f:=os.NewFile(uintptr(h),path)
    if f==nil { _=windows.CloseHandle(h); return nil,errors.New("invalid guard handle") }
    return f,nil
}
func hashConsumer(ctx context.Context, f *os.File, maximum int64, observe func(context.Context,*os.File,int64)error) (string,int64,error) {
    h:=sha256.New(); r:=io.NewSectionReader(f,0,maximum+1)
    buf:=make([]byte,64*1024); var total int64
    for {
        if err:=ctx.Err(); err!=nil { return "",total,err }
        if observe!=nil { if err:=observe(ctx,f,total); err!=nil { return "",total,err } }
        if err:=ctx.Err(); err!=nil { return "",total,err }
        n,err:=r.Read(buf); total+=int64(n)
        if total>maximum { return "",total,errors.New("tool exceeds hash limit") }
        if e:=ctx.Err(); e!=nil { return "",total,e }
        if n>0 { _,_=h.Write(buf[:n]) }
        if errors.Is(err,io.EOF) { return hex.EncodeToString(h.Sum(nil)),total,nil }
        if err!=nil { return "",total,err }
        if n==0 { return "",total,io.ErrNoProgress }
    }
}
```

Call hashConsumer with the scope's work context combined with caller cancellation and private ops.beforeRead. Its maximum is a positive bounded value, at most MaxEligibleBytes. Implement the reserved-entry protocol and all declared methods using the operation contract above. GetFileInformationByHandle must run only with a live owned guard under its identity/close lock; use consumer.Stat rather than unsynchronized consumer.Fd for comparisons. Guard attributes must reject directories/reparse points and zero file indexes. SHA error strings carry no caller secrets. Stats snapshot under lock; no process-global counters/cache.

- [ ] **Step 4: add remaining failure/concurrency tests before fixing those paths.** Complete named tests:

| Test | Real assertion / bounded test control |
| --- | --- |
| TestToolHashOwnerPathAndIdentityDrift | use another real file as consumer for an existing path; require error, sticky Err and failing Close; no digest fallback |
| TestToolHashDirectAliasRejected | direct-file checks reject forbidden final alias; file fixture uses actual reparse/symlink only if existing unprivileged helper works; otherwise operation-level path-drift test remains mandatory, report unavailable OS alias setup explicitly |
| TestToolHashOptionalMissAndCapacity | private limits2 entries/8 bytes; actual small files; third/larger gives false,nil, preserves original consumer hashing and count cap; injected protection-open failure leaves zero guards |
| TestToolHashConsumerLimitRemainsStrict | acquire under limit1024 then verify with limit1; reject despite hit; limit0 retains original unlimited semantics |
| TestToolHashCancelledAndHashFailure | cancel during observed consumer read; never publish; guard released; consumer closed/error produces actual read failure |
| TestToolHashConcurrentDuplicate | two independent real consumers, start barrier; one FullHashCalls, identical SHA, no shared-offset changes, exactly one retained entry |
| TestToolHashCloseDuringHash | gate a private pre-read observation channel; Close completes while hash worker remains gated; reopen file for write; releasing worker cannot publish, no retained guard |
| TestToolHashCloseIdentityAndError | gated short identity op excludes raw close; injected close op performs actual close then returns sentinel; repeated Close returns same failure, no second raw close |
| TestToolHashNonWindowsNoReuse | !windows file: Begin unchanged ctx,nil scope; Digest false,nil; no guard creation or evidence |

Private observation hooks may count/gate real operations, never replace SHA or synthesize filesystem identity. Test goroutines use channel handshakes, joins and an explicit5s test-only watchdog that fails and releases gates in cleanup; no sleeps/high-count loops. If an OS alias setup cannot run without new privilege, do not add t.Skip or claim that case passed; keep its integration gate open.

- [ ] **Step 5: RED/GREEN targeted boundary loop.** Run `go test ./internal/toolhash -run TestToolHash -count=1 -v`. Fix only failing declared paths. Check all guards can be released on success/error/cancel, and Close never waits for full hashing or an external probe. Guard admission/open and short identity operations are coordinated filesystem operations, not a claim that cleanup has zero kernel-IO latency. Race gate is consolidated in Task4.
- [ ] **Step 6: narrow commit.** Stage only five new scope/test files, `git diff --cached --check`; commit `perf: add scoped protected Windows tool digests`. Record BASE→HEAD and exact results, then independent task review. Do not enable runtime yet.

## Task 2: only CMake and paired CTest reuse

**Files:**
- Modify `apps/test-service/internal/cmake/file_snapshot.go` (capture/Verify only), `resolver.go` (standalone pair and exact two bundle executable entries).
- Create `apps/test-service/internal/cmake/tool_digest_reuse.go`, `tool_digest_reuse_windows_test.go`.
- Existing cmake/manifest.go, presets, launch inputs, platform openers and publication policies are not modified.

**Interfaces:** consumes Task1 Begin/Digest/Evidence.Verify. Produces private `captureToolFileSnapshot(ctx context.Context,path string,maximum int64)(*fileSnapshot,error)` and `verifyStandaloneExecutableWithContext(ctx context.Context,path string)(string,*fileSnapshot,error)`. Keep existing verifyStandaloneExecutable(path) wrapper for non-scoped/tests. Extend fileSnapshot with `toolEvidence *toolhash.Evidence`, `toolContext context.Context`, `maximum int64`, `onFullHash func()` (private counter hook only). Private `captureSnapshot(ctx context.Context,path string,maximum int64,eligible bool,onFullHash func())(*fileSnapshot,error)` preserves the original lstat/open/size/osIdentity/failure cleanup sequence. Existing captureFileSnapshot calls it with Background,false,nil; captureToolFileSnapshot uses supplied ctx,true,nil. No duplication of the original opening block or opt-in for ordinary inputs.

`onFullHash` is an optional per-snapshot observation, not a caller-supplied digest: fullDigest() invokes it then actual sha256OpenFile. Private test capture variant accepts the hook before initial hashing. Production nil-hook defaults cannot bypass hashing. Verify still runs direct path checks before/after, same-file/OS-identity checks and original sizes. Only its byte-hash selection changes.

- [ ] **Step 1: write focused tests using actual files and existing resolver helpers.** In the new Windows file, imports include context,os,path/filepath,testing and toolhash. Use this direct snapshot test body:

```go
func TestCMakeToolDigestScopeReusesOnlyToolBytes(t *testing.T) {
    p:=filepath.Join(t.TempDir(),"cmake.exe")
    if err:=os.WriteFile(p,[]byte("cmake fixture"),0600); err!=nil { t.Fatal(err) }
    ctx,s,err:=toolhash.Begin(context.Background()); if err!=nil { t.Fatal(err) }; defer s.Close()
    var full int
    for i:=0;i<2;i++ {
        snap,err:=captureToolFileSnapshotWithHook(ctx,p,0,func(){full++})
        if err!=nil { t.Fatal(err) }
        for j:=0;j<3;j++ { if err:=snap.Verify(); err!=nil { t.Fatal(err) } }
        if err:=snap.Close(); err!=nil { t.Fatal(err) }
    }
    if full!=0 || s.Stats().FullHashCalls!=1 { t.Fatalf("legacy=%d scope=%+v",full,s.Stats()) }
    ordinary,err:=captureFileSnapshot(p,0); if err!=nil { t.Fatal(err) }; defer ordinary.Close()
    if ordinary.toolEvidence!=nil { t.Fatal("ordinary input opted into cache") }
    ordinary.onFullHash=func(){full++}
    if err:=ordinary.Verify(); err!=nil || full!=1 { t.Fatal("ordinary verification skipped",err) }
}
```

`captureToolFileSnapshotWithHook(ctx context.Context,path string,maximum int64,onFullHash func())(*fileSnapshot,error)` is private and defined in tool_digest_reuse.go; it calls captureSnapshot(ctx,path,maximum,true,onFullHash), attaching observation only to actual full hash fallback. `(*fileSnapshot).fullDigest()(string,error)` invokes onFullHash if nonnil, then returns actual sha256OpenFile(snapshot.file). Add TestCMakeToolDigestResolverStillProbesBothQueries using existing currentExecutable(t), versionRunner, ResolverConfig: Resolve twice under the same scoped ctx; CMake/CTest hashes once each but runner.specs has4 real method invocations (two version probes per Resolve) with original args/time/output bounds. This fixture runner does not execute CMake and is not native evidence.

Add TestCMakeToolDigestBundleExcludesMetadata: use existing createBundle(t,"win32-x64")/resolveFixtureBundle fixture helpers; exact Archive.Executable/CTestExecutable may carry evidence; license/manifest/state remain ordinary, execute full Verify and cannot contribute scoped hits. Add TestCMakeToolDigestClosedScopeFails, TestCMakeToolDigestFallbackAndOriginalLimits and TestCMakeToolDigestPathDriftStillFails. Capacity exhaustion uses128 small guarded entries, not512MiB test files; optional size fallback is proved in Task1 with smaller private limits and checked in this integration's branch.

- [ ] **Step 2: behavioral RED.** Run `go test ./internal/cmake -run TestCMakeToolDigest -count=1 -v`; old full-hash path/no evidence must fail reuse counters without relying on compile errors.
- [ ] **Step 3: implement digest selection and explicit resolver opt-in.** Required selection in capture:

```go
snapshot:=&fileSnapshot{path:path,file:file,info:info,osIdentity:osIdentity,
    maximum:maximum,onFullHash:onFullHash}
var r toolhash.Result
protected:=false
if eligible { r,protected,err=toolhash.Digest(ctx,path,file,maximum) }
if err!=nil { return fail(err) }
if protected {
    snapshot.digest=r.SHA256
    snapshot.toolEvidence=r.Evidence
    snapshot.toolContext=ctx
} else {
    snapshot.digest,err=snapshot.fullDigest()
    if err!=nil { return fail(err) }
}
```

Required selection within Verify, after the existing path/handle/OS-identity checks and before its existing post-path check:

```go
var digest string
if snapshot.toolEvidence!=nil {
    digest,err=snapshot.toolEvidence.Verify(snapshot.toolContext,snapshot.path,snapshot.file,snapshot.maximum)
} else {
    digest,err=snapshot.fullDigest()
}
if err!=nil { return err }
if digest!=snapshot.digest { return fmt.Errorf("file content changed") }
```

Initialize snapshot.maximum from the original capture maximum, not a new constant. For tool snapshots, retain original executable-mode checks before consumer use. Resolve standalone pair calls the context-aware helper; old wrapper invokes it with Background (no scope). In resolveBundle's InstalledFiles loop, choose captureToolFileSnapshot **only** when relative==archive.Executable or relative==archive.CTestExecutable; all other entries use original captureFileSnapshot. Preserve expected manifest SHA comparisons, installed identities/sorting, root checks and every verify/probe call. UnityRunnerGenerator, license/state/manifest and launch/preset snapshots never opt in.
- [ ] **Step 4: GREEN plus selected legacy checks.** Run `go test ./internal/cmake -run 'TestCMakeToolDigest|TestResolverProbesJSONVersionWithFixedBounds|TestResolverRejectsMissingPairedCTest|TestResolverDoesNotSearchPath|TestResolverRejectsSelfConsistentBundleOutsideImmutableProductionPolicy' -count=1 -v`. Verify no chosen skip; inspect unchanged original generic hashing/publication code. Do not run native executable probes or full bundle suite.
- [ ] **Step 5: narrow commit.** Only the four owned files; commit `perf: reuse guarded CMake tool hashes per attestation`, independent review of BASE→HEAD. Runtime remains unenabled.

## Task 3: Windows binary toolchain snapshots, never scripts/SDK/Linux

**Files:**
- Modify `apps/test-service/internal/toolchain/gnu.go`: executableSnapshot fields and open/Verify digest selection only.
- Create `apps/test-service/internal/toolchain/tool_digest_reuse.go`, `tool_digest_reuse_windows_test.go`.
- Keep discover_windows.go, msvc_windows.go, clangcl_windows.go, non-Windows discovery and selected SDK-library implementation unchanged; audit their callers read-only.

**Interfaces:** consumes `toolhash.Digest(context.Context,string,*os.File,int64)(toolhash.Result,bool,error)`, `(*toolhash.Evidence).Verify(context.Context,string,*os.File,int64)(string,error)`, and `toolhash.Begin(context.Context)(context.Context,*toolhash.Scope,error)` for tests. Private `guardedExecutableDigest(ctx context.Context,path string,file *os.File,maximum int64)(toolhash.Result,bool,error)` returns optional miss unless GOOSwindows and .exe binary at a path already validated by the existing snapshot opener. `.bat`, `.cmd`, `.ps1`, `.lib`, `.dll` never opt in. Keep existing openExecutableSnapshot/WithLimit/WithLimitAndHook signatures; add private `openExecutableSnapshotWithDigestObserver(ctx context.Context,path string,maximum int64,beforeDigest func(),onFullHash func())(*executableSnapshot,error)` to attach actual-hash observation without changing the existing beforeDigest hook's once-before-initial-hash semantics. Fields toolEvidence and onFullHash are private; consumers retain Close ownership.

- [ ] **Step 1: write count/closure tests.** Implement complete `TestToolchainToolDigestScopedBinary` with a temporary actual file cl.exe, Begin, two independent snapshots through observer helper and three Verify calls each. Require FullHashCalls1, full-fallback counter0, unchanged identity string computed from existing Path/SHA identityInput, and write/delete denial until Scope.Close. After closing scope but before closing second snapshot, Verify must error, not rehash silently.

Implement this script boundary test:

```go
func TestToolchainToolDigestScriptsRemainFullHash(t *testing.T) {
    p:=filepath.Join(t.TempDir(),"VsDevCmd.bat")
    if err:=os.WriteFile(p,[]byte("@echo off\r\n"),0600); err!=nil { t.Fatal(err) }
    ctx,s,err:=toolhash.Begin(context.Background()); if err!=nil { t.Fatal(err) }; defer s.Close()
    full:=0
    snap,err:=openExecutableSnapshotWithDigestObserver(ctx,p,4096,nil,func(){full++})
    if err!=nil { t.Fatal(err) }; defer snap.Close()
    initial:=full
    if initial<1 || snap.toolEvidence!=nil { t.Fatal("script gained protected digest authority") }
    if err:=snap.Verify(ctx); err!=nil { t.Fatal(err) }
    if full!=initial+1 || s.Stats().FullHashCalls!=0 { t.Fatal("script did not rehash") }
}
```

Add TestToolchainToolDigestSmallerLimitAndCancelled, TestToolchainToolDigestIdentityChangesNextScope, TestToolchainToolDigestDifferentConsumerRejected, TestToolchainToolDigestSelectedSDKExcluded (extension-selection rejects .lib and original SDK hashing route has no Scope call). Inspect actual VsDevCmd, cmd, vswhere, clang-cl/LLVM, cl/link/ninja/msbuild call sites: executable fields eligible, script stays full. Record this exact caller audit, not a broad refactor.
- [ ] **Step 2: behavioral RED.** `go test ./internal/toolchain -run TestToolchainToolDigest -count=1 -v`; scoped binary count/evidence failures must be visible on compiling old-full-hash selection.
- [ ] **Step 3: implement only selection, retaining every prior guard.** Use:

```go
func guardedExecutableDigest(ctx context.Context,path string,file *os.File,maximum int64)(toolhash.Result,bool,error) {
    if runtime.GOOS!="windows" || !strings.EqualFold(filepath.Ext(path),".exe") {
        return toolhash.Result{},false,nil
    }
    return toolhash.Digest(ctx,path,file,maximum)
}
```

Opener first keeps canonicalExistingPath, stat/consumer same-object, direct/executable mode, maximum, existing beforeDigest hook. Then call guardedExecutableDigest: protected result sets digest/evidence, miss calls actual digestOpenFile, error fails/consumer-closes. IdentityInput Path/SHA and encoded digest are byte-for-byte unchanged. Existing Verify first checks ctx, current path/consumer identity and size, then evidence.Verify or original digestOpenFile, and compares stored SHA. Every before/after probe verification remains; no external probe under scope lock. onFullHash runs only at actual digestOpenFile fallback, never creates the value itself.
- [ ] **Step 4: GREEN/legacy boundaries.** `go test ./internal/toolchain -run 'TestToolchainToolDigest|TestExecutableSnapshotAcceptsExactLimitAndRejectsLimitPlusOne|TestExecutableSnapshotHonorsCancellationDuringOpenAndVerify|TestExecutableDigestStopsBetweenChunksAfterCancellation|TestExecutableSnapshotRejectsGrowthBetweenHandleCheckAndDigest' -count=1 -v`. These four existing names were checked in gnu_test.go at planning time; require each to appear in actual output, with no skip. Linux unchanged path is verified by source audit plus Task4 cross-compilation, not claimed Linux execution.
- [ ] **Step 5: narrow commit.** Only the three owned files; `perf: reuse protected Windows toolchain digests`; independent review before runtime opt-in.

## Task 4: actual current-index lifetime, focused integration and handoff

**Files:**
- Modify `apps/test-service/internal/runtime/coverage_detail_backend.go`: ReadCurrentCoverageIndex wrapper and private body only.
- Create `apps/test-service/internal/runtime/coverage_tool_digest_scope.go`, `coverage_tool_digest_scope_windows_test.go`, `coverage_tool_digest_scope_nonwindows_test.go`.
- Create `apps/test-service/internal/discovery/tool_digest_scope_windows_test.go` only for the real Inspector context/concurrency boundary; no Inspector production redesign.
- Update this plan's completion/evidence notes after verified results; do not modify W4 probes/UI/native fixtures or old evidence.

**Interfaces:**

```go
type coverageToolScope interface { Err() error; Close() error }
type coverageToolScopeFactory func(context.Context)(context.Context,coverageToolScope,error)
func beginCoverageToolScope(context.Context)(context.Context,coverageToolScope,error)
func (*Runtime) readCurrentCoverageIndex(context.Context,coveragedetail.CurrentIndexQuery,coverageToolScopeFactory)(coveragedetail.Index,error)
```

Actual exported ReadCurrentCoverageIndex delegates to the private helper with beginCoverageToolScope; no alternative IPC path/config option. The default factory calls toolhash.Begin; nil Scope on non-Windows must become a genuinely nil interface, not typed-nil. Test factories can observe or wrap real scopes and inject close errors; they cannot skip body validation. No new Runtime global/config state.

- [ ] **Step 1: write actual-entrypoint integration assertions.** New Windows-tagged test file uses existing sourceBoundDetailStore and runtimeCoordinator. Import context, errors, os, path/filepath, strings, testing, and the existing internal coveragedetail/coveragedomain/coveragenormalize/discovery/workspace packages plus toolhash. Use this complete lifetime fixture and test; no tool process is launched:

```go
type digestScopeCoordinator struct {
    runtimeCoordinator
    generation, path string
    results []toolhash.Result
    contexts []context.Context
}
func (c *digestScopeCoordinator) Inspect(ctx context.Context)(discovery.Snapshot,error) {
    f,err:=os.Open(c.path); if err!=nil { return discovery.Snapshot{},err }
    defer f.Close()
    result,protected,err:=toolhash.Digest(ctx,c.path,f,1024)
    if err!=nil { return discovery.Snapshot{},err }
    if !protected || result.Evidence==nil { return discovery.Snapshot{},errors.New("missing current-query scope") }
    c.results=append(c.results,result)
    c.contexts=append(c.contexts,ctx)
    return discovery.Snapshot{Generation:c.generation},nil
}
func TestRuntimeToolDigestScopeActualEntryAndFreshQuery(t *testing.T) {
    dir:=t.TempDir()
    source:=filepath.Join(dir,"a.c")
    if err:=os.WriteFile(source,[]byte("int f(void) { return 1; }\n"),0600); err!=nil { t.Fatal(err) }
    root,err:=workspace.OpenRoot(dir); if err!=nil { t.Fatal(err) }
    binding,err:=coveragenormalize.DigestSource(dir,source,coveragenormalize.DefaultLimits())
    if err!=nil { t.Fatal(err) }
    projectID,reportID,generation,runID:="core",strings.Repeat("a",32),strings.Repeat("b",64),strings.Repeat("c",32)
    fileID,err:=coveragedetail.StableFileID(projectID,binding.URI); if err!=nil { t.Fatal(err) }
    file:=coveragedetail.File{ID:fileID,RelativePath:binding.URI,SourceSHA256:binding.SHA256,Status:coveragedetail.StatusCurrent}
    store:=&sourceBoundDetailStore{
        report:coveragedomain.Report{ID:reportID,RunID:runID},
        run:coveragedomain.Run{Request:coveragedomain.Request{ProjectID:projectID,WorkspaceGeneration:generation}},
        index:coveragedetail.Index{ProjectID:projectID,ReportID:reportID,RunID:runID,WorkspaceGeneration:generation,
            Project:coveragedetail.Project{Status:coveragedetail.StatusCurrent},Files:[]coveragedetail.File{file}},
    }
    tool:=filepath.Join(t.TempDir(),"cl.exe")
    bytes:=[]byte("controlled tool bytes")
    if err:=os.WriteFile(tool,bytes,0600); err!=nil { t.Fatal(err) }
    coordinator:=&digestScopeCoordinator{generation:generation,path:tool}
    r:=&Runtime{store:store,coordinator:coordinator,workspaceRoot:root,trustedWorkspace:true}
    q:=coveragedetail.CurrentIndexQuery{ProjectID:projectID,ReportID:reportID,WorkspaceGeneration:generation}
    for call:=0;call<2;call++ {
        index,err:=r.ReadCurrentCoverageIndex(context.Background(),q)
        if err!=nil || len(index.Files)!=1 { t.Fatalf("query %d: %#v %v",call,index,err) }
        if len(coordinator.results)!=2*(call+1) { t.Fatal("lost one complete inspection") }
        first,last:=coordinator.results[2*call],coordinator.results[2*call+1]
        if first.Reused || !last.Reused || first.Evidence!=last.Evidence { t.Fatal("not one shared query scope") }
        if err:=os.WriteFile(tool,[]byte("changed after return"),0600); err!=nil { t.Fatal("guard leaked",err) }
        consumer,err:=os.Open(tool); if err!=nil { t.Fatal(err) }
        _,verifyErr:=first.Evidence.Verify(coordinator.contexts[2*call],tool,consumer,1024)
        if err:=consumer.Close(); err!=nil { t.Fatal(err) }
        if verifyErr==nil { t.Fatal("returned query retained digest authority") }
        if err:=os.WriteFile(tool,bytes,0600); err!=nil { t.Fatal(err) }
    }
    if coordinator.results[0].Evidence==coordinator.results[2].Evidence { t.Fatal("scope survived into next query") }
}
```

This controlled coordinator proves exported-entry lifetime and preserved two-inspection calls, not real discovery-generation derivation. Task1 supplies byte-counter proof that the next scope hashes changed bytes; Tasks2/3 supply actual snapshot-path integration.

TestRuntimeToolDigestScopeRetainsBothFreshnessChecks mutates generation on second Inspect and expects ErrStale. TestRuntimeToolDigestScopeSourceDrift keeps tool protected but changes another indexed source before source verification and still expects ErrStale. TestRuntimeToolDigestScopeEarlyBindingRejectsBeforeScope covers existing invalid-query/untrusted/run-project/run-generation errors before factory invocation, requiring zero new guards. Separately, TestRuntimeToolDigestScopeIndexBindingRejectsReportRunProject changes persisted index project/report/run/current/nonempty fields: the existing check is after first inspection, so require the original ErrStorageUnavailable plus release of that created scope, not a falsely claimed pre-scope rejection. TestRuntimeToolDigestScopeCancellationAndCleanupError exercises actual scope cancellation and wraps real Close with a returned sentinel; no valid Index may survive close failure.

- [ ] **Step 2: behavioral RED.** `go test ./internal/runtime -run TestRuntimeToolDigestScope -count=1 -v`; old exported entry has no scope, so coordinator cannot obtain guarded result. Missing private helper signatures alone are not RED evidence.
- [ ] **Step 3: implement scope lifetime without weakening body.** Preserve existing detailReader/attested-store/trust checks, GetCoverageReport/GetCoverageRun/project+generation matching **before** scope creation. Name result/err to reject cleanup failure. Add scope before first Inspect, using factory validation and this defer:

```go
defer func() {
    if scope==nil { return }
    if closeErr:=scope.Close(); closeErr!=nil {
        index=coveragedetail.Index{}
        if err==nil { err=task.ErrStorageUnavailable }
    }
}()
```

Scope.Err must also be checked before accepting the final index; preserve context cancellation's existing error identity when applicable. Keep both InspectWorkspace calls, both Generation comparisons, persisted validated-index project/report/run/current/nonempty checks, detailSourceReason across **all** indexed files, and selected target/profile/ownership checks in unchanged downstream readers. No discovery result caching. All return/error branches after Begin run the defer. No Scope/Evidence in returned Index, stored graph or IPC.

Default factory implementation:

```go
func beginCoverageToolScope(ctx context.Context)(context.Context,coverageToolScope,error) {
    scoped,s,err:=toolhash.Begin(ctx)
    if err!=nil { return ctx,nil,err }
    if s==nil { return scoped,nil,nil }
    return scoped,s,nil
}
```

- [ ] **Step 4: verify real Inspector propagation/cancellation boundary.** In package discovery use existing newTestInspector, fakeToolchainDiscovery, openProjectRoot, testInstallation and noPresetDiscovery helpers; no invented public registry adapter. Import context, errors, os, path/filepath, sync/atomic, testing, and internal cmake/probe/toolchain/toolhash/workspace packages. Implement:

```go
func TestInspectorToolDigestScopePropagation(t *testing.T) {
    paths:=[]string{filepath.Join(t.TempDir(),"cmake.exe"),filepath.Join(t.TempDir(),"cl.exe")}
    for _,p:=range paths { if err:=os.WriteFile(p,[]byte("controlled executable bytes"),0600); err!=nil { t.Fatal(err) } }
    ctx,scope,err:=toolhash.Begin(context.Background()); if err!=nil || scope==nil { t.Fatalf("begin: %v",err) }
    defer scope.Close()
    outcomes:=make(chan error,4)
    digest:=func(ctx context.Context,path string) error {
        f,err:=os.Open(path); if err!=nil { return err }; defer f.Close()
        r,protected,err:=toolhash.Digest(ctx,path,f,1024)
        if err!=nil { return err }
        if !protected || r.Evidence==nil { return errors.New("Inspector discarded scoped context") }
        return nil
    }
    var loads,resolves,toolchains atomic.Int32
    tools:=fakeToolchainDiscovery{discover:func(ctx context.Context)([]toolchain.Instance,[]toolchain.Issue) {
        toolchains.Add(1); outcomes<-digest(ctx,paths[1]); return nil,nil
    }}
    inspector:=newTestInspector(t,openProjectRoot(t,"."),tools,inspectorDependencies{
        loadConfig:func(workspace.Root)(workspace.LoadResult,error) {
            loads.Add(1); return workspace.LoadResult{Config:workspace.Config{Version:1}},nil
        },
        resolve:func(ctx context.Context,_ probe.Runner,_ cmake.ResolverConfig)(cmake.Installation,error) {
            resolves.Add(1); err:=digest(ctx,paths[0]); outcomes<-err
            if err!=nil { return cmake.Installation{},err }; return testInstallation(),nil
        },
        discoverPresets:noPresetDiscovery,
    })
    for i:=0;i<2;i++ { if _,err:=inspector.Inspect(ctx); err!=nil { t.Fatal(err) } }
    if len(outcomes)!=4 { t.Fatal("lost foundation callback") }
    for i:=0;i<4;i++ { if err:=<-outcomes; err!=nil { t.Fatal(err) } }
    if loads.Load()!=2 || resolves.Load()!=2 || toolchains.Load()!=2 { t.Fatal("cached a complete discovery") }
    if got:=scope.Stats(); got.FullHashCalls!=2 || got.Retained!=2 { t.Fatalf("guarded tools: %+v",got) }
    if err:=scope.Close(); err!=nil { t.Fatal(err) }
    for _,p:=range paths { if err:=os.WriteFile(p,[]byte("after close"),0600); err!=nil { t.Fatal("guard leaked",err) } }
}
```

The empty-project fixture deliberately does not assert preset callbacks; include the existing TestInspectorUsesPresetPriorityAndGeneratedProfilesUseCMakeConstructor in the focused regression command to preserve that distinct path. These real Inspector callbacks test scoped-context propagation to real guarded files; Task2's actual resolver and Task3's actual snapshot tests remain the byte-hash integration proof. Do not claim fake callback metadata is real discovery/native qualification.

Add TestInspectorToolDigestScopeCancellation using the existing TestInspectorRunsCMakeAndToolchainDiscoveryConcurrently channel pattern: both branches signal started; one completes real Digest then blocks at an explicit test-only gate, the other waits for ctx.Done. Cancel parent only after both started, require Inspect returns cancellation, call scope.Close before releasing the blocked branch, prove write access is restored, release/join the worker, require further Digest rejects that context. Cleanup always cancels/releases/joins with the same5s watchdog as Task1. This cannot invoke t.Fatal from a callback goroutine or introduce probe waits inside Scope.Close.

- [ ] **Step 5: focused GREEN and one consolidated race gate.**

```powershell
$env:GOPROXY='off'; $env:GOSUMDB='off'
& 'D:/Program Files/Go/bin/go.exe' test ./internal/toolhash ./internal/cmake ./internal/toolchain ./internal/runtime ./internal/discovery -run 'TestToolHash|TestCMakeToolDigest|TestToolchainToolDigest|TestRuntimeToolDigestScope|TestInspectorToolDigestScope|TestRuntimeCurrentCoverageTargetAttestsEverySourceAndExactGap|TestInspectorUsesPresetPriorityAndGeneratedProfilesUseCMakeConstructor' -count=1 -v
if ($LASTEXITCODE -ne 0) { throw 'Focused protected-digest verification failed' }
& 'D:/Program Files/Go/bin/go.exe' test -race ./internal/toolhash ./internal/cmake ./internal/toolchain ./internal/runtime ./internal/discovery -run 'TestToolHash|TestCMakeToolDigest|TestToolchainToolDigest|TestRuntimeToolDigestScope|TestInspectorToolDigestScope' -count=1
if ($LASTEXITCODE -ne 0) { throw 'Protected-digest race gate not passed' }
```

Do not change CGO/compiler settings blindly or report a missing-tool race failure as a source race. Record exact failure and leave that gate open until configured using existing approved tools. !windows test files exercise actual no-scope behavior only when run on a real non-Windows host. The local compile-only check uses this exact ignored scratch and restores process settings; it never executes the Linux binaries:

```powershell
$Names=@('GOOS','GOARCH','CGO_ENABLED')
$Saved=@{}
foreach ($Name in $Names) { $Saved[$Name]=[Environment]::GetEnvironmentVariable($Name,'Process') }
$Out=[IO.Path]::GetFullPath((Join-Path (Get-Location).Path '../../.superpowers/sdd/2026-10-09-windows-protected-tool-digest-reuse/compile-linux'))
try {
    $env:GOOS='linux'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'
    New-Item -ItemType Directory -Path $Out -Force | Out-Null
    foreach ($Package in @('toolhash','cmake','toolchain','runtime','discovery')) {
        & 'D:/Program Files/Go/bin/go.exe' test -c -o (Join-Path $Out "$Package.test") "./internal/$Package"
        if ($LASTEXITCODE -ne 0) { throw "Linux compile-only gate failed: $Package" }
    }
} finally {
    foreach ($Name in $Names) { [Environment]::SetEnvironmentVariable($Name,$Saved[$Name],'Process') }
}
```

Cross-compilation is compile evidence, not Linux runtime success, and must not trigger native/hosted. Leave the scratch for evidence rather than recursively deleting it.

- [ ] **Step 6: report and narrow commit.** List hash-call/byte totals, targeted counts/skips/times, context cancellation/release observations, source-drift preservation and any open race/alias/Linux gates. No claim of reducing real27s latency without a separately authorized measurement. Stage only owned runtime/discovery files and verified plan notes; commit `perf: scope protected digests to current coverage attestation`. Independent task review then whole-change review of all four tasks; open Important defects cannot be hidden by local unit success.

## Self-review coverage map and final boundaries

| Spec requirement | Plan coverage |
| --- | --- |
| Single actual attestation; new hash next query; no global/TTL cache | Task1 Begin+private context; Task4 actual entry false,true,false,true |
| First full SHA under deny-write/delete protection, every reuse identity | Task1 real guard/ReadAt/checkTriple; Tasks2/3 preserve consumer checks |
| Both discoveries and all source/report/trust checks retained | Task4 actual entry/negative fixtures; Inspector propagation, unchanged authority audit |
| Exclude user/config/report/license/scripts/SDK files | Task2 only exact CMake/CTest entries; Task3 binary selector and caller audit |
| Optional protection/capacity/size fallback; drift fails | Task1 separate boolean/error/sticky domain; Tasks2/3 fallback versus error |
| 128 entries/512MiB optional, stricter original limits | Task1 reservation/small real boundary tests; CMake limit0 preserved; toolchain positive maximum preserved |
| Close/error/cancel/concurrency no stale evidence or shared offsets | Task1 independent consumer hashing/short guard lock, gated tests; Task4 cleanup error gate |
| Linux/non-scoped behavior | build-tagged no-scope code, unchanged fallback branches; compile-only versus runtime evidence explicit |
| No automatic native, new timers, helper contracts or external mutations | globals, exact ownership, focused test policy and handoff restrictions |

Inline planning self-review on 2026-10-09 covered every spec section against the map above, checked existing source/helper/test signatures and removed undefined interface placeholders. It corrected the early-binding versus post-inspection index checks, guard-admission/publication lock ownership, the retained consumer-handle boundary and the empty-project Inspector fixture's unclaimed preset calls. Existing regression names are explicit; private observation cannot replace hashing. This is document/source inspection only: the planned Go snippets have not been compiled, tests have not been run, and no performance/native success is asserted. Final code must provide complete implementations and pass all required behavior tests; no forwarding stubs may remain in an implementation commit.

Completion is **focused protected-digest optimization**, not W4 completion, real dual-framework acceptance, full27s latency repair, all second-version functions, clean-SHA/whole-PR/global/hosted/package qualification, formal Windows signing or legal approval. Preserve all original warnings and absent native receipt. Return to original W4 only after reviewed focused changes and the separately required native/performance authority gate; no automatic native launch follows this plan.

## Primary implementation references

- Current local source: cmake/file_snapshot_windows.go for no-write/no-delete opener and volume/file index; toolchain/gnu.go for cancellable bounded full hashing; runtime/coverage_detail_backend.go for the exact two-inspection trust chain; discovery/inspector.go for parallel foundation cancellation.
- [Microsoft CreateFileW sharing contract](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-createfilew): sharing restrictions last for the open handle; select read sharing without write/delete, preserve direct-file and compatible-handle checks. Guard closure is a lifecycle boundary, not proof of arbitrary privileged/mapped-write resistance.
- Local Go1.26.6 `src/internal/poll/fd_windows.go`: regular-file Close may coordinate outstanding IO. This is why full hashing uses independent consumer handles and scope cleanup does not wait for a whole guard-handle hash.
