package gcovr

import (
	"bytes"
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
	for _, name := range []string{"simple.json", "branches.json", "functions.json", "empty.json"} {
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
