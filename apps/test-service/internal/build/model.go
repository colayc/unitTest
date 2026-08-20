package build

import (
	"errors"
	"time"

	"unit-test-ide.local/test-service/internal/cmake"
)

var (
	ErrWorkspaceChanged       = errors.New("workspace changed")
	ErrWorkspaceTrustRequired = errors.New("workspace trust required")
	ErrProjectNotFound        = errors.New("project not found")
	ErrBuildProfileNotFound   = errors.New("build profile not found")
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
}

type TargetsRequest struct {
	WorkspaceGeneration string
	ProjectID           string
	BuildProfileID      string
}
