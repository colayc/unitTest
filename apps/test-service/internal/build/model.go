package build

import (
	"errors"
	"time"

	"unit-test-ide.local/test-service/internal/buildcontract"
	"unit-test-ide.local/test-service/internal/cmake"
)

var (
	ErrWorkspaceChanged       = buildcontract.ErrWorkspaceChanged
	ErrWorkspaceTrustRequired = errors.New("workspace trust required")
	ErrProjectNotFound        = buildcontract.ErrProjectNotFound
	ErrBuildProfileNotFound   = buildcontract.ErrBuildProfileNotFound
	ErrTargetNotFound         = errors.New("target not found")
	ErrConfigureRequired      = errors.New("configure required")
)

type StartRequest struct {
	IdempotencyKey      string
	WorkspaceGeneration string
	ProjectID           string
	BuildProfileID      string
	TargetIDs           []string
	Jobs                int
	Timeout             time.Duration
	Coverage            *CoverageOptions `json:"-"`
}

type CoverageOptions struct {
	BinaryDir                  string
	TopLevelInclude            cmake.FingerprintFile
	InstrumentationFingerprint string
	ToolsetIdentity            string
	BinaryDirIdentity          string
}

type TargetsRequest struct {
	WorkspaceGeneration string
	ProjectID           string
	BuildProfileID      string
}
