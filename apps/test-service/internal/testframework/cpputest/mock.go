package cpputest

import (
	"strings"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

const maxStructuredFailureFieldBytes = 8192

func parseMockFailure(
	message string,
	locations []testframework.ParsedSourceLocation,
) (testframework.ParsedFailureDetail, bool) {
	lines := strings.Split(strings.ReplaceAll(message, "\r\n", "\n"), "\n")
	headerIndex := -1
	header := ""
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Mock Failure:") ||
			strings.HasPrefix(trimmed, "MockFailure:") {
			headerIndex = index
			header = trimmed
			break
		}
	}
	if headerIndex < 0 {
		return testframework.ParsedFailureDetail{}, false
	}

	subtype := classifyMockFailure(header)
	expected, actual := mockExpectedAndActual(lines[headerIndex+1:])
	if subtype == testdomain.FailureSubtypeMockUnexpectedCall && actual == "" {
		actual = mockFunctionFromHeader(header)
	}

	detailLocations := append(
		[]testframework.ParsedSourceLocation(nil),
		locations...,
	)
	if len(detailLocations) > 16 {
		detailLocations = detailLocations[:16]
	}
	for index := range detailLocations {
		// CppUMock's base MockFailure points at the test declaration. Official
		// output does not carry the expectation call site, so additional
		// framework locations may be classified as actual calls, but an
		// expectation location must never be invented here.
		if detailLocations[index].Provenance != "" &&
			detailLocations[index].Provenance != "framework-output" {
			continue
		}
		if index == 0 {
			detailLocations[index].Provenance = "test-declaration"
		} else {
			detailLocations[index].Provenance = "mock-actual-call"
		}
	}
	return testframework.ParsedFailureDetail{
		Category:  "assertion_failure",
		Subtype:   subtype,
		Message:   boundedMockText(strings.TrimSpace(message)),
		Expected:  boundedMockText(expected),
		Actual:    boundedMockText(actual),
		Locations: detailLocations,
	}, true
}

func classifyMockFailure(header string) testdomain.FailureSubtype {
	switch {
	case strings.HasPrefix(header, "Mock Failure: Unexpected call to function:"),
		strings.HasPrefix(header, "Mock Failure: Unexpected additional ("):
		return testdomain.FailureSubtypeMockUnexpectedCall
	case strings.HasPrefix(header, "Mock Failure: Expected call WAS NOT fulfilled."),
		strings.HasPrefix(header, "Mock Failure: Expected call on object"):
		return testdomain.FailureSubtypeMockMissingCall
	case strings.HasPrefix(header, "Mock Failure: Unexpected parameter"),
		strings.HasPrefix(header, "Mock Failure: Unexpected output parameter"),
		strings.HasPrefix(header, "Mock Failure: Expected parameter"):
		return testdomain.FailureSubtypeMockParameterMismatch
	default:
		return testdomain.FailureSubtypeMockFailure
	}
}

func mockExpectedAndActual(lines []string) (string, string) {
	const (
		sectionNone = iota
		sectionExpected
		sectionActual
	)
	section := sectionNone
	var expected []string
	var actual []string
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(
			upper,
			"EXPECTED CALLS THAT WERE NOT FULFILLED",
		), strings.HasPrefix(
			upper,
			"EXPECTED CALLS WITH MISSING PARAMETERS",
		):
			section = sectionExpected
			continue
		case strings.HasPrefix(upper, "EXPECTED CALLS THAT WERE FULFILLED"):
			section = sectionNone
			continue
		case strings.HasPrefix(upper, "ACTUAL UNEXPECTED"):
			section = sectionActual
			continue
		}
		if line == "" || line == "<none>" {
			continue
		}
		switch section {
		case sectionExpected:
			expected = append(expected, line)
		case sectionActual:
			actual = append(actual, line)
		}
	}
	return strings.Join(expected, "\n"), strings.Join(actual, "\n")
}

func mockFunctionFromHeader(header string) string {
	marker := "call to function:"
	index := strings.LastIndex(header, marker)
	if index < 0 {
		return ""
	}
	return strings.TrimSpace(header[index+len(marker):])
}

func boundedMockText(value string) string {
	if len(value) <= maxStructuredFailureFieldBytes {
		return value
	}
	limit := maxStructuredFailureFieldBytes - len("...")
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit] + "..."
}
