package coveragereport

import (
	"bytes"
	"encoding/xml"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/testdomain"
)

var (
	diagnosticURL     = regexp.MustCompile(`(?i)\b(?:https?|file)://[^\s<>"']+`)
	diagnosticSecret  = regexp.MustCompile(`(?i)\b(?:token|api[_-]?key|authorization|password|secret)\s*[:=]\s*\S+`)
	diagnosticRuntime = regexp.MustCompile(`(?i)\b(?:argv|command|executable|environment|env|llvm_profile_file)\s*[:=]\s*\S+`)
	diagnosticWindows = regexp.MustCompile(`(?i)(?:[a-z]:\\|\\\\)[^\s<>"']+`)
	diagnosticPOSIX   = regexp.MustCompile(`(?:^|\s)/(?:[^\s<>"']+/?)+`)
)

func renderJUnit(run testdomain.TestRun) ([]byte, error) {
	results := append([]testdomain.TestItemResult(nil), run.Results...)
	sort.Slice(results, func(i, j int) bool {
		if results[i].ItemID != results[j].ItemID {
			return results[i].ItemID < results[j].ItemID
		}
		return results[i].Iteration < results[j].Iteration
	})
	failures, errorsCount, skipped := 0, 0, 0
	for _, result := range results {
		switch result.Outcome {
		case testdomain.ItemFailed:
			failures++
		case testdomain.ItemErrored, testdomain.ItemCancelled, testdomain.ItemTimedOut:
			errorsCount++
		case testdomain.ItemSkipped, testdomain.ItemNotRun:
			skipped++
		}
	}

	var output bytes.Buffer
	output.WriteString(xml.Header)
	encoder := xml.NewEncoder(&output)
	if err := encoder.EncodeToken(xml.StartElement{Name: xml.Name{Local: "testsuite"}, Attr: []xml.Attr{
		{Name: xml.Name{Local: "name"}, Value: "coverage-test-run"},
		{Name: xml.Name{Local: "tests"}, Value: strconv.Itoa(len(results))},
		{Name: xml.Name{Local: "failures"}, Value: strconv.Itoa(failures)},
		{Name: xml.Name{Local: "errors"}, Value: strconv.Itoa(errorsCount)},
		{Name: xml.Name{Local: "skipped"}, Value: strconv.Itoa(skipped)},
	}}); err != nil {
		return nil, err
	}
	for _, result := range results {
		if err := writeJUnitCase(encoder, result); err != nil {
			return nil, err
		}
	}
	if err := encoder.EncodeToken(xml.EndElement{Name: xml.Name{Local: "testsuite"}}); err != nil {
		return nil, err
	}
	if err := encoder.Flush(); err != nil {
		return nil, err
	}
	output.WriteByte('\n')
	return output.Bytes(), nil
}

func writeJUnitCase(encoder *xml.Encoder, result testdomain.TestItemResult) error {
	name := result.ItemID.String()
	if result.Iteration > 1 {
		name += "#" + strconv.FormatInt(result.Iteration, 10)
	}
	start := xml.StartElement{Name: xml.Name{Local: "testcase"}, Attr: []xml.Attr{
		{Name: xml.Name{Local: "name"}, Value: name},
		{Name: xml.Name{Local: "classname"}, Value: result.ContainerID.String()},
	}}
	if err := encoder.EncodeToken(start); err != nil {
		return err
	}
	switch result.Outcome {
	case testdomain.ItemFailed:
		if err := writeJUnitDetail(encoder, "failure", result); err != nil {
			return err
		}
	case testdomain.ItemErrored, testdomain.ItemCancelled, testdomain.ItemTimedOut:
		if err := writeJUnitDetail(encoder, "error", result); err != nil {
			return err
		}
	case testdomain.ItemSkipped, testdomain.ItemNotRun:
		message := string(result.Outcome)
		if result.Reason != "" {
			message += ": " + string(result.Reason)
		}
		if err := encoder.EncodeToken(xml.StartElement{Name: xml.Name{Local: "skipped"}, Attr: []xml.Attr{{Name: xml.Name{Local: "message"}, Value: safeDiagnostic(message)}}}); err != nil {
			return err
		}
		if err := encoder.EncodeToken(xml.EndElement{Name: xml.Name{Local: "skipped"}}); err != nil {
			return err
		}
	}
	return encoder.EncodeToken(xml.EndElement{Name: xml.Name{Local: "testcase"}})
}

