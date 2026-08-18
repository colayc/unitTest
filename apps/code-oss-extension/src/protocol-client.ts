import {
  ProtocolClient,
  type CatalogGetInput,
  type EventSubscription,
  type ProtocolTestCatalog,
  type ProtocolTestRun,
  type TestDiscoveryInput,
  type TestRunInput,
  type WorkspaceSnapshot
} from "@unit-test-ide/test-client";

export interface ExtensionProtocolClient {
  inspectWorkspace(): Promise<WorkspaceSnapshot>;
  discoverTests(input: TestDiscoveryInput): ReturnType<ProtocolClient["discoverTests"]>;
  getTestCatalog(input: CatalogGetInput): Promise<ProtocolTestCatalog>;
  runTests(input: TestRunInput): ReturnType<ProtocolClient["runTests"]>;
  getTestRun(runId: string): Promise<ProtocolTestRun>;
  subscribeEvents(afterSequence: number): Promise<EventSubscription>;
  close(): void;
}

export function createProtocolClient(endpoint: string): Promise<ProtocolClient> {
  return ProtocolClient.connect(endpoint);
}
