package coveragereport

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"reflect"
	"sort"
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
	if err := validateHTML(value.CoverageHTML); err != nil {
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

func validateJUnit(value []byte) error {
	lower := strings.ToLower(string(value))
	if strings.Contains(lower, "<!doctype") || strings.Contains(lower, "<!entity") || strings.Contains(lower, "<![") || strings.Contains(lower, " time=") || strings.Contains(lower, " duration") {
		return fmt.Errorf("forbidden XML declaration or nondeterministic field")
	}
	decoder := xml.NewDecoder(bytes.NewReader(value))
	root := ""
	for {
		token, err := decoder.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return err
		}
		switch current := token.(type) {
		case xml.Directive:
			return fmt.Errorf("XML directive")
		case xml.StartElement:
			if root == "" {
				root = current.Name.Local
			}
		}
	}
	if root != "testsuite" {
		return fmt.Errorf("unexpected root")
	}
	return nil
}

func validateHTML(value []byte) error {
	text := string(value)
	lower := strings.ToLower(text)
	if !strings.HasPrefix(lower, "<!doctype html>\n<html") || !strings.Contains(text, "<meta http-equiv=\"Content-Security-Policy\" content=\""+reportCSP+"\">") || !strings.Contains(text, "<style>"+reportCSS+"</style>") || !strings.HasSuffix(text, "</body></html>\n") {
		return fmt.Errorf("doctype or CSP")
	}
	for _, forbidden := range []string{"<script", "<object", "<frame", "<form", "<base", "<link", "<a ", "<img", "@font-face", "sourcemappingurl", "<iframe"} {
		if strings.Contains(lower, forbidden) {
			return fmt.Errorf("forbidden HTML content")
		}
	}
	if strings.Count(lower, "<style>") != 1 || strings.Count(lower, "</style>") != 1 {
		return fmt.Errorf("unexpected style element")
	}
	return nil
}