func writeJUnitDetail(encoder *xml.Encoder, element string, result testdomain.TestItemResult) error {
	message, kind := junitMessage(result)
	if err := encoder.EncodeToken(xml.StartElement{Name: xml.Name{Local: element}, Attr: []xml.Attr{
		{Name: xml.Name{Local: "type"}, Value: kind},
		{Name: xml.Name{Local: "message"}, Value: safeDiagnostic(message)},
	}}); err != nil {
		return err
	}
	if err := encoder.EncodeToken(xml.CharData(safeDiagnostic(junitDetails(result)))); err != nil {
		return err
	}
	return encoder.EncodeToken(xml.EndElement{Name: xml.Name{Local: element}})
}

func junitMessage(result testdomain.TestItemResult) (string, string) {
	if len(result.FailureDetails) == 0 {
		return string(result.Outcome), string(result.Outcome)
	}
	detail := result.FailureDetails[0]
	if detail.Message == "" {
		return detail.Category, detail.Category
	}
	return safeDiagnostic(detail.Message), detail.Category
}

func junitDetails(result testdomain.TestItemResult) string {
	parts := make([]string, 0, 1+len(result.FailureDetails)*5)
	if result.SourceLocation != nil {
		parts = append(parts, "primary-location: "+safeLocation(*result.SourceLocation))
	}
	details := append([]testdomain.FailureDetail(nil), result.FailureDetails...)
	sort.SliceStable(details, func(i, j int) bool {
		return detailSortKey(details[i]) < detailSortKey(details[j])
	})
	for _, detail := range details {
		if detail.Subtype != "" {
			parts = append(parts, "subtype: "+string(detail.Subtype))
		}
		if detail.Message != "" {
			parts = append(parts, "message: "+safeDiagnostic(detail.Message))
		}
		if detail.Expected != "" {
			parts = append(parts, "expected: "+safeDiagnostic(detail.Expected))
		}
		if detail.Actual != "" {
			parts = append(parts, "actual: "+safeDiagnostic(detail.Actual))
		}
		locations := append([]testdomain.SourceLocation(nil), detail.Locations...)
		sort.SliceStable(locations, func(i, j int) bool { return locationSortKey(locations[i]) < locationSortKey(locations[j]) })
		for _, location := range locations {
			parts = append(parts, "location: "+safeLocation(location))
		}
	}
	return strings.Join(parts, "\n")
}

func detailSortKey(value testdomain.FailureDetail) string {
	return string(value.Subtype) + "\x00" + value.Category + "\x00" + value.Message + "\x00" + value.Expected + "\x00" + value.Actual
}
func locationSortKey(value testdomain.SourceLocation) string {
	return strconv.Itoa(value.Line) + "\x00" + strconv.Itoa(value.Column) + "\x00" + value.Provenance + "\x00" + value.URI
}

func safeLocation(value testdomain.SourceLocation) string {
	parts := []string{}
	if value.Line > 0 {
		parts = append(parts, "line "+strconv.Itoa(value.Line))
	}
	if value.Column > 0 {
		parts = append(parts, "column "+strconv.Itoa(value.Column))
	}
	if len(parts) == 0 {
		return "redacted"
	}
	return strings.Join(parts, ", ")
}

func safeDiagnostic(value string) string {
	value = diagnosticURL.ReplaceAllString(value, "[redacted-url]")
	value = diagnosticSecret.ReplaceAllString(value, "[redacted-secret]")
	value = diagnosticRuntime.ReplaceAllString(value, "[redacted-runtime]")
	value = diagnosticWindows.ReplaceAllString(value, "[redacted-path]")
	value = diagnosticPOSIX.ReplaceAllStringFunc(value, func(path string) string {
		if strings.HasPrefix(path, " ") {
			return " [redacted-path]"
		}
		return "[redacted-path]"
	})
	return xmlText(value)
}

func xmlText(value string) string {
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "�")
	}
	var output strings.Builder
	for _, runeValue := range value {
		if runeValue == '\t' || runeValue == '\n' || runeValue == '\r' || runeValue >= 0x20 && runeValue != 0xFFFE && runeValue != 0xFFFF {
			output.WriteRune(runeValue)
		} else {
			output.WriteRune('�')
		}
		if output.Len() >= maxDiagnosticBytes {
			break
		}
	}
	return output.String()
}
