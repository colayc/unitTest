package coveragereport

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/testdomain"
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
		{Name: xml.Name{Local: "name"}, Value: xmlText(name)},
		{Name: xml.Name{Local: "classname"}, Value: xmlText(result.ContainerID.String())},
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
		if err := encoder.EncodeToken(xml.StartElement{Name: xml.Name{Local: "skipped"}, Attr: []xml.Attr{{Name: xml.Name{Local: "message"}, Value: xmlText(message)}}}); err != nil {
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
		{Name: xml.Name{Local: "type"}, Value: xmlText(kind)},
		{Name: xml.Name{Local: "message"}, Value: xmlText(message)},
	}}); err != nil {
		return err
	}
	if err := encoder.EncodeToken(xml.CharData(xmlText(junitDetails(result)))); err != nil {
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
	return detail.Message, detail.Category
}

func junitDetails(result testdomain.TestItemResult) string {
	parts := make([]string, 0, len(result.FailureDetails)*2)
	for _, detail := range result.FailureDetails {
		if detail.Message != "" {
			parts = append(parts, detail.Message)
		}
		for _, location := range detail.Locations {
			if location.URI == "" {
				continue
			}
			value := location.URI
			if location.Line > 0 {
				value += ":" + strconv.Itoa(location.Line)
			}
			if location.Column > 0 {
				value += ":" + strconv.Itoa(location.Column)
			}
			parts = append(parts, value)
		}
	}
	return xmlText(strings.Join(parts, "\n"))
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

func junitCounts(run testdomain.TestRun) string {
	return fmt.Sprintf("%d", len(run.Results))
}
