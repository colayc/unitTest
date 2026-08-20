package coveragereport

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
	"unit-test-ide.local/test-service/internal/coveragenormalize"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func Render(input Input) (Set, error) {
	if len(input.CoverageJSON) == 0 || len(input.CoverageJSON) > maxCoverageJSONBytes {
		return Set{}, fmt.Errorf("%w: coverage JSON size", ErrInvalidSet)
	}
	document, err := coveragemodelv1.Decode(input.CoverageJSON)
	if err != nil {
		return Set{}, fmt.Errorf("%w: coverage JSON: %v", ErrInvalidSet, err)
	}
	canonical, err := coveragenormalize.EncodeCanonical(document)
	if err != nil || !bytes.Equal(canonical, input.CoverageJSON) || !reflect.DeepEqual(document, input.Document) {
		return Set{}, fmt.Errorf("%w: canonical coverage document mismatch", ErrInvalidSet)
	}
	run, err := testdomain.NewTestRun(input.TestRun)
	if err != nil || run.Status != testdomain.RunCompleted || run.FinishedAt == nil {
		return Set{}, fmt.Errorf("%w: test run is not completed", ErrInvalidSet)
	}
	if err := validateCompleteResults(run); err != nil {
		return Set{}, fmt.Errorf("%w: test run results: %v", ErrInvalidSet, err)
	}
	junit, err := renderJUnit(run)
	if err != nil {
		return Set{}, fmt.Errorf("%w: JUnit: %v", ErrInvalidSet, err)
	}
	html, err := renderHTML(document, input.Sources)
	if err != nil {
		return Set{}, fmt.Errorf("%w: HTML: %v", ErrInvalidSet, err)
	}
	set := Set{CoverageJSON: append([]byte(nil), input.CoverageJSON...), JUnitXML: append([]byte(nil), junit...), CoverageHTML: append([]byte(nil), html...), Summary: domainSummary(document.Summary), Sources: documentSources(document)}
	if err := Validate(set); err != nil {
		return Set{}, err
	}
	return cloneSet(set), nil
}

func Validate(value Set) error {
	if len(value.CoverageJSON) == 0 || len(value.CoverageJSON) > maxCoverageJSONBytes || len(value.JUnitXML) == 0 || len(value.JUnitXML) > maxJUnitXMLBytes || len(value.CoverageHTML) == 0 || len(value.CoverageHTML) > maxCoverageHTMLBytes {
		return fmt.Errorf("%w: artifact size or presence", ErrInvalidSet)
	}
	document, err := coveragemodelv1.Decode(value.CoverageJSON)
	if err != nil {
		return fmt.Errorf("%w: coverage JSON: %v", ErrInvalidSet, err)
	}
	canonical, err := coveragenormalize.EncodeCanonical(document)
	if err != nil || !bytes.Equal(canonical, value.CoverageJSON) {
		return fmt.Errorf("%w: noncanonical coverage JSON", ErrInvalidSet)
	}
	if value.Summary != domainSummary(document.Summary) || !reflect.DeepEqual(value.Sources, documentSources(document)) {
		return fmt.Errorf("%w: summary or source snapshots", ErrInvalidSet)
	}
	if err := validateJUnit(value.JUnitXML); err != nil {
		return fmt.Errorf("%w: JUnit: %v", ErrInvalidSet, err)
	}
	if err := validateHTML(value.CoverageHTML, document); err != nil {
		return fmt.Errorf("%w: HTML: %v", ErrInvalidSet, err)
	}
	return nil
}

func domainSummary(value coveragemodelv1.CoverageSummaryV1) coveragedomain.Summary {
	return coveragedomain.Summary{Lines: coveragedomain.Metric{Covered: value.Lines.Covered, Total: value.Lines.Total}, Branches: coveragedomain.Metric{Covered: value.Branches.Covered, Total: value.Branches.Total}, Functions: coveragedomain.Metric{Covered: value.Functions.Covered, Total: value.Functions.Total}}
}

func documentSources(document coveragemodelv1.CoverageDocumentV1) []coveragedomain.SourceSnapshot {
	sources := make([]coveragedomain.SourceSnapshot, len(document.Files))
	for i, file := range document.Files {
		sources[i] = coveragedomain.SourceSnapshot{URI: file.URI, SHA256: file.Sha256}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].URI < sources[j].URI })
	return sources
}

