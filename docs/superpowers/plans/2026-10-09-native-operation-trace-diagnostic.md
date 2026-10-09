# Native Operation Trace Diagnostic Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Identify the actual pending Windows native generation operation through unchanged-delegation, secret-free observation and one externally bounded diagnostic attempt.

**Architecture:** Add one trace context/facade in the existing native helper and its existing test file. Preserve the real client/result/rejection identities and all acceptance assertions; a separate owned-process supervisor enforces the diagnostic wall-clock budget. Source remains dirty pending the original W4 gate, so review the trace's raw before/after blobs rather than pretending a commit-only diff contains it.

**Tech Stack:** Existing TypeScript 6.0.3, Node.js 24.19, node:test, ProtocolClient, Windows PowerShell, installed offline CMake/LLVM/framework bundles. No new dependency.

**Approved spec:** [Native diagnostic design](../specs/2026-10-09-native-operation-trace-diagnostic-design.md), human reply “确认设计文档” on 2026-10-09.

## Global Constraints

- Exactly two existing source paths may change: `tools/service-probe/src/native-test-generation.ts` and `tools/service-probe/src/native-test-generation.test.ts`.
- Activation is exactly `UTIDE_NATIVE_OPERATION_TRACE=1`. Otherwise return the original client, create no observation timer and emit no new trace text.
- Invoke each original method exactly once, with the real client as receiver and the original argument objects unchanged.
- Return the original result, including the exact Promise object. Preserve the exact synchronous thrown object and asynchronous rejection object.
- Do not mutate the client or its prototype, replace a connection, add a request/deadline/cancellation timer, or perform extra client calls.
- Cache one facade per real client inside this trace context. A restarted client is different only when the underlying real client is different.
- Preserve `withNativeFirstCoverageReport(root, platform, framework, inspect)` and its two-argument inspect callback. `executeNativeManagedGeneration` stays unchanged.
- No argument, result, property value, error message/stack, path, endpoint, READY text, stdout body, credential, token, database value or arbitrary user string may be emitted in trace.
- Limit detailed history to 4,096 records per framework context plus one fixed history-budget marker. Independent pending aggregates survive suppression.
- Pending heartbeat: every 10 seconds, at most 128 snapshots per framework context. Each line is bounded to 65,536 UTF-8 bytes. Unref and dispose the heartbeat.
- One distinct retained Windows diagnostic attempt, no automatic retry. Execution budget is 1,200 seconds; stop confirmation budget is 15 seconds.
- Terminate only positively identified owned native Node/direct service processes, with PID/creation time/path revalidated. Never kill by name, wildcard, broad ancestry or guessed PID.
- Diagnostic expiry is inconclusive, not native PASS or a natural product failure. An unresolved client method is a boundary, not proof of the server root.
- Preserve the original W4 dirty work. Human-approved exception: defer diagnostic source commits to the original conditional W4 source gate; no source staging during this plan.
- No changes to shared generation helper, probe, client/connection, Go, protocol, fixtures, recipes, manifests, bundle locks, DTOs or workflows. No install/new worktree, DB/token reads, remote push/dispatch/merge/Release/signing.
- Do not run global verify, SDK race, clean-SHA native, hosted or packaging here. Do not redispatch completed W1/W2/W3 or repeat accepted UI/helper/identity/framework prerequisite gates.

## File and ownership map

| Path | Responsibility |
| --- | --- |
| `tools/service-probe/src/native-test-generation.ts` | Opt-in trace context, real-client facade and phase insertion only |
| `tools/service-probe/src/native-test-generation.test.ts` | Focused deterministic observation/privacy/delegation/budget/lifetime tests |
| This plan's ignored SDD directory | Protected raw blobs/hashes, briefs, original outputs, task reports and bound review package |
| `.native-e2e/artifacts/windows/` | One distinct runtime diagnostic output pair; ignored, never staged |

Workdir for every shell operation: `C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest`. Branch is `codex/phase10-production-generation`, a linked worktree, not the default checkout. Design commit is `e7fe61e80e0959bc1ca66bda0cfc20439acd0436`; accepted Go correction is `c6b66881b0ace85d3bd820a70fd52477f5cf1e81`. Original cumulative W4 BASE remains `df55ef702c5d2af5edfb7e4e5da4619504e6b6ff`.

Initialize only this plan's workspace at execution time with the selected skill's `sdd-workspace`. First ledger line must name this plan. Do not read/write a sibling SDD workspace; controller delivers original W4 interfaces and protection hashes in the brief. Main controller later switches explicitly back to the original plan; no original full-feature completion is inferred.

Runtime prefix:

```powershell
$env:PATH = 'C:/Users/DELL/AppData/Local/Temp/utide-pnpm-11.4.0;C:/Users/DELL/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin;D:/Program Files/Go/bin;' + $env:PATH
$env:CMAKE = 'C:/codex_project/unitTest/.bundled-tools/cmake/4.3.4/win32-x64/cmake-4.3.4-windows-x86_64/bin/cmake.exe'
```

---

### Task 1: Implement and review observation without changing the native route

**Files:**
- Modify/test: the two scoped source paths above.
- Write: this plan's `task-1-report.md` and hash/blob-bound review package.
- No source commit; explicit approved exception above.

**Interfaces:**
- Consumes: existing `ProtocolClient`, `Framework`, private `NativeGenerationLifecycle` with `workspaceRoot` and `restart: () => Promise<ProtocolClient>`.
- Produces: `createNativeOperationTrace(framework: Framework, options?: NativeOperationTraceOptions): NativeOperationTrace`; `observe<T extends object>(client: T): T`, `phase(value: NativeTracePhase): void`, `close(): void`, readonly `inconclusive: boolean`.
- Task 2 consumes reviewed source/dist SHA-256 values, authentic targeted test outputs and the factory's opt-in flag, prefix and closed schema.

- [ ] **Step 1: Protect the dirty starting state before any edit**

Record actual HEAD/index/status. The index must be empty. Record each scoped file's SHA-256 and raw Git blob, not an assumed HEAD version. `hash-object -w` stores a blob without staging/committing the source; do not create a new worktree or erase the dirty baseline.

```powershell
git rev-parse HEAD
git branch --show-current
git diff --cached --name-only
git status --short --untracked-files=normal
git hash-object -w --no-filters -- tools/service-probe/src/native-test-generation.ts tools/service-probe/src/native-test-generation.test.ts
Get-FileHash -LiteralPath tools/service-probe/src/native-test-generation.ts, tools/service-probe/src/native-test-generation.test.ts -Algorithm SHA256
```

