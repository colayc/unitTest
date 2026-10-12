package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
)

type productionBaselineStoreFixture struct {
	report   coveragedomain.Report
	artifact task.Artifact
}

func (fixture *productionBaselineStoreFixture) GetCoverageReport(context.Context, string) (coveragedomain.Report, error) {
	return fixture.report, nil
}

func (fixture *productionBaselineStoreFixture) GetArtifact(context.Context, string) (task.Artifact, error) {
	return fixture.artifact, nil
}

type productionBaselineChunksFixture struct {
	data    []byte
	changed bool
}

func (fixture *productionBaselineChunksFixture) ReadChunk(_ context.Context, _ task.Artifact, offset int64, length int) ([]byte, int64, bool, error) {
	if fixture.changed {
		return nil, offset, false, nil
	}
	end := offset + int64(length)
	if end > int64(len(fixture.data)) {
		end = int64(len(fixture.data))
	}
	return append([]byte(nil), fixture.data[offset:end]...), end, end == int64(len(fixture.data)), nil
}

func TestRuntimeCoverageBaselineReaderReadsExactAttestedArtifact(t *testing.T) {
	data := []byte(`{"schemaVersion":"1.0"}`)
	store := &productionBaselineStoreFixture{
		report: coveragedomain.Report{ID: strings.Repeat("1", 32), ArtifactID: strings.Repeat("2", 32)},
		artifact: task.Artifact{
			ID: strings.Repeat("2", 32), TaskID: strings.Repeat("3", 32), Kind: "coverage-json", MIMEType: "application/json",
			SHA256: productionBytesDigest(data), Size: int64(len(data)), CreatedAt: time.Unix(1, 0).UTC(),
		},
	}
	reader, err := newRuntimeCoverageBaselineReader(store, &productionBaselineChunksFixture{data: data})
	if err != nil {
		t.Fatal(err)
	}
	got, err := reader.ReadCoverageBaseline(context.Background(), store.report.ID)
	if err != nil || string(got) != string(data) {
		t.Fatalf("ReadCoverageBaseline() = %q, %v", got, err)
	}
	got[0] ^= 0xff
	again, err := reader.ReadCoverageBaseline(context.Background(), store.report.ID)
	if err != nil || string(again) != string(data) {
		t.Fatalf("caller changed baseline: %q, %v", again, err)
	}
}

func TestRuntimeCoverageBaselineReaderRejectsMetadataAndChunkDrift(t *testing.T) {
	data := []byte("baseline")
	fixture := func() (*productionBaselineStoreFixture, *productionBaselineChunksFixture) {
		return &productionBaselineStoreFixture{
			report:   coveragedomain.Report{ID: strings.Repeat("1", 32), ArtifactID: strings.Repeat("2", 32)},
			artifact: task.Artifact{ID: strings.Repeat("2", 32), TaskID: strings.Repeat("3", 32), Kind: "coverage-json", MIMEType: "application/json", SHA256: productionBytesDigest(data), Size: int64(len(data)), CreatedAt: time.Unix(1, 0).UTC()},
		}, &productionBaselineChunksFixture{data: data}
	}
	tests := []struct {
		name   string
		mutate func(*productionBaselineStoreFixture, *productionBaselineChunksFixture)
	}{
		{"wrong kind", func(store *productionBaselineStoreFixture, _ *productionBaselineChunksFixture) {
			store.artifact.Kind = "stdout"
		}},
		{"wrong digest", func(store *productionBaselineStoreFixture, _ *productionBaselineChunksFixture) {
			store.artifact.SHA256 = strings.Repeat("0", 64)
		}},
		{"wrong size", func(store *productionBaselineStoreFixture, _ *productionBaselineChunksFixture) { store.artifact.Size++ }},
		{"nonprogressing chunk", func(_ *productionBaselineStoreFixture, chunks *productionBaselineChunksFixture) {
			chunks.changed = true
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, chunks := fixture()
			test.mutate(store, chunks)
			reader, err := newRuntimeCoverageBaselineReader(store, chunks)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reader.ReadCoverageBaseline(context.Background(), store.report.ID); err == nil || errors.Is(err, context.Canceled) {
				t.Fatalf("ReadCoverageBaseline() error = %v", err)
			}
		})
	}
}
