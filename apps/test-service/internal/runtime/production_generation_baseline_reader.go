package runtime

import (
	"context"

	"unit-test-ide.local/test-service/internal/artifactstore"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
)

type productionCoverageBaselineStore interface {
	GetCoverageReport(context.Context, string) (coveragedomain.Report, error)
	GetArtifact(context.Context, string) (task.Artifact, error)
}

type productionCoverageBaselineChunks interface {
	ReadChunk(context.Context, task.Artifact, int64, int) ([]byte, int64, bool, error)
}

type runtimeCoverageBaselineReader struct {
	store  productionCoverageBaselineStore
	chunks productionCoverageBaselineChunks
}

func newRuntimeCoverageBaselineReader(store productionCoverageBaselineStore, chunks productionCoverageBaselineChunks) (*runtimeCoverageBaselineReader, error) {
	if store == nil || chunks == nil {
		return nil, task.ErrStorageUnavailable
	}
	return &runtimeCoverageBaselineReader{store: store, chunks: chunks}, nil
}

func (reader *runtimeCoverageBaselineReader) ReadCoverageBaseline(ctx context.Context, reportID string) ([]byte, error) {
	if reader == nil || ctx == nil || ctx.Err() != nil || !validProductionObjectID(reportID) {
		return nil, errProductionValidationUnavailable
	}
	report, err := reader.store.GetCoverageReport(ctx, reportID)
	if err != nil {
		return nil, err
	}
	if report.ID != reportID || !validProductionObjectID(report.ArtifactID) {
		return nil, errProductionValidationUnavailable
	}
	artifact, err := reader.store.GetArtifact(ctx, report.ArtifactID)
	if err != nil {
		return nil, err
	}
	if artifact.ID != report.ArtifactID || artifact.Kind != "coverage-json" || artifact.MIMEType != "application/json" ||
		artifact.Size < 1 || artifact.Size > 32<<20 || !validProductionDigest(artifact.SHA256) || artifact.CreatedAt.IsZero() {
		return nil, errProductionValidationUnavailable
	}
	result := make([]byte, 0, artifact.Size)
	for offset := int64(0); offset < artifact.Size; {
		length := artifactstore.MaxReadChunk
		if remaining := artifact.Size - offset; remaining < int64(length) {
			length = int(remaining)
		}
		chunk, next, eof, err := reader.chunks.ReadChunk(ctx, artifact, offset, length)
		if err != nil {
			return nil, err
		}
		if len(chunk) == 0 || len(chunk) > length || next != offset+int64(len(chunk)) || next > artifact.Size || eof != (next == artifact.Size) {
			return nil, errProductionValidationUnavailable
		}
		result = append(result, chunk...)
		offset = next
	}
	if int64(len(result)) != artifact.Size || productionBytesDigest(result) != artifact.SHA256 {
		return nil, errProductionValidationUnavailable
	}
	return result, nil
}

var _ productionCoverageBaselineReader = (*runtimeCoverageBaselineReader)(nil)
