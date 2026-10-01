import type { TestSelectionSnapshotV15, TestSelectionV15 } from "./test-v1-5.js";

export interface CoverageContractV15 { runStartRequest: CoverageRunStartRequest; run: CoverageRun; runPage: CoverageRunPage; report: CoverageReport; }
export interface CoverageRunStartRequest { idempotencyKey: string; workspaceGeneration: string; projectId: string; coverageProfileId: string; catalogRevision: string; selection: TestSelectionV15; repeatCount: number; timeoutMs: number; }
export interface CoverageRun { coverageRunId: string; taskId: string; testRunId: string; workspaceGeneration: string; projectId: string; coverageProfileId: string; catalogRevision: string; selectionSnapshot: TestSelectionSnapshotV15; repeatCount: number; timeoutMs: number; status: CoverageRunStatusV15; outcome?: CoverageRunOutcomeV15; reason?: CoverageRunReasonV15; createdAt: Date; startedAt?: Date; finishedAt?: Date; reportId?: string; lastSequence: number; }
export interface CoverageRunPage { items: CoverageRun[]; nextCursor?: string; }
export interface CoverageReport { reportId: string; coverageRunId: string; testRunId: string; schemaVersion: CoverageSchemaVersionV15; createdAt: Date; completeness: CoverageCompletenessV15; summary: CoverageSummaryV15; toolProvenance: CoverageToolProvenanceV15; artifactId: string; sources?: CoverageSourceSnapshotV15[]; }
export interface CoverageSourceSnapshotV15 { uri: string; sha256: string; }
export interface CoverageMetricV15 { covered: number; total: number; }
export interface CoverageSummaryV15 { lines: CoverageMetricV15; branches: CoverageMetricV15; functions: CoverageMetricV15; }
export interface CoverageCompilerV15 { family: CoverageCompilerFamilyV15; version: string; }
export interface CoverageDriverV15 { name: CoverageDriverNameV15; version: string; }
export interface CoverageCollectorV15 { name: CoverageCollectorNameV15; version: string; }
export interface CoverageToolProvenanceV15 { platform: CoveragePlatformV15; architecture: CoverageArchitectureV15; compiler: CoverageCompilerV15; driver: CoverageDriverV15; collector: CoverageCollectorV15; normalizerVersion: string; instrumentationFingerprint: string; }
export interface CoverageCompletenessV15 { outcome: CoverageCompletenessOutcomeV15; reasons: CoverageIncompleteReasonV15[]; }
export enum CoverageSchemaVersionV15 { The10 = "1.0" }
export enum CoverageCompletenessOutcomeV15 { Available = "available", Partial = "partial" }
export enum CoverageIncompleteReasonV15 { TestCrashed = "test_crashed", TestTimedOut = "test_timed_out", ProfileMissingForFailedInvocation = "profile_missing_for_failed_invocation" }
export enum CoveragePlatformV15 { Windows = "windows", Linux = "linux" }
export enum CoverageArchitectureV15 { X86 = "x86", X64 = "x64", Arm64 = "arm64" }
export enum CoverageCompilerFamilyV15 { GCC = "gcc", Clang = "clang", ClangCl = "clang-cl" }
export enum CoverageDriverNameV15 { Gcov = "gcov", LlvmCov = "llvm-cov" }
export enum CoverageCollectorNameV15 { Gcovr = "gcovr", LlvmCov = "llvm-cov" }
export enum CoverageRunStatusV15 { Queued = "queued", Running = "running", Finished = "finished" }
export enum CoverageRunOutcomeV15 { Available = "available", Partial = "partial", Unavailable = "unavailable", Cancelled = "cancelled" }
export enum CoverageRunReasonV15 { UserCancelled = "user_cancelled", TaskTimedOut = "task_timed_out", InstrumentationFailed = "instrumentation_failed", BuildFailed = "build_failed", ProfileCollectionFailed = "profile_collection_failed", MergeFailed = "merge_failed", NormalizationFailed = "normalization_failed", ReportGenerationFailed = "report_generation_failed", PersistenceFailed = "persistence_failed", ServiceRestarted = "service_restarted" }
