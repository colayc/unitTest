package diagnostic

import "regexp"

var (
	gnuUndefinedReferencePattern = regexp.MustCompile(
		`^(?:.+/)?ld(?:\.exe)?: (.+): undefined reference to (.+)$`,
	)
	lldUndefinedSymbolPattern = regexp.MustCompile(
		`^ld\.lld: error: undefined symbol: (.+)$`,
	)
	lldLinkErrorPattern = regexp.MustCompile(
		`^lld-link: error: (.+)$`,
	)
	collect2ErrorPattern = regexp.MustCompile(
		`^collect2: error: (.+)$`,
	)
)
