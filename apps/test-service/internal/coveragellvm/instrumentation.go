package coveragellvm

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"unit-test-ide.local/test-service/internal/coverageplatform"
	"unit-test-ide.local/test-service/internal/coveragerun"
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
	linuxInstrumentationVersion  = "clang-linux-instrumentation-v1"
	linuxInstrumentationContents = "cmake_minimum_required(VERSION 3.25)\n" +
		"if(NOT CMAKE_C_COMPILER MATCHES \"(^|[/\\\\])clang$\")\n" +
		"  message(FATAL_ERROR \"unit-test-ide coverage requires clang\")\n" +
		"endif()\n" +
		"if(NOT CMAKE_CXX_COMPILER MATCHES \"(^|[/\\\\])clang\\\\+\\\\+$\")\n" +
		"  message(FATAL_ERROR \"unit-test-ide coverage requires clang++\")\n" +
		"endif()\n" +
		"add_compile_options(\"$<$<COMPILE_LANGUAGE:C,CXX>:-fprofile-instr-generate>\" \"$<$<COMPILE_LANGUAGE:C,CXX>:-fcoverage-mapping>\")\n" +
		"add_link_options(\"-fprofile-instr-generate\")\n"
)

type Instrumentation = coverageplatform.Instrumentation

type BuildRequest struct{ TaskRoot string }

type InstrumentationPlan struct {
	Instrumentation coverageplatform.Instrumentation
	CCompiler       coveragerun.TrustedPath
	CXXCompiler     coveragerun.TrustedPath
	CompileFlags    []string
	LinkFlags       []string
}

type instrumentationRootPin struct {
	path   string
	file   *os.File
	info   os.FileInfo
	native nativeFileIdentity
}

// InstrumentationFingerprint identifies the host platform's retained LLVM
// coverage instrumentation contract. Toolchain identity and version are
// validated separately by the execution owner.
func InstrumentationFingerprint() string {
	platform := "windows"
	if runtime.GOOS == "linux" {
		platform = "linux"
	}
	return InstrumentationFingerprintForPlatform(platform)
}

func InstrumentationFingerprintForPlatform(platform string) string {
	version, contents, ok := instrumentationContract(platform)
	if !ok {
		return ""
	}
	digest := sha256.Sum256([]byte(contents))
	fingerprint := sha256.Sum256([]byte(version + "\x00" + hex.EncodeToString(digest[:])))
	return hex.EncodeToString(fingerprint[:])
}

// InstrumentationSHA256 identifies the exact bytes WriteInstrumentation
// publishes. Consumers use it only to bind a retained file snapshot to this
// contract; it is not a substitute for the snapshot's OS file identity.
func InstrumentationSHA256() string {
	platform := "windows"
	if runtime.GOOS == "linux" {
		platform = "linux"
	}
	_, contents, _ := instrumentationContract(platform)
	digest := sha256.Sum256([]byte(contents))
	return hex.EncodeToString(digest[:])
}

func instrumentationContract(platform string) (version, contents string, ok bool) {
	switch platform {
	case "windows":
		return instrumentationVersion, instrumentationContents, true
	case "linux":
		return linuxInstrumentationVersion, linuxInstrumentationContents, true
	default:
		return "", "", false
	}
}

// PlanInstrumentation binds compiler pins to the platform's retained CMake
// instrumentation contract before any build process is prepared.
func PlanInstrumentation(toolset *Toolset, request BuildRequest) (InstrumentationPlan, error) {
	if toolset == nil || toolset.Verify() != nil {
		return InstrumentationPlan{}, ErrInvalidToolset
	}
	c, cxx, err := platformInstrumentationCompilers(toolset)
	if err != nil {
		return InstrumentationPlan{}, err
	}
	instrumentation, err := WriteInstrumentation(request.TaskRoot)
	if err != nil {
		return InstrumentationPlan{}, err
	}
	return InstrumentationPlan{Instrumentation: instrumentation, CCompiler: c, CXXCompiler: cxx,
		CompileFlags: []string{"-fprofile-instr-generate", "-fcoverage-mapping"},
		LinkFlags:    []string{"-fprofile-instr-generate"}}, nil
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
	platform := "windows"
	if runtime.GOOS == "linux" {
		platform = "linux"
	}
	version, contents, _ := instrumentationContract(platform)
	published, err := coverageplatform.PublishInstrumentation(
		taskRoot, instrumentationFileName, contents, version,
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
