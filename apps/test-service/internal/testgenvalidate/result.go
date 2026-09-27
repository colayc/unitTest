package testgenvalidate

import "unit-test-ide.local/test-service/internal/testgendomain"

// Stage is a closed, strictly ordered validation transition.
type Stage string

const (
	StageConfigure Stage = "configure"
	StageCompile   Stage = "compile"
	StageDiscover  Stage = "discover"
	StageCandidate Stage = "candidate"
	StageSuite     Stage = "suite"
	StageCoverage  Stage = "coverage"
)

var stageOrder = [...]Stage{StageConfigure, StageCompile, StageDiscover, StageCandidate, StageSuite, StageCoverage}

type Diagnostic string

const (
	DiagnosticNone        Diagnostic = ""
	DiagnosticInvalid     Diagnostic = "invalid-input"
	DiagnosticIsolation   Diagnostic = "isolation-failed"
	DiagnosticStageFailed Diagnostic = "stage-failed"
	DiagnosticNoDiscovery Diagnostic = "candidate-not-discovered"
	DiagnosticCoverage    Diagnostic = "coverage-invalid"
	DiagnosticNoDelta     Diagnostic = "no-coverage-delta"
	DiagnosticRegression  Diagnostic = "coverage-regression"
	DiagnosticAssertion   Diagnostic = "assertion-unproven"
	DiagnosticCleanup     Diagnostic = "cleanup-failed"
)

type Metrics struct{ Functions, Lines, Branches bool }

type AssertionEvidence struct {
	Kind                   testgendomain.CandidateKind
	IndependentProofDigest string
	ObservedOutputReceipt  string
}

const (
	Verified         = testgendomain.KindVerified
	Characterization = testgendomain.KindCharacterization
)

type StageReceipt struct {
	Stage          Stage
	Digest         string
	OutputDigest   string
	CoverageDigest string
}

type CoverageDelta = testgendomain.Coverage

type ValidationResult struct {
	Retained   bool
	Diagnostic Diagnostic
	Receipts   []StageReceipt
	Delta      CoverageDelta
}
