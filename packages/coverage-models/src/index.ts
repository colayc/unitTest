export type {
  CoverageCompletenessV1,
  CoverageDocumentV1,
  CoverageFileV1,
  CoverageLineV1,
  CoverageMetricV1,
  CoverageProvenanceV1,
  CoverageSummaryV1
} from "./generated/coverage-v1.js";

export {
  decodeCoverageDocumentV1,
  validateCoverageDocumentV1
} from "./decoder.js";
