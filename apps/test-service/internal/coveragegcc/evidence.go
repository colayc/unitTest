package coveragegcc

import (
	"context"
	"errors"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/testrun"
)

var ErrInvalidEvidence = errors.New("invalid GCC coverage evidence")

type Entry struct {
	RelativePath string
	SHA256       string
	Size         int64
}
type Manifest struct {
	Notes          []Entry
	Data           []Entry
	PartialReasons []coveragedomain.CompletenessReason
	state          *evidenceState
}
type PreparedEvidence struct {
	Notes []Entry
	state *evidenceState
}
type evidenceState struct {
	verify  func([]Entry, []Entry, []coveragedomain.CompletenessReason) error
	close   func([]Entry) error
	prepare func([]Entry) error
	seal    func(context.Context, []Entry, []testrun.InvocationOutcome) (Manifest, error)
}

func SealEvidence(ctx context.Context, objectRoot string, outcomes []testrun.InvocationOutcome) (Manifest, error) {
	return sealEvidence(ctx, objectRoot, outcomes)
}
func PrepareEvidence(objectRoot string) (*PreparedEvidence, error) {
	return prepareEvidence(objectRoot)
}

// PrepareBuildEvidence scans a service-owned CMake build root. CMake places
// compiler notes/data alongside generated build files, so this variant admits
// only the fixed CMake artifact shape while retaining evidence integrity checks.
func PrepareBuildEvidence(objectRoot string) (*PreparedEvidence, error) {
	return prepareBuildEvidence(objectRoot)
}
func (p *PreparedEvidence) Seal(ctx context.Context, outcomes []testrun.InvocationOutcome) (Manifest, error) {
	manifest, err := sealPreparedEvidence(ctx, p, outcomes)
	if err == nil && p != nil {
		p.Notes = nil
		p.state = nil
	}
	return manifest, err
}
func (p *PreparedEvidence) Close() error {
	if p == nil || p.state == nil || p.state.close == nil {
		return nil
	}
	state := p.state
	p.state = nil
	p.Notes = nil
	return state.close(nil)
}
func (m Manifest) Verify() error { return verifyEvidenceManifest(m) }
func (m *Manifest) Close() error { return closeEvidenceManifest(m) }
