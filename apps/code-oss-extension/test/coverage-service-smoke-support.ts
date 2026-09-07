import { randomBytes } from "node:crypto";
import {
  lstat,
  mkdir,
  open,
  readFile,
  rename,
  rm
} from "node:fs/promises";
import { dirname, isAbsolute, join } from "node:path";
import {
  validateWfpOfflineReport,
  type WfpOfflineReport
} from "@unit-test-ide/service-probe/coverage-bundle";

const XML_DECLARATION = '<?xml version="1.0" encoding="UTF-8"?>';
const MAX_JUNIT_BYTES = 64 * 1024 * 1024;
const TEST_ID = /^utid-v1-[0-9a-f]{64}$/u;
const ITERATED_TEST_ID = /^utid-v1-[0-9a-f]{64}#([2-9][0-9]*)$/u;

export interface ParsedJUnit {
  readonly tests: number;
  readonly failures: number;
  readonly errors: number;
  readonly skipped: number;
}

export interface EvidencePublishOptions {
  readonly readBack?: (path: string) => Promise<Uint8Array>;
}

export interface LinuxGccCoverageEvidence {
  readonly schemaVersion: 1;
  readonly platform: "linux-x64";
  readonly toolchain: { readonly family: "gcc"; readonly digest: string };
  readonly bundleDigest: string;
  readonly frameworkBundleDigest: string;
  readonly faults: readonly LinuxGccFaultEvidence[];
  readonly cases: readonly LinuxGccCoverageCaseEvidence[];
  readonly determinism: {
    readonly coverageJsonByteIdentical: true;
    readonly sha256Identical: true;
  };
  readonly startedAt: string;
  readonly finishedAt: string;
}

export interface LinuxGccFaultEvidence {
  readonly fault: TestOnlyCoverageFault;
  readonly testRunOutcome: string;
  readonly coverageRunOutcome: string;
  readonly reason: string;
}

export interface LinuxGccCoverageCaseEvidence {
  readonly framework: "cpputest" | "unity";
  readonly testRunOutcome: "failed" | "passed";
  readonly coverageRunOutcome: "available";
  readonly reportOutcome: "available";
  readonly summary: {
    readonly lines: CoverageMetricEvidence;
    readonly branches: CoverageMetricEvidence;
    readonly functions: CoverageMetricEvidence;
  };
  readonly artifactDigest: string;
}

export interface CoverageMetricEvidence {
  readonly covered: number;
  readonly total: number;
}

/** Test-only fault seam; it is intentionally absent from Workspace/Protocol schemas. */
export type TestOnlyCoverageFault =
  | "crash"
  | "timeout"
  | "cancel"
  | "missing-data"
  | "malformed-pinned-json";

export type CoverageToolsetPreflight =
  | { readonly status: "unavailable"; readonly digest: string }
  | { readonly status: "verified"; readonly version: string; readonly digest: string };

export interface CoverageToolsetPreflightGate<Boundary, Result> {
  readonly required: boolean;
  readonly preflight: () => Promise<CoverageToolsetPreflight>;
  readonly skip: (message: string) => void;
  readonly installBoundary: () => Promise<Boundary>;
  readonly execute: (
    boundary: Boundary,
    toolset: Extract<CoverageToolsetPreflight, { readonly status: "verified" }>
  ) => Promise<Result>;
}

const COVERAGE_TOOLSET_SKIP = "SKIP: verified clang-cl coverage toolset is unavailable";
const SHA256 = /^[0-9a-f]{64}$/u;
const ISO_TIMESTAMP = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/u;
const SENSITIVE_EVIDENCE_KEY = /(?:path|env|argv|secret|token|password|cwd|command|executable|endpoint)/iu;
const NATIVE_PATH = /(?:^[A-Za-z]:[\\/]|^\\\\|^\/|file:\/{2,})/iu;
const TEST_ONLY_COVERAGE_FAULTS = new Set<TestOnlyCoverageFault>([
  "crash", "timeout", "cancel", "missing-data", "malformed-pinned-json"
]);
const FIXTURE_METRICS: Readonly<Record<LinuxGccCoverageCaseEvidence["framework"], LinuxGccCoverageCaseEvidence["summary"]>> = {
  cpputest: {
    lines: { covered: 7, total: 8 },
    branches: { covered: 3, total: 4 },
    functions: { covered: 2, total: 2 }
  },
  unity: {
    lines: { covered: 6, total: 6 },
    branches: { covered: 2, total: 2 },
    functions: { covered: 2, total: 2 }
  }
};

