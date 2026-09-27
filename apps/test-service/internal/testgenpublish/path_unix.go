//go:build !windows

package testgenpublish

import "os"

func linked(info os.FileInfo) bool           { return info == nil || info.Mode()&os.ModeSymlink != 0 }
func sameMode(actual, want os.FileMode) bool { return actual == want }
