package runtime

import (
	"errors"
	"path/filepath"
)

var ErrUnsafeDataDir = errors.New("service data directory is not owner-only")

type Layout struct {
	Root      string
	Database  string
	Artifacts string
	Lock      string
}

func PrepareDataDir(root string) (Layout, error) {
	if root == "" {
		return Layout{}, ErrUnsafeDataDir
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return Layout{}, ErrUnsafeDataDir
	}
	absolute = filepath.Clean(absolute)
	if err := prepareOwnerOnlyDirectory(absolute); err != nil {
		return Layout{}, ErrUnsafeDataDir
	}
	return Layout{
		Root:      absolute,
		Database:  filepath.Join(absolute, "history.sqlite3"),
		Artifacts: filepath.Join(absolute, "artifacts"),
		Lock:      filepath.Join(absolute, "service.lock"),
	}, nil
}
