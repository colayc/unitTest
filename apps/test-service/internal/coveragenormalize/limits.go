package coveragenormalize

import "errors"

var (
	ErrInvalidLimits = errors.New("invalid coverage normalization limits")
	ErrLimitExceeded = errors.New("coverage normalization limit exceeded")
)

// Limits bounds all untrusted coverage parser and normalizer dimensions.
// Counts use uint64 so malformed oversized JSON values cannot wrap into a
// smaller accepted value before the check.
type Limits struct {
	MaxInputBytes  int64
	MaxFiles       int64
	MaxLines       int64
	MaxBranches    int64
	MaxFunctions   int64
	MaxStringBytes int64
	MaxDepth       int64
}

func DefaultLimits() Limits {
	return Limits{
		MaxInputBytes:  64 << 20,
		MaxFiles:       100_000,
		MaxLines:       2_000_000,
		MaxBranches:    2_000_000,
		MaxFunctions:   500_000,
		MaxStringBytes: 8 << 20,
		MaxDepth:       64,
	}
}

func (limits Limits) Validate() error {
	if limits.MaxInputBytes <= 0 || limits.MaxFiles <= 0 || limits.MaxLines <= 0 ||
		limits.MaxBranches <= 0 || limits.MaxFunctions <= 0 || limits.MaxStringBytes <= 0 || limits.MaxDepth <= 0 {
		return ErrInvalidLimits
	}
	return nil
}

func (limits Limits) check(value uint64, maximum int64) error {
	if err := limits.Validate(); err != nil {
		return err
	}
	if value > uint64(maximum) {
		return ErrLimitExceeded
	}
	return nil
}

func (limits Limits) CheckInputBytes(value uint64) error {
	return limits.check(value, limits.MaxInputBytes)
}
func (limits Limits) CheckFiles(value uint64) error { return limits.check(value, limits.MaxFiles) }
func (limits Limits) CheckLines(value uint64) error { return limits.check(value, limits.MaxLines) }
func (limits Limits) CheckBranches(value uint64) error {
	return limits.check(value, limits.MaxBranches)
}
func (limits Limits) CheckFunctions(value uint64) error {
	return limits.check(value, limits.MaxFunctions)
}
func (limits Limits) CheckStringBytes(value uint64) error {
	return limits.check(value, limits.MaxStringBytes)
}
func (limits Limits) CheckDepth(value uint64) error { return limits.check(value, limits.MaxDepth) }
