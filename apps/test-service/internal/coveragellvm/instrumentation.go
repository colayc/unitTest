package coveragellvm

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"unit-test-ide.local/test-service/internal/coverageplatform"
)

const (
	instrumentationFileName = "coverage-instrumentation.cmake"
	instrumentationVersion  = "clang-cl-instrumentation-v1"
	instrumentationContents = "cmake_minimum_required(VERSION 3.25)\n" +
		"if(NOT CMAKE_CXX_COMPILER MATCHES \"(^|[/\\\\])clang-cl(\\\\.exe)?$\")\n" +
		"  message(FATAL_ERROR \"unit-test-ide coverage requires clang-cl\")\n" +
		"endif()\n" +
		"add_compile_options(\"$<$<COMPILE_LANGUAGE:C,CXX>:-fprofile-instr-generate>\" \"$<$<COMPILE_LANGUAGE:C,CXX>:-fcoverage-mapping>\")\n" +
		"add_link_options(\"-fprofile-instr-generate\")\n"
)

type Instrumentation = coverageplatform.Instrumentation

type instrumentationRootPin struct {
	path   string
	file   *os.File
	info   os.FileInfo
	native nativeFileIdentity
}

// InstrumentationFingerprint is the stable identity of the exact retained
// clang-cl coverage instrumentation contract. Toolchain identity and version
// are deliberately validated separately by the execution owner.
func InstrumentationFingerprint() string {
	fingerprint := sha256.Sum256([]byte(instrumentationVersion + "\x00" + InstrumentationSHA256()))
	return hex.EncodeToString(fingerprint[:])
}

// InstrumentationSHA256 identifies the exact bytes WriteInstrumentation
// publishes. Consumers use it only to bind a retained file snapshot to this
// contract; it is not a substitute for the snapshot's OS file identity.
func InstrumentationSHA256() string {
	digest := sha256.Sum256([]byte(instrumentationContents))
	return hex.EncodeToString(digest[:])
}

func WriteInstrumentation(taskRoot string) (coverageplatform.Instrumentation, error) {
	if taskRoot == "" || strings.ContainsRune(taskRoot, 0) || !filepath.IsAbs(taskRoot) || filepath.Clean(taskRoot) != taskRoot {
		return Instrumentation{}, ErrInvalidToolset
	}
	rootPin, err := pinInstrumentationRoot(taskRoot)
	if err != nil {
		return Instrumentation{}, ErrInvalidToolset
	}
	defer rootPin.file.Close()
	published, err := coverageplatform.PublishInstrumentation(
		taskRoot, instrumentationFileName, instrumentationContents, instrumentationVersion,
	)
	if err != nil {
		return Instrumentation{}, errors.Join(ErrInvalidToolset, err)
	}
	if err := verifyInstrumentationRoot(rootPin); err != nil {
		return Instrumentation{}, errors.Join(ErrInvalidToolset, errors.New("Task root identity changed"))
	}
	if published.Fingerprint != InstrumentationFingerprint() || published.SHA256 != InstrumentationSHA256() {
		return Instrumentation{}, errors.Join(ErrInvalidToolset, errors.New("instrumentation contract mismatch"))
	}
	return published, nil
}