Expected initial SHA-256: helper `B5DAFFDBA31970502A0293DD02A0D630B54C0B17DE5B0FFD66234D71C3F0CC7C`; test `EDDBAF5700FA69CB814191C7D7395F0BB712FE1CE674614A261ACA439F92BF55`. If either differs, stop for controller baseline resolution, do not overwrite/reconstruct it. Controller supplies the six other protected W4 hashes and verifies them unchanged after Task 1.

- [ ] **Step 2: Write behavioral tests and a compiling no-op seam**

Add the factory/interface declarations with a temporary compiling no-op implementation returning the real client and no trace. This is only enough to let tests fail for behavior; it is not accepted production implementation. Add the following tests in the existing test file, importing the factory/constants from the same helper module.

Copy the constants and type/interface declarations from Step 4 first, but use this temporary factory body instead of the Step 4 factory. Definitions remain in this same task; do not add an undefined import or count a compiler failure as RED.

```typescript
export function createNativeOperationTrace(
  framework: Framework, options: NativeOperationTraceOptions = {},
): NativeOperationTrace {
  void framework; void options;
  return { observe: <T extends object>(client: T): T => client,
    phase() {}, close() {}, inconclusive: false };
}
```

```typescript
import { createNativeOperationTrace, NATIVE_TRACE_METHODS, NATIVE_TRACE_PHASES } from './native-test-generation.js';
const tracePrefix = 'native-operation-trace: ';
type TestEvent = Record<string, unknown>;
function decodeTrace(lines: readonly string[]): TestEvent[] {
  const keys = new Set(['framework','phase','method','call','client','pending','kind','seq','elapsedMs']);
  const rowKeys = new Set(['phase','method','count','cohortFirstCall','cohortLatestCall']);
  let seq = 0, time = 0;
  return lines.map((line) => {
    assert.ok(line.startsWith(tracePrefix));
    assert.ok(Buffer.byteLength(line, 'utf8') <= 65_536);
    const event = JSON.parse(line.slice(tracePrefix.length)) as TestEvent;
    assert.ok(Object.keys(event).every((key) => keys.has(key)));
    assert.ok(event.framework === 'cpputest' || event.framework === 'unity');
    assert.ok(NATIVE_TRACE_PHASES.includes(event.phase as never));
    assert.ok(['phase','before','returned','threw','pending','closed','history-budget-exhausted','trace-inconclusive'].includes(String(event.kind)));
    assert.ok(Number.isSafeInteger(event.seq) && Number(event.seq) > seq);
    assert.ok(Number.isSafeInteger(event.elapsedMs) && Number(event.elapsedMs) >= time);
    seq = Number(event.seq); time = Number(event.elapsedMs);
    if (event.method !== undefined) assert.ok(NATIVE_TRACE_METHODS.includes(event.method as never));
    if (event.pending !== undefined) {
      assert.ok(Array.isArray(event.pending));
      for (const row of event.pending as TestEvent[]) {
        assert.ok(Object.keys(row).every((key) => rowKeys.has(key)));
        assert.ok(NATIVE_TRACE_PHASES.includes(row.phase as never));
        assert.ok(NATIVE_TRACE_METHODS.includes(row.method as never));
        assert.ok(Number.isSafeInteger(row.count) && Number(row.count) > 0);
      }
    }
    return event;
  });
}
function deferredTraceValue<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
function traceFixture(enabled = true) {
  const lines: string[] = [];
  let clock = 0;
  let pulse: () => void = () => {};
  let stops = 0;
  const trace = createNativeOperationTrace('cpputest', {
    enabled, write: (line) => { lines.push(line); }, now: () => clock,
    schedule: (callback) => { pulse = callback; return () => { stops++; }; },
  });
  return { trace, lines, events: () => decodeTrace(lines),
    tick: () => { clock += 10_000; pulse(); }, stops: () => stops };
}
function pendingRows(event: TestEvent): TestEvent[] {
  return event.pending as TestEvent[];
}

test('native operation trace records unresolved calls and exact delegation identity', async () => {
  const f = traceFixture();
  const d = deferredTraceValue<object>();
  const argument = { secret: 'ARGUMENT_SENTINEL' };
  const value = { secret: 'RESULT_SENTINEL' };
  class RealReceiver {
    #calls = 0;
    get calls() { return this.#calls; }
    getTask(input: object) { assert.strictEqual(input, argument); this.#calls++; return d.promise; }
  }
  const real = new RealReceiver();
  const client = f.trace.observe(real);
  f.trace.phase('baseline-coverage');
  const actual = client.getTask(argument);
  assert.strictEqual(actual, d.promise);
  assert.equal(real.calls, 1);
  assert.equal(f.events().filter((e) => e.kind === 'before').length, 1);
  assert.equal(f.events().filter((e) => e.kind === 'returned').length, 0);
  f.tick();
  const waiting = f.events().find((e) => e.kind === 'pending')!;
  assert.equal(pendingRows(waiting)[0]!.method, 'getTask');
  assert.equal(pendingRows(waiting)[0]!.count, 1);
  d.resolve(value);
  assert.strictEqual(await actual, value);
  assert.equal(f.events().filter((e) => e.kind === 'returned').length, 1);
  f.trace.close();
  assert.equal(f.stops(), 1);
  assert.ok(!f.lines.join('').includes('ARGUMENT_SENTINEL'));
  assert.ok(!f.lines.join('').includes('RESULT_SENTINEL'));
});

test('native operation trace preserves synchronous and asynchronous rejection objects', async () => {
  const f = traceFixture();
  const sentinel = { secret: 'THROWN_SENTINEL', toString() { throw new Error('do not stringify'); } };
  const sync = f.trace.observe({ getTask() { throw sentinel; } });
  assert.throws(() => sync.getTask(), (error) => error === sentinel);
  const d = deferredTraceValue<never>();
  const asyncClient = f.trace.observe({ getTask() { return d.promise; } });
  const returned = asyncClient.getTask();
  assert.strictEqual(returned, d.promise);
  d.reject(sentinel);
  await assert.rejects(returned, (error) => error === sentinel);
  assert.equal(f.events().filter((e) => e.kind === 'threw').length, 2);
  assert.ok(!f.lines.join('').includes('THROWN_SENTINEL'));
  f.trace.close();
});

test('native operation trace observes every allowlisted method without payload getters', async () => {
  const f = traceFixture();
  const value = Object.defineProperty({}, 'payload', { get() { throw new Error('secret payload read'); } });
  for (const method of NATIVE_TRACE_METHODS) {
    let calls = 0;
    const promise = Promise.resolve(value);
    const real = { [method](argument: object) { assert.strictEqual(this, real); assert.strictEqual(argument, value); calls++; return promise; } };
    const client = f.trace.observe(real);
    assert.strictEqual(client[method]!(value), promise);
    assert.strictEqual(await promise, value);
    assert.equal(calls, 1);
    assert.ok(f.events().some((e) => e.kind === 'before' && e.method === method));
    assert.ok(f.events().some((e) => e.kind === 'returned' && e.method === method));
  }
  f.trace.close();
});

test('native operation trace keeps non-audited delegation and property values silent', () => {
  const f = traceFixture();
  const real = { secret: 'PROPERTY_SENTINEL', other() { assert.strictEqual(this, real); return real.secret; } };
  const client = f.trace.observe(real);
  assert.equal(client.other(), real.secret);
  assert.equal(client.secret, real.secret);
  assert.deepEqual(f.events(), []);
  f.trace.close();
  assert.ok(!f.lines.join('').includes('PROPERTY_SENTINEL'));
});

test('native operation trace is silent when disabled and activates only exact env flag', () => {
  const old = process.env.UTIDE_NATIVE_OPERATION_TRACE;
  const real = { getTask() { return 1; } };
  try {
    for (const flag of [undefined, '0', 'true', '01', '1']) {
      if (flag === undefined) delete process.env.UTIDE_NATIVE_OPERATION_TRACE;
      else process.env.UTIDE_NATIVE_OPERATION_TRACE = flag;
      const lines: string[] = [];
      let scheduled = 0;
      const trace = createNativeOperationTrace('cpputest', {
        write: (line) => { lines.push(line); }, now: () => 0,
        schedule: () => { scheduled++; return () => {}; },
      });
      const client = trace.observe(real);
      assert.equal(client.getTask(), 1);
      assert.equal(scheduled, flag === '1' ? 1 : 0);
      if (flag !== '1') { assert.strictEqual(client, real); assert.deepEqual(lines, []); }
      else { assert.notStrictEqual(client, real); assert.ok(decodeTrace(lines).some((e) => e.kind === 'before')); }
      trace.close();
    }
  } finally {
    if (old === undefined) delete process.env.UTIDE_NATIVE_OPERATION_TRACE;
    else process.env.UTIDE_NATIVE_OPERATION_TRACE = old;
  }
});

test('native operation trace cache represents real client identity and stable methods', () => {
  const f = traceFixture();
  const first = { getTask() { return 1; } };
  const reopened = { getTask() { return 2; } };
  const a = f.trace.observe(first);
  assert.strictEqual(f.trace.observe(first), a);
  assert.strictEqual(a.getTask, a.getTask);
  const b = f.trace.observe(reopened);
  assert.notStrictEqual(a, b);
  assert.strictEqual(f.trace.observe(reopened), b);
  f.trace.close();
});

test('native operation trace history budget preserves unresolved operation snapshots', () => {
  const f = traceFixture();
  f.trace.phase('managed-generation');
  const d = deferredTraceValue<unknown>();
  let pending = false;
  const client = f.trace.observe({ getTask() { return pending ? d.promise : 1; } });
  for (let i = 0; i < 4096; i++) client.getTask();
  pending = true;
  assert.strictEqual(client.getTask(), d.promise);
  f.tick();
  const events = f.events();
  assert.equal(events.filter((e) => ['phase', 'before', 'returned', 'threw'].includes(String(e.kind))).length, 4096);
  assert.equal(events.filter((e) => e.kind === 'history-budget-exhausted').length, 1);
  const rows = pendingRows(events.find((e) => e.kind === 'pending')!);
  assert.equal(rows[0]!.method, 'getTask');
  assert.equal(rows[0]!.phase, 'managed-generation');
  assert.equal(rows[0]!.count, 1);
  f.trace.close();
});

test('native operation trace overlapping calls retain counts and originating phase', async () => {
  const f = traceFixture();
  const a = deferredTraceValue<number>(), b = deferredTraceValue<number>();
  let calls = 0;
  const client = f.trace.observe({ getTask() { return ++calls === 1 ? a.promise : b.promise; } });
  f.trace.phase('managed-generation');
  const pa = client.getTask(), pb = client.getTask();
  f.trace.phase('applied-coverage');
  f.tick();
  let row = pendingRows(f.events().filter((e) => e.kind === 'pending').at(-1)!)[0]!;
  assert.equal(row.count, 2);
  assert.equal(row.phase, 'managed-generation');
  assert.equal(row.cohortFirstCall, 1);
  assert.equal(row.cohortLatestCall, 2);
  b.resolve(2); await pb; f.tick();
  row = pendingRows(f.events().filter((e) => e.kind === 'pending').at(-1)!)[0]!;
  assert.equal(row.count, 1);
  a.resolve(1); await pa;
  f.trace.close();
});

test('native operation trace bounds heartbeat and disposes idempotently', () => {
  const f = traceFixture();
  const d = deferredTraceValue<unknown>();
  f.trace.observe({ getTask() { return d.promise; } }).getTask();
  for (let i = 0; i < 140; i++) f.tick();
  assert.equal(f.events().filter((e) => e.kind === 'pending').length, 128);
  assert.equal(f.trace.inconclusive, true);
  f.trace.close(); f.trace.close();
  const afterClose = f.lines.length;
  f.tick();
  assert.equal(f.lines.length, afterClose);
  assert.equal(f.stops(), 1);
  assert.equal(f.events().filter((e) => e.kind === 'closed').length, 1);
});

test('native operation trace sink and clock faults cannot change delegated behavior', async () => {
  const d = deferredTraceValue<object>();
  const sentinel = { secret: 'FAULT_RESULT' };
  const trace = createNativeOperationTrace('unity', {
    enabled: true, write() { throw new Error('SINK_SECRET'); }, now: () => 0,
    schedule: () => () => { throw new Error('STOP_SECRET'); },
  });
  const returned = trace.observe({ getTask() { return d.promise; } }).getTask();
  assert.strictEqual(returned, d.promise);
  d.resolve(sentinel); assert.strictEqual(await returned, sentinel);
  assert.equal(trace.inconclusive, true);
  assert.doesNotThrow(() => trace.close());
  const faultyClock = createNativeOperationTrace('unity', {
    enabled: true, now() { throw new Error('CLOCK_SECRET'); }, write() {},
    schedule: () => () => {},
  });
  assert.equal(faultyClock.observe({ getTask() { return 3; } }).getTask(), 3);
  assert.equal(faultyClock.inconclusive, true);
  faultyClock.close();
});

test('native operation trace rejects arbitrary labels without exposing strings', () => {
  const f = traceFixture();
  f.trace.phase('PHASE_SECRET' as never);
  assert.equal(f.trace.inconclusive, true);
  assert.ok(!f.lines.join('').includes('PHASE_SECRET'));
  f.trace.close();
  const lines: string[] = [];
  const bad = createNativeOperationTrace('FRAMEWORK_SECRET' as never, {
    enabled: true, write: (line) => { lines.push(line); },
  });
  const real = { getTask() { return 1; } };
  assert.strictEqual(bad.observe(real), real);
  assert.equal(bad.inconclusive, true);
  assert.deepEqual(lines, []);
  bad.close();
});
```

