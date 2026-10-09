package cmake

import "context"

func captureToolFileSnapshot(ctx context.Context, path string, maximum int64) (*fileSnapshot, error) {
	return captureSnapshot(ctx, path, maximum, true, nil)
}

// The hook observes real fallback hashing, including capture's initial Verify;
// it cannot supply a digest or bypass reading the file.
func captureToolFileSnapshotWithHook(ctx context.Context, path string, maximum int64, onFullHash func()) (*fileSnapshot, error) {
	return captureSnapshot(ctx, path, maximum, true, onFullHash)
}

func (snapshot *fileSnapshot) fullDigest() (string, error) {
	if snapshot.onFullHash != nil {
		snapshot.onFullHash()
	}
	return sha256OpenFile(snapshot.file)
}