/** Builds the closed, path-free Linux native smoke evidence payload. */
export function buildLinuxGccCoverageEvidence(
  input: LinuxGccCoverageEvidence
): LinuxGccCoverageEvidence {
  validateLinuxGccCoverageEvidence(input);
  return {
    schemaVersion: 1,
    platform: "linux-x64",
    toolchain: { family: "gcc", digest: input.toolchain.digest },
    bundleDigest: input.bundleDigest,
    frameworkBundleDigest: input.frameworkBundleDigest,
    faults: input.faults.map((entry) => ({ ...entry })),
    cases: input.cases.map((entry) => ({
      framework: entry.framework,
      testRunOutcome: entry.testRunOutcome,
      coverageRunOutcome: entry.coverageRunOutcome,
      reportOutcome: entry.reportOutcome,
      summary: {
        lines: { ...entry.summary.lines },
        branches: { ...entry.summary.branches },
        functions: { ...entry.summary.functions }
      },
      artifactDigest: entry.artifactDigest
    })),
    determinism: { ...input.determinism },
    startedAt: input.startedAt,
    finishedAt: input.finishedAt
  };
}

/** Rejects schema drift and values that could disclose native execution inputs. */
export function validateLinuxGccCoverageEvidence(value: unknown): asserts value is LinuxGccCoverageEvidence {
  const evidence = closedObject(value, [
    "schemaVersion", "platform", "toolchain", "bundleDigest", "frameworkBundleDigest", "faults", "cases", "determinism", "startedAt", "finishedAt"
  ], "evidence");
  if (evidence.schemaVersion !== 1 || evidence.platform !== "linux-x64") {
    throw linuxEvidenceError("has an invalid identity");
  }
  const toolchain = closedObject(evidence.toolchain, ["family", "digest"], "toolchain");
  if (toolchain.family !== "gcc" || !isDigest(toolchain.digest) || !isDigest(evidence.bundleDigest) || !isDigest(evidence.frameworkBundleDigest)) {
    throw linuxEvidenceError("has an invalid toolchain or bundle digest");
  }
  const mappings: Record<TestOnlyCoverageFault, readonly string[]> = {
    crash: ["errored", "partial", "none"],
    timeout: ["timed_out", "cancelled", "task_timed_out"],
    cancel: ["cancelled", "cancelled", "user_cancelled"],
    "missing-data": ["passed", "unavailable", "profile_collection_failed"],
    "malformed-pinned-json": ["passed", "unavailable", "normalization_failed"]
  };
  if (!Array.isArray(evidence.faults) || evidence.faults.length !== 5) throw linuxEvidenceError("must prove all five fault mappings");
  const seenFaults = new Set<string>();
  for (const value of evidence.faults) {
    const entry = closedObject(value, ["fault", "testRunOutcome", "coverageRunOutcome", "reason"], "fault");
    if (typeof entry.fault !== "string" || !Object.hasOwn(mappings, entry.fault) || seenFaults.has(entry.fault)) throw linuxEvidenceError("has an invalid fault mapping");
    const expected = mappings[entry.fault as TestOnlyCoverageFault];
    if (entry.testRunOutcome !== expected[0] || entry.coverageRunOutcome !== expected[1] || entry.reason !== expected[2]) throw linuxEvidenceError("has an invalid fault mapping");
    seenFaults.add(entry.fault);
  }
  if (!Array.isArray(evidence.cases) || evidence.cases.length !== 2) {
    throw linuxEvidenceError("must contain exactly the CppUTest and Unity cases");
  }
  const frameworks = new Set<string>();
  for (const entry of evidence.cases) {
    const item = closedObject(entry, [
      "framework", "testRunOutcome", "coverageRunOutcome", "reportOutcome", "summary", "artifactDigest"
    ], "case");
    const framework = item.framework;
    if (framework !== "cpputest" && framework !== "unity") {
      throw linuxEvidenceError("has an invalid framework outcome");
    }
    if (
      (framework === "cpputest" && item.testRunOutcome !== "failed") ||
      (framework === "unity" && item.testRunOutcome !== "passed") ||
      item.coverageRunOutcome !== "available" || item.reportOutcome !== "available" ||
      !isDigest(item.artifactDigest) || frameworks.has(framework)
    ) {
      throw linuxEvidenceError("has an invalid framework outcome");
    }
    frameworks.add(framework);
    const summary = closedObject(item.summary, ["lines", "branches", "functions"], "summary");
    for (const metric of [summary.lines, summary.branches, summary.functions]) validateMetric(metric);
    if (!sameMetrics(summary, FIXTURE_METRICS[framework])) {
      throw linuxEvidenceError("does not match the known fixture coverage counts");
    }
  }
  if (!frameworks.has("cpputest") || !frameworks.has("unity")) {
    throw linuxEvidenceError("must identify both required frameworks");
  }
  const determinism = closedObject(evidence.determinism, ["coverageJsonByteIdentical", "sha256Identical"], "determinism");
  if (determinism.coverageJsonByteIdentical !== true || determinism.sha256Identical !== true) {
    throw linuxEvidenceError("must prove byte and digest determinism");
  }
  if (!isTimestamp(evidence.startedAt) || !isTimestamp(evidence.finishedAt) || evidence.startedAt > evidence.finishedAt) {
    throw linuxEvidenceError("has invalid timestamps");
  }
  rejectSensitiveEvidence(value);
}

