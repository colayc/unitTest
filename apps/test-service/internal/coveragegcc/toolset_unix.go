//go:build !windows

package coveragegcc

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"unit-test-ide.local/test-service/internal/toolchain"
)

const maximumGCCToolBytes int64 = 512 * 1024 * 1024

type nativeFileIdentity struct{ device, inode uint64 }

func (identity nativeFileIdentity) String() string {
	return "unix:" + strconvFormat(identity.device) + ":" + strconvFormat(identity.inode)
}

func strconvFormat(value uint64) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var output [20]byte
	i := len(output)
	for value > 0 {
		i--
		output[i] = digits[value%10]
		value /= 10
	}
	return string(output[i:])
}

func PinToolset(instance toolchain.Instance) (*Toolset, error) {
	if runtime.GOOS != "linux" {
		return nil, ErrUnsupportedPlatform
	}
	if instance.Family != toolchain.FamilyGCC || instance.TargetArchitecture != "x64" ||
		!validVersion(instance.Version) || instance.CCompiler == "" || instance.CXXCompiler == "" ||
		instance.Coverage.GCov == "" || instance.Coverage.GCovVersion != instance.Version ||
		instance.Coverage.ToolsetIdentity == "" {
		return nil, ErrInvalidToolset
	}
	paths := []string{instance.CCompiler, instance.CXXCompiler, instance.Coverage.GCov}
	names := []string{"gcc", "g++", "gcov"}
	for index := range paths {
		canonical, err := canonicalDirectUnixPath(paths[index])
		if err != nil || filepath.Base(canonical) != names[index] {
			return nil, ErrInvalidToolset
		}
		paths[index] = canonical
	}
	evidence := []toolchain.ExecutableEvidence{instance.Coverage.CompilerEvidence, instance.Coverage.CXXCompilerEvidence, instance.Coverage.GCovEvidence}
	if toolchain.GCCToolsetIdentity(instance.Version, paths, evidence) != instance.Coverage.ToolsetIdentity {
		return nil, ErrInvalidToolset
	}
	result := &Toolset{version: instance.Version, identity: instance.Coverage.ToolsetIdentity}
	tools := []*pinnedTool{&result.compiler, &result.cxxCompiler, &result.gcov}
	fail := func(cause error) (*Toolset, error) {
		_ = result.Close()
		return nil, errors.Join(ErrInvalidToolset, cause)
	}
	for index, path := range paths {
		tool, err := pinUnixTool(path)
		if err != nil {
			return fail(err)
		}
		*tools[index] = tool
		if tool.sha256 != evidence[index].SHA256 || tool.native.String() != evidence[index].FileIdentity {
			return fail(errors.New("GCC tool no longer matches discovery evidence"))
		}
	}
	if err := result.Verify(); err != nil {
		return fail(err)
	}
	return result, nil
}

func validVersion(value string) bool {
	if value == "" || len(value) > 128 || strings.ContainsRune(value, 0) {
		return false
	}
	for _, part := range strings.Split(value, ".") {
		if part == "" {
			return false
		}
		for _, char := range part {
			if char < '0' || char > '9' {
				return false
			}
		}
	}
	return true
}

func canonicalDirectUnixPath(path string) (string, error) {
	if path == "" || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errors.New("tool path is not canonical")
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("tool path is a symlink")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != path {
		return "", errors.New("tool path is not direct")
	}
	return path, nil
}

func pinUnixTool(path string) (pinnedTool, error) {
	before, err := unixPathIdentity(path)
	if err != nil {
		return pinnedTool{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return pinnedTool{}, err
	}
	fail := func(cause error) (pinnedTool, error) { _ = file.Close(); return pinnedTool{}, cause }
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Size() < 0 || info.Size() > maximumGCCToolBytes {
		return fail(errors.New("GCC tool is not a bounded executable"))
	}
	native, err := unixFileIdentity(info)
	if err != nil || native != before {
		return fail(errors.New("GCC tool changed while pinning"))
	}
	digest, err := digestHandle(file, maximumGCCToolBytes)
	if err != nil {
		return fail(err)
	}
	after, err := unixPathIdentity(path)
	if err != nil || after != native {
		return fail(errors.New("GCC tool path changed while pinning"))
	}
	return pinnedTool{path: path, file: file, info: info, sha256: digest, native: native}, nil
}

func verifyPinnedTool(tool *pinnedTool) error {
	if tool == nil || tool.file == nil || tool.info == nil || tool.sha256 == "" {
		return errors.New("GCC tool pin is closed")
	}
	before, err := unixPathIdentity(tool.path)
	if err != nil || before != tool.native {
		return errors.New("GCC tool path identity changed")
	}
	info, err := tool.file.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(tool.info, info) || info.Mode().Perm()&0o111 == 0 || info.Size() < 0 || info.Size() > maximumGCCToolBytes {
		return errors.New("GCC tool file information changed")
	}
	native, err := unixFileIdentity(info)
	if err != nil || native != tool.native {
		return errors.New("GCC tool handle identity changed")
	}
	digest, err := digestHandle(tool.file, maximumGCCToolBytes)
	if err != nil || digest != tool.sha256 {
		return errors.New("GCC tool content changed")
	}
	after, err := unixPathIdentity(tool.path)
	if err != nil || after != native {
		return errors.New("GCC tool path changed while validating")
	}
	return nil
}

func unixPathIdentity(path string) (nativeFileIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return nativeFileIdentity{}, errors.New("GCC path is not a direct executable")
	}
	return unixFileIdentity(info)
}

func unixFileIdentity(info os.FileInfo) (nativeFileIdentity, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Dev == 0 || stat.Ino == 0 {
		return nativeFileIdentity{}, errors.New("GCC Unix identity is unavailable")
	}
	return nativeFileIdentity{device: uint64(stat.Dev), inode: uint64(stat.Ino)}, nil
}

func digestHandle(file *os.File, maximum int64) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(file, maximum+1))
	if err != nil || count > maximum {
		return "", errors.New("GCC tool exceeds digest budget")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
