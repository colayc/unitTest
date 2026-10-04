package artifactstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

const maxGenerationSourceBytes = 4 * 1024 * 1024
const maxGenerationEvidenceBytes = 16 * 1024 * 1024

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
	_, err := s.ReadGenerationSource(ctx, artifact)
	return err
}

func (s *Store) ReadGenerationSource(ctx context.Context, artifact task.Artifact) ([]byte, error) {
	return s.readGenerationArtifact(ctx, artifact, "test-generation-source", ".source", maxGenerationSourceBytes)
}

// CommitGenerationEvidence stores the canonical retained candidate set and
// ordered validation receipts outside task/event rows. It is never executable
// input and remains digest-bound to the artifact metadata committed with the
// generation checkpoint.
func (s *Store) CommitGenerationEvidence(ctx context.Context, taskID, artifactID string, at time.Time, evidence []byte) (task.Artifact, error) {
	if ctx == nil || len(evidence) == 0 || len(evidence) > maxGenerationEvidenceBytes {
		return task.Artifact{}, ErrInvalidArtifact
	}
	return s.commitArtifactData(ctx, taskID, artifactID, "test-generation-evidence", at, evidence)
}

func (s *Store) VerifyGenerationEvidence(ctx context.Context, artifact task.Artifact) error {
	_, err := s.ReadGenerationEvidence(ctx, artifact)
	return err
}

func (s *Store) ReadGenerationEvidence(ctx context.Context, artifact task.Artifact) ([]byte, error) {
	return s.readGenerationArtifact(ctx, artifact, "test-generation-evidence", ".evidence", maxGenerationEvidenceBytes)
}

func (s *Store) readGenerationArtifact(ctx context.Context, artifact task.Artifact, kind, extension string, limit int64) ([]byte, error) {
	if s == nil || s.root == nil || ctx == nil || artifact.Kind != kind || artifact.MIMEType != "application/octet-stream" || artifact.Size < 1 || artifact.Size > limit ||
		artifact.RelativePath != artifactRelativePathFor(artifact.TaskID, artifact.ID, extension) || !validArtifact(artifact) {
		return nil, ErrInvalidArtifact
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, info, err := openVerifiedFile(s.root, artifact.RelativePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if !info.Mode().IsRegular() || info.Size() != artifact.Size {
		return nil, ErrArtifactChanged
	}
	hash := sha256.New()
	data, err := io.ReadAll(io.LimitReader(io.TeeReader(file, hash), limit+1))
	if err != nil || int64(len(data)) != artifact.Size || int64(len(data)) > limit || hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
		return nil, ErrArtifactChanged
	}
	after, err := file.Stat()
	if err != nil || !info.ModTime().Equal(after.ModTime()) || info.Size() != after.Size() {
		return nil, ErrArtifactChanged
	}
	return data, nil
}
