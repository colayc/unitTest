// Package gcovr parses the closed gcovr 8.6 JSON export used by Linux GCC
// coverage collection. The model intentionally contains only evidence needed
// by the public coverage schema.
package gcovr

import "errors"

const maxSafeInteger int64 = 9_007_199_254_740_991

var (
	ErrInvalidExport = errors.New("invalid gcovr coverage export")
	ErrInvalidLimits = errors.New("invalid gcovr coverage parser limits")
	ErrLimitExceeded = errors.New("gcovr coverage parser limit exceeded")
)

type Export struct {
	FormatVersion string
	Files         []File
}

type File struct {
	RelativePath string
	Functions    Metric
	Lines        []Line
}

type Metric struct {
	Covered int64
	Total   int64
}

type Line struct {
	Number   int64
	Count    int64
	Branches Metric
}

type Limits struct {
	MaxInputBytes  int64
	MaxDepth       int64
	MaxFiles       int64
	MaxFunctions   int64
	MaxLines       int64
	MaxBranches    int64
	MaxStringBytes int64
}

func DefaultLimits() Limits {
	return Limits{
		MaxInputBytes: 64 << 20, MaxDepth: 64, MaxFiles: 100_000,
		MaxFunctions: 500_000, MaxLines: 2_000_000, MaxBranches: 2_000_000,
		MaxStringBytes: 8 << 20,
	}
}

func (limits Limits) validate() error {
	if limits.MaxInputBytes <= 0 || limits.MaxDepth <= 0 || limits.MaxFiles <= 0 ||
		limits.MaxFunctions <= 0 || limits.MaxLines <= 0 || limits.MaxBranches <= 0 ||
		limits.MaxStringBytes <= 0 {
		return ErrInvalidLimits
	}
	return nil
}
