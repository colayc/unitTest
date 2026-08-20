package coveragereport

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"html/template"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
	"unit-test-ide.local/test-service/internal/coveragenormalize"
)

const reportCSS = "body{font-family:system-ui,sans-serif;margin:2rem;color:#17202a;background:#fff}h1,h2{margin:1.25rem 0 .5rem}table{border-collapse:collapse;width:100%;margin:.5rem 0 1rem}th,td{border:1px solid #bfc9ca;padding:.4rem;text-align:left;vertical-align:top}th{background:#ebf5fb}pre{margin:0;white-space:pre-wrap;word-break:break-word}.metadata{color:#566573}.covered{background:#e8f8f5}.uncovered{background:#fdedec}"
const reportCSP = "default-src 'none'; img-src data:; style-src 'sha256-M8dIDePpmq1uziHlvoBCVJcWNZdVurNhgMktIvjnME8='; script-src 'none'; object-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'"

const reportHTMLTemplate = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="` + reportCSP + `"><title>Coverage report</title><style>` + reportCSS + `</style></head><body>
<h1>Coverage report</h1><p class="metadata">Completeness: {{.Completeness}}</p>
<h2>Summary</h2><table><thead><tr><th>Metric</th><th>Covered</th><th>Total</th></tr></thead><tbody>{{range .Metrics}}<tr><td>{{.Name}}</td><td>{{.Covered}}</td><td>{{.Total}}</td></tr>{{end}}</tbody></table>
<h2>Provenance</h2><table><tbody><tr><th>Platform</th><td>{{.Platform}}</td></tr><tr><th>Architecture</th><td>{{.Architecture}}</td></tr><tr><th>Compiler</th><td>{{.Compiler}}</td></tr><tr><th>Driver</th><td>{{.Driver}}</td></tr><tr><th>Collector</th><td>{{.Collector}}</td></tr><tr><th>Normalizer</th><td>{{.Normalizer}}</td></tr><tr><th>Instrumentation fingerprint</th><td>{{.Fingerprint}}</td></tr></tbody></table>
<h2>Files</h2>{{range .Files}}<section><h3>{{.URI}}</h3><p class="metadata">SHA-256: {{.SHA256}}; {{.SourceState}}</p><table><thead><tr><th>Line</th><th>Hits</th><th>Branches</th><th>Source</th></tr></thead><tbody>{{range .Lines}}<tr class="{{.Class}}"><td>{{.Number}}</td><td>{{.Count}}</td><td>{{.Branches}}</td><td><pre>{{.Source}}</pre></td></tr>{{end}}</tbody></table></section>{{end}}</body></html>
`

type htmlMetric struct {
	Name           string
	Covered, Total int64
}
type htmlLine struct {
	Number, Count           int64
	Branches, Source, Class string
}
type htmlFile struct {
	URI, SHA256, SourceState string
	Lines                    []htmlLine
}
type htmlReport struct {
	Completeness, Platform, Architecture, Compiler, Driver, Collector, Normalizer, Fingerprint string
	Metrics                                                                                    []htmlMetric
	Files                                                                                      []htmlFile
}

func renderHTML(document coveragemodelv1.CoverageDocumentV1, bindings []coveragenormalize.SourceBinding) ([]byte, error) {
	bindingByURI := make(map[string]coveragenormalize.SourceBinding, len(bindings))
	for _, binding := range bindings {
		if _, exists := bindingByURI[binding.URI]; !exists {
			bindingByURI[binding.URI] = binding
		}
	}
	report := htmlReport{
		Completeness: string(document.Completeness.Outcome), Platform: string(document.Provenance.Platform), Architecture: string(document.Provenance.Architecture),
		Compiler:   string(document.Provenance.Compiler.Family) + " " + document.Provenance.Compiler.Version,
		Driver:     string(document.Provenance.Driver.Name) + " " + document.Provenance.Driver.Version,
		Collector:  string(document.Provenance.Collector.Name) + " " + document.Provenance.Collector.Version,
		Normalizer: document.Provenance.NormalizerVersion, Fingerprint: document.Provenance.InstrumentationFingerprint,
		Metrics: []htmlMetric{{"Lines", document.Summary.Lines.Covered, document.Summary.Lines.Total}, {"Branches", document.Summary.Branches.Covered, document.Summary.Branches.Total}, {"Functions", document.Summary.Functions.Covered, document.Summary.Functions.Total}},
	}
	for _, file := range document.Files {
		source, state := sourceContent(bindingByURI[file.URI], file.Sha256)
		lines := strings.Split(source, "\n")
		htmlFile := htmlFile{URI: file.URI, SHA256: file.Sha256, SourceState: state, Lines: make([]htmlLine, 0, len(file.Lines))}
		for _, line := range file.Lines {
			text := ""
			if state == "source retained" && line.Line > 0 && line.Line <= int64(len(lines)) {
				text = lines[line.Line-1]
			}
			class := "uncovered"
			if line.Count > 0 {
				class = "covered"
			}
			htmlFile.Lines = append(htmlFile.Lines, htmlLine{Number: line.Line, Count: line.Count, Branches: fmt.Sprintf("%d/%d", line.Branches.Covered, line.Branches.Total), Source: text, Class: class})
		}
		report.Files = append(report.Files, htmlFile)
	}
	sort.Slice(report.Files, func(i, j int) bool { return report.Files[i].URI < report.Files[j].URI })
	tmpl, err := template.New("report").Parse(reportHTMLTemplate)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, report); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func sourceContent(binding coveragenormalize.SourceBinding, expectedSHA string) (string, string) {
	if binding.URI == "" || binding.SHA256 != expectedSHA || binding.NativePath == "" {
		return "", "metadata only"
	}
	info, err := os.Stat(binding.NativePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSourceTextBytes {
		return "", "metadata only"
	}
	data, err := os.ReadFile(binding.NativePath)
	if err != nil || len(data) > maxSourceTextBytes || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", "metadata only"
	}
	sum := sha256.Sum256(data)
	if fmt.Sprintf("%x", sum) != expectedSHA {
		return "", "metadata only"
	}
	return string(data), "source retained"
}
