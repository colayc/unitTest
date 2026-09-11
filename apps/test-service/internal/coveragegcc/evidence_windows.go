//go:build windows

package coveragegcc

import (
	"context"
	"unit-test-ide.local/test-service/internal/testrun"
)

func sealEvidence(context.Context, string, []testrun.InvocationOutcome) (Manifest, error) {
	return Manifest{}, ErrUnsupportedPlatform
}
func prepareEvidence(string) (*PreparedEvidence, error)      { return nil, ErrUnsupportedPlatform }
func prepareBuildEvidence(string) (*PreparedEvidence, error) { return nil, ErrUnsupportedPlatform }
func sealPreparedEvidence(context.Context, *PreparedEvidence, []testrun.InvocationOutcome) (Manifest, error) {
	return Manifest{}, ErrUnsupportedPlatform
}
func verifyEvidenceManifest(Manifest) error { return ErrUnsupportedPlatform }
func closeEvidenceManifest(*Manifest) error { return nil }
