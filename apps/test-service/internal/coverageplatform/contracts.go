// Package coverageplatform defines the retained capabilities shared by
// coverage implementations without prescribing a particular toolchain.
package coverageplatform

import (
	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/task"
)

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
