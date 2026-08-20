package buildcontract

import "errors"

var (
	ErrWorkspaceChanged     = errors.New("workspace changed")
	ErrProjectNotFound      = errors.New("project not found")
	ErrBuildProfileNotFound = errors.New("build profile not found")
)
