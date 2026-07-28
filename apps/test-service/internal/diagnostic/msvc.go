package diagnostic

import "regexp"

var (
	msvcLocationPattern = regexp.MustCompile(
		`^(.+)\(([0-9]+)(?:,([0-9]+))?\): (fatal error|error|warning|note) ([A-Z]+[0-9]+): (.+)$`,
	)
	msvcLinkPattern = regexp.MustCompile(
		`^LINK : fatal error (LNK[0-9]+): (.+)$`,
	)
	msvcNotePattern = regexp.MustCompile(
		`^(.+)\(([0-9]+)(?:,([0-9]+))?\): note: (.+)$`,
	)
	msvcProjectSuffixPattern = regexp.MustCompile(`\s+\[[^\]]+\.vcxproj\]$`)
)
