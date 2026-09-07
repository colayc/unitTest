package gcovr

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseGCovrExportAcrossChunks(t *testing.T) {
	encoded := validExport("src/simple.c")
	want := Export{FormatVersion: "0.14", Files: []File{{
		RelativePath: "src/simple.c", Functions: Metric{Covered: 1, Total: 1},
		Lines: []Line{{Number: 1, Count: 2, Branches: Metric{Covered: 1, Total: 2}}},
	}}}
	for chunk := 1; chunk <= len(encoded); chunk++ {
		got, err := Parse(&chunkReader{data: []byte(encoded), chunk: chunk}, DefaultLimits())
		if err != nil {
			t.Fatalf("chunk %d: Parse() error = %v", chunk, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("chunk %d: Parse() = %#v, want %#v", chunk, got, want)
		}
	}
}

func TestParseGCovrLockedFormatFixtures(t *testing.T) {
	for _, name := range []string{"simple.json", "branches.json", "functions.json", "schema-variants.json", "empty.json"} {
		t.Run(name, func(t *testing.T) {
			encoded, err := os.ReadFile("testdata/" + name)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Parse(bytes.NewReader(encoded), DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			if got.FormatVersion != "0.14" {
				t.Fatalf("format = %q", got.FormatVersion)
			}
		})
	}
	for _, name := range []string{"malformed.json", "duplicate.json"} {
		t.Run(name, func(t *testing.T) {
			encoded, err := os.ReadFile("testdata/" + name)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := Parse(bytes.NewReader(encoded), DefaultLimits()); err == nil || !reflect.DeepEqual(got, Export{}) {
				t.Fatalf("Parse() = %#v, %v", got, err)
			}
		})
	}
}

// TestParseGCovrRawLinuxArtifact is intentionally driven only by
// verify-linux-fixtures.sh. The verifier rejects a missing artifact before it
// invokes this test, so ordinary host-independent test runs remain hermetic.
func TestParseGCovrRawLinuxArtifact(t *testing.T) {
	path := os.Getenv("UNIT_TEST_IDE_GCOVR_RAW_FIXTURE")
	if path == "" {
		t.Skip("raw gcovr fixture artifact is supplied by the Linux verifier")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(bytes.NewReader(encoded), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if got.FormatVersion != "0.14" || len(got.Files) == 0 {
		t.Fatalf("raw artifact export = %#v", got)
	}
}

func TestGCovrFixtureProvenancePinsBundleAndFixtureDigests(t *testing.T) {
	encoded, err := os.ReadFile("testdata/provenance.json")
	if err != nil {
		t.Fatal(err)
	}
	var provenance struct {
		Bundle struct {
			ManifestSHA256, PythonVersion, GCovrVersion, Platform string
		}
		Generator struct {
			Source, SourceSHA256, Command, ArtifactDirectory, RawOutput, RawOutputDigestSidecar, CanonicalOutput, CanonicalOutputDigestSidecar, VerificationCommand string
		}
		Fixtures map[string]struct{ SHA256 string }
	}
	if err := json.Unmarshal(encoded, &provenance); err != nil {
		t.Fatal(err)
	}
	if provenance.Bundle.ManifestSHA256 != "62ce3b007ce12261f7d29484ab08ec5e85da63e150d45a2cd26d96b2b4bdb61a" || provenance.Bundle.PythonVersion != "3.14.6" || provenance.Bundle.GCovrVersion != "8.6" || provenance.Bundle.Platform != "linux-x64" {
		t.Fatalf("bundle provenance = %#v", provenance.Bundle)
	}
	manifest, err := os.ReadFile("../../../../../tools/coverage-bundle/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest := sha256.Sum256(manifest)
	if provenance.Bundle.ManifestSHA256 != hex.EncodeToString(manifestDigest[:]) {
		t.Fatalf("manifest digest = %s", hex.EncodeToString(manifestDigest[:]))
	}
	if bytes.Contains(encoded, []byte("rawOutputSHA256")) || provenance.Generator.Source != "fixture-source.c" || provenance.Generator.Command != "bash apps/test-service/internal/coverageparser/gcovr/testdata/generate-linux-fixtures.sh" || provenance.Generator.ArtifactDirectory != "required UNIT_TEST_IDE_GCOVR_FIXTURE_ARTIFACT_DIR" || provenance.Generator.RawOutput != "raw-coverage.json" || provenance.Generator.RawOutputDigestSidecar != "raw-coverage.json.sha256" || provenance.Generator.CanonicalOutput != "gcovr-8.6.canonical.json" || provenance.Generator.CanonicalOutputDigestSidecar != "gcovr-8.6.canonical.json.sha256" || provenance.Generator.VerificationCommand != "bash apps/test-service/internal/coverageparser/gcovr/testdata/verify-linux-fixtures.sh <artifact-dir>" {
		t.Fatalf("generator provenance = %#v", provenance.Generator)
	}
	source, err := os.ReadFile("testdata/" + provenance.Generator.Source)
	if err != nil {
		t.Fatal(err)
	}
	sourceDigest := sha256.Sum256(source)
	if provenance.Generator.SourceSHA256 != hex.EncodeToString(sourceDigest[:]) {
		t.Fatalf("source digest = %s", hex.EncodeToString(sourceDigest[:]))
	}
	script, err := os.ReadFile("testdata/generate-linux-fixtures.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range [][]byte{
		[]byte("-I -S"), []byte("manifest.resolved.json"), []byte("prepare.mjs"),
		[]byte("linux-x64"), []byte("3.14.6"), []byte("8.6"),
		[]byte("UNIT_TEST_IDE_GCC"), []byte("UNIT_TEST_IDE_GCOV"),
		[]byte("UNIT_TEST_IDE_GCOVR_FIXTURE_ARTIFACT_DIR"), []byte("raw-coverage.json.sha256"),
		[]byte("unbound bundle root override is forbidden"), []byte("expected_manifest_sha256"),
	} {
		if !bytes.Contains(script, required) {
			t.Fatalf("generator lacks %q", required)
		}
	}
	verifier, err := os.ReadFile("testdata/verify-linux-fixtures.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range [][]byte{[]byte("raw artifact is required"), []byte("raw-coverage.json.sha256"), []byte("TestParseGCovrRawLinuxArtifact"), []byte("TestGCovrFixtureProvenancePinsBundleAndFixtureDigests")} {
		if !bytes.Contains(verifier, required) {
			t.Fatalf("verifier lacks %q", required)
		}
	}
	for _, name := range []string{"simple.json", "branches.json", "functions.json", "empty.json", "schema-variants.json", "malformed.json", "duplicate.json"} {
		record, ok := provenance.Fixtures[name]
		if !ok || len(record.SHA256) != 64 {
			t.Fatalf("missing digest for %s", name)
		}
		fixture, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		actual := sha256.Sum256(fixture)
		if record.SHA256 != hex.EncodeToString(actual[:]) {
			t.Fatalf("digest for %s = %s", name, hex.EncodeToString(actual[:]))
		}
	}
}

func TestParseGCovrAcceptsDocumentedSchemaVariantsAndExcludesEvidence(t *testing.T) {
	got, err := Parse(bytes.NewBufferString(schemaVariantExport()), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 1 || len(got.Files[0].Lines) != 2 {
		t.Fatalf("files/lines = %#v", got)
	}
	if got.Files[0].Lines[0].Branches != (Metric{Covered: 1, Total: 1}) {
		t.Fatalf("first branch metric = %#v", got.Files[0].Lines[0].Branches)
	}
	if got.Files[0].Lines[1].Number != 3 || got.Files[0].Lines[1].Branches != (Metric{Total: 1}) {
		t.Fatalf("excluded branch or line leaked: %#v", got.Files[0].Lines)
	}
	if got.Files[0].Functions != (Metric{Covered: 1, Total: 1}) {
		t.Fatalf("excluded or unknown function metric = %#v", got.Files[0].Functions)
	}
}

func TestParseGCovrRejectsMalformedNestedSchemaValues(t *testing.T) {
	valid := schemaVariantExport()
	cases := map[string]string{
		"unknown condition":      strings.Replace(valid, `"conditionno":0`, `"unexpected":1,"conditionno":0`, 1),
		"duplicate call":         strings.Replace(valid, `"callno":0`, `"callno":0,"callno":0`, 1),
		"negative decision":      strings.Replace(valid, `"count_true":1`, `"count_true":-1`, 1),
		"fractional destination": strings.Replace(valid, `"destination_block_id":1`, `"destination_block_id":1.5`, 1),
		"overflow data":          strings.Replace(valid, `"source_block_id":0`, `"source_block_id":9007199254740992`, 1),
		"deprecated noncode":     strings.Replace(valid, `"function_name"`, `"gcovr/noncode":false,"function_name"`, 1),
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Parse(bytes.NewBufferString(encoded), DefaultLimits())
			if err == nil || !reflect.DeepEqual(got, Export{}) {
				t.Fatalf("Parse() = %#v, %v", got, err)
			}
		})
	}
}

func TestParseGCovrRejectsMalformedSchemaAndUnsafePaths(t *testing.T) {
	valid := validExport("src/simple.c")
	cases := map[string]string{
		"malformed":           `{`,
		"duplicate root":      strings.Replace(valid, `"files":`, `"files":[],"files":`, 1),
		"unknown root":        strings.Replace(valid, `"files":`, `"unknown":0,"files":`, 1),
		"duplicate file":      strings.Replace(valid, `"file":`, `"file":"src/simple.c","file":`, 1),
		"unknown file":        strings.Replace(valid, `"file":`, `"unknown":0,"file":`, 1),
		"duplicate line":      strings.Replace(valid, `"line_number":`, `"line_number":1,"line_number":`, 1),
		"unknown branch":      strings.Replace(valid, `"count":1,"fallthrough"`, `"unknown":0,"count":1,"fallthrough"`, 1),
		"unsupported version": strings.Replace(valid, `"0.14"`, `"0.13"`, 1),
		"trailing value":      valid + ` {}`,
		"negative":            strings.Replace(valid, `"count":2`, `"count":-1`, 1),
		"floating":            strings.Replace(valid, `"count":2`, `"count":2.5`, 1),
		"overflow":            strings.Replace(valid, `"count":2`, `"count":9007199254740992`, 1),
		"invalid UTF-8":       strings.Replace(valid, "simple.c", string([]byte{'s', 0xff}), 1),
	}
	for _, path := range []string{".", "..", "../escape.c", "/absolute.c", `src\\backslash.c`, "src/\x00nul.c"} {
		cases["unsafe path "+path] = validExport(path)
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Parse(bytes.NewBufferString(encoded), DefaultLimits())
			if err == nil || !reflect.DeepEqual(got, Export{}) {
				t.Fatalf("Parse() = %#v, %v; want no evidence and error", got, err)
			}
		})
	}
}

func TestParseGCovrLimitsFailClosed(t *testing.T) {
	valid := []byte(validExport("src/simple.c"))
	cases := []struct {
		name  string
		input []byte
		edit  func(*Limits)
	}{
		{"input", valid, func(v *Limits) { v.MaxInputBytes = 1 }},
		{"depth", valid, func(v *Limits) { v.MaxDepth = 2 }},
		{"files", []byte(exportFiles(fileJSON("src/simple.c"), fileJSON("src/other.c"))), func(v *Limits) { v.MaxFiles = 1 }},
		{"functions", []byte(strings.Replace(string(valid), `}]}`, `},{"name":"other","lineno":2,"execution_count":0,"blocks_percent":0}]}`, 1)), func(v *Limits) { v.MaxFunctions = 1 }},
		{"lines", []byte(strings.Replace(string(valid), `}],"functions"`, `},{"line_number":2,"count":0,"branches":[],"gcovr/noncode":false}],"functions"`, 1)), func(v *Limits) { v.MaxLines = 1 }},
		{"branches", valid, func(v *Limits) { v.MaxBranches = 1 }},
		{"string", valid, func(v *Limits) { v.MaxStringBytes = 3 }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			limits := DefaultLimits()
			test.edit(&limits)
			got, err := Parse(bytes.NewReader(test.input), limits)
			if !errors.Is(err, ErrLimitExceeded) || !reflect.DeepEqual(got, Export{}) {
				t.Fatalf("Parse() = %#v, %v; want ErrLimitExceeded", got, err)
			}
		})
	}
}

func validExport(path string) string { return exportFiles(fileJSON(path)) }
func exportFiles(files ...string) string {
	return `{"gcovr/format_version":"0.14","files":[` + strings.Join(files, ",") + `]}`
}
func fileJSON(path string) string {
	return `{"file":"` + path + `","lines":[{"line_number":1,"function_name":"entry","count":2,"branches":[{"branchno":0,"count":1,"fallthrough":false,"throw":false,"source_block_id":0},{"branchno":1,"count":0,"fallthrough":false,"throw":false,"source_block_id":0}],"gcovr/md5":"0123456789abcdef0123456789abcdef"}],"functions":[{"name":"entry","lineno":1,"execution_count":2,"blocks_percent":100}]}`
}

// schemaVariantExport is a fixed test input copied from the field shapes in
// gcovr 8.6's JSON format reference (format 0.14). It is not claimed to be
// generated on this Windows host; native bundle generation is exercised in CI.
func schemaVariantExport() string {
	return `{"gcovr/format_version":"0.14","files":[{"file":"src/variant.c","gcovr/data_sources":["object.gcda"],"lines":[{"line_number":1,"function_name":"entry","block_ids":[0],"count":2,"branches":[{"branchno":0,"count":1,"fallthrough":false,"throw":false,"source_block_id":0,"destination_block_id":1,"gcovr/data_sources":["object.gcda"]}],"conditions":[{"conditionno":0,"count":2,"covered":1,"not_covered_false":[1],"not_covered_true":[0],"gcovr/data_sources":["object.gcda"]}],"gcovr/decision":{"type":"conditional","count_true":1,"count_false":1,"gcovr/data_sources":["object.gcda"]},"calls":[{"callno":0,"source_block_id":0,"destination_block_id":1,"returned":1,"gcovr/data_sources":["object.gcda"]}],"gcovr/md5":"0123456789abcdef0123456789abcdef","gcovr/data_sources":["object.gcda"]},{"line_number":2,"count":9,"branches":[],"gcovr/excluded":true},{"line_number":3,"count":4,"branches":[{"count":10,"fallthrough":false,"throw":false,"gcovr/excluded":true},{"count":0,"fallthrough":false,"throw":false}]}],"functions":[{"name":"entry","lineno":1,"execution_count":1,"blocks_percent":100},{"name":"excluded","lineno":2,"execution_count":9,"blocks_percent":100,"gcovr/excluded":true},{"name":"<unknown function>"}]}]}`
}

type chunkReader struct {
	data  []byte
	chunk int
}

func (r *chunkReader) Read(destination []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	maximum := r.chunk
	if maximum > len(destination) {
		maximum = len(destination)
	}
	if maximum > len(r.data) {
		maximum = len(r.data)
	}
	copy(destination, r.data[:maximum])
	r.data = r.data[maximum:]
	return maximum, nil
}
