package runtime

import (
	"errors"
	"io"
	"path/filepath"

	"unit-test-ide.local/test-service/internal/serviceanchor"
)

// CoverageAuthority returns the opaque issuer created with the prepared
// service data directory. Consumers must receive this value from runtime;
// build no longer mints authorities from arbitrary path strings.
func CoverageAuthority(layout Layout) (serviceanchor.Anchor, error) {
	if layout.CoverageAnchor.Root() == "" || layout.CoverageAnchor.Root() != layout.Root || layout.CoverageAnchor.Verify(layout.Coverage) != nil {
		return serviceanchor.Anchor{}, ErrUnsafeDataDir
	}
	return layout.CoverageAnchor, nil
}

var ErrUnsafeDataDir = errors.New("service data directory is not owner-only")

type Layout struct {
	Root           string
	Database       string
	Artifacts      string
	Build          string
	Coverage       string
	Controls       string
	Lock           string
	CoverageAnchor serviceanchor.Anchor
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
	layout := Layout{
		Root:      absolute,
		Database:  filepath.Join(absolute, "history.sqlite3"),
		Artifacts: filepath.Join(absolute, "artifacts"),
		Build:     filepath.Join(absolute, "build"),
		Coverage:  filepath.Join(absolute, "coverage"),
		Controls:  filepath.Join(absolute, "controls"),
		Lock:      filepath.Join(absolute, "service.lock"),
	}
	anchor, err := newServiceAnchor(absolute)
	if err != nil {
		return Layout{}, nil, errors.Join(ErrUnsafeDataDir, guard.Close())
	}
	layout.CoverageAnchor = anchor
	buildGuard, err := pinOwnerOnlyDirectory(layout.Build)
	if err != nil {
		return Layout{}, nil, errors.Join(ErrUnsafeDataDir, guard.Close())
	}
	controlGuard, err := pinOwnerOnlyDirectory(layout.Controls)
	if err != nil {
		return Layout{}, nil, errors.Join(
			ErrUnsafeDataDir,
			buildGuard.Close(),
			guard.Close(),
		)
	}
	coverageGuard, err := pinOwnerOnlyDirectory(layout.Coverage)
	if err != nil {
		return Layout{}, nil, errors.Join(
			ErrUnsafeDataDir,
			controlGuard.Close(),
			buildGuard.Close(),
			guard.Close(),
		)
	}
	return layout, directoryGuardSet{
		coverageGuard,
		controlGuard,
		buildGuard,
		guard,
	}, nil
}

type directoryGuardSet []io.Closer

func (g directoryGuardSet) Close() error {
	var result error
	for _, guard := range g {
		if guard != nil {
			result = errors.Join(result, guard.Close())
		}
	}
	return result
}
