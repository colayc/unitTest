import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createVSCodeTestingController } from "../src/vscode-testing-bridge.js";

class FakeUri {
  static readonly parsed: string[] = [];
  static parse(value: string): FakeUri {
    this.parsed.push(value);
    return new FakeUri(value);
  }
  constructor(readonly value: string) {}
}

class FakeRange {
  constructor(
    readonly startLine: number,
    readonly startColumn: number,
    readonly endLine: number,
    readonly endColumn: number
  ) {}
}

class FakeLocation {
  constructor(readonly uri: FakeUri, readonly range: FakeRange) {}
}

class FakeTestMessage {
  location?: FakeLocation;
  constructor(readonly message: string) {}
}

interface RawItem {
  readonly id: string;
  label: string;
  readonly uri?: FakeUri;
  readonly parent?: RawItem;
  children: RawCollection;
  canResolveChildren: boolean;
  error?: string;
  description?: string;
  range?: FakeRange;
}

class RawCollection {
  readonly entries = new Map<string, RawItem>();
  add(item: RawItem): void { this.entries.set(item.id, item); }
  delete(id: string): void { this.entries.delete(id); }
  get(id: string): RawItem | undefined { return this.entries.get(id); }
  replace(items: readonly RawItem[]): void {
    this.entries.clear();
    for (const item of items) this.entries.set(item.id, item);
  }
}

test("production bridge converts Testing API values to real VS Code shapes", async () => {
  FakeUri.parsed.length = 0;
  const root = new RawCollection();
  const profileCalls: unknown[][] = [];
  let rawRunHandler: ((request: unknown, token: unknown) => void | Promise<void>) | undefined;
  const runRequests: unknown[] = [];
  const failed: unknown[][] = [];
  const errored: unknown[][] = [];
  const rawController = {
    items: root,
    refreshHandler: undefined,
    createTestItem(id: string, label: string, uri?: FakeUri): RawItem {
      return {
        id,
        label,
        uri,
        children: new RawCollection(),
        canResolveChildren: false,
        get parent(): RawItem | undefined { return undefined; }
      };
    },
    createRunProfile(label: string, kind: number, handler: typeof rawRunHandler, isDefault?: boolean) {
      profileCalls.push([label, kind, handler, isDefault]);
      rawRunHandler = handler;
      return { label, dispose() {} };
    },
    createTestRun(request: unknown) {
      runRequests.push(request);
      return {
        started() {}, passed() {}, skipped() {}, end() {},
        failed(...args: unknown[]) { failed.push(args); },
        errored(...args: unknown[]) { errored.push(args); }
      };
    },
    dispose() {}
  };
  const runtime = {
    Uri: FakeUri,
    Range: FakeRange,
    Location: FakeLocation,
    TestMessage: FakeTestMessage,
    TestRunProfileKind: { Run: 42 }
  };
  const controller = createVSCodeTestingController(runtime as never, rawController as never);
  const parent = controller.createTestItem?.("parent", "Parent", "file:///workspace/parent.cpp");
  const child = controller.createTestItem?.("child", "Child", "file:///workspace/child.cpp");
  assert.ok(parent?.children && child);
  child.sourceLocation = {
    uri: "file:///workspace/child.cpp",
    line: 9,
    column: 3,
    navigable: true,
    provenance: "test-declaration"
  };
  parent.children.replace([child]);
  controller.items?.replace([parent]);

  let testingRequest: unknown;
  controller.createRunProfile?.("Run Tests", "run", (request) => { testingRequest = request; }, true);
  assert.equal(profileCalls[0]?.[0], "Run Tests");
  assert.equal(profileCalls[0]?.[1], 42);
  assert.equal(typeof profileCalls[0]?.[2], "function");
  assert.equal(profileCalls[0]?.[3], true);
  const rawRequest = { include: [rawController.createTestItem("child", "Child")], exclude: undefined };
  await rawRunHandler?.(rawRequest, { cancelled: false });
  assert.ok(testingRequest);
  const run = controller.createTestRun?.(testingRequest as never);
  run?.failed(child, "assertion failed", 12);
  run?.errored(child, new Error("runner failed"));

  assert.deepEqual(FakeUri.parsed, ["file:///workspace/parent.cpp", "file:///workspace/child.cpp"]);
  const rawChild = rawController.items.get("parent")?.children.get("child");
  assert.ok(rawChild?.range instanceof FakeRange);
  assert.deepEqual(rawChild?.range, new FakeRange(8, 2, 8, 2));
  assert.equal(runRequests[0], rawRequest);
  assert.ok(failed[0]?.[1] instanceof FakeTestMessage);
  assert.equal((failed[0]?.[1] as FakeTestMessage).message, "assertion failed");
  assert.ok((failed[0]?.[1] as FakeTestMessage).location instanceof FakeLocation);
  assert.ok(errored[0]?.[1] instanceof FakeTestMessage);
});

test("activation uses the explicit bridge without hiding TestController incompatibilities", async () => {
  const source = await readFile(new URL("../src/extension.js", import.meta.url), "utf8");
  assert.match(source, /createVSCodeTestingController\(\s*vscode,/);
  assert.doesNotMatch(source, /as\s+unknown\s+as\s+TestingController/);
});
