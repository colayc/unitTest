package diagnostic

import "regexp"

var cmakeLocationPattern = regexp.MustCompile(
	`^CMake (Error|Warning) at (.+):([0-9]+)(?: \(([^)]+)\))?:$`,
)

var cmakeSourceDirectoryErrorPattern = regexp.MustCompile(
	`^CMake Error: The source directory ".+" does not appear to contain CMakeLists\.txt\.$`,
)
