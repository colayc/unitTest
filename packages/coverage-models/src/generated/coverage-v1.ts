export interface CoverageDocumentV1 {
    completeness:  CoverageCompletenessV1;
    files:         CoverageFileV1[];
    provenance:    CoverageProvenanceV1;
    schemaVersion: SchemaVersion;
    summary:       CoverageSummaryV1;
}

export interface CoverageCompletenessV1 {
    outcome: Outcome;
    reasons: Reason[];
}

export type Outcome = "available" | "partial";

export type Reason = "test_crashed" | "test_timed_out" | "profile_missing_for_failed_invocation";

export interface CoverageFileV1 {
    lines:   CoverageLineV1[];
    sha256:  string;
    summary: CoverageSummaryV1;
    uri:     string;
}

export interface CoverageLineV1 {
    branches: CoverageMetricV1;
    count:    number;
    line:     number;
}

export interface CoverageMetricV1 {
    covered: number;
    total:   number;
}

export interface CoverageSummaryV1 {
    branches:  CoverageMetricV1;
    functions: CoverageMetricV1;
    lines:     CoverageMetricV1;
}

export interface CoverageProvenanceV1 {
    architecture:               Architecture;
    collector:                  CoverageCollectorV1;
    compiler:                   CoverageCompilerV1;
    driver:                     CoverageDriverV1;
    instrumentationFingerprint: string;
    normalizerVersion:          string;
    platform:                   Platform;
}

export type Architecture = "x86" | "x64" | "arm64";

export interface CoverageCollectorV1 {
    name:    CollectorName;
    version: string;
}

export type CollectorName = "gcovr" | "llvm-cov";

export interface CoverageCompilerV1 {
    family:  Family;
    version: string;
}

export type Family = "gcc" | "clang" | "clang-cl";

export interface CoverageDriverV1 {
    name:    DriverName;
    version: string;
}

export type DriverName = "gcov" | "llvm-cov";

export type Platform = "windows" | "linux";

export type SchemaVersion = "1.0";
