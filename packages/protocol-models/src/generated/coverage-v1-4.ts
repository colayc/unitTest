import type { TestSelectionSnapshotV14, TestSelectionV14 } from "./test-v1-4.js";

export interface CoverageContractV14 { runStartRequest: CoverageRunStartRequest; run: CoverageRun; runPage: CoverageRunPage; report: CoverageReport; }
export interface CoverageRunStartRequest { idempotencyKey: string; workspaceGeneration: string; projectId: string; coverageProfileId: string; catalogRevision: string; selection: TestSelectionV14; repeatCount: number; timeoutMs: number; }
export interface CoverageRun { coverageRunId: string; taskId: string; testRunId: string; workspaceGeneration: string; projectId: string; coverageProfileId: string; catalogRevision: string; selectionSnapshot: TestSelectionSnapshotV14; repeatCount: number; timeoutMs: number; status: CoverageRunStatusV14; outcome?: CoverageRunOutcomeV14; reason?: CoverageRunReasonV14; createdAt: Date; startedAt?: Date; finishedAt?: Date; reportId?: string; lastSequence: number; }
export interface CoverageRunPage { items: CoverageRun[]; nextCursor?: string; }
export interface CoverageReport { reportId: string; coverageRunId: string; testRunId: string; schemaVersion: CoverageSchemaVersionV14; createdAt: Date; completeness: CoverageCompletenessV14; summary: CoverageSummaryV14; toolProvenance: CoverageToolProvenanceV14; artifactId: string; }
export interface CoverageMetricV14 { covered: number; total: number; }
export interface CoverageSummaryV14 { lines: CoverageMetricV14; branches: CoverageMetricV14; functions: CoverageMetricV14; }
export interface CoverageCompilerV14 { family: CoverageCompilerFamilyV14; version: string; }
export interface CoverageDriverV14 { name: CoverageDriverNameV14; version: string; }
export interface CoverageCollectorV14 { name: CoverageCollectorNameV14; version: string; }
export interface CoverageToolProvenanceV14 { platform: CoveragePlatformV14; architecture: CoverageArchitectureV14; compiler: CoverageCompilerV14; driver: CoverageDriverV14; collector: CoverageCollectorV14; normalizerVersion: string; instrumentationFingerprint: string; }
export interface CoverageCompletenessV14 { outcome: CoverageCompletenessOutcomeV14; reasons: CoverageIncompleteReasonV14[]; }
export enum CoverageSchemaVersionV14 { The10 = "1.0" }
export enum CoverageCompletenessOutcomeV14 { Available = "available", Partial = "partial" }
export enum CoverageIncompleteReasonV14 { TestCrashed = "test_crashed", TestTimedOut = "test_timed_out", ProfileMissingForFailedInvocation = "profile_missing_for_failed_invocation" }
export enum CoveragePlatformV14 { Windows = "windows", Linux = "linux" }
export enum CoverageArchitectureV14 { X86 = "x86", X64 = "x64", Arm64 = "arm64" }
export enum CoverageCompilerFamilyV14 { GCC = "gcc", Clang = "clang", ClangCl = "clang-cl" }
export enum CoverageDriverNameV14 { Gcov = "gcov", LlvmCov = "llvm-cov" }
export enum CoverageCollectorNameV14 { Gcovr = "gcovr", LlvmCov = "llvm-cov" }
export enum CoverageRunStatusV14 { Queued = "queued", Running = "running", Finished = "finished" }
export enum CoverageRunOutcomeV14 { Available = "available", Partial = "partial", Unavailable = "unavailable", Cancelled = "cancelled" }
export enum CoverageRunReasonV14 { UserCancelled = "user_cancelled", TaskTimedOut = "task_timed_out", InstrumentationFailed = "instrumentation_failed", BuildFailed = "build_failed", ProfileCollectionFailed = "profile_collection_failed", MergeFailed = "merge_failed", NormalizationFailed = "normalization_failed", ReportGenerationFailed = "report_generation_failed", PersistenceFailed = "persistence_failed", ServiceRestarted = "service_restarted" }
