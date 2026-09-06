package coverageplatform

import (
	"errors"
	"testing"
)

type retainedContractVerifier struct {
	path   string
	valid  bool
	retain RetainedDirectory
}

type nilRetainedClone struct{}

func (*nilRetainedClone) Path() string { panic("typed-nil retained clone Path called") }
func (*nilRetainedClone) Verify() error { panic("typed-nil retained clone Verify called") }
func (*nilRetainedClone) Close() error { panic("typed-nil retained clone Close called") }

func (verifier *retainedContractVerifier) Path() string {
	if verifier == nil || !verifier.valid {
		return ""
	}
	return verifier.path
}

func (verifier *retainedContractVerifier) Verify() error {
	if verifier == nil || !verifier.valid {
		return ErrInvalidCapability
	}
	return nil
}

func (verifier *retainedContractVerifier) RetainDirectory() (RetainedDirectory, error) {
	if verifier == nil || !verifier.valid || verifier.retain == nil {
		return nil, ErrInvalidCapability
	}
	return verifier.retain, nil
}

func (*retainedContractVerifier) Close() error { return nil }

func TestRetainDirectoryRequiresAttestedExactClone(t *testing.T) {
	if _, err := RetainDirectory(&contractVerifier{valid: true}); !errors.Is(err, ErrInvalidCapability) {
		t.Fatalf("RetainDirectory accepted bare verifier: %v", err)
	}
	retained := &retainedContractVerifier{path: "C:\\coverage", valid: true}
	retained.retain = &retainedContractVerifier{path: "C:\\coverage", valid: true}
	clone, err := RetainDirectory(retained)
	if err != nil {
		t.Fatalf("RetainDirectory() = %v", err)
	}
	if clone == retained || clone.Path() != retained.Path() {
		t.Fatalf("RetainDirectory() = %#v, want independent exact clone", clone)
	}
	mismatch := &retainedContractVerifier{path: "C:\\coverage", valid: true}
	mismatch.retain = &retainedContractVerifier{path: "C:\\other", valid: true}
	if _, err := RetainDirectory(mismatch); !errors.Is(err, ErrInvalidCapability) {
		t.Fatalf("RetainDirectory accepted path mismatch: %v", err)
	}
}

func TestRetainDirectoryRejectsTypedNilCloneWithoutPanic(t *testing.T) {
	retainer := &retainedContractVerifier{path: "C:\\coverage", valid: true}
	var typedNil *nilRetainedClone
	retainer.retain = typedNil
	if _, err := RetainDirectory(retainer); !errors.Is(err, ErrInvalidCapability) {
		t.Fatalf("RetainDirectory accepted typed-nil clone: %v", err)
	}
}
