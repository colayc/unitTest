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
import type {
  Capabilities,
  CapabilitiesV11,
  CapabilitiesV12,
  CapabilitiesV13,
  CapabilitiesV14,
  CapabilitiesV15,
  TestGenerationAcceptInput,
  TestGenerationCandidateListInput,
  TestGenerationCandidatePageV15,
  TestGenerationEventPageV15,
  TestGenerationEventReplayInput,
  TestGenerationRunV15,
  TestGenerationStartInput,
  TestGenerationTargetListInput,
  TestGenerationTargetListV15
} from "@unit-test-ide/test-client";

export type ExtensionCapabilities = Capabilities | CapabilitiesV11 | CapabilitiesV12 | CapabilitiesV13 | CapabilitiesV14 | CapabilitiesV15;

export interface ExtensionGenerationProtocolClient {
  getCapabilities(): Promise<ExtensionCapabilities>;
  listTestGenerationTargets(input: TestGenerationTargetListInput): Promise<TestGenerationTargetListV15>;
  startTestGeneration(input: TestGenerationStartInput): Promise<TestGenerationRunV15>;
  getTestGenerationRun(runId: string): Promise<TestGenerationRunV15>;
  cancelTestGeneration(runId: string): Promise<TestGenerationRunV15>;
  replayTestGenerationEvents(input: TestGenerationEventReplayInput): Promise<TestGenerationEventPageV15>;
  listTestGenerationCandidates(input: TestGenerationCandidateListInput): Promise<TestGenerationCandidatePageV15>;
  acceptTestGeneration(input: TestGenerationAcceptInput): Promise<TestGenerationRunV15>;
}

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
  getCapabilities?: ExtensionGenerationProtocolClient["getCapabilities"];
  listTestGenerationTargets?: ExtensionGenerationProtocolClient["listTestGenerationTargets"];
  startTestGeneration?: ExtensionGenerationProtocolClient["startTestGeneration"];
  getTestGenerationRun?: ExtensionGenerationProtocolClient["getTestGenerationRun"];
  cancelTestGeneration?: ExtensionGenerationProtocolClient["cancelTestGeneration"];
  replayTestGenerationEvents?: ExtensionGenerationProtocolClient["replayTestGenerationEvents"];
  listTestGenerationCandidates?: ExtensionGenerationProtocolClient["listTestGenerationCandidates"];
  acceptTestGeneration?: ExtensionGenerationProtocolClient["acceptTestGeneration"];
  subscribeEvents(afterSequence: number): Promise<EventSubscription>;
  close(): void;
}

export function createProtocolClient(endpoint: string): Promise<ProtocolClient> {
  return ProtocolClient.connect(endpoint);
}
