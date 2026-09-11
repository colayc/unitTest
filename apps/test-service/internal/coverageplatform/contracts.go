// Package coverageplatform defines the retained capabilities shared by
// coverage implementations without prescribing a particular toolchain.
package coverageplatform

import (
	"errors"
	"reflect"
	"strings"

	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/task"
)

var ErrInvalidCapability = errors.New("invalid coverage platform capability")

type DirectoryVerifier interface {
	Path() string
	Verify() error
}

// RetainedDirectoryVerifier is an attested directory capability that can
// issue an independently owned verifier for the same directory. Consumers
// must close only the returned verifier when it also exposes ownership; the
// original view remains owned by its producer.
type RetainedDirectory interface {
	DirectoryVerifier
	Close() error
}

type RetainedDirectoryVerifier interface {
	DirectoryVerifier
	RetainDirectory() (RetainedDirectory, error)
}

type Output interface {
	ReadAll() ([]byte, error)
}

type OwnershipClaim interface {
	Commit()
	Rollback()
}

type Toolset interface {
	Version() string
	Identity() string
	CCompiler() coveragerun.TrustedPath
	CXXCompiler() coveragerun.TrustedPath
	Tools() []coveragerun.TrustedPath
	Verify() error
	ClaimOwnership() (OwnershipClaim, error)
	Close() error
}

type CollectorExecution interface {
	ProcessSpec() task.ProcessSpec
	Verify() error
	VerifyAfter() error
	ValidateProcessTarget(string, []string, []string, []string, string) error
	PinnedOutput() (Output, error)
	Close() error
}

type Instrumentation struct {
	IncludePath string
	SHA256      string
	Fingerprint string
}

func VerifyDirectory(value DirectoryVerifier) error {
	if value == nil || nilDirectoryVerifier(value) || strings.TrimSpace(value.Path()) == "" {
		return ErrInvalidCapability
	}
	if err := value.Verify(); err != nil {
		return errors.Join(ErrInvalidCapability, err)
	}
	return nil
}

// RetainDirectory rejects ordinary path/verification facades and returns an
// independently retained verifier whose path and verification result still
// match the source capability. This is the narrow bridge from a build-owned
// view to a consumer-owned retained capability.
func RetainDirectory(value DirectoryVerifier) (RetainedDirectory, error) {
	if err := VerifyDirectory(value); err != nil {
		return nil, err
	}
	retainer, ok := value.(RetainedDirectoryVerifier)
	if !ok || nilRetainedDirectoryVerifier(retainer) {
		return nil, ErrInvalidCapability
	}
	retained, err := retainer.RetainDirectory()
	if err != nil {
		return nil, errors.Join(ErrInvalidCapability, err)
	}
	if retained == nil || nilRetainedDirectory(retained) {
		return nil, ErrInvalidCapability
	}
	if sameCapabilityObject(retained, value) {
		// The producer returned the caller-owned object rather than a clone.
		// Reject it without closing it: ownership never crossed the boundary.
		return nil, ErrInvalidCapability
	}
	if retained.Path() != value.Path() {
		return nil, errors.Join(ErrInvalidCapability, retained.Close())
	}
	if err := VerifyDirectory(retained); err != nil {
		return nil, errors.Join(err, retained.Close())
	}
	if err := VerifyDirectory(value); err != nil || retained.Path() != value.Path() {
		closeErr := retained.Close()
		if err != nil {
			return nil, errors.Join(err, closeErr)
		}
		return nil, errors.Join(ErrInvalidCapability, closeErr)
	}
	return retained, nil
}

func VerifyToolset(value Toolset) error {
	if value == nil || nilToolset(value) || value.Version() == "" || value.Identity() == "" {
		return ErrInvalidCapability
	}
	for _, path := range []coveragerun.TrustedPath{value.CCompiler(), value.CXXCompiler()} {
		if err := verifyTrustedPath(path); err != nil {
			return err
		}
	}
	tools := value.Tools()
	if len(tools) == 0 {
		return ErrInvalidCapability
	}
	for _, path := range tools {
		if err := verifyTrustedPath(path); err != nil {
			return err
		}
	}
	if err := value.Verify(); err != nil {
		return errors.Join(ErrInvalidCapability, err)
	}
	return nil
}

func verifyTrustedPath(value coveragerun.TrustedPath) error {
	if value == nil || nilTrustedPath(value) || strings.TrimSpace(value.Path()) == "" {
		return ErrInvalidCapability
	}
	if err := value.Verify(); err != nil {
		return errors.Join(ErrInvalidCapability, err)
	}
	return nil
}

func nilDirectoryVerifier(value DirectoryVerifier) bool {
	return nilInterfaceValue(reflect.ValueOf(value))
}
func nilRetainedDirectoryVerifier(value RetainedDirectoryVerifier) bool {
	return nilInterfaceValue(reflect.ValueOf(value))
}
func nilRetainedDirectory(value RetainedDirectory) bool {
	return nilInterfaceValue(reflect.ValueOf(value))
}
func nilToolset(value Toolset) bool { return nilInterfaceValue(reflect.ValueOf(value)) }
func nilTrustedPath(value coveragerun.TrustedPath) bool {
	return nilInterfaceValue(reflect.ValueOf(value))
}

func nilInterfaceValue(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func sameCapabilityObject(left, right any) bool {
	leftValue, rightValue := reflect.ValueOf(left), reflect.ValueOf(right)
	if !leftValue.IsValid() || !rightValue.IsValid() || leftValue.Type() != rightValue.Type() {
		return false
	}
	switch leftValue.Kind() {
	case reflect.Chan, reflect.Map, reflect.Pointer, reflect.UnsafePointer:
		return leftValue.Pointer() == rightValue.Pointer()
	default:
		// Non-reference implementations cannot prove independent ownership, so
		// RetainDirectory's Verify/Path checks remain the fail-closed boundary.
		return false
	}
}
