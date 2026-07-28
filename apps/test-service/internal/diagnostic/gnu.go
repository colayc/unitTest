package diagnostic

import "regexp"

var (
	gnuLineColumnPattern = regexp.MustCompile(
		`^(.+):([0-9]+):([0-9]+): (fatal error|error|warning|note): (.+)$`,
	)
	gnuLinePattern = regexp.MustCompile(
		`^(.+):([0-9]+): (fatal error|error|warning|note): (.+)$`,
	)
)
