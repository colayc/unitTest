//go:build !windows

package coveragebundle

import "os"

func captureNativeIdentity(file *os.File) (any, error)     { return nil, nil }
func verifyNativeIdentity(path string, expected any) error { return nil }
