//go:build !windows

package cmake

func canonicalNativePath(path string) string { return path }
