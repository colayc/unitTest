package artifactstore

import (
	"context"
	"errors"
	"time"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

const maxTestCatalogArtifactBytes = 64 * 1024 * 1024

func (s *Store) CommitTestCatalog(
	ctx context.Context,
	taskID string,
	artifactID string,
	at time.Time,
	catalog testdomain.Catalog,
) (task.Artifact, error) {
	encoded, err := testdomain.EncodeCatalog(catalog)
	if err != nil || len(encoded) > maxTestCatalogArtifactBytes {
		return task.Artifact{}, ErrInvalidArtifact
	}
	return s.commitArtifactData(ctx, taskID, artifactID, "test-catalog", at, encoded)
}

func (s *Store) ReadTestCatalog(ctx context.Context, artifact task.Artifact) (testdomain.Catalog, error) {
	if s == nil || s.root == nil || ctx == nil {
		return testdomain.Catalog{}, ErrStoreUnavailable
	}
	if err := ctx.Err(); err != nil {
		return testdomain.Catalog{}, err
	}
	if !validArtifact(artifact) || artifact.Kind != "test-catalog" ||
		artifact.Size > maxTestCatalogArtifactBytes {
		return testdomain.Catalog{}, ErrInvalidArtifact
	}
	file, info, err := openVerifiedFile(s.root, artifact.RelativePath)
	if err != nil {
		return testdomain.Catalog{}, err
	}
	defer file.Close()
	if !info.Mode().IsRegular() || info.Size() != artifact.Size {
		return testdomain.Catalog{}, ErrArtifactChanged
	}
	encoded, err := verifiedChunk(ctx, file, artifact, 0, int(artifact.Size), s.hooks.afterSnapshotRead)
	if err != nil {
		return testdomain.Catalog{}, err
	}
	if s.hooks.afterVerifiedSnapshot != nil {
		s.hooks.afterVerifiedSnapshot()
	}
	catalog, err := testdomain.DecodeCatalog(encoded)
	if err != nil {
		return testdomain.Catalog{}, errors.Join(ErrArtifactChanged, err)
	}
	return catalog, nil
}