/** Revalidates the exact bytes that CI publishes, including canonical encoding. */
export function validateCoverageEvidenceBytes(
  platform: "linux" | "windows",
  bytes: Uint8Array
): LinuxGccCoverageEvidence | WfpOfflineReport {
  const value = validateCanonicalJSON(Buffer.from(bytes));
  if (platform === "linux") {
    validateLinuxGccCoverageEvidence(value);
    return value;
  }
  validateWfpOfflineReport(value as WfpOfflineReport);
  const report = value as WfpOfflineReport;
  if (report.outcome !== "passed") {
    throw new Error("required Windows coverage evidence must pass");
  }
  rejectSensitiveEvidence(report);
  return report;
}

export async function runWithTestOnlyCoverageFault<Result>(
  fault: TestOnlyCoverageFault,
  inject: (fault: TestOnlyCoverageFault) => Promise<void>,
  execute: () => Promise<Result>
): Promise<Result> {
  if (!TEST_ONLY_COVERAGE_FAULTS.has(fault)) {
    throw new Error("test-only coverage fault is not recognized");
  }
  await inject(fault);
  return await execute();
}

export async function runAfterVerifiedCoverageToolsetPreflight<Boundary, Result>(
  gate: CoverageToolsetPreflightGate<Boundary, Result>
): Promise<{ readonly status: "skipped" } | { readonly status: "executed"; readonly value: Result }> {
  const preflight = await gate.preflight();
  if (preflight.status === "unavailable") {
    if (gate.required) {
      throw new Error("required verified clang-cl coverage toolset is unavailable");
    }
    gate.skip(COVERAGE_TOOLSET_SKIP);
    return { status: "skipped" };
  }
  if (!/^[0-9]+\.[0-9]+(?:\.[0-9]+)?$/u.test(preflight.version)) {
    throw new Error("verified clang-cl coverage toolset version is invalid");
  }
  const boundary = await gate.installBoundary();
  return { status: "executed", value: await gate.execute(boundary, preflight) };
}

interface XMLStartToken {
  readonly kind: "start";
  readonly name: string;
  readonly attributes: ReadonlyMap<string, string>;
}

interface XMLEndToken {
  readonly kind: "end";
  readonly name: string;
}

interface XMLTextToken {
  readonly kind: "text";
  readonly raw: string;
  readonly value: string;
}

type XMLToken = XMLStartToken | XMLEndToken | XMLTextToken;

interface OpenElement {
  readonly name: string;
  outcome?: boolean;
}

