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
		"unsupported major":  []byte(strings.Replace(valid, `"2.0.1"`, `"4.0.0"`, 1)),
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

func TestParseLLVMGlobalOutputLineLimitStopsCrossFileExpansion(t *testing.T) {
	encoded := []byte(`{"version":"2.0.1","type":"llvm.coverage.json.export","data":[{"files":[{"filename":"C:\\workspace\\src\\one.cpp","segments":[[1,1,1,true,true,false],[5,1,0,false,false,false]],"branches":[],"mcdc_records":[],"expansions":[],"summary":{"lines":{"count":4,"covered":4,"percent":100},"functions":{"count":0,"covered":0,"percent":100},"instantiations":{"count":0,"covered":0,"percent":100},"regions":{"count":0,"covered":0,"notcovered":0,"percent":100},"branches":{"count":0,"covered":0,"notcovered":0,"percent":100},"mcdc":{"count":0,"covered":0,"notcovered":0,"percent":100}}},{"filename":"C:\\workspace\\src\\two.cpp","segments":[[1,1,1,true,true,false],[5,1,0,false,false,false]],"branches":[],"mcdc_records":[],"expansions":[],"summary":{"lines":{"count":4,"covered":4,"percent":100},"functions":{"count":0,"covered":0,"percent":100},"instantiations":{"count":0,"covered":0,"percent":100},"regions":{"count":0,"covered":0,"notcovered":0,"percent":100},"branches":{"count":0,"covered":0,"notcovered":0,"percent":100},"mcdc":{"count":0,"covered":0,"notcovered":0,"percent":100}}}],"functions":[],"totals":{"lines":{"count":8,"covered":8,"percent":100},"functions":{"count":0,"covered":0,"percent":100},"instantiations":{"count":0,"covered":0,"percent":100},"regions":{"count":0,"covered":0,"notcovered":0,"percent":100},"branches":{"count":0,"covered":0,"notcovered":0,"percent":100},"mcdc":{"count":0,"covered":0,"notcovered":0,"percent":100}}}]}`)
	limits := DefaultLimits()
	limits.MaxLines = 4
	got, err := Parse(bytes.NewReader(encoded), limits)
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("Parse() error = %v, want ErrLimitExceeded", err)
	}
	if !reflect.DeepEqual(got, Export{}) {
		t.Fatalf("Parse() returned partial export %#v", got)
	}
}

func TestParseLLVMRejectsBranchWithoutExecutableLineAndReturnsNoPartialEvidence(t *testing.T) {
	encoded := bytes.Replace(
		readFixture(t, "branches.json"),
		[]byte(`"segments":[[4,1,7,true,true,false],[5,1,0,false,false,false]]`),
		[]byte(`"segments":[[5,1,7,true,true,false],[6,1,0,false,false,false]]`),
		1,
	)
	got, err := Parse(bytes.NewReader(encoded), DefaultLimits())
	if err == nil {
		t.Fatalf("Parse() = %#v, want inconsistent branch error", got)
	}
	if !reflect.DeepEqual(got, Export{}) {
		t.Fatalf("Parse() returned partial uncovered evidence %#v", got)
	}
}

func TestParseLLVMRejectsSemanticallyInvalidTuplesAndSummaries(t *testing.T) {
	simple := readFixture(t, "simple.json")
	branches := readFixture(t, "branches.json")
	replace := func(input []byte, old, replacement string) []byte {
		t.Helper()
		result := bytes.Replace(input, []byte(old), []byte(replacement), 1)
		if bytes.Equal(result, input) {
			t.Fatalf("fixture does not contain %q", old)
		}
		return result
	}
	tests := map[string][]byte{
		"reversed region": replace(simple,
			`[2,1,3,2,5,0,0,0]`, `[3,2,2,1,5,0,0,0]`),
		"unsupported region kind": replace(simple,
			`[2,1,3,2,5,0,0,0]`, `[2,1,3,2,5,0,0,7]`),
		"function without code region": replace(simple,
			`[2,1,3,2,5,0,0,0]`, `[2,1,3,2,5,0,0,1]`),
		"reversed branch": replace(branches,
			`[4,3,4,8,1,0,0,0,4]`, `[4,8,4,3,1,0,0,0,4]`),
		"unsupported branch kind": replace(branches,
			`[4,3,4,8,1,0,0,0,4]`, `[4,3,4,8,1,0,0,0,0]`),
		"reversed mcdc location": replace(branches,
			`[4,3,4,15,0,5,[true,false]]`, `[4,15,4,3,0,5,[true,false]]`),
		"unsupported mcdc kind": replace(branches,
			`[4,3,4,15,0,5,[true,false]]`, `[4,3,4,15,0,6,[true,false]]`),
		"covered exceeds count": replace(simple,
			`"lines":{"count":2,"covered":2`, `"lines":{"count":2,"covered":3`),
		"notcovered inconsistent": replace(simple,
			`"regions":{"count":2,"covered":2,"notcovered":0`, `"regions":{"count":2,"covered":2,"notcovered":1`),
	}
	for name, encoded := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := Parse(bytes.NewReader(encoded), DefaultLimits())
			if err == nil {
				t.Fatalf("Parse() = %#v, want semantic validation error", got)
			}
			if !reflect.DeepEqual(got, Export{}) {
				t.Fatalf("Parse() returned partial export %#v", got)
			}
		})
	}
}

func TestParseLLVMAllowsZeroLengthFunctionRegions(t *testing.T) {
	simple := readFixture(t, "simple.json")
	encoded := bytes.Replace(
		simple,
		[]byte(`[2,1,3,2,5,0,0,0]`),
		[]byte(`[2,1,2,1,5,0,0,0]`),
		1,
	)
	got, err := Parse(bytes.NewReader(encoded), DefaultLimits())
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(got.Files) == 0 {
		t.Fatal("Parse() returned no files")
	}
}

func TestParseLLVMAllowsVersionThree(t *testing.T) {
	encoded := bytes.Replace(readFixture(t, "simple.json"), []byte(`"2.0.1"`), []byte(`"3.1.0"`), 1)
	if _, err := Parse(bytes.NewReader(encoded), DefaultLimits()); err != nil {
		t.Fatalf("Parse() error = %v", err)
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
