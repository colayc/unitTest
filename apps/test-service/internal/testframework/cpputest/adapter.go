package cpputest

import "errors"

var (
	ErrInvalidList   = errors.New("invalid CppUTest discovery list")
	ErrLimitExceeded = errors.New("CppUTest discovery limit exceeded")
	ErrInvalidLimits = errors.New("invalid CppUTest discovery limits")
)

const ContractVersion = "cpputest.v1"

type CaseIdentity struct {
	Group string
	Name  string
}

type Limits struct {
	MaxDocumentBytes int
	MaxTokenBytes    int
	MaxCases         int
}

func DefaultLimits() Limits {
	return Limits{
		MaxDocumentBytes: 16 * 1024 * 1024,
		MaxTokenBytes:    64 * 1024,
		MaxCases:         100_000,
	}
}

func (limits Limits) Valid() bool {
	return limits.MaxDocumentBytes >= 0 &&
		limits.MaxTokenBytes >= 0 &&
		limits.MaxCases >= 0
}