export function parseStrictJUnit(bytes: Uint8Array): ParsedJUnit {
  if (bytes.byteLength === 0 || bytes.byteLength > MAX_JUNIT_BYTES) {
    throw junitError("size is outside the bounded contract");
  }
  let xml: string;
  try {
    xml = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch (error) {
    throw junitError("is not valid UTF-8", error);
  }
  if (!xml.startsWith(XML_DECLARATION)) {
    throw junitError("must begin with the exact XML declaration");
  }

  const tokenizer = new StrictXMLTokenizer(xml, XML_DECLARATION.length);
  const stack: OpenElement[] = [];
  let declared: ParsedJUnit | undefined;
  const actual = { tests: 0, failures: 0, errors: 0, skipped: 0 };
  let rootClosed = false;
  for (;;) {
    const token = tokenizer.next();
    if (token === undefined) break;
    if (token.kind === "text") {
      if (stack.length !== 3 && !/^[\t\n\r ]*$/u.test(token.raw)) {
        throw junitError("contains text outside an outcome detail");
      }
      continue;
    }
    if (token.kind === "end") {
      const current = stack.pop();
      if (current === undefined || current.name !== token.name) {
        throw junitError("contains mismatched element nesting");
      }
      if (stack.length === 0) rootClosed = true;
      continue;
    }
    if (rootClosed) throw junitError("contains an extra root element");
    if (stack.length === 0) {
      if (token.name !== "testsuite" || declared !== undefined) {
        throw junitError("root must be one testsuite");
      }
      declared = parseSuiteAttributes(token.attributes);
      stack.push({ name: token.name });
      continue;
    }
    if (stack.length === 1) {
      if (token.name !== "testcase") {
        throw junitError("testsuite contains an unknown child");
      }
      validateTestcaseAttributes(token.attributes);
      actual.tests++;
      stack.push({ name: token.name, outcome: false });
      continue;
    }
    if (stack.length === 2) {
      const testcase = stack[1]!;
      if (testcase.outcome) throw junitError("testcase contains multiple outcomes");
      validateOutcomeAttributes(token.name, token.attributes);
      testcase.outcome = true;
      if (token.name === "failure") actual.failures++;
      if (token.name === "error") actual.errors++;
      if (token.name === "skipped") actual.skipped++;
      stack.push({ name: token.name });
      continue;
    }
    throw junitError("outcome detail contains nested elements");
  }
  if (stack.length !== 0 || declared === undefined || !rootClosed) {
    throw junitError("document is incomplete");
  }
  if (
    declared.tests !== actual.tests ||
    declared.failures !== actual.failures ||
    declared.errors !== actual.errors ||
    declared.skipped !== actual.skipped
  ) {
    throw junitError("declared counts do not match structure");
  }
  return actual;
}

export async function publishEvidenceAtomically(
  target: string,
  bytes: Uint8Array,
  options: EvidencePublishOptions = {}
): Promise<void> {
  if (!isAbsolute(target) || target.includes("\0")) {
    throw new Error("coverage evidence target must be an absolute path");
  }
  const expected = Buffer.from(bytes);
  validateCanonicalJSON(expected);
  const directory = dirname(target);
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const directoryInfo = await lstat(directory);
  if (!directoryInfo.isDirectory() || directoryInfo.isSymbolicLink()) {
    throw new Error("coverage evidence directory is unsafe");
  }
  try {
    await lstat(target);
    throw new Error("coverage evidence target already exists");
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
  }

  const temporary = join(
    directory,
    `.coverage-execution-report-${process.pid}-${randomBytes(8).toString("hex")}.tmp`
  );
  let handle: Awaited<ReturnType<typeof open>> | undefined;
  try {
    handle = await open(temporary, "wx", 0o600);
    await handle.writeFile(expected);
    await handle.sync();
    await handle.close();
    handle = undefined;
    const readBack = Buffer.from(await (options.readBack ?? readFile)(temporary));
    if (!readBack.equals(expected)) {
      throw new Error("temporary evidence readback does not match the intended bytes");
    }
    validateCanonicalJSON(readBack);
    await rename(temporary, target);
  } catch (error) {
    await handle?.close().catch(() => undefined);
    await rm(temporary, { force: true }).catch(() => undefined);
    await rm(target, { force: true }).catch(() => undefined);
    throw error;
  }
}

export async function teardownThenPublish(
  teardown: readonly (() => Promise<void>)[],
  publish: () => Promise<void>
): Promise<void> {
  const errors: unknown[] = [];
  for (const step of teardown) {
    try {
      await step();
    } catch (error) {
      errors.push(error);
    }
  }
  if (errors.length > 0) {
    throw new AggregateError(errors, "coverage smoke teardown failed; evidence was not published");
  }
  await publish();
}

export interface CoverageServiceSmokeBoundary {
  runGuarded<Result>(
    execute: (signal: AbortSignal) => Promise<Result>,
    onBoundaryLoss?: () => Promise<void>
  ): Promise<Result>;
}

export interface CoverageServiceSmokeExecution<Result> {
  readonly boundary: CoverageServiceSmokeBoundary;
  readonly execute: (signal: AbortSignal) => Promise<Result>;
  readonly stopService: () => Promise<void>;
  readonly closeOfflineBoundary: () => Promise<void>;
  readonly cleanupFixture: () => Promise<void>;
  readonly publish: (result: Result) => Promise<void>;
}

/** Runs native work under guardian liveness, then tears down in ownership order before publishing. */
export async function executeCoverageServiceSmoke<Result>(
  execution: CoverageServiceSmokeExecution<Result>
): Promise<Result> {
  const result = await execution.boundary.runGuarded(execution.execute, execution.stopService);
  await teardownThenPublish([
    execution.stopService,
    execution.closeOfflineBoundary,
    execution.cleanupFixture
  ], () => execution.publish(result));
  return result;
}

class StrictXMLTokenizer {
  readonly #pending: XMLToken[] = [];
  #index: number;

  constructor(
    private readonly xml: string,
    index: number
  ) {
    this.#index = index;
  }

  next(): XMLToken | undefined {
    const pending = this.#pending.shift();
    if (pending !== undefined) return pending;
    if (this.#index >= this.xml.length) return undefined;
    if (this.xml[this.#index] !== "<") return this.#text();
    if (this.xml.startsWith("</", this.#index)) return this.#end();
    if (this.xml.startsWith("<!", this.#index)) {
      throw junitError("DOCTYPE, ENTITY, comment and CDATA declarations are forbidden");
    }
    if (this.xml.startsWith("<?", this.#index)) {
      throw junitError("processing instructions are forbidden after the XML declaration");
    }
    return this.#start();
  }

  #text(): XMLTextToken {
    const end = this.xml.indexOf("<", this.#index);
    const next = end < 0 ? this.xml.length : end;
    const raw = this.xml.slice(this.#index, next);
    this.#index = next;
    if (raw.includes("]]>", 0)) throw junitError("text contains a forbidden CDATA terminator");
    return { kind: "text", raw, value: decodeXMLValue(raw) };
  }

  #end(): XMLEndToken {
    this.#index += 2;
    const name = this.#name();
    this.#whitespace();
    this.#expect(">");
    return { kind: "end", name };
  }

  #start(): XMLStartToken {
    this.#index++;
    const name = this.#name();
    const attributes = new Map<string, string>();
    for (;;) {
      const whitespace = this.#whitespace();
      if (this.xml.startsWith("/>", this.#index)) {
        this.#index += 2;
        this.#pending.push({ kind: "end", name });
        return { kind: "start", name, attributes };
      }
      if (this.xml[this.#index] === ">") {
        this.#index++;
        return { kind: "start", name, attributes };
      }
      if (!whitespace) throw junitError("attributes must be separated by XML whitespace");
      const attributeName = this.#name();
      if (attributes.has(attributeName)) throw junitError("contains a duplicate attribute");
      this.#whitespace();
      this.#expect("=");
      this.#whitespace();
      const quote = this.xml[this.#index];
      if (quote !== '"' && quote !== "'") {
        throw junitError("attribute values must be quoted");
      }
      this.#index++;
      const end = this.xml.indexOf(quote, this.#index);
      if (end < 0) throw junitError("contains an unterminated attribute");
      const raw = this.xml.slice(this.#index, end);
      if (raw.includes("<")) throw junitError("attribute contains a raw less-than character");
      attributes.set(attributeName, decodeXMLValue(raw));
      this.#index = end + 1;
    }
  }

  #name(): string {
    const start = this.#index;
    const first = this.xml[this.#index];
    if (first === undefined || !/[A-Za-z_]/u.test(first)) {
      throw junitError("contains an invalid XML name");
    }
    this.#index++;
    while (this.#index < this.xml.length && /[A-Za-z0-9_.-]/u.test(this.xml[this.#index]!)) {
      this.#index++;
    }
    return this.xml.slice(start, this.#index);
  }

  #whitespace(): boolean {
    const start = this.#index;
    while (this.#index < this.xml.length && /[\t\n\r ]/u.test(this.xml[this.#index]!)) {
      this.#index++;
    }
    return this.#index !== start;
  }

  #expect(value: string): void {
    if (!this.xml.startsWith(value, this.#index)) {
      throw junitError(`expected ${value}`);
    }
    this.#index += value.length;
  }
}

function decodeXMLValue(raw: string): string {
  validateXMLCharacters(raw);
  let output = "";
  let index = 0;
  while (index < raw.length) {
    const ampersand = raw.indexOf("&", index);
    if (ampersand < 0) {
      output += raw.slice(index);
      break;
    }
    output += raw.slice(index, ampersand);
    const semicolon = raw.indexOf(";", ampersand + 1);
    if (semicolon < 0) throw junitError("contains an unterminated entity");
    const entity = raw.slice(ampersand + 1, semicolon);
    const builtin: Readonly<Record<string, string>> = {
      amp: "&",
      apos: "'",
      gt: ">",
      lt: "<",
      quot: '"'
    };
    if (builtin[entity] !== undefined) {
      output += builtin[entity];
    } else {
      let digits: string;
      let radix: 10 | 16;
      if (/^#[0-9]+$/u.test(entity)) {
        digits = entity.slice(1);
        radix = 10;
      } else if (/^#x[0-9A-Fa-f]+$/u.test(entity)) {
        digits = entity.slice(2);
        radix = 16;
      } else {
        throw junitError("contains an unknown entity");
      }
      const codePoint = Number.parseInt(digits, radix);
      if (!isXMLCharacter(codePoint)) throw junitError("contains an invalid numeric entity");
      output += String.fromCodePoint(codePoint);
    }
    index = semicolon + 1;
  }
  validateXMLCharacters(output);
  return output;
}

function validateXMLCharacters(value: string): void {
  for (const character of value) {
    const codePoint = character.codePointAt(0)!;
    if (!isXMLCharacter(codePoint)) throw junitError("contains an invalid XML character");
  }
}

function isXMLCharacter(codePoint: number): boolean {
  return codePoint === 0x09 || codePoint === 0x0a || codePoint === 0x0d ||
    codePoint >= 0x20 && codePoint <= 0xd7ff ||
    codePoint >= 0xe000 && codePoint <= 0xfffd ||
    codePoint >= 0x10000 && codePoint <= 0x10ffff;
}

function parseSuiteAttributes(attributes: ReadonlyMap<string, string>): ParsedJUnit {
  const values = exactAttributes(attributes, ["name", "tests", "failures", "errors", "skipped"]);
  if (values.name !== "coverage-test-run") throw junitError("testsuite name is invalid");
  return {
    tests: parseCount(values.tests),
    failures: parseCount(values.failures),
    errors: parseCount(values.errors),
    skipped: parseCount(values.skipped)
  };
}

function validateTestcaseAttributes(attributes: ReadonlyMap<string, string>): void {
  const values = exactAttributes(attributes, ["name", "classname"]);
  if (!TEST_ID.test(values.classname)) throw junitError("testcase classname is invalid");
  if (!TEST_ID.test(values.name) && !ITERATED_TEST_ID.test(values.name)) {
    throw junitError("testcase name is invalid");
  }
}

function validateOutcomeAttributes(
  name: string,
  attributes: ReadonlyMap<string, string>
): void {
  if (name === "skipped") {
    exactAttributes(attributes, ["message"]);
    return;
  }
  if (name !== "failure" && name !== "error") {
    throw junitError("testcase contains an unknown outcome");
  }
  const values = exactAttributes(attributes, ["type", "message"]);
  if (values.type.length === 0) throw junitError("outcome type is empty");
}

function exactAttributes<const Name extends string>(
  attributes: ReadonlyMap<string, string>,
  names: readonly Name[]
): Record<Name, string> {
  if (attributes.size !== names.length) throw junitError("element has the wrong attribute count");
  const values = {} as Record<Name, string>;
  for (const name of names) {
    const value = attributes.get(name);
    if (value === undefined) throw junitError(`element is missing attribute ${name}`);
    values[name] = value;
  }
  return values;
}

function parseCount(value: string): number {
  if (!/^(?:0|[1-9][0-9]*)$/u.test(value)) throw junitError("suite count is invalid");
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed)) throw junitError("suite count exceeds the safe range");
  return parsed;
}

function closedObject(
  value: unknown,
  keys: readonly string[],
  label: string
): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw linuxEvidenceError(`${label} must be an object`);
  }
  const record = value as Record<string, unknown>;
  const actual = Object.keys(record).sort();
  const expected = [...keys].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) {
    throw linuxEvidenceError(`${label} has an additional, missing or forbidden property`);
  }
  return record;
}

function validateMetric(value: unknown): void {
  const metric = closedObject(value, ["covered", "total"], "summary metric");
  if (
    !Number.isSafeInteger(metric.covered) || !Number.isSafeInteger(metric.total) ||
    (metric.covered as number) < 0 || (metric.total as number) < 0 ||
    (metric.covered as number) > (metric.total as number)
  ) {
    throw linuxEvidenceError("has an invalid known coverage metric");
  }
}

function isDigest(value: unknown): value is string {
  return typeof value === "string" && SHA256.test(value);
}

function isTimestamp(value: unknown): value is string {
  if (typeof value !== "string" || !ISO_TIMESTAMP.test(value)) return false;
  const instant = new Date(value);
  return Number.isFinite(instant.getTime()) && instant.toISOString() === value;
}

function sameMetrics(
  value: Record<string, unknown>,
  expected: LinuxGccCoverageCaseEvidence["summary"]
): boolean {
  for (const name of ["lines", "branches", "functions"] as const) {
    const metric = value[name] as Record<string, unknown>;
    if (metric.covered !== expected[name].covered || metric.total !== expected[name].total) return false;
  }
  return true;
}

function rejectSensitiveEvidence(value: unknown): void {
  if (typeof value === "string") {
    if (NATIVE_PATH.test(value)) throw linuxEvidenceError("contains a native path");
    return;
  }
  if (value === null || typeof value !== "object") return;
  if (Array.isArray(value)) {
    for (const item of value) rejectSensitiveEvidence(item);
    return;
  }
  for (const [key, nested] of Object.entries(value)) {
    if (SENSITIVE_EVIDENCE_KEY.test(key)) throw linuxEvidenceError("contains a sensitive process field");
    rejectSensitiveEvidence(nested);
  }
}

function linuxEvidenceError(message: string): Error {
  return new Error(`Linux GCC coverage evidence ${message}`);
}

function validateCanonicalJSON(bytes: Buffer): unknown {
  let text: string;
  try {
    text = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch (error) {
    throw new Error("coverage evidence is not valid UTF-8", { cause: error });
  }
  if (!text.endsWith("\n") || text.slice(0, -1).includes("\n") || text.includes("\r")) {
    throw new Error("coverage evidence must be one newline-terminated JSON object");
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(text.slice(0, -1));
  } catch (error) {
    throw new Error("coverage evidence is not strict JSON", { cause: error });
  }
  if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("coverage evidence root must be an object");
  }
  if (`${JSON.stringify(parsed)}\n` !== text) {
    throw new Error("coverage evidence must use canonical compact JSON encoding");
  }
  return parsed;
}

function junitError(message: string, cause?: unknown): Error {
  return new Error(`JUnit XML ${message}`, cause === undefined ? undefined : { cause });
}
