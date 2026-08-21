import assert from "node:assert/strict";
import { mkdtemp, readFile, readdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  parseStrictJUnit,
  publishEvidenceAtomically,
  runAfterVerifiedCoverageToolsetPreflight,
  teardownThenPublish
} from "./coverage-service-smoke-support.js";

const itemA = `utid-v1-${"a".repeat(64)}`;
const itemB = `utid-v1-${"b".repeat(64)}`;
const container = `utid-v1-${"c".repeat(64)}`;
const unavailableMessage = "SKIP: verified clang-cl coverage toolset is unavailable";

test("local unavailable coverage toolset skips before every boundary and execution side effect", async () => {
  const trace: string[] = [];
  const result = await runAfterVerifiedCoverageToolsetPreflight({
    required: false,
    async preflight() {
      trace.push("preflight");
      return { status: "unavailable", digest: "a".repeat(64) };
    },
    skip(message) { trace.push(`skip:${message}`); },
    async installBoundary() { trace.push("boundary"); return "boundary"; },
    async execute() { trace.push("service-start/native-execution"); return 1; }
  });
  assert.deepEqual(result, { status: "skipped" });
  assert.deepEqual(trace, ["preflight", `skip:${unavailableMessage}`]);
});

test("required unavailable coverage toolset fails before every boundary and execution side effect", async () => {
  const trace: string[] = [];
  await assert.rejects(
    runAfterVerifiedCoverageToolsetPreflight({
      required: true,
      async preflight() {
        trace.push("preflight");
        return { status: "unavailable", digest: "b".repeat(64) };
      },
      skip(message) { trace.push(`skip:${message}`); },
      async installBoundary() { trace.push("boundary"); return "boundary"; },
      async execute() { trace.push("service-start/native-execution"); return 1; }
    }),
    /required verified clang-cl coverage toolset is unavailable/u
  );
  assert.deepEqual(trace, ["preflight"]);
});

test("verified coverage toolset establishes the boundary before Service and native execution", async () => {
  const trace: string[] = [];
  const result = await runAfterVerifiedCoverageToolsetPreflight({
    required: true,
    async preflight() {
      trace.push("preflight");
      return { status: "verified", version: "18.1.8", digest: "c".repeat(64) };
    },
    skip(message) { trace.push(`skip:${message}`); },
    async installBoundary() { trace.push("boundary"); return "boundary"; },
    async execute(boundary, toolset) {
      trace.push(`service-start/native-execution:${boundary}:${toolset.version}`);
      return 7;
    }
  });
  assert.deepEqual(result, { status: "executed", value: 7 });
  assert.deepEqual(trace, [
    "preflight",
    "boundary",
    "service-start/native-execution:boundary:18.1.8"
  ]);

  let executed = false;
  await assert.rejects(
    runAfterVerifiedCoverageToolsetPreflight({
      required: true,
      async preflight() { return { status: "verified", version: "18.1.8", digest: "d".repeat(64) }; },
      skip() {},
      async installBoundary() { throw new Error("guardian failed closed"); },
      async execute() { executed = true; }
    }),
    /guardian failed closed/u
  );
  assert.equal(executed, false, "a verified toolset cannot bypass a failed OS boundary");
});

test("strict JUnit tokenizer accepts quoted attributes and legal builtin/numeric entities", () => {
  const xml = junit([
    `<testcase name='${itemA}' classname="${container}"></testcase>`,
    `<testcase name="${itemB}" classname='${container}'><failure type="assertion_failure" message="want &lt;1&gt; &amp; &#x41; &#65; &quot;&apos;">detail &#10; ok</failure></testcase>`
  ], { tests: 2, failures: 1, errors: 0, skipped: 0 });
  assert.deepEqual(parseStrictJUnit(Buffer.from(xml)), {
    tests: 2,
    failures: 1,
    errors: 0,
    skipped: 0
  });
});

test("strict JUnit tokenizer rejects malformed entities", () => {
  const malformed = [
    `<failure type="x" message="&bogus;">bad</failure>`,
    `<failure type="x" message="bare & value">bad</failure>`,
    `<failure type="x" message="&#0;">bad</failure>`,
    `<failure type="x" message="&#xD800;">bad</failure>`,
    `<failure type="x" message="&#x110000;">bad</failure>`,
    `<failure type="x" message="&#65">bad</failure>`
  ];
  for (const detail of malformed) {
    assert.throws(
      () => parseStrictJUnit(Buffer.from(junit([
        `<testcase name="${itemA}" classname="${container}">${detail}</testcase>`
      ], { tests: 1, failures: 1, errors: 0, skipped: 0 }))),
      /JUnit XML/u
    );
  }
});

test("strict JUnit tokenizer rejects unquoted, duplicate, missing and unknown attributes", () => {
  const malformed = [
    `<testcase name=${itemA} classname="${container}"></testcase>`,
    `<testcase name="${itemA}" name="${itemB}" classname="${container}"></testcase>`,
    `<testcase name="${itemA}"></testcase>`,
    `<testcase name="${itemA}" classname="${container}" time="1"></testcase>`
  ];
  for (const testcase of malformed) {
    assert.throws(
      () => parseStrictJUnit(Buffer.from(junit([testcase], {
        tests: 1,
        failures: 0,
        errors: 0,
        skipped: 0
      }))),
      /JUnit XML/u
    );
  }
});

