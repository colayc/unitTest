package diagnostic

import "regexp"

var (
	gnuUndefinedReferencePattern = regexp.MustCompile(
		`^(?:.+/)?ld(?:\.exe)?: (.+): undefined reference to (.+)$`,
	)
	lldUndefinedSymbolPattern = regexp.MustCompile(
		`^ld\.lld: error: undefined symbol: (.+)$`,
	)
	collect2ErrorPattern = regexp.MustCompile(
		`^collect2: error: (.+)$`,
	)
)
