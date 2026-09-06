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