test("strict JUnit tokenizer rejects mismatched nesting, extra roots and forbidden declarations", () => {
  const validCase = `<testcase name="${itemA}" classname="${container}"></testcase>`;
  const malformed = [
    junitRaw(`<testsuite name="coverage-test-run" tests="1" failures="0" errors="0" skipped="0"><testcase name="${itemA}" classname="${container}"></testsuite></testcase>`),
    `${junit([validCase], { tests: 1, failures: 0, errors: 0, skipped: 0 })}<testsuite name="coverage-test-run" tests="0" failures="0" errors="0" skipped="0"></testsuite>`,
    `<!DOCTYPE testsuite>${junit([validCase], { tests: 1, failures: 0, errors: 0, skipped: 0 })}`,
    `<?xml version="1.0" encoding="UTF-8"?><?report unsafe?><testsuite name="coverage-test-run" tests="0" failures="0" errors="0" skipped="0"></testsuite>`,
    junitRaw(`<testsuite name="coverage-test-run" tests="0" failures="0" errors="0" skipped="0"><!--comment--></testsuite>`),
    junitRaw(`<testsuite name="coverage-test-run" tests="0" failures="0" errors="0" skipped="0"><![CDATA[text]]></testsuite>`),
    `${junit([validCase], { tests: 1, failures: 0, errors: 0, skipped: 0 })}&#10;`
  ];
  for (const xml of malformed) {
    assert.throws(() => parseStrictJUnit(Buffer.from(xml)), /JUnit XML/u);
  }
});

test("strict JUnit tokenizer enforces the closed schema and declared counts", () => {
  const malformed = [
    junit([`<testcase name="${itemA}" classname="${container}"></testcase>`], {
      tests: 2,
      failures: 0,
      errors: 0,
      skipped: 0
    }),
    junit([`<testcase name="${itemA}" classname="${container}"><failure type="x" message="x">x</failure><skipped message="x"></skipped></testcase>`], {
      tests: 1,
      failures: 1,
      errors: 0,
      skipped: 1
    }),
    junit([`<unknown></unknown>`], { tests: 0, failures: 0, errors: 0, skipped: 0 }),
    junit([`<testcase name="${itemA}" classname="${container}">text</testcase>`], {
      tests: 1,
      failures: 0,
      errors: 0,
      skipped: 0
    })
  ];
  for (const xml of malformed) {
    assert.throws(() => parseStrictJUnit(Buffer.from(xml)), /JUnit XML/u);
  }
});

test("atomic evidence validates temporary readback before publishing", async () => {
  const root = await mkdtemp(join(tmpdir(), "coverage-evidence-publish-"));
  try {
    const target = join(root, "coverage-execution-report.json");
    const bytes = Buffer.from('{"schemaVersion":1}\n');
    await publishEvidenceAtomically(target, bytes);
    assert.deepEqual(await readFile(target), bytes);
    assert.deepEqual(await readdir(root), ["coverage-execution-report.json"]);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("atomic evidence removes its temporary write when injected readback fails", async () => {
  const root = await mkdtemp(join(tmpdir(), "coverage-evidence-readback-"));
  try {
    const target = join(root, "coverage-execution-report.json");
    let readbackObserved = false;
    await assert.rejects(
      publishEvidenceAtomically(target, Buffer.from('{"schemaVersion":1}\n'), {
        async readBack(path) {
          readbackObserved = true;
          const bytes = await readFile(path);
          return Buffer.concat([bytes, Buffer.from("corrupt")]);
        }
      }),
      /temporary evidence readback/u
    );
    assert.equal(readbackObserved, true, "fault must occur after the temporary write");
    await assert.rejects(readFile(target), /ENOENT/u);
    assert.deepEqual(await readdir(root), []);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("teardown failure runs every cleanup and suppresses evidence publication", async () => {
  const root = await mkdtemp(join(tmpdir(), "coverage-evidence-teardown-"));
  const target = join(root, "coverage-execution-report.json");
  const trace: string[] = [];
  try {
    await assert.rejects(
      teardownThenPublish([
        async () => {
          trace.push("service-stop");
          throw new Error("injected service teardown fault");
        },
        async () => {
          trace.push("fixture-cleanup");
        },
        async () => {
          trace.push("offline-cleanup");
        }
      ], async () => {
        trace.push("publish");
        await publishEvidenceAtomically(target, Buffer.from('{"schemaVersion":1}\n'));
      }),
      /coverage smoke teardown failed/u
    );
    assert.deepEqual(trace, ["service-stop", "fixture-cleanup", "offline-cleanup"]);
    await assert.rejects(readFile(target), /ENOENT/u);
    assert.deepEqual(await readdir(root), []);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("evidence publication occurs only after every teardown succeeds", async () => {
  const trace: string[] = [];
  await teardownThenPublish([
    async () => { trace.push("service-stop"); },
    async () => { trace.push("fixture-cleanup"); },
    async () => { trace.push("offline-cleanup"); }
  ], async () => {
    trace.push("publish");
  });
  assert.deepEqual(trace, ["service-stop", "fixture-cleanup", "offline-cleanup", "publish"]);
});

function junit(
  children: readonly string[],
  counts: { tests: number; failures: number; errors: number; skipped: number }
): string {
  return junitRaw(`<testsuite name="coverage-test-run" tests="${counts.tests}" failures="${counts.failures}" errors="${counts.errors}" skipped="${counts.skipped}">${children.join("")}</testsuite>`);
}

function junitRaw(body: string): string {
  return `<?xml version="1.0" encoding="UTF-8"?>\n${body}\n`;
}
