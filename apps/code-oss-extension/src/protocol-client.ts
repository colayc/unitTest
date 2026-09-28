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
  CapabilitiesV16,
  CoverageProjectInputV16,
  CoverageFileListInputV16,
  CoverageFunctionListInputV16,
  CoverageLineListInputV16,
  ManagedTestListInputV16,
  ManagedReviewGetInputV16,
  ManagedReviewApplyInputV16,
  CoverageProjectV16,
  CoverageFilePageV16,
  CoverageFunctionPageV16,
  CoverageLinePageV16,
  ManagedTestRecordPageV16,
  ManagedReviewV16,
  ManagedReviewApplyResultV16,
  TestGenerationAcceptInput,
  TestGenerationCandidateListInput,
  TestGenerationCandidatePageV15,
  TestGenerationEventPageV15,
  TestGenerationEventReplayInput,
  TestGenerationRunV15,
  TestGenerationRunV16,
  TestGenerationStartInput,
  TestGenerationStartInputV16,
  TestGenerationTargetListInput,
  TestGenerationTargetListV15
} from "@unit-test-ide/test-client";

export type ExtensionCapabilities = Capabilities | CapabilitiesV11 | CapabilitiesV12 | CapabilitiesV13 | CapabilitiesV14 | CapabilitiesV15 | CapabilitiesV16;

export interface ExtensionGenerationProtocolClient {
  getCapabilities(): Promise<ExtensionCapabilities>;
  listTestGenerationTargets(input: TestGenerationTargetListInput): Promise<TestGenerationTargetListV15>;
  startTestGeneration(input: TestGenerationStartInput): Promise<TestGenerationRunV15>;
  startTestGeneration(input: TestGenerationStartInputV16): Promise<TestGenerationRunV16>;
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

export interface ExtensionCoverageDetailsProtocolClient {
  getCoverageProject(input: CoverageProjectInputV16): Promise<CoverageProjectV16>;
  listCoverageFiles(input: CoverageFileListInputV16): Promise<CoverageFilePageV16>;
  listCoverageFunctions(input: CoverageFunctionListInputV16): Promise<CoverageFunctionPageV16>;
  listCoverageLines(input: CoverageLineListInputV16): Promise<CoverageLinePageV16>;
}

export interface ExtensionManagedProtocolClient {
  listManagedTests(input: ManagedTestListInputV16): Promise<ManagedTestRecordPageV16>;
  getManagedReview(input: ManagedReviewGetInputV16): Promise<ManagedReviewV16>;
  applyManagedReview(input: ManagedReviewApplyInputV16): Promise<ManagedReviewApplyResultV16>;
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
  getCoverageProject?: ExtensionCoverageDetailsProtocolClient["getCoverageProject"];
  listCoverageFiles?: ExtensionCoverageDetailsProtocolClient["listCoverageFiles"];
  listCoverageFunctions?: ExtensionCoverageDetailsProtocolClient["listCoverageFunctions"];
  listCoverageLines?: ExtensionCoverageDetailsProtocolClient["listCoverageLines"];
  listManagedTests?: ExtensionManagedProtocolClient["listManagedTests"];
  getManagedReview?: ExtensionManagedProtocolClient["getManagedReview"];
  applyManagedReview?: ExtensionManagedProtocolClient["applyManagedReview"];
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
