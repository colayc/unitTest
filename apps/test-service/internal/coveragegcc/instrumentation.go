package coveragegcc

import (
	"crypto/sha256"
	"encoding/hex"
	"runtime"

	"unit-test-ide.local/test-service/internal/coverageplatform"
)

const gccInstrumentationFileName = "coverage-instrumentation.cmake"
const gccInstrumentationVersion = "gcc-gcov-instrumentation-v1"
const gccInstrumentationContents = "cmake_minimum_required(VERSION 3.25)\n" +
	"if(NOT CMAKE_C_COMPILER_ID STREQUAL \"GNU\" OR NOT CMAKE_CXX_COMPILER_ID STREQUAL \"GNU\")\n" +
	"  message(FATAL_ERROR \"unit-test-ide coverage requires GCC and G++\")\n" +
	"endif()\n" +
	"add_compile_options(\"$<$<COMPILE_LANGUAGE:C,CXX>:--coverage>\" \"$<$<COMPILE_LANGUAGE:C,CXX>:-O0>\" \"$<$<COMPILE_LANGUAGE:C,CXX>:-g>\")\n" +
	"add_link_options(\"--coverage\")\n"

func InstrumentationSHA256() string {
	sum := sha256.Sum256([]byte(gccInstrumentationContents))
	return hex.EncodeToString(sum[:])
}

func WriteInstrumentation(root string) (coverageplatform.Instrumentation, error) {
	if runtime.GOOS == "windows" {
		return coverageplatform.Instrumentation{}, ErrUnsupportedPlatform
	}
	return coverageplatform.PublishInstrumentation(root, gccInstrumentationFileName, gccInstrumentationContents, gccInstrumentationVersion)
}
