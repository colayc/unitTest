package toolhash

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestToolHashValidateContextBindingAndLiveness(t *testing.T) {
	for _, name := range []string{"live", "foreign", "closed", "failed", "call-cancelled", "parent-cancelled", "nil-scope", "nil-context"} {
		t.Run(name, func(t *testing.T) {
			parent, cancelParent := context.WithCancel(context.Background())
			defer cancelParent()
			s := newScope(parent, scopeOps{}, MaxEntries, MaxEligibleBytes)
			defer s.Close()
			ctx := context.WithValue(parent, scopeKey{}, s)
			wantErr := name != "live"
			sentinel := errors.New("bound validation failed")
			switch name {
			case "foreign":
				other := newScope(parent, scopeOps{}, MaxEntries, MaxEligibleBytes)
				defer other.Close()
				ctx = context.WithValue(ctx, scopeKey{}, other)
			case "closed":
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			case "failed":
				s.poison(sentinel)
			case "call-cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "parent-cancelled":
				cancelParent()
			case "nil-scope":
				s = nil
			case "nil-context":
				ctx = nil
			}
			var before Stats
			if s != nil {
				before = s.Stats()
			}
			err := s.ValidateContext(ctx)
			if (err != nil) != wantErr {
				t.Fatalf("%s: accepted invalid binding/lifetime: %v", name, err)
			}
			if name == "failed" && !errors.Is(err, sentinel) {
				t.Fatalf("lost sticky error: %v", err)
			}
			if (name == "call-cancelled" || name == "parent-cancelled") && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			if s != nil && s.Stats() != before {
				t.Fatal("read-only check changed stats")
			}
			if name == "foreign" && s.Err() != nil {
				t.Fatal("foreign check poisoned owner")
			}
		})
	}
}

func TestToolHashValidateContextConcurrentClose(t *testing.T) {
	ctx := context.Background()
	s := newScope(ctx, scopeOps{}, MaxEntries, MaxEligibleBytes)
	ctx = context.WithValue(ctx, scopeKey{}, s)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 32; n++ {
				_ = s.ValidateContext(ctx)
			}
		}()
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if s.ValidateContext(ctx) == nil {
		t.Fatal("closed scope accepted after concurrent readers drained")
	}
	if s.Err() != nil || s.Stats().Retained != 0 {
		t.Fatal("ordinary close semantics changed")
	}
}
