import {
  ProtocolClient,
  type ArtifactPage,
  type CatalogGetInput,
  type CoverageRunInput,
  type CoverageRunListInput,
  type EventSubscription,
  type PageInput,
  type CoverageReport,
  type CoverageRun,
  type CoverageRunPage,
  type ProtocolTestCatalog,
  type ProtocolTestRun,
  type TestDiscoveryInput,
  type TestRunInput,
  type WorkspaceSnapshot
} from "@unit-test-ide/test-client";

export interface ExtensionCoverageProtocolClient {
  startCoverage(input: CoverageRunInput): Promise<CoverageRun>;
  getCoverageRun(runId: string): Promise<CoverageRun>;
  listCoverageRuns(input?: CoverageRunListInput): Promise<CoverageRunPage>;
  getCoverageReport(reportId: string): Promise<CoverageReport>;
  listArtifacts(taskId: string, input?: PageInput): Promise<ArtifactPage>;
  readArtifact(artifactId: string): Promise<Uint8Array>;
}

export interface ExtensionProtocolClient {
  inspectWorkspace(): Promise<WorkspaceSnapshot>;
  discoverTests(input: TestDiscoveryInput): ReturnType<ProtocolClient["discoverTests"]>;
  getTestCatalog(input: CatalogGetInput): Promise<ProtocolTestCatalog>;
  runTests(input: TestRunInput): ReturnType<ProtocolClient["runTests"]>;
  getTestRun(runId: string): Promise<ProtocolTestRun>;
  startCoverage?: ExtensionCoverageProtocolClient["startCoverage"];
  getCoverageRun?: ExtensionCoverageProtocolClient["getCoverageRun"];
  listCoverageRuns?: ExtensionCoverageProtocolClient["listCoverageRuns"];
  getCoverageReport?: ExtensionCoverageProtocolClient["getCoverageReport"];
  listArtifacts?: ExtensionCoverageProtocolClient["listArtifacts"];
  readArtifact?: ExtensionCoverageProtocolClient["readArtifact"];
  subscribeEvents(afterSequence: number): Promise<EventSubscription>;
  close(): void;
}

export function createProtocolClient(endpoint: string): Promise<ProtocolClient> {
  return ProtocolClient.connect(endpoint);
}
