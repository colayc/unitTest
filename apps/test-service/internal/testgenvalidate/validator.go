// Package testgenvalidate validates staged generated tests without publishing
// them into the user workspace.
package testgenvalidate

import (
	"context"
	"errors"
	"reflect"

	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenrender"
)

var ErrInvalidRequest = errors.New("invalid test generation validation request")

type ValidationRequest struct {
	TaskID           string
	CandidateID      string
	Edits            testgenrender.StagedEditSet
	BaselineCoverage []byte
	Metrics          Metrics
	Assertion        AssertionEvidence
}

// StageExecutor is supplied by the service's trusted build/test/coverage
// planners. The request cannot provide a command, argument, or environment.
type StageExecutor interface {
	Execute(context.Context, Stage, Roots) (StageEvidence, error)
}

type StageEvidence struct {
	ExitCode          int
	Output            []byte
	DiscoveredCaseIDs []string
	CoverageJSON      []byte
}

// EvidenceVerifier checks product-owned oracle proof or repeated-output
// receipts. Caller-provided digest strings are never sufficient on their own.
type EvidenceVerifier func(context.Context, string, AssertionEvidence) bool

// ResolvedCandidate is read from the durable, service-owned candidate and
// baseline records. It is never reconstructed from client request fields.
type ResolvedCandidate struct {
	ID            string
	Kind          testgendomain.CandidateKind
	TargetSymbol  string
	TargetFileURI string
	// TargetLines is the authoritative source-line set for TargetSymbol.
	// Coverage JSON v1 has line IDs but no function or branch IDs.
	TargetLines          []int64
	BaselineSHA256       string
	SourceSnapshotDigest string
	AssertionDigest      string
}
type CandidateResolver func(context.Context, string) (ResolvedCandidate, error)

type Config struct {
	SourceRoot       string
	TempRoot         string
	Planner          StageExecutor
	VerifyEvidence   EvidenceVerifier
	ResolveCandidate CandidateResolver
}

type Validator struct{ Config Config }

func validDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func validRequest(r ValidationRequest) bool {
	if !validDigest(r.TaskID) || !validDigest(r.CandidateID) || len(r.Edits.Files) > 128 || !r.Metrics.Functions && !r.Metrics.Lines && !r.Metrics.Branches || len(r.BaselineCoverage) == 0 || len(r.BaselineCoverage) > 32<<20 {
		return false
	}
	switch r.Assertion.Kind {
	case Verified:
		return validDigest(r.Assertion.IndependentProofDigest) && r.Assertion.ObservedOutputReceipt == ""
	case Characterization:
		return validDigest(r.Assertion.ObservedOutputReceipt) && r.Assertion.IndependentProofDigest == ""
	default:
		return false
	}
}

func (v Validator) Validate(ctx context.Context, r ValidationRequest) (result ValidationResult, err error) {
	if ctx == nil || !validRequest(r) || v.Config.Planner == nil || v.Config.VerifyEvidence == nil || v.Config.ResolveCandidate == nil {
		return result, ErrInvalidRequest
	}
	resolved, resolveErr := v.Config.ResolveCandidate(ctx, r.CandidateID)
	if resolveErr != nil || resolved.ID != r.CandidateID || (resolved.Kind != Verified && resolved.Kind != Characterization) || resolved.Kind != r.Assertion.Kind || resolved.TargetSymbol == "" || !safeRelative(resolved.TargetFileURI) || !validTargetLines(resolved.TargetLines) || !validDigest(resolved.BaselineSHA256) || resolved.BaselineSHA256 != digestBytes(r.BaselineCoverage) || !validDigest(resolved.AssertionDigest) || resolved.AssertionDigest != assertionDigest(r.Assertion) {
		return ValidationResult{Diagnostic: DiagnosticAssertion}, nil
	}
	if _, err := decodeCoverage(r.BaselineCoverage); err != nil {
		return ValidationResult{Diagnostic: DiagnosticCoverage}, nil
	}
	original, fingerprint, err := sourceFingerprint(v.Config.SourceRoot)
	if err != nil {
		return ValidationResult{Diagnostic: DiagnosticIsolation}, nil
	}
	if !validDigest(resolved.SourceSnapshotDigest) || resolved.SourceSnapshotDigest != fingerprint {
		return ValidationResult{Diagnostic: DiagnosticIsolation}, nil
	}
	roots, root, err := snapshot(v.Config.SourceRoot, v.Config.TempRoot, r.Edits.Files, original)
	if root != "" {
		defer func() {
			if cleanupErr := cleanup(root); cleanupErr != nil {
				result.Retained = false
				result.Diagnostic = DiagnosticCleanup
				err = errors.Join(err, cleanupErr)
			}
		}()
	}
	if err != nil {
		return ValidationResult{Diagnostic: DiagnosticIsolation}, nil
	}
	current, _, err := sourceFingerprint(v.Config.SourceRoot)
	if err != nil || !reflect.DeepEqual(original, current) {
		return ValidationResult{Diagnostic: DiagnosticIsolation}, nil
	}
	staged, stagedDigest, err := sourceFingerprint(roots.Source)
	if err != nil {
		return ValidationResult{Diagnostic: DiagnosticIsolation}, nil
	}
	roots.SnapshotDigest = digestBytes([]byte(fingerprint + stagedDigest))
	if !v.Config.VerifyEvidence(ctx, r.CandidateID, r.Assertion) {
		return ValidationResult{Diagnostic: DiagnosticAssertion}, nil
	}
	return v.runStages(ctx, r, roots, original, staged, resolved)
}

func validTargetLines(lines []int64) bool {
	if len(lines) == 0 || len(lines) > 10000 {
		return false
	}
	var previous int64
	for _, line := range lines {
		if line <= previous || line > 9007199254740991 {
			return false
		}
		previous = line
	}
	return true
}

func assertionDigest(e AssertionEvidence) string {
	if e.Kind == Verified {
		return e.IndependentProofDigest
	}
	return e.ObservedOutputReceipt
}
