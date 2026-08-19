package coveragenormalize

import (
	"errors"
	"testing"
)

func TestDefaultLimitsArePositiveAndValidate(t *testing.T) {
	limits := DefaultLimits()
	if err := limits.Validate(); err != nil {
		t.Fatal(err)
	}
	if limits.MaxInputBytes <= 0 || limits.MaxFiles <= 0 || limits.MaxLines <= 0 || limits.MaxBranches <= 0 || limits.MaxFunctions <= 0 || limits.MaxStringBytes <= 0 || limits.MaxDepth <= 0 {
		t.Fatalf("invalid defaults = %#v", limits)
	}
}

func TestLimitsRejectInvalidConfigurationAndBoundEachDimension(t *testing.T) {
	limits := Limits{MaxInputBytes: 10, MaxFiles: 2, MaxLines: 3, MaxBranches: 4, MaxFunctions: 5, MaxStringBytes: 6, MaxDepth: 7}
	for name, check := range map[string]func() error{
		"input":     func() error { return limits.CheckInputBytes(11) },
		"files":     func() error { return limits.CheckFiles(3) },
		"lines":     func() error { return limits.CheckLines(4) },
		"branches":  func() error { return limits.CheckBranches(5) },
		"functions": func() error { return limits.CheckFunctions(6) },
		"strings":   func() error { return limits.CheckStringBytes(7) },
		"depth":     func() error { return limits.CheckDepth(8) },
	} {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(check(), ErrLimitExceeded) {
				t.Fatalf("error = %v", check())
			}
		})
	}
	if err := limits.CheckInputBytes(10); err != nil {
		t.Fatal(err)
	}
	if err := limits.CheckFiles(2); err != nil {
		t.Fatal(err)
	}
}

func TestLimitsRejectZeroNegativeAndOverflowValues(t *testing.T) {
	invalid := DefaultLimits()
	invalid.MaxFiles = 0
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("zero config error = %v", err)
	}
	invalid = DefaultLimits()
	invalid.MaxStringBytes = -1
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("negative config error = %v", err)
	}
	limits := Limits{MaxInputBytes: 10, MaxFiles: 10, MaxLines: 10, MaxBranches: 10, MaxFunctions: 10, MaxStringBytes: 10, MaxDepth: 10}
	if err := limits.CheckInputBytes(^uint64(0)); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("overflow input error = %v", err)
	}
}
