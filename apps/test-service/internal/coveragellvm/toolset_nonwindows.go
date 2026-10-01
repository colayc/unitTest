//go:build !windows

package coveragellvm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/toolchain"
)

const maximumLLVMToolBytes int64 = 512 * 1024 * 1024

type nativeFileIdentity struct {
	device uint64
	inode  uint64
}

func PinToolset(instance toolchain.Instance) (*Toolset, error) {
	if instance.Family != toolchain.FamilyClang || instance.Version == "" ||
		instance.CCompiler == "" || instance.CXXCompiler == "" ||
		instance.Coverage.LLVMProfdata == "" || instance.Coverage.LLVMCov == "" ||
		instance.Coverage.ToolsetIdentity == "" {
		return nil, ErrInvalidToolset
	}
	paths := []string{instance.CCompiler, instance.CXXCompiler, instance.Coverage.LLVMProfdata, instance.Coverage.LLVMCov}
	roles := []string{"clang", "clang++", "llvm-profdata", "llvm-cov"}
	evidence := []toolchain.ExecutableEvidence{instance.Coverage.CompilerEvidence, instance.Coverage.CXXCompilerEvidence, instance.Coverage.ProfdataEvidence, instance.Coverage.CovEvidence}
	tools := make([]toolchain.LLVMToolEvidence, len(paths))
	for index, path := range paths {
		tools[index] = toolchain.LLVMToolEvidence{Role: roles[index], Path: path, Evidence: evidence[index]}
	}
	identity, err := toolchain.LLVMToolsetIdentityForTools(instance.Version, tools)
	if err != nil || identity != instance.Coverage.ToolsetIdentity {
		return nil, ErrInvalidToolset
	}
	root := filepath.Dir(paths[0])
	if err := directUnixPath(root); err != nil {
		return nil, errors.Join(ErrInvalidToolset, err)
	}
	directory, err := os.Open(root)
	if err != nil {
		return nil, errors.Join(ErrInvalidToolset, err)
	}
	info, err := directory.Stat()
	if err != nil || !info.IsDir() {
		directory.Close()
		return nil, ErrInvalidToolset
	}
	native, err := unixIdentity(info)
	if err != nil {
		directory.Close()
		return nil, ErrInvalidToolset
	}
	result := &Toolset{version: instance.Version, identity: identity, fourTools: true, installationPath: root, installationFile: directory, installationInfo: info, installationNative: native}
	fail := func(cause error) (*Toolset, error) {
		_ = result.Close()
		return nil, errors.Join(ErrInvalidToolset, cause)
	}
	for index, path := range paths {
		pinned, err := pinUnixTool(path)
		if err != nil {
			return fail(err)
		}
		switch index {
		case 0:
			result.compiler = pinned
		case 1:
			result.cxx = pinned
		case 2:
			result.profdata = pinned
		case 3:
			result.cov = pinned
		}
		if pinned.sha256 != evidence[index].SHA256 || unixIdentityString(pinned.native) != evidence[index].FileIdentity {
			return fail(errors.New("LLVM tool no longer matches discovery evidence"))
		}
	}
	if err := result.Verify(); err != nil {
		return fail(err)
	}
	// A recomputed identity alone cannot attest to the claimed version. Probe
	// each retained executable under a bound, revalidating the pins around it.
	runner := probe.NewRunner()
	for index, path := range paths {
		if err := result.Verify(); err != nil {
			return fail(err)
		}
		output, runErr := runner.Run(context.Background(), probe.Spec{
			Executable: path, Args: []string{"--version"}, Env: []string{},
			Timeout: 5 * time.Second, MaxOutput: 64 * 1024,
		})
		if err := result.Verify(); err != nil {
			return fail(err)
		}
		if runErr != nil || output.ExitCode != 0 || len(output.Stderr) != 0 {
			return fail(errors.New("LLVM version probe failed"))
		}
		version, err := toolchain.LLVMVersionFromBanner(roles[index], output.Stdout)
		if err != nil || version != instance.Version {
			return fail(errors.New("LLVM tool version mismatch"))
		}
	}
	return result, nil
}

func directUnixPath(path string) error {
	if path == "" || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("LLVM path is not canonical")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return errors.New("LLVM path crosses a symlink")
	}
	return nil
}

func unixIdentity(info os.FileInfo) (nativeFileIdentity, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nativeFileIdentity{}, errors.New("Unix identity unavailable")
	}
	return nativeFileIdentity{device: uint64(stat.Dev), inode: uint64(stat.Ino)}, nil
}

func unixIdentityString(identity nativeFileIdentity) string {
	return fmt.Sprintf("unix:%d:%d", identity.device, identity.inode)
}

func pinUnixTool(path string) (pinnedTool, error) {
	if err := directUnixPath(path); err != nil {
		return pinnedTool{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return pinnedTool{}, err
	}
	result := pinnedTool{path: path, file: file}
	fail := func(err error) (pinnedTool, error) { _ = file.Close(); return pinnedTool{}, err }
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Size() < 0 || info.Size() > maximumLLVMToolBytes {
		return fail(errors.New("LLVM tool is not a bounded executable"))
	}
	native, err := unixIdentity(info)
	if err != nil {
		return fail(err)
	}
	result.info, result.native = info, native
	digest, err := digestUnixTool(file)
	if err != nil {
		return fail(err)
	}
	result.sha256 = digest
	if err := verifyPinnedTool(&result); err != nil {
		return fail(err)
	}
	return result, nil
}

func digestUnixTool(file *os.File) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(file, maximumLLVMToolBytes+1))
	if err != nil || count > maximumLLVMToolBytes {
		return "", errors.New("LLVM tool exceeds digest budget")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func verifyPinnedTool(tool *pinnedTool) error {
	if tool == nil || tool.file == nil || tool.info == nil || tool.sha256 == "" {
		return errors.New("LLVM tool pin is closed")
	}
	if err := directUnixPath(tool.path); err != nil {
		return err
	}
	pathInfo, err := os.Stat(tool.path)
	if err != nil {
		return err
	}
	handleInfo, err := tool.file.Stat()
	if err != nil || !pathInfo.Mode().IsRegular() || !os.SameFile(pathInfo, tool.info) || !os.SameFile(handleInfo, tool.info) || handleInfo.Size() > maximumLLVMToolBytes {
		return errors.New("LLVM tool identity changed")
	}
	native, err := unixIdentity(handleInfo)
	if err != nil || native != tool.native {
		return errors.New("LLVM tool native identity changed")
	}
	digest, err := digestUnixTool(tool.file)
	if err != nil || digest != tool.sha256 {
		return errors.New("LLVM tool content changed")
	}
	return nil
}

func verifyPinnedDirectory(path string, file *os.File, expected os.FileInfo, native nativeFileIdentity) error {
	if file == nil || expected == nil {
		return errors.New("LLVM installation pin is closed")
	}
	if err := directUnixPath(path); err != nil {
		return err
	}
	pathInfo, err := os.Stat(path)
	if err != nil {
		return err
	}
	handleInfo, err := file.Stat()
	if err != nil || !pathInfo.IsDir() || !os.SameFile(pathInfo, expected) || !os.SameFile(handleInfo, expected) {
		return errors.New("LLVM installation identity changed")
	}
	current, err := unixIdentity(handleInfo)
	if err != nil || current != native {
		return errors.New("LLVM installation native identity changed")
	}
	return nil
}
