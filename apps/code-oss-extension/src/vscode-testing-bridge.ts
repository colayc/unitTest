import type * as vscodeTypes from "vscode";
import type {
  TestingController,
  TestingRun,
  TestingRunRequest,
  TestingSourceLocation,
  TestingTestItem,
  TestingTestItemCollection
} from "./testing-api.js";

export function createVSCodeTestingController(
  vscode: typeof vscodeTypes,
  controller: vscodeTypes.TestController
): TestingController {
  const testingByRaw = new WeakMap<vscodeTypes.TestItem, TestingTestItem>();
  const rawByTesting = new WeakMap<TestingTestItem, vscodeTypes.TestItem>();
  const rawRequests = new WeakMap<TestingRunRequest, vscodeTypes.TestRunRequest>();

  const rawItem = (item: TestingTestItem): vscodeTypes.TestItem => {
    const raw = rawByTesting.get(item);
    if (!raw) throw new Error("Testing API item is not owned by this controller.");
    return raw;
  };

  const wrapCollection = (collection: vscodeTypes.TestItemCollection): TestingTestItemCollection => ({
    add: (item) => collection.add(rawItem(item)),
    delete: (id) => collection.delete(id),
    get: (id) => {
      const raw = collection.get(id);
      return raw === undefined ? undefined : wrapItem(raw);
    },
    replace: (items) => collection.replace(items.map(rawItem))
  });

  const wrapItem = (raw: vscodeTypes.TestItem): TestingTestItem => {
    const existing = testingByRaw.get(raw);
    if (existing) return existing;
    let sourceLocation: TestingSourceLocation | undefined;
    const wrapped: TestingTestItem = {
      get id() { return raw.id; },
      get label() { return raw.label; },
      set label(value) { raw.label = value; },
      get uri() { return raw.uri; },
      children: wrapCollection(raw.children),
      get canResolveChildren() { return raw.canResolveChildren; },
      set canResolveChildren(value) { raw.canResolveChildren = value ?? false; },
      get error(): string | Error | undefined { return typeof raw.error === "string" ? raw.error : undefined; },
      set error(value: string | Error | undefined) { raw.error = value instanceof Error ? value.message : value; },
      get description() { return raw.description; },
      set description(value) { raw.description = value; },
      get sourceLocation() { return sourceLocation; },
      set sourceLocation(value) {
        sourceLocation = value;
        if (!value?.navigable) {
          raw.range = undefined;
          return;
        }
        const line = Math.max(0, (value.line ?? 1) - 1);
        const column = Math.max(0, (value.column ?? 1) - 1);
        raw.range = new vscode.Range(line, column, line, column);
      }
    };
    testingByRaw.set(raw, wrapped);
    rawByTesting.set(wrapped, raw);
    return wrapped;
  };

  const testMessage = (item: TestingTestItem, value: string | Error): vscodeTypes.TestMessage => {
    const message = new vscode.TestMessage(value instanceof Error ? value.message : value);
    const raw = rawItem(item);
    if (raw.uri && raw.range) message.location = new vscode.Location(raw.uri, raw.range);
    return message;
  };

  const wrapRun = (run: vscodeTypes.TestRun): TestingRun => ({
    started: (item) => run.started(rawItem(item)),
    passed: (item, duration) => run.passed(rawItem(item), duration),
    failed: (item, message, duration) => run.failed(rawItem(item), testMessage(item, message), duration),
    skipped: (item) => run.skipped(rawItem(item)),
    errored: (item, message, duration) => run.errored(rawItem(item), testMessage(item, message), duration),
    end: () => run.end(),
    dispose() {}
  });

  let refreshHandler: TestingController["refreshHandler"];
  return {
    items: wrapCollection(controller.items),
    get refreshHandler() { return refreshHandler; },
    set refreshHandler(value) {
      refreshHandler = value;
      controller.refreshHandler = value === undefined ? undefined : (token) => value(token);
    },
    createTestItem(id, label, uri) {
      const parsed = typeof uri === "string" ? vscode.Uri.parse(uri) : undefined;
      return wrapItem(controller.createTestItem(id, label, parsed));
    },
    createRunProfile(label, kind, handler, isDefault) {
      const profile = controller.createRunProfile(
        label,
        vscode.TestRunProfileKind.Run,
        (request, token) => {
          const wrapped: TestingRunRequest = {
            ...(request.include === undefined ? {} : { include: request.include.map(wrapItem) }),
            ...(request.exclude === undefined ? {} : { exclude: request.exclude.map(wrapItem) })
          };
          rawRequests.set(wrapped, request);
          return handler(wrapped, token);
        },
        isDefault
      );
      return { label: profile.label, dispose: () => profile.dispose() };
    },
    createTestRun(request, name, persist) {
      const rawRequest = request === undefined ? new vscode.TestRunRequest() : rawRequests.get(request);
      if (!rawRequest) throw new Error("Testing API run request is not owned by this controller.");
      return wrapRun(controller.createTestRun(rawRequest, name, persist));
    },
    dispose: () => controller.dispose()
  };
}
