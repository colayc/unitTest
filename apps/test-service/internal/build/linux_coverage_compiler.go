package build

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/toolchain"
)

const maxLinuxCoverageCacheBytes = 16 << 20

// The include checks compiler families; this checkpoint binds CMake's actual
// compiler selection to the already pinned, role-specific Clang executables.
func verifyLinuxCoverageCompilerCache(cachePath string, instance toolchain.Instance) error {
	if instance.Family != toolchain.FamilyClang || instance.CCompiler == "" || instance.CXXCompiler == "" {
		return task.ErrInvalidArgument
	}
	info, err := os.Lstat(cachePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxLinuxCoverageCacheBytes {
		return task.ErrInvalidArgument
	}
	file, err := os.Open(cachePath)
	if err != nil {
		return task.ErrInvalidArgument
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return task.ErrInvalidArgument
	}
	contents, err := io.ReadAll(io.LimitReader(file, maxLinuxCoverageCacheBytes+1))
	if err != nil || len(contents) > maxLinuxCoverageCacheBytes {
		return task.ErrInvalidArgument
	}
	values := make(map[string]string, 2)
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	scanner.Buffer(make([]byte, 4096), maxLinuxCoverageCacheBytes)
	for scanner.Scan() {
		line := scanner.Text()
		for _, role := range []string{"CMAKE_C_COMPILER", "CMAKE_CXX_COMPILER"} {
			prefix := role + ":FILEPATH="
			if strings.HasPrefix(line, prefix) {
				if _, exists := values[role]; exists {
					return task.ErrInvalidArgument
				}
				values[role] = strings.TrimPrefix(line, prefix)
			} else if strings.HasPrefix(line, role+":") {
				return task.ErrInvalidArgument
			}
		}
	}
	if scanner.Err() != nil {
		return task.ErrInvalidArgument
	}
	for _, role := range []struct{ name, expected string }{{"CMAKE_C_COMPILER", instance.CCompiler}, {"CMAKE_CXX_COMPILER", instance.CXXCompiler}} {
		actual := filepath.FromSlash(values[role.name])
		if !filepath.IsAbs(actual) || filepath.Clean(actual) != filepath.Clean(role.expected) {
			return task.ErrInvalidArgument
		}
		actualInfo, actualErr := os.Stat(actual)
		expectedInfo, expectedErr := os.Stat(role.expected)
		if actualErr != nil || expectedErr != nil || !actualInfo.Mode().IsRegular() || !os.SameFile(actualInfo, expectedInfo) {
			return task.ErrInvalidArgument
		}
	}
	return nil
}