func cloneSet(value Set) Set {
	value.CoverageJSON = append([]byte(nil), value.CoverageJSON...)
	value.JUnitXML = append([]byte(nil), value.JUnitXML...)
	value.CoverageHTML = append([]byte(nil), value.CoverageHTML...)
	value.Sources = append([]coveragedomain.SourceSnapshot(nil), value.Sources...)
	return value
}

func validateCompleteResults(run testdomain.TestRun) error {
	if run.Results == nil {
		return errors.New("missing result set")
	}
	actual := testdomain.RunSummary{Total: int64(len(run.Results)), Iterations: 1}
	seen := make(map[string]struct{}, len(run.Results))
	for _, result := range run.Results {
		key := result.ItemID.String() + "#" + strconv.FormatInt(result.Iteration, 10)
		if _, duplicate := seen[key]; duplicate {
			return errors.New("duplicate item iteration")
		}
		seen[key] = struct{}{}
		if result.Iteration > actual.Iterations {
			actual.Iterations = result.Iteration
		}
		switch result.Outcome {
		case testdomain.ItemPassed:
			actual.Completed++
			actual.Passed++
		case testdomain.ItemFailed:
			actual.Completed++
			actual.Failed++
		case testdomain.ItemSkipped:
			actual.Completed++
			actual.Skipped++
		case testdomain.ItemErrored:
			actual.Completed++
			actual.Errored++
		case testdomain.ItemCancelled:
			actual.Completed++
			actual.Cancelled++
		case testdomain.ItemTimedOut:
			actual.Completed++
			actual.TimedOut++
		case testdomain.ItemNotRun:
			actual.NotRun++
		default:
			return errors.New("unsupported item outcome")
		}
	}
	revision, err := testdomain.ResultRevision(run.Results)
	if err != nil || revision != run.ResultRevision {
		return errors.New("result revision")
	}
	if actual != run.Summary {
		return errors.New("result summary")
	}
	return nil
}

func validateJUnit(value []byte) error {
	lower := strings.ToLower(string(value))
	if strings.Contains(lower, "<!doctype") || strings.Contains(lower, "<!entity") || strings.Contains(lower, "<![") || strings.Contains(lower, " time=") || strings.Contains(lower, " duration") {
		return fmt.Errorf("forbidden XML declaration or nondeterministic field")
	}
	decoder := xml.NewDecoder(bytes.NewReader(value))
	var suite junitSuite
	caseOpen, caseHasOutcome, detail := false, false, ""
	rootOpen, rootClosed := false, false
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		switch current := token.(type) {
		case xml.Directive:
			return fmt.Errorf("XML directive")
		case xml.ProcInst:
			if current.Target != "xml" {
				return fmt.Errorf("XML processing instruction")
			}
		case xml.Comment:
			return fmt.Errorf("XML comment")
		case xml.StartElement:
			if current.Name.Space != "" {
				return fmt.Errorf("qualified XML element")
			}
			if !rootOpen {
				if current.Name.Local != "testsuite" || rootClosed {
					return fmt.Errorf("unexpected root")
				}
				parsed, parseErr := parseJUnitSuite(current)
				if parseErr != nil {
					return parseErr
				}
				suite = parsed
				rootOpen = true
				continue
			}
			if !caseOpen {
				if current.Name.Local != "testcase" {
					return fmt.Errorf("unexpected suite child")
				}
				if err := validateJUnitCase(current); err != nil {
					return err
				}
				caseOpen, caseHasOutcome = true, false
				continue
			}
			if detail != "" {
				return fmt.Errorf("nested detail")
			}
			if caseHasOutcome {
				return fmt.Errorf("multiple testcase outcomes")
			}
			if current.Name.Local != "failure" && current.Name.Local != "error" && current.Name.Local != "skipped" {
				return fmt.Errorf("unexpected testcase child")
			}
			if err := validateJUnitDetail(current); err != nil {
				return err
			}
			detail, caseHasOutcome = current.Name.Local, true
		case xml.CharData:
			if detail == "" && strings.TrimSpace(string(current)) != "" {
				return fmt.Errorf("unexpected XML text")
			}
		case xml.EndElement:
			if current.Name.Space != "" {
				return fmt.Errorf("qualified XML element")
			}
			if detail != "" {
				if current.Name.Local != detail {
					return fmt.Errorf("mismatched detail")
				}
				switch detail {
				case "failure":
					suite.failures++
				case "error":
					suite.errors++
				case "skipped":
					suite.skipped++
				}
				detail = ""
				continue
			}
			if caseOpen {
				if current.Name.Local != "testcase" {
					return fmt.Errorf("mismatched testcase")
				}
				suite.tests++
				caseOpen = false
				continue
			}
			if rootOpen && current.Name.Local == "testsuite" {
				rootOpen = false
				rootClosed = true
				continue
			}
			return fmt.Errorf("mismatched root")
		}
	}
	if rootOpen || caseOpen || detail != "" || !rootClosed || suite.tests != suite.declaredTests || suite.failures != suite.declaredFailures || suite.errors != suite.declaredErrors || suite.skipped != suite.declaredSkipped {
		return fmt.Errorf("JUnit structure or counts")
	}
	return nil
}