- [ ] **Step 3: Run the focused RED, not native or the whole file**

```powershell
pnpm exec tsc -b tools/service-probe/tsconfig.json --force
if ($LASTEXITCODE -ne 0) { throw 'Trace tests did not compile; not behavioral RED' }
node --test --test-name-pattern='^native operation trace' tools/service-probe/dist/native-test-generation.test.js
exit $LASTEXITCODE
```

Expected: actual behavior assertions fail with the no-op seam. Record original output/exit; nonselected preexisting tests may be reported skipped by Node and must be recorded separately. All 11 named new tests must be selected; no new test may silently skip. No CMake membership/native/UI/shared-helper/Go tests here.

- [ ] **Step 4: Replace the seam with the minimal complete trace implementation**

Add `performance` from `node:perf_hooks` and `isPromise` from `node:util/types`. Insert this implementation alongside existing private native constants; do not restructure the large helper file.

```typescript
export const NATIVE_TRACE_METHODS = [
  'inspectWorkspace', 'startCMakeBuild', 'getTask', 'discoverTests', 'getTestCatalog',
  'startCoverage', 'getCoverageRun', 'getCoverageProject', 'listCoverageFiles',
  'listCoverageFunctions', 'listCoverageLines', 'getCapabilities', 'startTestGeneration',
  'getTestGenerationRun', 'getManagedReview', 'applyManagedReview', 'listManagedTests',
  'listArtifacts', 'readArtifact', 'getCoverageReport',
] as const;
export const NATIVE_TRACE_PHASES = [
  'service-start', 'profile-selection', 'baseline-coverage', 'managed-generation',
  'applied-coverage', 'maintenance-edit', 'maintenance-reopen', 'maintained-coverage',
  'evidence', 'dispose',
] as const;
export type NativeTracePhase = typeof NATIVE_TRACE_PHASES[number];
type NativeTraceMethod = typeof NATIVE_TRACE_METHODS[number];
type NativeTraceKind = 'phase' | 'before' | 'returned' | 'threw' | 'pending' | 'closed'
  | 'history-budget-exhausted' | 'trace-inconclusive';
interface NativeTracePending {
  phase: NativeTracePhase; method: NativeTraceMethod; count: number;
  cohortFirstCall: number; cohortLatestCall: number;
}
interface NativeTraceFields {
  phase?: NativeTracePhase; method?: NativeTraceMethod;
  call?: number; client?: number; pending?: readonly NativeTracePending[];
}
export interface NativeOperationTraceOptions {
  enabled?: boolean;
  write?: (line: string) => void;
  now?: () => number;
  schedule?: (pulse: () => void) => () => void;
}
export interface NativeOperationTrace {
  observe<T extends object>(client: T): T;
  phase(value: NativeTracePhase): void;
  close(): void;
  readonly inconclusive: boolean;
}
export function createNativeOperationTrace(
  framework: Framework, options: NativeOperationTraceOptions = {},
): NativeOperationTrace {
  const validFramework = framework === 'cpputest' || framework === 'unity';
  const enabled = (options.enabled ?? process.env.UTIDE_NATIVE_OPERATION_TRACE === '1') && validFramework;
  if (!enabled) return {
    observe: <T extends object>(client: T): T => client,
    phase() {}, close() {}, inconclusive: !validFramework,
  };
  const write = options.write ?? ((line: string) => { process.stderr.write(line); });
  const now = options.now ?? (() => performance.now());
  const schedule = options.schedule ?? ((pulse: () => void) => {
    const timer = setInterval(pulse, 10_000);
    timer.unref();
    return () => { clearInterval(timer); };
  });
  const methods = new Set<string>(NATIVE_TRACE_METHODS);
  const phases = new Set<string>(NATIVE_TRACE_PHASES);
  const facades = new WeakMap<object, object>();
  const pending = new Map<string, NativeTracePending>();
  let phase: NativeTracePhase = 'service-start';
  let origin = 0, previousTime = 0;
  let events = 0, calls = 0, clients = 0, details = 0, snapshots = 0;
  let closed = false, inconclusive = false, budgetMarker = false, faultMarker = false;
  let stop: () => void = () => {};
  try {
    origin = now();
    if (!Number.isFinite(origin)) throw new Error('Invalid diagnostic clock');
    previousTime = origin;
  } catch { inconclusive = true; }
  function raw(kind: NativeTraceKind, fields: NativeTraceFields = {}): void {
    if (inconclusive && !faultMarker && kind !== 'trace-inconclusive') {
      faultMarker = true;
      raw('trace-inconclusive');
    }
    try {
      const instant = now();
      if (!Number.isFinite(instant) || instant < previousTime || instant < origin) {
        throw new Error('Invalid diagnostic clock');
      }
      previousTime = instant;
      const record = { framework, phase, ...fields, kind, seq: ++events,
        elapsedMs: Math.floor(instant - origin) };
      const line = 'native-operation-trace: ' + JSON.stringify(record) + String.fromCharCode(10);
      if (Buffer.byteLength(line, 'utf8') > 65_536) throw new Error('Diagnostic line bound');
      write(line);
    } catch { inconclusive = true; }
  }
  function fault(): void {
    inconclusive = true;
    if (!faultMarker) { faultMarker = true; raw('trace-inconclusive'); }
  }
  function detail(kind: 'phase' | 'before' | 'returned' | 'threw', fields: NativeTraceFields = {}): void {
    if (closed) return;
    if (details < 4096) { details++; raw(kind, fields); }
    else if (!budgetMarker) { budgetMarker = true; raw('history-budget-exhausted'); }
  }
  function snapshot(): NativeTracePending[] {
    return Array.from(pending.values(), (row) => ({ ...row }));
  }
  function pulse(): void {
    if (closed || pending.size === 0) return;
    if (snapshots >= 128) { fault(); return; }
    snapshots++;
    raw('pending', { pending: snapshot() });
  }
  try { stop = schedule(pulse); } catch { fault(); }
  return {
    observe<T extends object>(client: T): T {
      const existing = facades.get(client);
      if (existing !== undefined) return existing as T;
      const slot = ++clients;
      const wrappers = new Map<PropertyKey, { original: unknown; wrapper: (...args: unknown[]) => unknown }>();
      const facade = new Proxy(client, {
        get(target, key) {
          const original: unknown = Reflect.get(target, key, target);
          if (typeof original !== 'function') return original;
          const cached = wrappers.get(key);
          if (cached?.original === original) return cached.wrapper;
          const wrapper = (...args: unknown[]): unknown => {
            if (closed || typeof key !== 'string' || !methods.has(key)) {
              return Reflect.apply(original, target, args);
            }
            const method = key as NativeTraceMethod, atPhase = phase, call = ++calls;
            const groupKey = atPhase + '/' + method;
            const row = pending.get(groupKey) ?? { phase: atPhase, method, count: 0,
              cohortFirstCall: call, cohortLatestCall: call };
            row.count++; row.cohortLatestCall = call; pending.set(groupKey, row);
            const fields = { phase: atPhase, method, call, client: slot };
            detail('before', fields);
            let settled = false;
            const finish = (kind: 'returned' | 'threw'): void => {
              if (settled) return;
              settled = true;
              row.count--;
              if (row.count === 0) pending.delete(groupKey);
              detail(kind, fields);
            };
            let result: unknown;
            try { result = Reflect.apply(original, target, args); }
            catch (error) { finish('threw'); throw error; }
            try {
              if (isPromise(result)) {
                void Promise.prototype.then.call(result,
                  () => { finish('returned'); }, () => { finish('threw'); });
              } else finish('returned');
            } catch { fault(); }
            return result;
          };
          wrappers.set(key, { original, wrapper });
          return wrapper;
        },
      });
      facades.set(client, facade);
      return facade;
    },
    phase(value: NativeTracePhase): void {
      if (closed) return;
      if (!phases.has(value)) { fault(); return; }
      phase = value; detail('phase');
    },
    close(): void {
      if (closed) return;
      closed = true;
      try { stop(); } catch { fault(); }
      raw('closed', { pending: snapshot() });
    },
    get inconclusive() { return inconclusive; },
  };
}
```

