package unity

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

const maxOutputFailureFieldBytes = 8192

type unityOutputRecord struct {
	path     string
	line     int
	identity string
	status   testframework.CaseStatus
	message  string
}

func parseUnityOutput(data []byte) []unityOutputRecord {
	if len(data) == 0 || !utf8.Valid(data) {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	result := make([]unityOutputRecord, 0, 1)
	for _, line := range lines {
		if len(line) == 0 || len(line) > maxRecordBytes {
			continue
		}
		if record, ok := parseUnityOutputLine(strings.TrimSuffix(line, "\r")); ok {
			result = append(result, record)
		}
	}
	return result
}

func parseUnityOutputLine(line string) (unityOutputRecord, bool) {
	type marker struct {
		value  string
		status testframework.CaseStatus
	}
	markers := []marker{
		{value: ":FAIL:", status: testframework.CaseFailed},
		{value: ":IGNORE:", status: testframework.CaseSkipped},
		{value: ":PASS", status: testframework.CasePassed},
	}
	for _, candidate := range markers {
		index := strings.Index(line, candidate.value)
		if index <= 0 {
			continue
		}
		if candidate.status == testframework.CasePassed &&
			index+len(candidate.value) != len(line) {
			continue
		}
		path, lineNumber, identity, ok := parseUnityOutputPrefix(line[:index])
		if !ok {
			return unityOutputRecord{}, false
		}
		message := ""
		if candidate.status != testframework.CasePassed {
			message = strings.TrimSpace(line[index+len(candidate.value):])
		}
		return unityOutputRecord{
			path:     path,
			line:     lineNumber,
			identity: identity,
			status:   candidate.status,
			message:  message,
		}, true
	}
	return unityOutputRecord{}, false
}

func parseUnityOutputPrefix(prefix string) (string, int, string, bool) {
	identitySeparator := strings.LastIndexByte(prefix, ':')
	if identitySeparator <= 0 || identitySeparator == len(prefix)-1 {
		return "", 0, "", false
	}
	lineSeparator := strings.LastIndexByte(prefix[:identitySeparator], ':')
	if lineSeparator <= 0 || lineSeparator == identitySeparator-1 {
		return "", 0, "", false
	}
	lineNumber, err := strconv.Atoi(prefix[lineSeparator+1 : identitySeparator])
	if err != nil || lineNumber <= 0 {
		return "", 0, "", false
	}
	path := prefix[:lineSeparator]
	identity := prefix[identitySeparator+1:]
	if !validOutputText(path) || !validOutputText(identity) {
		return "", 0, "", false
	}
	return path, lineNumber, identity, true
}

func outputFailureDetail(
	record unityOutputRecord,
) testframework.ParsedFailureDetail {
	location := testframework.ParsedSourceLocation{
		Path:       record.path,
		Line:       record.line,
		Provenance: "framework-output",
	}
	detail := testframework.ParsedFailureDetail{
		Category:  "assertion_failure",
		Message:   boundedOutputText(record.message),
		Locations: []testframework.ParsedSourceLocation{location},
	}
	if subtype, expected, actual, ok := parseCMockFailure(record.message); ok {
		detail.Subtype = subtype
		detail.Expected = boundedOutputText(expected)
		detail.Actual = boundedOutputText(actual)
	}
	return detail
}

func parseCMockFailure(
	message string,
) (testdomain.FailureSubtype, string, string, bool) {
	if !validOutputText(message) {
		return "", "", "", false
	}
	lower := strings.ToLower(message)
	if !strings.Contains(lower, "function ") {
		return "", "", "", false
	}
	var subtype testdomain.FailureSubtype
	switch {
	case strings.Contains(lower, "called more times than expected"),
		strings.Contains(lower, "unexpected call"):
		subtype = testdomain.FailureSubtypeMockUnexpectedCall
	case strings.Contains(lower, "called fewer times than expected"),
		strings.Contains(lower, "expected call"):
		subtype = testdomain.FailureSubtypeMockMissingCall
	case strings.Contains(lower, "unexpected value"),
		strings.Contains(lower, "argument ") &&
			strings.Contains(lower, "expected"):
		subtype = testdomain.FailureSubtypeMockParameterMismatch
	default:
		return "", "", "", false
	}

	expected, actual := expectedAndActual(message)
	function := cmockFunctionName(message)
	switch subtype {
	case testdomain.FailureSubtypeMockUnexpectedCall:
		if actual == "" {
			actual = function
		}
	case testdomain.FailureSubtypeMockMissingCall:
		if expected == "" {
			expected = function
		}
	}
	return subtype, expected, actual, true
}

func expectedAndActual(message string) (string, string) {
	expectedIndex := strings.Index(message, "Expected ")
	if expectedIndex < 0 {
		return "", ""
	}
	value := message[expectedIndex+len("Expected "):]
	wasIndex := strings.Index(value, " Was ")
	if wasIndex < 0 {
		return "", ""
	}
	expected := strings.TrimSpace(value[:wasIndex])
	actual := value[wasIndex+len(" Was "):]
	for _, marker := range []string{". Function ", ". CMock", "\n"} {
		if index := strings.Index(actual, marker); index >= 0 {
			actual = actual[:index]
			break
		}
	}
	return strings.TrimSpace(expected), strings.TrimSpace(actual)
}

func cmockFunctionName(message string) string {
	index := strings.Index(message, "Function ")
	if index < 0 {
		return ""
	}
	value := message[index+len("Function "):]
	for index := range len(value) {
		character := value[index]
		if character == '_' ||
			character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			index > 0 && character >= '0' && character <= '9' {
			continue
		}
		value = value[:index]
		break
	}
	return strings.TrimSpace(value)
}

func validOutputText(value string) bool {
	return value != "" && utf8.ValidString(value) &&
		!strings.ContainsRune(value, '\x00')
}

func boundedOutputText(value string) string {
	if len(value) <= maxOutputFailureFieldBytes {
		return value
	}
	limit := maxOutputFailureFieldBytes - len("...")
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit] + "..."
}
