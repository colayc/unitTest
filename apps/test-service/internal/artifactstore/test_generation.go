package artifactstore

import (
	"context"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

const maxGenerationSourceBytes = 4 * 1024 * 1024

// CommitGenerationSource stores staged source bytes outside task/event state.
// Its returned digest-bound metadata is committed by the generation checkpoint.
func (s *Store) CommitGenerationSource(ctx context.Context, taskID, artifactID string, at time.Time, source []byte) (task.Artifact, error) {
	if ctx == nil || len(source) == 0 || len(source) > maxGenerationSourceBytes {
		return task.Artifact{}, ErrInvalidArtifact
	}
	return s.commitArtifactData(ctx, taskID, artifactID, "test-generation-source", at, source)
}

// VerifyGenerationSource rereads all bytes and checks the complete digest.
func (s *Store) VerifyGenerationSource(ctx context.Context, artifact task.Artifact) error {
	if s == nil || ctx == nil || artifact.Kind != "test-generation-source" || artifact.MIMEType != "application/octet-stream" || artifact.Size < 1 || artifact.Size > maxGenerationSourceBytes ||
		artifact.RelativePath != artifactRelativePathFor(artifact.TaskID, artifact.ID, ".source") {
		return ErrInvalidArtifact
	}
	_, _, _, err := s.ReadChunk(ctx, artifact, 0, 1)
	return err
}
