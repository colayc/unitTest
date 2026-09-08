import assert from "node:assert/strict";
import { execFile as execCallback, spawn } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { once } from "node:events";
import { createConnection } from "node:net";
import { cp, lstat, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import test from "node:test";
import { promisify } from "node:util";
import { pathToFileURL } from "node:url";
import { ProtocolClient, ProtocolError, TestSelectionModeV14, type CoverageRun, type WorkspaceSnapshot, type ProtocolArtifactMetadata, type ProtocolTaskEvent, EventSubscription } from "@unit-test-ide/test-client";
import { decodeCoverageDocumentV1 } from "@unit-test-ide/coverage-models";
import { ServiceManager } from "../src/service-manager.js";
import { createCoverageController } from "../src/coverage-controller.js";
import { openCoverageHtml } from "../src/coverage-viewer.js";
import { redactServiceError } from "../src/service-resources.js";
import { buildLinuxGccCoverageEvidence, parseStrictJUnit, publishEvidenceAtomically, type LinuxGccCoverageCaseEvidence, type LinuxGccFaultEvidence, type TestOnlyCoverageFault } from "./coverage-service-smoke-support.js";
import { assertLinuxCoveragePresentation, createGccFaultOverlay } from "./coverage-service-smoke-linux-support.js";

const execFile = promisify(execCallback);
const root = resolve(import.meta.dirname, "../../../..");
const timeout = 300_000;
const projectId = "coverage-fixture";
const coverageProfileId = "coverage-gcc";
const evidencePath = join(root, ".native-e2e/artifacts/linux/coverage-execution-report.json");
const delay = (ms: number) => new Promise<void>((done) => setTimeout(done, ms));
const digest = (bytes: Uint8Array | string) => createHash("sha256").update(bytes).digest("hex");
type Framework = "cpputest" | "unity";
type Selected = ReturnType<typeof selectGcc>;

function selectGcc(snapshot: WorkspaceSnapshot) {
  const project = snapshot.projects.find((item) => item.projectId === projectId);
  const choices = snapshot.toolchains.filter((tool) => tool.family === "gcc" && tool.hostArchitecture === "x64" && tool.targetArchitecture === "x64" && tool.capabilities.coverageDrivers.some((driver) => driver === "gcov"))
    .sort((a, b) => a.toolchainId.localeCompare(b.toolchainId, "en"));
  for (const toolchain of choices) {
    const profile = project?.buildProfiles.find((item) => item.toolchainId === toolchain.toolchainId && item.generator === "Ninja" && item.configuration === "Debug" && item.origin === "generated");
    if (profile) return { snapshot, toolchain, profile };
  }
  throw new Error("verified GCC/gcov Debug Ninja profile is unavailable");
}

async function selectGccEventually(client: ProtocolClient): Promise<Selected> {
  const deadline = Date.now() + 120_000;
  let lastSnapshot: WorkspaceSnapshot | undefined;
  let lastError: unknown;
  for (;;) {
    lastSnapshot = await client.inspectWorkspace();
    try {
      return selectGcc(lastSnapshot);
    } catch (error) {
      lastError = error;
      if (Date.now() >= deadline) {
        const project = lastSnapshot.projects.find((item) => item.projectId === projectId);
        const toolchains = lastSnapshot.toolchains.map((item) => ({ id: item.toolchainId, family: item.family, host: item.hostArchitecture, target: item.targetArchitecture, generators: item.generators, coverage: item.capabilities.coverageDrivers }));
        const profiles = project?.buildProfiles.map((item) => ({ id: item.buildProfileId, origin: item.origin, generator: item.generator, configuration: item.configuration, toolchainId: item.toolchainId })) ?? [];
        throw new Error(`verified GCC/gcov Debug Ninja profile is unavailable after bounded discovery wait; toolchains=${JSON.stringify(toolchains)} profiles=${JSON.stringify(profiles)}: ${lastError instanceof Error ? lastError.message : String(lastError)}`);
      }
      await delay(1000);
    }
  }
}

function collectTaskOutput(subscription: EventSubscription) {
  const output = new Map<string, string>();
  const pump = (async () => {
    for await (const event of subscription) {
      const previous = output.get(event.taskId) ?? "";
      if (event.event === "task.output") {
        const text = (event as ProtocolTaskEvent & { payload: { text?: unknown } }).payload.text;
        if (typeof text === "string") output.set(event.taskId, previous.length >= 32_768 ? previous : `${previous}${text}`.slice(0, 32_768));
      } else if (event.event === "task.diagnostic") {
        const diagnostic = (event as ProtocolTaskEvent & { payload: { diagnostic?: { code?: unknown; message?: unknown } } }).payload.diagnostic;
        if (diagnostic && typeof diagnostic.code === "string" && typeof diagnostic.message === "string") {
          const detail = `diagnostic=${diagnostic.code}: ${diagnostic.message}`;
          output.set(event.taskId, previous.length >= 32_768 ? previous : `${previous}${previous ? "\n" : ""}${detail}`.slice(0, 32_768));
        }
      }
    }
  })();
  return { output, async close() { subscription.close(); await pump; } };
}

async function taskFinished(client: ProtocolClient, id: string, label = "native task", output?: Map<string, string>) {
  const deadline = Date.now() + timeout;
  for (;;) {
    const task = await client.getTask(id);
    if (task.status === "finished") {
      if (task.outcome !== "succeeded") {
        const detail = task.errorMessage ? `: ${task.errorMessage}` : "";
        const commandOutput = output?.get(id);
        throw new Error(`${label} finished with outcome ${task.outcome ?? "unknown"}${task.errorCode ? ` (${task.errorCode})` : ""}${detail}${commandOutput ? `; output=${commandOutput}` : ""}`);
      }
      return task;
    }
    if (Date.now() >= deadline) throw new Error("native task completion timeout");
    await delay(100);
  }
}

async function coverageFinished(client: ProtocolClient, id: string): Promise<CoverageRun> {
  const deadline = Date.now() + timeout;
  for (;;) {
    const run = await client.getCoverageRun(id);
    if (run.status === "finished") return run;
    if (Date.now() >= deadline) throw new Error("native coverage completion timeout");
    await delay(100);
  }
}

async function config(workspace: string, framework: Framework, base?: string) {
  await mkdir(join(workspace, ".unit-test-ide"), { recursive: true });
  await writeFile(join(workspace, ".unit-test-ide/workspace.json"), JSON.stringify({
    version: 3,
    projects: [{ id: projectId, sourceDir: ".", fallback: { configurations: ["Debug"], preferredGenerator: "Ninja" }, tests: { containers: [{ ctestName: framework === "unity" ? "coverage-unity-tests" : "coverage-tests", framework }] } }],
    ...(base ? { coverageProfiles: [{ id: coverageProfileId, baseBuildProfileId: base, include: ["src/**"], exclude: ["test/**"] }] } : {})
  }));
}

/** No shell interpolation: CMake bracket arguments are closed over verified paths. */
function cmakePath(value: string): string {
  assert.ok(value.startsWith("/") && value === resolve(value) && !/[\r\n\0]/u.test(value) && !value.includes("]=]"));
  return `[=[${value}]=]`;
}

async function injectFixtureFault(workspace: string, fault: TestOnlyCoverageFault): Promise<string | undefined> {
  if (fault === "missing-data" || fault === "malformed-pinned-json") return undefined;
  const marker = join(workspace, "test-only-invocation-started");
  const body = fault === "crash" ? "if (__gcov_dump) __gcov_dump(); raise(SIGSEGV);" : "sleep(240);";
  // Compile-time fixture seam, not environment/Workspace/Protocol configuration.
  const preamble = `#include <signal.h>\n#include <stdio.h>\n#include <unistd.h>\nextern void __gcov_dump(void) __attribute__((weak));\nstatic void test_only_fault(void) { FILE *f = fopen(${JSON.stringify(marker)}, "w"); if (f) { fputs("started", f); fclose(f); } ${body} }\n`;
  const path = join(workspace, "test/test_math.c");
  const source = await readFile(path, "utf8");
  const needle = "void test_covers_positive_branch(void) {";
  assert.equal(source.split(needle).length, 2);
  await writeFile(path, preamble + source.replace(needle, `${needle}\n test_only_fault();`));
  return marker;
}

async function artifacts(client: ProtocolClient, run: CoverageRun, framework: Framework, selected: Selected, catalogRevision: string, partial = false) {
  assert.ok(run.reportId);
  const report = await client.getCoverageReport(run.reportId);
  const metadata: ProtocolArtifactMetadata[] = [];
  let cursor: string | undefined;
  do {
    const page = await client.listArtifacts(run.taskId, { limit: 200, ...(cursor ? { cursor } : {}) });
    metadata.push(...page.items); cursor = page.nextCursor;
  } while (cursor !== undefined);
  const data = new Map<string, Uint8Array>();
  for (const kind of ["coverage-json", "junit-xml", "coverage-html"] as const) {
    const matches = metadata.filter((item) => item.kind === kind);
    assert.equal(matches.length, 1);
    const item = matches[0]!;
    // readArtifact owns the v1.4 offset/chunk/total size/digest protocol checks.
    const bytes = await client.readArtifact(item.artifactId);
    assert.equal(bytes.byteLength, item.sizeBytes);
    assert.equal(digest(bytes), item.sha256);
    if (kind === "coverage-json") assert.equal(item.artifactId, report.artifactId);
    data.set(kind, bytes);
  }
  const bytes = data.get("coverage-json")!;
  const document = decodeCoverageDocumentV1(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes)));
  assert.deepEqual(document.summary, report.summary);
  assert.deepEqual(document.provenance, report.toolProvenance);
  assert.equal(document.provenance.platform, "linux");
  assert.equal(document.provenance.architecture, "x64");
  assert.equal(document.provenance.compiler.family, "gcc");
  assert.equal(document.provenance.compiler.version, selected.toolchain.version);
  assert.equal(document.provenance.driver.name, "gcov");
  assert.equal(document.provenance.driver.version, selected.toolchain.version);
  assert.deepEqual(document.provenance.collector, { name: "gcovr", version: "8.6" });
  assert.deepEqual(document.files.map((item) => item.uri), [framework === "unity" ? "src/math.c" : "src/math.cpp"]);
  const junit = parseStrictJUnit(data.get("junit-xml")!);
  assert.equal(junit.tests, 2);
  if (!partial) assert.deepEqual(junit, { tests: 2, failures: framework === "cpputest" ? 1 : 0, errors: 0, skipped: 0 });
  let html = "";
  await openCoverageHtml({ openCoverageHtml: (value) => { html = value; } }, { kind: "coverage-html", bytes: data.get("coverage-html")! });
  assert.match(html, /Content-Security-Policy/u);
  assert.match(html, /default-src 'none'/u);
  assert.match(html, /Completeness:/u);
  for (const file of document.files) { assert.ok(html.includes(file.uri)); assert.ok(html.includes(file.sha256)); }
  assert.doesNotMatch(html, /https?:\/\//iu);
  const controller = createCoverageController({ readContext: () => ({ trust: "trusted", client, serviceRunning: true, workspaceGeneration: selected.snapshot.workspaceGeneration, catalog: { projectId, profileId: selected.profile.buildProfileId, revision: catalogRevision, workspaceGeneration: selected.snapshot.workspaceGeneration }, coverageProfileId }) });
  try {
    const state = await controller.refresh(run.coverageRunId);
    assertLinuxCoveragePresentation(state, run.outcome, report.completeness, partial);
    assert.equal(state.reportId, report.reportId);
    assert.deepEqual(state.summary, report.summary);
  } finally { controller.dispose(); }
  return { bytes, document, report, data };
}