type junitSuite struct{ declaredTests, declaredFailures, declaredErrors, declaredSkipped, tests, failures, errors, skipped int }

func parseJUnitSuite(element xml.StartElement) (junitSuite, error) {
	attrs, err := exactAttrs(element, "name", "tests", "failures", "errors", "skipped")
	if err != nil || attrs["name"] != "coverage-test-run" {
		return junitSuite{}, errors.New("suite attributes")
	}
	values := []string{attrs["tests"], attrs["failures"], attrs["errors"], attrs["skipped"]}
	parsed := make([]int, len(values))
	for i, value := range values {
		parsed[i], err = strconv.Atoi(value)
		if err != nil || parsed[i] < 0 {
			return junitSuite{}, errors.New("suite count")
		}
	}
	return junitSuite{declaredTests: parsed[0], declaredFailures: parsed[1], declaredErrors: parsed[2], declaredSkipped: parsed[3]}, nil
}

func validateJUnitCase(element xml.StartElement) error {
	attrs, err := exactAttrs(element, "name", "classname")
	if err != nil {
		return err
	}
	name := attrs["name"]
	if index := strings.LastIndexByte(name, '#'); index >= 0 {
		iteration, parseErr := strconv.ParseInt(name[index+1:], 10, 64)
		if parseErr != nil || iteration < 2 {
			return errors.New("testcase iteration")
		}
		name = name[:index]
	}
	if !testdomain.ValidID(testdomain.ID(name)) || !testdomain.ValidID(testdomain.ID(attrs["classname"])) {
		return errors.New("testcase identity")
	}
	return nil
}

func validateJUnitDetail(element xml.StartElement) error {
	if element.Name.Local == "skipped" {
		_, err := exactAttrs(element, "message")
		return err
	}
	attrs, err := exactAttrs(element, "type", "message")
	if err != nil || attrs["type"] == "" {
		return errors.New("detail attributes")
	}
	return nil
}

func exactAttrs(element xml.StartElement, names ...string) (map[string]string, error) {
	if len(element.Attr) != len(names) {
		return nil, errors.New("attribute count")
	}
	attrs := make(map[string]string, len(names))
	expected := make(map[string]struct{}, len(names))
	for _, name := range names {
		expected[name] = struct{}{}
	}
	for _, attr := range element.Attr {
		if attr.Name.Space != "" {
			return nil, errors.New("attribute namespace")
		}
		if _, ok := expected[attr.Name.Local]; !ok {
			return nil, errors.New("unknown attribute")
		}
		if _, duplicate := attrs[attr.Name.Local]; duplicate {
			return nil, errors.New("duplicate attribute")
		}
		attrs[attr.Name.Local] = attr.Value
	}
	return attrs, nil
}

var sourcePre = regexp.MustCompile(`(?s)<pre>.*?</pre>`)

func validateHTML(value []byte, document coveragemodelv1.CoverageDocumentV1) error {
	expected, err := renderHTML(document, nil)
	if err != nil {
		return err
	}
	normalized := sourcePre.ReplaceAll(value, []byte("<pre></pre>"))
	normalized = bytes.ReplaceAll(normalized, []byte("; source retained</p>"), []byte("; metadata only</p>"))
	if !bytes.Equal(normalized, expected) {
		return fmt.Errorf("HTML does not exactly bind Coverage JSON")
	}
	return nil
}