The pending map has at most 20 methods × 10 originating phases. Cohort sequence bounds describe calls initiated during a nonempty in-flight epoch, not assertions that both endpoint calls still remain pending. `count` is the outstanding count. Do not add an unbounded list of completed/pending call IDs to compute exact individual extrema.

- [ ] **Step 5: Wire into the existing one-setup/restart path**

Wrap the existing fixture setup in an outer trace try/finally. Keep the existing nested error/retention catches and fixture disposal, but move them inside this outer lifetime. Existing construction parameters and all file/coverage/source assertions remain unchanged.

```typescript
const trace = createNativeOperationTrace(framework);
try {
  trace.phase('service-start');
  const fixture = await startService(serviceBinary, serviceDirectory, {
    workspaceRoot, trustedWorkspace: true, timeoutMs: operationTimeoutMs,
    startupTimeoutMs: 120_000,
    handshakeSupportedProtocolVersions: ['1.6', '1.5', '1.4', '1.3', '1.2', '1.1', '1.0'],
    ...productionBundleRoots(root, platform),
  });
  // Retain the existing try/catch bodies, using this stable observed client:
  const client = trace.observe(fixture.client);
  try {
    trace.phase('profile-selection');
    const initial = await selectProfileEventually(client, platform);
    const config = buildNativeGenerationWorkspaceConfig(framework, initial.profile.buildProfileId, toolchain);
    await writeFile(join(workspaceRoot, '.unit-test-ide/workspace.json'), JSON.stringify(config, null, 2) + String.fromCharCode(10), 'utf8');
    await selectProfileEventually(client, platform, initial.snapshot.workspaceGeneration);
    const relativePath = framework === 'cpputest' ? 'src/classifier.cpp' : 'src/classifier.c';
    // Place the following inside the existing diagnostic/retention catch envelope:
    trace.phase('baseline-coverage');
    const baseline = await runCoverageCycle(client, platform, relativePath);
    return await inspect(client, baseline, {
      workspaceRoot,
      tracePhase: (phase) => { trace.phase(phase); },
      restart: async () => { await fixture.restart(); return trace.observe(fixture.client); },
    });
  } finally {
    trace.phase('dispose');
    await fixture.dispose();
  }
} finally { trace.close(); }
```

