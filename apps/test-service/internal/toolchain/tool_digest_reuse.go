package toolchain

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"unit-test-ide.local/test-service/internal/toolhash"
)

// Only Windows executable binaries may reuse a digest; callers validate the
// path and retain ownership of their independent snapshot read handle.
func guardedExecutableDigest(ctx context.Context, path string, file *os.File, maximum int64) (toolhash.Result, bool, error) {
	if !eligibleExecutableDigest(path) {
		return toolhash.Result{}, false, nil
	}
	return toolhash.Digest(ctx, path, file, maximum)
}

func eligibleExecutableDigest(path string) bool {
	return runtime.GOOS == "windows" && strings.EqualFold(filepath.Ext(path), ".exe")
}