test("real offline Protocol v1.4 Linux GCC CppUTest/Unity coverage and fault mappings", { skip: process.platform !== "linux" ? "Linux-native smoke requires Linux" : false, timeout: 30 * 60_000 }, async () => {
  await rm(evidencePath, { force: true });
  const startedAt = new Date().toISOString();
  const bundleInput = process.env.UNIT_TEST_IDE_TEST_COVERAGE_BUNDLE_ROOT;
  const lockedBundle = join(root, ".superpowers/runtime/coverage-bundle/linux-x64");
  assert.equal(bundleInput, lockedBundle, "an explicit, exact locked test-only coverage bundle input is required");
  // Real namespace/socket/DNS proof; an environment variable cannot bypass it.
  await execFile(process.execPath, [join(root, "tools/linux-offline/probe.mjs")], { timeout: 30_000 });
  await execFile(process.execPath, [join(root, "tools/coverage-bundle/prepare.mjs"), "--check"], { cwd: root, timeout });
  const buildRoot = join(root, "build");
  await mkdir(buildRoot, { recursive: true });
  const scratch = await mkdtemp(join(buildRoot, "linux-gcc-smoke-"));
  const secret = `linux-gcc-smoke-${randomBytes(16).toString("hex")}`;
  const sensitive = [scratch, lockedBundle, secret, root];
  let manager: ServiceManager | undefined;
  try {
    const go = process.env.UNIT_TEST_IDE_GO_EXECUTABLE || "go";
    const goEnv = { ...process.env, GOENV: "off", GOTOOLCHAIN: "local" };
    const service = join(scratch, "unit-test-service");
    const generator = join(scratch, "unity-runner-generator");
    await execFile(go, ["build", "-trimpath", "-o", service, "./apps/test-service/cmd/unit-test-service"], { cwd: root, env: goEnv, timeout });
    await execFile(go, ["build", "-trimpath", "-o", generator, "./apps/test-service/cmd/unity-runner-generator"], { cwd: root, env: goEnv, timeout });
    const { prepareLinuxFrameworkInputs } = await import(pathToFileURL(join(root, "tools/service-probe/dist/linux-framework-inputs.js")).href);
    const frameworkBoundary = await prepareLinuxFrameworkInputs({
      manifest: JSON.parse(await readFile(join(root, "tools/framework-bundle/manifest.json"), "utf8")), cacheRoot: join(root, ".superpowers/cache/framework-bundle"), sourceRoot: join(root, ".superpowers/runtime/framework-bundle/linux-x64"), helperPath: join(root, "sdk/cmake/UnitTestIDE.cmake"), generatorPath: generator, repositoryRoot: root
    }) as { identityDigest: string; environment: Record<string, string> };
    const inputs = frameworkBoundary.environment;
    await mkdir(join(scratch, "bundles"));
    await cp(lockedBundle, join(scratch, "bundles/coverage"), { recursive: true, force: false, errorOnExist: true });
    const bundleDigest = digest(await readFile(join(scratch, "bundles/coverage/manifest.resolved.json")));
    const faultServices = new Map<string, string>();
    for (const fault of ["missing-data", "malformed-pinned-json"] as const) {
      const original = join(root, "apps/test-service/internal/runtime/coverage_execution.go");
      const replacement = join(scratch, `${fault}.go`);
      const overlay = join(scratch, `${fault}.json`);
      await writeFile(replacement, createGccFaultOverlay(await readFile(original, "utf8"), fault));
      await writeFile(overlay, JSON.stringify({ Replace: { [original]: replacement } }));
      const binary = join(scratch, `unit-test-service-${fault}`);
      await execFile(go, ["build", "-trimpath", "-overlay", overlay, "-o", binary, "./apps/test-service/cmd/unit-test-service"], { cwd: root, env: goEnv, timeout });
      faultServices.set(fault, binary);
    }
    const cases: LinuxGccCoverageCaseEvidence[] = [];
    const faults: LinuxGccFaultEvidence[] = [];
    let unityBytes: Uint8Array | undefined;
    let toolchainDigest = "";
    for (const scenario of ["cpputest", "unity", "crash", "timeout", "cancel", "missing-data", "malformed-pinned-json"] as const) {
      const framework: Framework = scenario === "cpputest" ? "cpputest" : "unity";
      const fault = scenario === "cpputest" || scenario === "unity" ? undefined : scenario;
      const workspace = join(scratch, scenario);
      await cp(join(root, "apps/code-oss-extension/test/fixtures", framework === "unity" ? "coverage-unity" : "coverage"), workspace, { recursive: true });
      await writeFile(join(workspace, "linux-inputs.cmake"), [
        `set(UTIDE_TEST_CPPUTEST_ROOT ${cmakePath(inputs.UNIT_TEST_IDE_TEST_CPPUTEST_ROOT!)})`,
        `set(UTIDE_TEST_UNITY_ROOT ${cmakePath(inputs.UNIT_TEST_IDE_TEST_UNITY_ROOT!)})`,
        `set(UTIDE_TEST_CMAKE_HELPER ${cmakePath(inputs.UNIT_TEST_IDE_TEST_CMAKE_HELPER!)})`,
        `set(UTIDE_UNITY_RUNNER_GENERATOR ${cmakePath(inputs.UNIT_TEST_IDE_TEST_UNITY_RUNNER_GENERATOR!)})`, ""
      ].join("\n"));
      const marker = fault ? await injectFixtureFault(workspace, fault) : undefined;
      await config(workspace, framework);
      let wire: Buffer[] = [];
      let wireSize = 0;
      let wireOverflow = false;
      const recordWire = (value: Uint8Array | string) => {
        const bytes = Buffer.from(value); wireSize += bytes.length;
        // Never throw out of a socket event handler; the awaited test boundary
        // below fails closed and retains normal Service teardown ownership.
        if (wireSize > 64 * 1024 * 1024) { wireOverflow = true; return; }
        wire.push(bytes);
      };
      manager = new ServiceManager({ serviceExecutable: faultServices.get(scenario) ?? service, workspaceRoot: workspace, dataDirectory: join(scratch, `data-${scenario}`), timeoutMs: 120_000, trusted: () => true, operations: {
        spawnService(binary, args) { return spawn(binary, [...args, "--cmake-bundle-root", join(root, ".bundled-tools/cmake")], { stdio: "pipe", env: { ...process.env, UNIT_TEST_IDE_COVERAGE_SMOKE_SECRET: secret, UT_DEBUG_PROCESS_HOST_FAILURES: "1" } }); },
        async connect(endpoint) {
          const socket = createConnection(endpoint);
          const write = socket.write.bind(socket) as unknown as (...args: unknown[]) => boolean;
          socket.write = ((chunk: unknown, ...args: unknown[]) => {
            if (typeof chunk === "string" || chunk instanceof Uint8Array) recordWire(chunk);
            return write(chunk, ...args);
          }) as typeof socket.write;
          socket.on("data", recordWire);
          try { await once(socket, "connect"); } catch (error) { socket.destroy(); throw error; }
          return ProtocolClient.attach(socket);
        }
      } });
      const session = await manager.start();
      sensitive.push(session.endpoint, session.tokenFile, session.sessionDirectory);
      assert.ok((await lstat(session.endpoint)).isSocket(), "Service must expose a real Unix socket");
      const client = session.client;
      const caps = await client.getCapabilities();
      assert.ok("coverageRun" in caps && caps.coverageRun && "coverageReport" in caps && caps.coverageReport);
      let selected = await selectGccEventually(client);
      await config(workspace, framework, selected.profile.buildProfileId);
      for (let attempt = 0; ; attempt++) {
        selected = await selectGccEventually(client);
        const outputSubscription = await client.subscribeEvents(0);
        const taskOutput = collectTaskOutput(outputSubscription);
        try {
          const build = await client.startCMakeBuild({ idempotencyKey: randomBytes(16).toString("hex"), workspaceGeneration: selected.snapshot.workspaceGeneration, projectId, buildProfileId: selected.profile.buildProfileId, targetIds: [], jobs: 2, timeoutMs: timeout });
          await taskFinished(client, build.taskId, `${scenario} build`, taskOutput.output); break;
        } catch (error) { if (!(error instanceof ProtocolError) || error.code !== "WORKSPACE_CHANGED" || attempt >= 1) throw error; }
        finally { await taskOutput.close(); }
      }
      selected = await selectGccEventually(client);
      const discovery = await client.discoverTests({ idempotencyKey: randomBytes(16).toString("hex"), projectId, profileId: selected.profile.buildProfileId });
      await taskFinished(client, discovery.taskId, `${scenario} discovery`);
      const catalog = await client.getTestCatalog({ projectId, profileId: selected.profile.buildProfileId, limit: 100 });
      assert.equal(catalog.partial, false);
      assert.equal(catalog.items.filter((item) => item.kind === "case").length, 2);
      selected = await selectGccEventually(client);
      const currentDigest = digest(JSON.stringify({ toolchainId: selected.toolchain.toolchainId, version: selected.toolchain.version }));
      if (!toolchainDigest) toolchainDigest = currentDigest;
      assert.equal(currentDigest, toolchainDigest);
      for (let repeat = 0; repeat < (scenario === "unity" ? 2 : 1); repeat++) {
        // Workspace inspection legitimately includes source URIs; only the
        // coverage/run/report/artifact exchange is subject to this leak gate.
        wire = []; wireSize = 0; wireOverflow = false;
        const initial = await client.startCoverage({ idempotencyKey: randomBytes(16).toString("hex"), workspaceGeneration: selected.snapshot.workspaceGeneration, projectId, coverageProfileId, catalogRevision: catalog.revision, selection: { mode: TestSelectionModeV14.All }, repeatCount: 1, timeoutMs: fault === "timeout" ? 120_000 : timeout });
        if (fault === "cancel") {
          assert.ok(marker);
          const deadline = Date.now() + timeout;
          while (!(await lstat(marker).catch(() => undefined))?.isFile()) {
            assert.notEqual((await client.getCoverageRun(initial.coverageRunId)).status, "finished", "cancel fixture must enter test execution");
            if (Date.now() >= deadline) throw new Error("cancel fixture never entered test execution");
            await delay(50);
          }
          await client.cancelTask(initial.taskId);
        }
        const run = await coverageFinished(client, initial.coverageRunId);
        const testRun = await client.getTestRun(run.testRunId);
        assert.equal(testRun.status, "completed");
        if (fault === "cancel" || fault === "timeout") {
          assert.ok(marker && (await lstat(marker)).isFile(), "timeout/cancel must reach a real native test invocation");
          assert.equal(run.outcome, "cancelled");
          assert.equal(run.reason, fault === "cancel" ? "user_cancelled" : "task_timed_out");
          assert.equal(testRun.outcome, fault === "cancel" ? "cancelled" : "timed_out");
          assert.equal(run.reportId, undefined);
        } else if (fault === "missing-data" || fault === "malformed-pinned-json") {
          assert.equal(run.outcome, "unavailable");
          assert.equal(run.reason, fault === "missing-data" ? "profile_collection_failed" : "normalization_failed");
          assert.equal(testRun.outcome, "passed");
          assert.equal(run.reportId, undefined);
        } else {
          assert.equal(run.outcome, fault === "crash" ? "partial" : "available");
          assert.equal(run.reason, undefined);
          assert.equal(testRun.outcome, fault === "crash" ? "errored" : framework === "cpputest" ? "failed" : "passed");
          const result = await artifacts(client, run, framework, selected, catalog.revision, fault === "crash");
          if (fault === "crash") assert.ok(result.report.completeness.reasons.some((reason) => reason === "test_crashed"));
          for (const bytes of result.data.values()) for (const value of sensitive) assert.ok(!Buffer.from(bytes).includes(Buffer.from(value)), "artifact leaked a private execution value");
          if (!fault && repeat === 0) cases.push({ framework, testRunOutcome: framework === "cpputest" ? "failed" : "passed", coverageRunOutcome: "available", reportOutcome: "available", summary: result.document.summary, artifactDigest: digest(result.bytes) });
          if (scenario === "unity") {
            if (repeat === 0) unityBytes = result.bytes;
            else { assert.deepEqual(result.bytes, unityBytes, "equivalent successful runs must be byte-identical"); assert.equal(digest(result.bytes), digest(unityBytes!)); }
          }
        }
        assert.equal(wireOverflow, false, "bounded coverage wire capture exceeded");
        const publicWire = Buffer.concat(wire);
        for (const value of sensitive) assert.ok(!publicWire.includes(Buffer.from(value)), "coverage Protocol exchange leaked a private execution value");
        if (fault) faults.push({ fault, testRunOutcome: testRun.outcome!, coverageRunOutcome: run.outcome!, reason: run.reason ?? "none" });
      }
      await manager.stop(); manager = undefined;
    }
    const evidence = buildLinuxGccCoverageEvidence({ schemaVersion: 1, platform: "linux-x64", toolchain: { family: "gcc", digest: toolchainDigest }, bundleDigest, frameworkBundleDigest: frameworkBoundary.identityDigest, cases, faults, determinism: { coverageJsonByteIdentical: true, sha256Identical: true }, startedAt, finishedAt: new Date().toISOString() });
    const bytes = Buffer.from(`${JSON.stringify(evidence)}\n`);
    await rm(scratch, { recursive: true, force: true });
    await publishEvidenceAtomically(evidencePath, bytes);
  } catch (error) { throw redactServiceError(error, sensitive); }
  finally { try { await manager?.stop(); } finally { await rm(scratch, { recursive: true, force: true }); } }
});