The snippet specifies replacement setup/client/lifetime statements, not authorization to remove existing catches: preserve existing safe failure formatting and existing service-debug retention verbatim around baseline/inspect. This avoids copying/refactoring unchanged error policy. Add exactly this private lifecycle field:

```typescript
interface NativeGenerationLifecycle {
  readonly workspaceRoot: string;
  readonly restart: () => Promise<ProtocolClient>;
  readonly tracePhase: (phase: NativeTracePhase) => void;
}
```

In the existing acceptance callback, insert fixed phase transitions around unchanged calls:

```typescript
lifecycle.tracePhase('managed-generation');
const receipt = await executeNativeManagedGeneration({
  client, projectId, ...binding, framework, maxPollAttempts: 600, pollIntervalMs: 500,
  refreshCoverage: async () => {
    lifecycle.tracePhase('applied-coverage');
    try {
      appliedBinding = await runCoverageCycle(client, platform,
        framework === 'cpputest' ? 'src/classifier.cpp' : 'src/classifier.c');
      return appliedBinding;
    } finally { lifecycle.tracePhase('managed-generation'); }
  },
});
// After the existing literal receipt-improvement assertions:
lifecycle.tracePhase('maintenance-edit');
const maintained = await editNativeManagedTestForMaintenance(lifecycle.workspaceRoot, framework);
lifecycle.tracePhase('maintenance-reopen');
const reopened = await lifecycle.restart();
// Retain existing actual fresh-client and edited-byte assertions here.
lifecycle.tracePhase('maintained-coverage');
// Retain existing reopened profile selection and formal runCoverageCycle.
// Immediately before existing post-rerun project/artifact/report reads:
lifecycle.tracePhase('evidence');
```

Do not pass trace as a third public callback argument or add it to receipts. The disabled facade is the original client; enabled caching preserves real initial/restarted client relationships. If setup fails before a fixture exists, outer finally still closes the heartbeat.

- [ ] **Step 6: Run focused GREEN and capture the reviewed output identities**

Run the exact Step 3 compile/test commands once after implementation. Expected: all 11 selected trace tests pass; explicitly distinguish nonselected preexisting skips. Record complete actual stdout/stderr, exits and durations in `task-1-report.md`, plus:

