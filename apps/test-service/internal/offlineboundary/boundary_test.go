package offlineboundary

import (
	"context"
	"errors"
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
