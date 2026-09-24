//go:build !windows

package ctest

func canonicalNativePath(path string) string { return path }

func launchNativePath(path string) string { return path }