```powershell
git diff --check -- tools/service-probe/src/native-test-generation.ts tools/service-probe/src/native-test-generation.test.ts
git hash-object -w --no-filters -- tools/service-probe/src/native-test-generation.ts tools/service-probe/src/native-test-generation.test.ts
Get-FileHash -LiteralPath tools/service-probe/src/native-test-generation.ts, tools/service-probe/src/native-test-generation.test.ts, tools/service-probe/dist/native-test-generation.js -Algorithm SHA256
git diff --cached --name-only
```

No fake GREEN count from compiler success, no full-file/native rerun, no source commit. Source edits must use apply_patch. Controller confirms the six unrelated protected W4 source hashes remain unchanged.

- [ ] **Step 7: Independent two-file review before a native run**

Controller builds a uniquely named package in this plan's SDD from the actual recorded raw blob IDs. Include HEAD, precise file→before/after blob/SHA-256 mapping, full current two-file context and `git diff --no-ext-diff --no-textconv -U10 BEFORE_BLOB AFTER_BLOB` for each file. Use the real recorded values as variables, never HEAD~1 or a guessed baseline. Any package-generation script must itself be written with apply_patch; do not dump the entire diff into controller context. A commit-only review-package is insufficient because these sources are intentionally uncommitted.

Use this complete ignored artifact script as `review-dirty-blobs.mjs` in this plan's own SDD directory, created via apply_patch. It is review tooling, not another tracked source path. It generates a package mechanically without exposing its full contents through the controller's tool output:

```javascript
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
const root = 'C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest';
const dir = resolve(root, '.superpowers/sdd/2026-10-09-native-operation-trace-diagnostic');
const args = process.argv.slice(2);
if (args.length !== 5) throw new Error('Expected package name and four recorded blob IDs');
const [name, bh, ah, bt, at] = args;
if (!/^review-task1-[a-zA-Z0-9-]+[.]md$/u.test(name)) throw new Error('Invalid own package name');
for (const id of [bh, ah, bt, at]) if (!/^[0-9a-f]{40}$/u.test(id)) throw new Error('Invalid raw Git blob ID');
const git = (...values) => execFileSync('git', values, { cwd: root, windowsHide: true, maxBuffer: 8*1024*1024 });
const sha = (bytes) => createHash('sha256').update(bytes).digest('hex');
const nl = String.fromCharCode(10);
const sections = ['# Dirty trace review package', 'HEAD: ' + git('rev-parse','HEAD').toString().trim(),
  'No source commit: compare raw pre-edit/after blobs, not HEAD changes.'];
for (const [path, before, after] of [
  ['tools/service-probe/src/native-test-generation.ts', bh, ah],
  ['tools/service-probe/src/native-test-generation.test.ts', bt, at],
]) {
  const old = git('cat-file','blob',before), current = git('cat-file','blob',after);
  const actual = await readFile(resolve(root,path));
  if (!actual.equals(current)) throw new Error('Working source drifted from after blob');
  sections.push('## ' + path,
    'Before raw blob: ' + before + '; SHA256: ' + sha(old),
    'After raw blob: ' + after + '; SHA256: ' + sha(current),
    '### Actual trace-only diff',
    git('diff','--no-ext-diff','--no-textconv','-U10',before,after).toString('utf8'),
    '### Full current file context', current.toString('utf8'));
}
const output = resolve(dir,name);
await writeFile(output, sections.join(nl+nl), { flag:'wx' });
process.stdout.write(output + nl);
```

Invoke with the exact four recorded IDs as arguments, in helper-before/helper-after/test-before/test-after order. The package name is a fresh `review-task1-<UTC-token>.md` in this directory. The controller must validate those IDs against the recorded pre-edit/post-GREEN ledger before invocation; the script independently validates current bytes match after blobs. The printed path, not its body, is passed to the reviewer.

Reviewer receives this task brief, report, package and the exact global constraints. It must judge spec and code quality, privacy, current-phase/pending truth, promise/receiver/cache/cleanup lifetime and unchanged existing route/error catches. No repeated tests on identical reviewed code. Fix findings in the usual scoped SDD loop; re-review actual fix blob diffs. Resolve each cannot-verify item explicitly. Bind Task 1 approval to final source/dist hashes and recorded deferred-commit exception, not to a clean SHA.

---

### Task 2: Run one externally bounded diagnostic and record only real evidence

**Files:**
- No source modification or staging.
- Write: this plan's ledger and `task-2-report.md`; distinct ignored runtime output pair.

**Interfaces:**
- Consumes: Task 1 approval, exact two-source/dist hashes, flag `UTIDE_NATIVE_OPERATION_TRACE=1`, prefix `native-operation-trace: `, existing retention flag `UT_DEBUG_PROCESS_HOST_FAILURES=1`.
- Produces: one native process identity/deadline/output pair, actual observed client/phase boundary or explicit inconclusive limit, measured exit/stop/cleanup status. No product fix or formal completion claim.

- [ ] **Step 1: Hash-bound preflight and necessary dependency preparation**

Verify actual Task 1 source/dist hashes and the unchanged original six protected source hashes; stop on drift. Index must remain empty. Do not turn on native target-membership tests, run full verify or SDK race.

Check existing service identity. Planning observed a 24,400,896-byte service with last-write 09:05:51 on 2026-10-09; `go version -m` did not provide vcs.revision evidence. A timestamp is not source proof. Unless the controller has a recorded matching source/executable hash binding by execution time, build only the required real service once, not all five unchanged fixture binaries:

```powershell
& 'D:/Program Files/Go/bin/go.exe' build -o build/unit-test-service.exe ./apps/test-service/cmd/unit-test-service
if ($LASTEXITCODE -ne 0) { throw 'Necessary service build failed; do not launch native' }
Get-FileHash -LiteralPath build/unit-test-service.exe -Algorithm SHA256
git diff --name-only -- apps/test-service go.mod go.sum
git diff c6b66881b0ace85d3bd820a70fd52477f5cf1e81 HEAD --name-only -- apps/test-service go.mod go.sum
```

Both Go source-diff lists must be empty for the delivered correction; otherwise escalate, do not reuse an assumed service identity. Record actual build output/exit/source SHA/executable hash if building, or exact recorded reuse authority if not. The real service remains dirty-candidate execution, not clean-SHA acceptance.

Use existing bundles `.superpowers/runtime/product-bundles/windows-x64/bundles/{cmake,coverage,testgen}`, prepared CMake manifest `5c574826f31d6d6b02dfd620b47dfd567edc934d8d4d22ff872fe7847aa87cf0`, LLVM22.1.8, singleton SDK10.0.26100.0/x64/winmm. Do not rebuild/re-download/re-lock native inputs or rerun metadata/configure gates.

