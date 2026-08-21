package offlineboundary

import (
	"context"
	"errors"
	"io"
	"runtime"
	"testing"
)

func TestStartRequiresOwnerIdentity(t *testing.T) {
	_, err := New(Config{}).Start(context.Background(), OwnerIdentity{})
	if !errors.Is(err, ErrOwnerIdentityMismatch) {
		t.Fatalf("error = %v", err)
	}
}

func TestNonWindowsBoundaryHasNoNativeSideEffect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows contract")
	}
	_, err := New(Config{}).Start(context.Background(), OwnerIdentity{PID: 1, CreationTime: 1})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error = %v", err)
	}
}

func TestLeaseIDEntropyFailureFailsClosed(t *testing.T) {
	leaseID, err := newLeaseIDFrom(func([]byte) (int, error) {
		return 0, io.ErrUnexpectedEOF
	})
	if leaseID != nil {
		t.Fatalf("newLeaseIDFrom() lease ID = %x, want nil", leaseID)
	}
	if err == nil {
		t.Fatal("newLeaseIDFrom() error = nil, want entropy failure")
	}
}
