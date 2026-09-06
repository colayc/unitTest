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
	verify  func() error
	close   func() error
	cleanup func(string) error
	prepare func([]Entry) error
	root    string
}

func SealEvidence(ctx context.Context, objectRoot string, outcomes []testrun.InvocationOutcome) (Manifest, error) {
	return sealEvidence(ctx, objectRoot, outcomes)
}
func PrepareEvidence(objectRoot string) (*PreparedEvidence, error) {
	return prepareEvidence(objectRoot)
}
func (p *PreparedEvidence) Seal(ctx context.Context, outcomes []testrun.InvocationOutcome) (Manifest, error) {
	return sealPreparedEvidence(ctx, p, outcomes)
}
func (p *PreparedEvidence) Close() error {
	if p == nil || p.state == nil || p.state.close == nil {
		return nil
	}
	return p.state.close()
}
func (m Manifest) Verify() error { return verifyEvidenceManifest(m) }
func (m *Manifest) Close() error { return closeEvidenceManifest(m) }