- [ ] **Step 2: Record launch intent in this plan's ledger**

Controller records distinct run token/output paths and “not launched”, reviewed hashes, truthful current HEAD plus dirty state, 1,200s execution/15s cleanup budget and the fact that no second attempt is authorized. Use a fresh UTC timestamp token, not an old attempt/log. No runtime launch until Task 1 independent review is approved.

- [ ] **Step 3: Start the persistent owned-process supervisor once**

The following is a shell command to run through exec_command from the explicit workdir, not a new tracked source file. The supervisor stays alive in its one exec session across context recovery; it does not depend on model polling to enforce its deadline. Use an initial yield ≤10s and subsequent waits ≤60s. Return its actual session ID/output to the controller immediately, so the controller appends process/deadline identity without restarting it.

```powershell
$ErrorActionPreference = 'Stop'
$Root = 'C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest'
$Node = 'C:/Users/DELL/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin/node.exe'
$Entry = Join-Path $Root 'tools/service-probe/dist/native-test-generation.js'
$Service = [IO.Path]::GetFullPath((Join-Path $Root 'build/unit-test-service.exe'))
$ArtifactRoot = Join-Path $Root '.native-e2e/artifacts/windows'
$Token = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfffffffZ')
$Out = Join-Path $ArtifactRoot ('operation-trace-' + $Token + '.stdout.log')
$Err = Join-Path $ArtifactRoot ('operation-trace-' + $Token + '.stderr.log')
foreach ($Path in @($Node, $Entry, $Service, $ArtifactRoot)) {
  if (-not (Test-Path -LiteralPath $Path)) { throw 'Required diagnostic input absent' }
}
if ((Test-Path -LiteralPath $Out) -or (Test-Path -LiteralPath $Err)) { throw 'Diagnostic output collision' }
$Candidate = (git rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or $Candidate -cnotmatch '^[0-9a-f]{40}$') { throw 'Invalid dirty candidate SHA' }
if (@(git diff --cached --name-only).Count -ne 0) { throw 'Index unexpectedly staged' }
$SavedEnv = @{}
foreach ($Name in @('UTIDE_NATIVE_OPERATION_TRACE','UT_DEBUG_PROCESS_HOST_FAILURES','GITHUB_SHA','PATH','CMAKE')) {
  $SavedEnv[$Name] = [Environment]::GetEnvironmentVariable($Name, 'Process')
}
$Services = @{}
$Native = $null
$NativeIdentity = $null
$Outcome = 'inconclusive'
$Expired = $false
$StopLimitations = [Collections.Generic.List[string]]::new()
$Clock = [Diagnostics.Stopwatch]::StartNew()

function Get-OwnedIdentity([Diagnostics.Process]$Process, [string]$ExpectedPath) {
  $Process.Refresh()
  if ($Process.HasExited) { return $null }
  # Hold the actual process handle; do not stop a later PID-reused process.
  $HeldHandle = $Process.Handle
  $Path = [IO.Path]::GetFullPath($Process.MainModule.FileName)
  if (-not [string]::Equals($Path, $ExpectedPath, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'Owned process executable identity mismatch'
  }
  return [pscustomobject]@{ Process=$Process; Pid=$Process.Id; Path=$Path;
    StartTicks=$Process.StartTime.ToUniversalTime().Ticks }
}
function Stop-MatchingOwned($Identity) {
  if ($null -eq $Identity) { return }
  try {
    $Identity.Process.Refresh()
    if ($Identity.Process.HasExited) { return }
    $Current = Get-OwnedIdentity $Identity.Process $Identity.Path
    if ($null -eq $Current -or $Current.Pid -ne $Identity.Pid -or $Current.StartTicks -ne $Identity.StartTicks) {
      throw 'Owned process identity changed'
    }
    Stop-Process -InputObject $Identity.Process -ErrorAction Stop
  } catch { $StopLimitations.Add('identified-process-stop-unconfirmed') }
}
try {
  $env:UTIDE_NATIVE_OPERATION_TRACE = '1'
  $env:UT_DEBUG_PROCESS_HOST_FAILURES = '1'
  $env:GITHUB_SHA = $Candidate
  $env:PATH = 'C:/Users/DELL/AppData/Local/Temp/utide-pnpm-11.4.0;C:/Users/DELL/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin;D:/Program Files/Go/bin;' + $env:PATH
  $env:CMAKE = 'C:/codex_project/unitTest/.bundled-tools/cmake/4.3.4/win32-x64/cmake-4.3.4-windows-x86_64/bin/cmake.exe'
  $Native = Start-Process -FilePath $Node -ArgumentList @(('"' + $Entry + '"'),'--platform','win32') -WorkingDirectory $Root -WindowStyle Hidden -PassThru -RedirectStandardOutput $Out -RedirectStandardError $Err
  $NativeIdentity = Get-OwnedIdentity $Native ([IO.Path]::GetFullPath($Node))
  if ($null -eq $NativeIdentity) { throw 'Native exited before identity registration' }
  $StartUtc = $Native.StartTime.ToUniversalTime()
  $DeadlineUtc = $StartUtc.AddSeconds(1200)
  $InitialElapsed = [Math]::Max(0, ([DateTime]::UtcNow - $StartUtc).TotalSeconds)
  $InitialClock = $Clock.Elapsed.TotalSeconds
  Write-Output ([pscustomobject]@{ kind='diagnostic-start'; pid=$NativeIdentity.Pid;
    startUtc=$StartUtc.ToString('o'); deadlineUtc=$DeadlineUtc.ToString('o');
    executable=$NativeIdentity.Path; candidate=$Candidate; dirty=$true; stdout=$Out; stderr=$Err } | ConvertTo-Json -Compress)
  $NextMetadata = 0.0
  while (-not $Native.HasExited) {
    $Elapsed = $InitialElapsed + ($Clock.Elapsed.TotalSeconds - $InitialClock)
    if ($Elapsed -ge 1200) { $Expired=$true; $Outcome='budget-expired-inconclusive'; break }
    if ($Elapsed -ge $NextMetadata -and $Elapsed -lt 1197) {
      try {
        $Children = @(Get-CimInstance -ClassName Win32_Process -Property ProcessId,ParentProcessId,ExecutablePath,CreationDate -Filter ('ParentProcessId = ' + $Native.Id) -OperationTimeoutSec 2)
        foreach ($Child in $Children) {
          if ($null -eq $Child.ExecutablePath) { continue }
          if (-not [string]::Equals([IO.Path]::GetFullPath($Child.ExecutablePath), $Service, [StringComparison]::OrdinalIgnoreCase)) { continue }
          $Process = Get-Process -Id $Child.ProcessId -ErrorAction Stop
          $Identity = Get-OwnedIdentity $Process $Service
          if ($null -ne $Identity -and $Identity.StartTicks -ge $NativeIdentity.StartTicks) {
            $CimBirth = ([DateTime]$Child.CreationDate).ToUniversalTime().ToString('yyyyMMddHHmmssffffff')
            $HandleBirth = [DateTime]::new($Identity.StartTicks, [DateTimeKind]::Utc).ToString('yyyyMMddHHmmssffffff')
            if ($CimBirth -cne $HandleBirth -or $Child.ParentProcessId -ne $Native.Id) { continue }
            $Key = [string]$Identity.Pid + ':' + [string]$Identity.StartTicks
            if (-not $Services.ContainsKey($Key)) {
              $Services[$Key]=$Identity
              Write-Output ([pscustomobject]@{ kind='owned-service'; pid=$Identity.Pid;
                startTicks=$Identity.StartTicks; executable=$Identity.Path; parent=$Native.Id } | ConvertTo-Json -Compress)
            }
          }
        }
      } catch { $StopLimitations.Add('service-metadata-registration-incomplete') }
      $NextMetadata = $Elapsed + 10
    }
    [void]$Native.WaitForExit(500)
    $Native.Refresh()
  }
  if (-not $Expired) {
    $Native.WaitForExit()
    $Outcome = 'natural-exit'
    Write-Output ([pscustomobject]@{ kind=$Outcome; exitCode=$Native.ExitCode;
      elapsedSeconds=($InitialElapsed+$Clock.Elapsed.TotalSeconds-$InitialClock) } | ConvertTo-Json -Compress)
  } else {
    Write-Output ([pscustomobject]@{ kind=$Outcome; elapsedSeconds=$Elapsed; productVerdict='not-established' } | ConvertTo-Json -Compress)
  }
} catch {
  $Outcome='supervisor-inconclusive'
  $StopLimitations.Add('supervisor-metadata-or-launch-limit')
} finally {
  if ($null -ne $Native) {
    $CleanupClock = [Diagnostics.Stopwatch]::StartNew()
    if ($null -ne $NativeIdentity -and (-not $Native.HasExited)) { Stop-MatchingOwned $NativeIdentity }
    if ($null -eq $NativeIdentity -and (-not $Native.HasExited)) { $StopLimitations.Add('native-identity-unavailable-stop-not-authorized') }
    foreach ($Identity in $Services.Values) {
      if ($CleanupClock.Elapsed.TotalSeconds -ge 15) { $StopLimitations.Add('stop-confirmation-budget-expired'); break }
      Stop-MatchingOwned $Identity
    }
    do {
      $Alive = $false
      foreach ($Identity in @($NativeIdentity) + @($Services.Values)) {
        if ($null -eq $Identity) { continue }
        try { $Identity.Process.Refresh(); if (-not $Identity.Process.HasExited) { $Alive=$true } }
        catch { $Alive=$true }
      }
      if (-not $Alive) { break }
      Start-Sleep -Milliseconds 200
    } while ($CleanupClock.Elapsed.TotalSeconds -lt 15)
    if ($Alive) { $StopLimitations.Add('identified-process-exit-not-confirmed') }
  }
  foreach ($Name in $SavedEnv.Keys) {
    [Environment]::SetEnvironmentVariable($Name, $SavedEnv[$Name], 'Process')
  }
  Write-Output ([pscustomobject]@{ kind='diagnostic-disposition'; outcome=$Outcome;
    expired=$Expired; limitations=@($StopLimitations | Select-Object -Unique);
    stdout=$Out; stderr=$Err; descendantsCleanup='not-established' } | ConvertTo-Json -Compress)
}
```

