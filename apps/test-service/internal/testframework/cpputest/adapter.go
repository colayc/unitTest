package cpputest

import (
	"errors"

	"unit-test-ide.local/test-service/internal/testdomain"
)

var (
	ErrInvalidAdapter         = errors.New("invalid CppUTest adapter")
	ErrDiscoveryFailed        = errors.New("CppUTest discovery failed")
	ErrInvalidList            = errors.New("invalid CppUTest discovery list")
	ErrLimitExceeded          = errors.New("CppUTest discovery limit exceeded")
	ErrInvalidLimits          = errors.New("invalid CppUTest discovery limits")
	ErrInvalidRunPlan         = errors.New("invalid CppUTest run plan")
	ErrIncompatibleDescriptor = errors.New("incompatible CTest execution descriptor")
	ErrReservedArguments      = errors.New("reserved CppUTest argument conflict")
	ErrInvalidResult          = errors.New("invalid CppUTest result")
	ErrResultLimitExceeded    = errors.New("CppUTest result limit exceeded")
)

const ContractVersion = "cpputest.v1"

type CaseIdentity struct {
	Group string
	Name  string
}

type SelectionMode string

const (
	SelectionAll   SelectionMode = "all"
	SelectionGroup SelectionMode = "group"
	SelectionCases SelectionMode = "cases"
)

type SelectedCase struct {
	ItemID testdomain.ID
	Group  string
	Name   string
}

type Selection struct {
	Mode  SelectionMode
	Group string
	Cases []SelectedCase
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
