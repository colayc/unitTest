//go:build !windows

package toolhash

import (
	"context"
	"testing"
)

func TestToolHashNonWindowsNoReuse(t *testing.T) {
	parent := context.Background()
	ctx, s, err := Begin(parent)
	if err != nil || ctx != parent || s != nil {
		t.Fatal("non-Windows scope created", err)
	}
	r, ok, err := Digest(ctx, "unused", nil, 0)
	if err != nil || ok || r.Evidence != nil {
		t.Fatal("non-Windows evidence created", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(parent)
	cancel()
	if _, _, err := Digest(cancelled, "unused", nil, 0); err == nil {
		t.Fatal("cancellation hidden")
	}
	if _, _, err := Begin(nil); err == nil {
		t.Fatal("nil context accepted")
	}
}