This uses only the registered process handle and direct service lineage, not command lines or a tree kill. A metadata or identity failure is explicitly inconclusive. Metadata calls have a two-second operation cap and are not issued in the last three seconds; record actual observed expiry/stop times, never claim real-time OS scheduling precision. The supervisor already runs independently of model context; never reset its monotonic epoch. Do not substitute an unbounded `Start-Sleep` or add a second launcher.

- [ ] **Step 4: Consume authentic trace and distinguish natural result from external stop**

Owner of the single supervisor exec session consumes its full output; another agent must not drain it. Between waits, controller records only meaningful safe phase changes and gives concise user updates. Read only bounded trace-prefixed lines, never READY/auth/DB or arbitrary service stdout:

```powershell
Get-Content -LiteralPath $RecordedStderrPath -Tail 200 |
  Where-Object { $_.StartsWith('native-operation-trace: ') } |
  Select-Object -Last 12
Get-FileHash -LiteralPath $RecordedStdoutPath, $RecordedStderrPath -Algorithm SHA256
```

Here `$RecordedStdoutPath`/`$RecordedStderrPath` are the exact returned launch paths written in this plan's ledger, not guessed or newly created files. Verify their resolved paths remain inside this worktree's artifact directory. To locate the first obstruction, compare its before/returned/threw sequence and pending snapshots; do not infer the method from file creation order alone.

If a natural exit occurs, record real exit/log/receipt identities; do not require an error merely because the run was diagnostic. If authentic full dirty receipts completed, retain their original assertion/identity limits and hand off without another run. If expired, record the actual last pending method/phase, call-cohort/count evidence, termination request/result, cleanup limitations and absent receipts. If trace is inconclusive or cannot establish a boundary, stop; no retry or speculative product fix.

- [ ] **Step 5: Evidence review and explicit controller return to original W4**

Write a full Task 2 report: reviewed starting hashes, dependency build/reuse provenance, actual Node/service identities/deadline, execution/cleanup times, stdout/stderr hashes and retained attempt path, authentic selected trace, method/phase evidence limits, actual receipt presence/absence and interpretation. No fabricated launch/error/time/skip. Return only disposition/report path/one-line boundary/concerns to the controller.

Independent reviewer checks evidence classification and preserved source hashes, not a repeated native execution. Controller resolves cannot-verify items and new scope implications. After completing this diagnostic disposition, controller explicitly switches back to the original Windows plan, reads its owned recovery map and appends the real boundary/remaining blocker. Original W4 stays incomplete until its original dual-framework maintenance/coverage, race/global, source/clean-SHA and task/whole-PR gates actually pass.

No final whole-PR review, remote change, merge, signing or Release is authorized by this two-task diagnostic plan. The final review for this plan is bounded to its trace and evidence, on the most capable reviewer as required by the chosen execution skill; preserve audit artifacts until the original full-feature gate, and do not bypass any earlier cleanup-policy rejection.
