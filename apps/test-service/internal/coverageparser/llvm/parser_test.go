package llvm

import (
	"bytes"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseLLVMExportAcrossChunks(t *testing.T) {
	encoded := readFixture(t, "simple.json")
	want := Export{
		Version: "2.0.1",
		Files: []File{{
			NativePath: `C:\workspace\src\simple.cpp`,
			Functions:  Metric{Covered: 1, Total: 1},
			Lines: []Line{
				{Number: 2, Count: 5},
				{Number: 3, Count: 5},
			},
		}},
	}
	for chunk := 1; chunk <= 257; chunk++ {
		got, err := Parse(&chunkReader{data: encoded, chunk: chunk}, DefaultLimits())
		if err != nil {
			t.Fatalf("chunk %d: Parse() error = %v", chunk, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("chunk %d: Parse() = %#v, want %#v", chunk, got, want)
		}
	}
}

func TestParseLLVMBranchesAndFunctionsDeduplicateSemanticIdentities(t *testing.T) {
	got, err := Parse(bytes.NewReader(readFixture(t, "branches.json")), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	want := Export{Version: "2.0.1", Files: []File{{
		NativePath: `C:\workspace\src\branch.cpp`,
		Functions:  Metric{Covered: 1, Total: 1},
		Lines: []Line{{
			Number: 4, Count: 7, Branches: Metric{Covered: 2, Total: 4},
		}},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse() = %#v, want %#v", got, want)
	}
}

func TestParseLLVMKeepsWindowsPathInternal(t *testing.T) {
	got, err := Parse(bytes.NewReader(readFixture(t, "windows-path.json")), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 1 || got.Files[0].NativePath != `C:\Users\Example\Project\src\windows.cpp` {
		t.Fatalf("files = %#v", got.Files)
	}
}

func TestParseLLVMRejectsMalformedDuplicateUnknownAndUnsafeValues(t *testing.T) {
	valid := string(readFixture(t, "simple.json"))
	cases := map[string][]byte{
		"malformed":          readFixture(t, "malformed.json"),
		"duplicate root":     []byte(strings.Replace(valid, `"version":"2.0.1"`, `"version":"2.0.1","version":"2.0.1"`, 1)),
		"unknown root":       []byte(strings.Replace(valid, `"type":`, `"unknown":0,"type":`, 1)),
		"unknown file":       []byte(strings.Replace(valid, `"filename":`, `"unknown":0,"filename":`, 1)),
		"unknown summary":    []byte(strings.Replace(valid, `"lines":{`, `"unknown":0,"lines":{`, 1)),
		"unsupported major":  []byte(strings.Replace(valid, `"2.0.1"`, `"3.0.0"`, 1)),
		"wrong type":         []byte(strings.Replace(valid, `"llvm.coverage.json.export"`, `"other"`, 1)),
		"negative integer":   []byte(strings.Replace(valid, `[2,1,3,true`, `[2,1,-1,true`, 1)),
		"floating integer":   []byte(strings.Replace(valid, `[2,1,3,true`, `[2,1,3.5,true`, 1)),
		"unsafe integer":     []byte(strings.Replace(valid, `[2,1,3,true`, `[2,1,9007199254740992,true`, 1)),
		"wrong tuple length": []byte(strings.Replace(valid, `[2,1,3,true,true,false]`, `[2,1,3,true,true]`, 1)),
		"trailing value":     append(append([]byte(nil), []byte(valid)...), []byte(` {}`)...),
		"missing root field": []byte(strings.Replace(valid, `"type":"llvm.coverage.json.export",`, ``, 1)),
		"invalid UTF-8":      bytes.Replace([]byte(valid), []byte("simple.cpp"), []byte{'s', 'i', 'm', 'p', 'l', 'e', '.', 0xff}, 1),
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Parse(bytes.NewReader(encoded), DefaultLimits())
			if err == nil {
				t.Fatalf("Parse() = %#v, want error", got)
			}
			if !reflect.DeepEqual(got, Export{}) {
				t.Fatalf("Parse() returned partial export %#v", got)
			}
		})
	}
}

func TestParseLLVMLimitsFailClosed(t *testing.T) {
	simple := readFixture(t, "simple.json")
	branches := readFixture(t, "branches.json")
	twoFiles := bytes.Replace(simple, []byte(`}] ,"functions"`), []byte(`}] ,"functions"`), 1)
	// Clone the sole file at the JSON boundary without constructing parser output.
	fileStart := bytes.Index(simple, []byte(`{"filename"`))
	fileEnd := bytes.Index(simple[fileStart:], []byte(`}],"functions"`))
	if fileStart < 0 || fileEnd < 0 {
		t.Fatal("fixture boundary missing")
	}
	fileEnd += fileStart + 1
	twoFiles = append(append(append([]byte(nil), simple[:fileEnd]...), ','), simple[fileStart:]...)

	cases := []struct {
		name   string
		input  []byte
		mutate func(*Limits)
	}{
		{name: "input", input: simple, mutate: func(v *Limits) { v.MaxInputBytes = 1 }},
		{name: "depth", input: simple, mutate: func(v *Limits) { v.MaxDepth = 2 }},
		{name: "files", input: twoFiles, mutate: func(v *Limits) { v.MaxFiles = 1 }},
		{name: "functions", input: branches, mutate: func(v *Limits) { v.MaxFunctions = 1 }},
		{name: "lines", input: simple, mutate: func(v *Limits) { v.MaxLines = 1 }},
		{name: "branches", input: branches, mutate: func(v *Limits) { v.MaxBranches = 1 }},
		{name: "string", input: simple, mutate: func(v *Limits) { v.MaxStringBytes = 8 }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			limits := DefaultLimits()
			test.mutate(&limits)
			got, err := Parse(bytes.NewReader(test.input), limits)
			if !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("error = %v, want ErrLimitExceeded", err)
			}
			if !reflect.DeepEqual(got, Export{}) {
				t.Fatalf("Parse() returned partial export %#v", got)
			}
		})
	}

	invalid := DefaultLimits()
	invalid.MaxDepth = 0
	if _, err := Parse(bytes.NewReader(simple), invalid); !errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("invalid limits error = %v", err)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	encoded, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
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
