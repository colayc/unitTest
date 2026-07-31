package testdomain

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidIdentity           = errors.New("invalid test identity")
	ErrInvalidFramework          = errors.New("invalid test framework")
	ErrInvalidCatalog            = errors.New("invalid test catalog")
	ErrInvalidResult             = errors.New("invalid test result")
	ErrDuplicateIdentity         = errors.New("duplicate test identity")
	ErrInvalidSelection          = errors.New("invalid test selection")
	ErrEmptySelection            = errors.New("empty test selection")
	ErrSelectionTooLarge         = errors.New("test selection exceeds limit")
	ErrUnknownSelectionID        = errors.New("unknown test selection id")
	ErrFailedRunResolverRequired = errors.New("failedFromRun requires persisted run resolver")
	ErrCatalogStale              = errors.New("test catalog cursor is stale")
)

type ValidationError struct {
	Kind   error
	Field  string
	Detail string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return fmt.Sprintf("%v: %s", e.Kind, e.Detail)
	}
	return fmt.Sprintf("%v: %s: %s", e.Kind, e.Field, e.Detail)
}

func (e *ValidationError) Unwrap() error {
	return e.Kind
}

func invalid(kind error, field, detail string) error {
	return &ValidationError{Kind: kind, Field: field, Detail: detail}
}
