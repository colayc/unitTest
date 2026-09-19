//go:build !windows

package ctest

func canonicalNativePath(path string) string { return path }
