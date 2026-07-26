package runtime

import (
	"errors"
	"io"
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
	layout, guard, err := prepareDataDirGuard(root)
	if err != nil {
		return Layout{}, err
	}
	if err := guard.Close(); err != nil {
		return Layout{}, ErrUnsafeDataDir
	}
	return layout, nil
}

func prepareDataDirGuard(root string) (Layout, io.Closer, error) {
	if root == "" {
		return Layout{}, nil, ErrUnsafeDataDir
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return Layout{}, nil, ErrUnsafeDataDir
	}
	absolute = filepath.Clean(absolute)
	guard, err := pinOwnerOnlyDirectory(absolute)
	if err != nil {
		return Layout{}, nil, ErrUnsafeDataDir
	}
	return Layout{
		Root:      absolute,
		Database:  filepath.Join(absolute, "history.sqlite3"),
		Artifacts: filepath.Join(absolute, "artifacts"),
		Lock:      filepath.Join(absolute, "service.lock"),
	}, guard, nil
}
