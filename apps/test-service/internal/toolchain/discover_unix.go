//go:build !windows

package toolchain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"

	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/workspace"
)

const (
	maxUnixPATHBytes        = 64 * 1024
	maxUnixPATHSegments     = 64
	maxUnixManualToolchains = 64
	maxUnixFamilyCandidates = 64
)

func NewUnixAdapters(runner probe.Runner, manual []workspace.ToolchainConfig) []Adapter {
	adapters, err := newUnixAdapters(runner, manual, os.Getenv("PATH"), nativeArchitecture())
	if err != nil {
		return []Adapter{failedDiscoveryAdapter{}}
	}
	return adapters
}

func newUnixAdapters(
	runner probe.Runner,
	manual []workspace.ToolchainConfig,
	pathValue string,
	hostArchitecture string,
) ([]Adapter, error) {
	candidates, err := discoverUnixCandidates(pathValue, manual)
	if err != nil {
		return nil, err
	}
	gcc, err := newGNUAdapter(runner, FamilyGCC, candidates[FamilyGCC], hostArchitecture)
	if err != nil {
		return nil, err
	}
	clang, err := newGNUAdapter(runner, FamilyClang, candidates[FamilyClang], hostArchitecture)
	if err != nil {
		return nil, err
	}
	return []Adapter{gcc, clang}, nil
}

func discoverUnixCandidates(
	pathValue string,
	manual []workspace.ToolchainConfig,
) (map[Family][]Candidate, error) {
	if len(pathValue) > maxUnixPATHBytes {
		return nil, fmt.Errorf("%w: PATH exceeds %d bytes", ErrInvalidToolchain, maxUnixPATHBytes)
	}
	if len(manual) > maxUnixManualToolchains {
		return nil, fmt.Errorf("%w: manual toolchains exceed %d", ErrInvalidToolchain, maxUnixManualToolchains)
	}
	segments := filepath.SplitList(pathValue)
	if len(segments) > maxUnixPATHSegments {
		return nil, fmt.Errorf("%w: PATH exceeds %d entries", ErrInvalidToolchain, maxUnixPATHSegments)
	}
	directories := canonicalPATHDirectories(segments)
	ninja := firstDiscoveredExecutable(directories, "ninja")
	makeExecutable := firstDiscoveredExecutable(directories, "make")

	result := map[Family][]Candidate{
		FamilyGCC:   {},
		FamilyClang: {},
	}
	for _, config := range manual {
		family := Family(config.Family)
		if family != FamilyGCC && family != FamilyClang {
			continue
		}
		if !filepath.IsAbs(config.CCompiler) || !filepath.IsAbs(config.CPPCompiler) ||
			!validInstanceID(config.ID) {
			return nil, fmt.Errorf("%w: manual %s toolchain is invalid", ErrInvalidToolchain, family)
		}
		result[family] = append(result[family], Candidate{
			ID:          config.ID,
			Family:      family,
			CCompiler:   filepath.Clean(config.CCompiler),
			CXXCompiler: filepath.Clean(config.CPPCompiler),
			Manual:      true,
			Ninja:       ninja,
			Make:        makeExecutable,
		})
	}

	for _, directory := range directories {
		appendDiscoveredPair(result, FamilyGCC, directory, "gcc", "g++", ninja, makeExecutable)
		appendDiscoveredPair(result, FamilyClang, directory, "clang", "clang++", ninja, makeExecutable)
	}
	for _, family := range []Family{FamilyGCC, FamilyClang} {
		if len(result[family]) > maxUnixFamilyCandidates {
			return nil, fmt.Errorf(
				"%w: %s discovery exceeds %d candidates",
				ErrInvalidToolchain,
				family,
				maxUnixFamilyCandidates,
			)
		}
		sort.SliceStable(result[family], func(left, right int) bool {
			a, b := result[family][left], result[family][right]
			if a.Manual != b.Manual {
				return a.Manual
			}
			return lessStrings(
				[]string{a.ID, identityPath(a.CCompiler), identityPath(a.CXXCompiler)},
				[]string{b.ID, identityPath(b.CCompiler), identityPath(b.CXXCompiler)},
			)
		})
	}
	return result, nil
}

func canonicalPATHDirectories(segments []string) []string {
	directories := make([]string, 0, len(segments))
	seen := make(map[string]struct{}, len(segments))
	for _, segment := range segments {
		if segment == "" || strings.IndexByte(segment, 0) >= 0 || !filepath.IsAbs(segment) {
			continue
		}
		canonical, err := filepath.EvalSymlinks(filepath.Clean(segment))
		if err != nil {
			continue
		}
		canonical, err = filepath.Abs(canonical)
		if err != nil {
			continue
		}
		info, err := os.Stat(canonical)
		if err != nil || !info.IsDir() {
			continue
		}
		key := identityPath(canonical)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		directories = append(directories, canonical)
	}
	return directories
}

func appendDiscoveredPair(
	result map[Family][]Candidate,
	family Family,
	directory string,
	cName string,
	cxxName string,
	ninja string,
	makeExecutable string,
) {
	cCompiler := discoveredExecutable(directory, cName)
	cxxCompiler := discoveredExecutable(directory, cxxName)
	if cCompiler == "" || cxxCompiler == "" {
		return
	}
	result[family] = append(result[family], Candidate{
		Family:      family,
		CCompiler:   cCompiler,
		CXXCompiler: cxxCompiler,
		Ninja:       ninja,
		Make:        makeExecutable,
	})
}

func firstDiscoveredExecutable(directories []string, name string) string {
	for _, directory := range directories {
		if executable := discoveredExecutable(directory, name); executable != "" {
			return executable
		}
	}
	return ""
}

func discoveredExecutable(directory, name string) string {
	path := filepath.Join(directory, name)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return ""
	}
	return path
}

func nativeArchitecture() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "386":
		return "x86"
	case "arm64":
		return "arm64"
	default:
		return ""
	}
}

func directoryOSIdentity(info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Dev == 0 || stat.Ino == 0 {
		return "", fmt.Errorf("filesystem did not provide directory identity")
	}
	return fmt.Sprintf("unix:%x:%x", uint64(stat.Dev), uint64(stat.Ino)), nil
}

type failedDiscoveryAdapter struct{}

func (failedDiscoveryAdapter) Discover(context.Context) ([]Instance, error) {
	return nil, &discoveryIssuesError{issues: []Issue{{
		Code:     "TOOLCHAIN_DISCOVERY_FAILED",
		Message:  "Unix toolchain discovery configuration is invalid",
		Blocking: false,
	}}}
}

func (failedDiscoveryAdapter) Probe(context.Context, Candidate) (Instance, error) {
	return Instance{}, ErrInvalidToolchain
}
