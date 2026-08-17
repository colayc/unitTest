package cpputest

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	completeCasePattern = regexp.MustCompile(
		`^(TEST|IGNORE_TEST)\(([^,\r\n]+), ([^)\r\n]+)\) - ([0-9]+) ms$`,
	)
	caseStartPattern = regexp.MustCompile(
		`^(TEST|IGNORE_TEST)\(([^,\r\n]+), ([^)\r\n]+)\)$`,
	)
	caseEndPattern    = regexp.MustCompile(`^ - ([0-9]+) ms$`)
	gccFailurePattern = regexp.MustCompile(
		`^(.+):([0-9]+): error: Failure in (TEST|IGNORE_TEST)\(([^,\r\n]+), ([^)\r\n]+)\)$`,
	)
	msvcFailurePattern = regexp.MustCompile(
		`^(.+)\(([0-9]+)\): error: Failure in (TEST|IGNORE_TEST)\(([^,\r\n]+), ([^)\r\n]+)\)$`,
	)
	gccBareErrorPattern  = regexp.MustCompile(`^(.+):([0-9]+): error:$`)
	msvcBareErrorPattern = regexp.MustCompile(`^(.+)\(([0-9]+)\): error:$`)
	okSummaryPattern     = regexp.MustCompile(
		`^OK \(([0-9]+) tests, ([0-9]+) ran, ([0-9]+) checks, ([0-9]+) ignored, ([0-9]+) filtered out, ([0-9]+) ms\)$`,
	)
	errorSummaryPattern = regexp.MustCompile(
		`^Errors \(([0-9]+) failures, ([0-9]+) tests, ([0-9]+) ran, ([0-9]+) checks, ([0-9]+) ignored, ([0-9]+) filtered out, ([0-9]+) ms\)$`,
	)
)

type grammarCase struct {
	macro    string
	group    string
	name     string
	duration int64
}

type grammarFailure struct {
	path  string
	line  int
	macro string
	group string
	name  string
}

type grammarSummary struct {
	ok          bool
	failures    int
	tests       int
	ran         int
	checks      int
	ignored     int
	filteredOut int
	duration    int64
}

func parseCompleteCase(line string) (grammarCase, bool) {
	match := completeCasePattern.FindStringSubmatch(line)
	if match == nil {
		return grammarCase{}, false
	}
	duration, err := strconv.ParseInt(match[4], 10, 64)
	if err != nil {
		return grammarCase{}, false
	}
	return grammarCase{
		macro:    match[1],
		group:    match[2],
		name:     match[3],
		duration: duration,
	}, true
}

func parseCaseStart(line string) (grammarCase, bool) {
	match := caseStartPattern.FindStringSubmatch(line)
	if match == nil {
		return grammarCase{}, false
	}
	return grammarCase{macro: match[1], group: match[2], name: match[3]}, true
}

func parseCaseEnd(line string) (int64, bool) {
	match := caseEndPattern.FindStringSubmatch(line)
	if match == nil {
		return 0, false
	}
	duration, err := strconv.ParseInt(match[1], 10, 64)
	return duration, err == nil
}

func parseFailure(line string) (grammarFailure, bool) {
	match := msvcFailurePattern.FindStringSubmatch(line)
	if match == nil {
		match = gccFailurePattern.FindStringSubmatch(line)
	}
	if match == nil {
		return grammarFailure{}, false
	}
	lineNumber, err := strconv.Atoi(match[2])
	if err != nil || lineNumber <= 0 {
		return grammarFailure{}, false
	}
	return grammarFailure{
		path:  match[1],
		line:  lineNumber,
		macro: match[3],
		group: match[4],
		name:  match[5],
	}, true
}

func parseBareErrorLocation(line string) (grammarFailure, bool) {
	match := msvcBareErrorPattern.FindStringSubmatch(line)
	if match == nil {
		match = gccBareErrorPattern.FindStringSubmatch(line)
	}
	if match == nil {
		return grammarFailure{}, false
	}
	lineNumber, err := strconv.Atoi(match[2])
	if err != nil || lineNumber <= 0 {
		return grammarFailure{}, false
	}
	return grammarFailure{path: match[1], line: lineNumber}, true
}

func parseSummary(line string) (grammarSummary, bool) {
	if match := okSummaryPattern.FindStringSubmatch(line); match != nil {
		values, ok := parseDecimalFields(match[1:])
		if !ok {
			return grammarSummary{}, false
		}
		return grammarSummary{
			ok:          true,
			tests:       values[0],
			ran:         values[1],
			checks:      values[2],
			ignored:     values[3],
			filteredOut: values[4],
			duration:    int64(values[5]),
		}, true
	}
	if match := errorSummaryPattern.FindStringSubmatch(line); match != nil {
		values, ok := parseDecimalFields(match[1:])
		if !ok {
			return grammarSummary{}, false
		}
		return grammarSummary{
			failures:    values[0],
			tests:       values[1],
			ran:         values[2],
			checks:      values[3],
			ignored:     values[4],
			filteredOut: values[5],
			duration:    int64(values[6]),
		}, true
	}
	return grammarSummary{}, false
}

func parseDecimalFields(fields []string) ([]int, bool) {
	values := make([]int, len(fields))
	for index, field := range fields {
		value, err := strconv.ParseUint(field, 10, 31)
		if err != nil {
			return nil, false
		}
		values[index] = int(value)
	}
	return values, true
}

func stripANSILine(line []byte) (string, error) {
	if !utf8.Valid(line) {
		return "", fmt.Errorf("%w: malformed UTF-8", ErrInvalidResult)
	}
	var result strings.Builder
	result.Grow(len(line))
	for index := 0; index < len(line); {
		if line[index] != 0x1b {
			if line[index] == 0 {
				return "", fmt.Errorf("%w: NUL in process output", ErrInvalidResult)
			}
			result.WriteByte(line[index])
			index++
			continue
		}
		if index+1 >= len(line) || line[index+1] != '[' {
			return "", fmt.Errorf("%w: unsupported ANSI escape", ErrInvalidResult)
		}
		index += 2
		for {
			if index >= len(line) {
				return "", fmt.Errorf("%w: incomplete ANSI CSI sequence", ErrInvalidResult)
			}
			value := line[index]
			index++
			if value >= 0x40 && value <= 0x7e {
				break
			}
			if value < 0x20 || value > 0x3f {
				return "", fmt.Errorf("%w: malformed ANSI CSI sequence", ErrInvalidResult)
			}
		}
	}
	return result.String(), nil
}
