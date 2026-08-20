package coveragellvm

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	instrumentationFileName = "coverage-instrumentation.cmake"
	instrumentationVersion  = "clang-cl-instrumentation-v1"
	instrumentationContents = "cmake_minimum_required(VERSION 3.25)\n" +
		"if(NOT CMAKE_CXX_COMPILER_ID MATCHES \"Clang\")\n" +
		"  message(FATAL_ERROR \"unit-test-ide coverage requires clang-cl\")\n" +
		"endif()\n" +
		"add_compile_options(\"$<$<COMPILE_LANGUAGE:C,CXX>:-fprofile-instr-generate>\" \"$<$<COMPILE_LANGUAGE:C,CXX>:-fcoverage-mapping>\")\n" +
		"add_link_options(\"-fprofile-instr-generate\")\n"
)

type Instrumentation struct {
	IncludePath string
	SHA256      string
	Fingerprint string
}

type instrumentationRootPin struct {
	path   string
	file   *os.File
	info   os.FileInfo
	native nativeFileIdentity
}

func WriteInstrumentation(taskRoot string) (Instrumentation, error) {
	if taskRoot == "" || strings.ContainsRune(taskRoot, 0) || !filepath.IsAbs(taskRoot) || filepath.Clean(taskRoot) != taskRoot {
		return Instrumentation{}, ErrInvalidToolset
	}
	rootPin, err := pinInstrumentationRoot(taskRoot)
	if err != nil {
		return Instrumentation{}, ErrInvalidToolset
	}
	defer rootPin.file.Close()
	entries, err := os.ReadDir(taskRoot)
	if err != nil || len(entries) != 0 {
		return Instrumentation{}, ErrInvalidToolset
	}
	temporary, temporaryPath, err := createExclusiveInstrumentation(taskRoot)
	if err != nil {
		return Instrumentation{}, errors.Join(ErrInvalidToolset, err)
	}
	closed := false
	fail := func(cause error) (Instrumentation, error) {
		if !closed {
			_ = temporary.Close()
		}
		_ = os.Remove(temporaryPath)
		return Instrumentation{}, errors.Join(ErrInvalidToolset, cause)
	}
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temporary, hash), strings.NewReader(instrumentationContents)); err != nil {
		return fail(err)
	}
	if err := temporary.Chmod(0o400); err != nil {
		return fail(err)
	}
	if err := temporary.Sync(); err != nil {
		return fail(err)
	}
	if err := temporary.Close(); err != nil {
		closed = true
		return fail(err)
	}
	closed = true
	digest := hex.EncodeToString(hash.Sum(nil))
	finalPath := filepath.Join(taskRoot, instrumentationFileName)
	if _, err := os.Lstat(finalPath); !os.IsNotExist(err) {
		return fail(errors.New("instrumentation destination already exists"))
	}
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return fail(err)
	}
	if err := verifyInstrumentationRoot(rootPin); err != nil {
		return Instrumentation{}, errors.Join(ErrInvalidToolset, errors.New("Task root identity changed"))
	}
	published, err := os.Lstat(finalPath)
	if err != nil || !published.Mode().IsRegular() || published.Mode()&os.ModeSymlink != 0 {
		return Instrumentation{}, errors.Join(ErrInvalidToolset, errors.New("instrumentation publication is not regular"))
	}
	fingerprintHash := sha256.Sum256([]byte(instrumentationVersion + "\x00" + digest))
	return Instrumentation{
		IncludePath: finalPath,
		SHA256:      digest,
		Fingerprint: hex.EncodeToString(fingerprintHash[:]),
	}, nil
}

func createExclusiveInstrumentation(root string) (*os.File, string, error) {
	for attempt := 0; attempt < 32; attempt++ {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, "", err
		}
		path := filepath.Join(root, ".coverage-instrumentation-"+hex.EncodeToString(nonce[:])+".tmp")
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if os.IsExist(err) {
			continue
		}
		return file, path, err
	}
	return nil, "", errors.New("unable to allocate instrumentation temporary")
}
