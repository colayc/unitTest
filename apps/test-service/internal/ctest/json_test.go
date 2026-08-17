package ctest

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseShowOnlyJSONGoldenFiles(t *testing.T) {
	t.Run("linux", func(t *testing.T) {
		snapshot := parseGolden(t, "show-only-linux.json")
		if snapshot.Kind != "ctestInfo" || snapshot.Version != (Version{Major: 1, Minor: 0}) {
			t.Fatalf("snapshot header = %#v", snapshot)
		}
		if len(snapshot.Tests) != 1 || snapshot.Tests[0].Name != "core.math[fast]" {
			t.Fatalf("tests = %#v", snapshot.Tests)
		}
		test := snapshot.Tests[0]
		if !reflect.DeepEqual(test.Command, []string{
			"/workspace/build/bin/core tests", "--mode", "fast",
		}) {
			t.Fatalf("command = %#v", test.Command)
		}
		if len(test.Properties) != 4 ||
			test.Properties[0].Name != "LABELS" ||
			test.Properties[0].Value.Kind != PropertyStrings ||
			!reflect.DeepEqual(test.Properties[0].Value.Strings, []string{"fast", "unicode-数学"}) ||
			test.Properties[1].Value.Kind != PropertyNumber ||
			test.Properties[1].Value.Number != "30" ||
			test.Properties[2].Value.Kind != PropertyBoolean ||
			test.Properties[2].Value.Boolean ||
			test.Properties[3].Value.Kind != PropertyString {
			t.Fatalf("properties = %#v", test.Properties)
		}
		if len(snapshot.BacktraceGraph.Nodes) != 2 ||
			snapshot.BacktraceGraph.Nodes[0].Parent == nil ||
			*snapshot.BacktraceGraph.Nodes[0].Parent != 1 {
			t.Fatalf("backtrace graph = %#v", snapshot.BacktraceGraph)
		}
	})

	t.Run("windows crlf", func(t *testing.T) {
		encoded := readGolden(t, "show-only-windows.json")
		encoded = []byte(strings.ReplaceAll(string(encoded), "\n", "\r\n"))
		snapshot, err := ParseShowOnlyJSON(encoded, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		test := snapshot.Tests[0]
		if test.Name != "windows unicode 测试" ||
			test.Command[0] != `C:\work space\构建\Debug\runner.exe` ||
			test.Properties[0].Value.String != `C:\work space\构建` ||
			snapshot.BacktraceGraph.Files[0] != `C:\work space\源码\CMakeLists.txt` {
			t.Fatalf("Windows snapshot = %#v", snapshot)
		}
	})

	t.Run("multi config", func(t *testing.T) {
		snapshot := parseGolden(t, "show-only-multiconfig.json")
		if len(snapshot.Tests) != 1 ||
			snapshot.Tests[0].Config != "Debug" ||
			snapshot.Tests[0].Command[0] != "/workspace/build/Debug/multi-runner" {
			t.Fatalf("multi-config snapshot = %#v", snapshot)
		}
	})
}

func TestParseShowOnlyJSONIgnoresUnknownMinorFields(t *testing.T) {
	encoded := `{
		"kind":"ctestInfo",
		"version":{"major":1,"minor":7,"futureVersionField":true},
		"backtraceGraph":{
			"commands":["add_test"],
			"files":["CMakeLists.txt"],
			"nodes":[{"file":0,"line":1,"command":0,"futureNodeField":[1,{"nested":true}]}],
			"futureGraphField":{"ignored":"yes"}
		},
		"tests":[{
			"name":"future-compatible",
			"command":["runner"],
			"backtrace":0,
			"properties":[{"name":"LABELS","value":["fast"],"futurePropertyField":1}],
			"futureTestField":{"ignored":true}
		}],
		"futureRootField":["ignored"]
	}`
	snapshot, err := ParseShowOnlyJSON([]byte(encoded), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version.Minor != 7 || snapshot.Tests[0].Name != "future-compatible" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestParseShowOnlyJSONRejectsInvalidDocuments(t *testing.T) {
	valid := `{
		"kind":"ctestInfo",
		"version":{"major":1,"minor":0},
		"backtraceGraph":{
			"commands":["add_test"],
			"files":["CMakeLists.txt"],
			"nodes":[{"file":0,"line":1,"command":0}]
		},
		"tests":[{
			"name":"case",
			"command":["runner","--flag"],
			"backtrace":0,
			"properties":[
				{"name":"LABELS","value":["fast"]},
				{"name":"TIMEOUT","value":10},
				{"name":"DISABLED","value":false},
				{"name":"WORKING_DIRECTORY","value":"build"}
			]
		}]
	}`
	cases := map[string]string{
		"malformed JSON":       `{"kind":`,
		"multiple JSON values": valid + `{}`,
		"invalid UTF-8": strings.Replace(
			valid,
			`"name":"case"`,
			`"name":"ca`+string([]byte{0xff})+`se"`,
			1,
		),
		"wrong kind":        strings.Replace(valid, `"ctestInfo"`, `"other"`, 1),
		"unsupported major": strings.Replace(valid, `"major":1`, `"major":2`, 1),
		"negative minor":    strings.Replace(valid, `"minor":0`, `"minor":-1`, 1),
		"unsafe integer":    strings.Replace(valid, `"minor":0`, `"minor":9007199254740992`, 1),
		"duplicate logical name": strings.Replace(valid, `
		}]
	}`, `,
			{"name":"case","command":["other"],"backtrace":0}
		]
	}`, 1),
		"empty logical name":         strings.Replace(valid, `"name":"case"`, `"name":""`, 1),
		"logical name NUL":           strings.Replace(valid, `"name":"case"`, `"name":"ca\u0000se"`, 1),
		"missing command":            strings.Replace(valid, `"command":["runner","--flag"],`, ``, 1),
		"empty command":              strings.Replace(valid, `"command":["runner","--flag"]`, `"command":[]`, 1),
		"empty executable":           strings.Replace(valid, `"runner","--flag"`, `"","--flag"`, 1),
		"command NUL":                strings.Replace(valid, `"runner","--flag"`, `"run\u0000ner","--flag"`, 1),
		"missing backtrace":          strings.Replace(valid, `"backtrace":0,`, ``, 1),
		"broken test backtrace":      strings.Replace(valid, `"backtrace":0`, `"backtrace":7`, 1),
		"broken node file":           strings.Replace(valid, `"file":0`, `"file":7`, 1),
		"broken node command":        strings.Replace(valid, `"command":0`, `"command":7`, 1),
		"broken node parent":         strings.Replace(valid, `"line":1,"command":0`, `"line":1,"command":0,"parent":7`, 1),
		"zero line":                  strings.Replace(valid, `"line":1`, `"line":0`, 1),
		"backtrace cycle":            strings.Replace(valid, `"line":1,"command":0`, `"line":1,"command":0,"parent":0`, 1),
		"empty property name":        strings.Replace(valid, `"name":"LABELS"`, `"name":""`, 1),
		"invalid property object":    strings.Replace(valid, `"value":["fast"]`, `"value":{"label":"fast"}`, 1),
		"mixed property array":       strings.Replace(valid, `"value":["fast"]`, `"value":["fast",1]`, 1),
		"unsafe property integer":    strings.Replace(valid, `"value":10`, `"value":9007199254740992`, 1),
		"property string NUL":        strings.Replace(valid, `"value":"build"`, `"value":"bu\u0000ild"`, 1),
		"backtrace graph file NUL":   strings.Replace(valid, `"CMakeLists.txt"`, `"CMake\u0000Lists.txt"`, 1),
		"missing graph":              strings.Replace(valid, `"backtraceGraph":{`, `"ignoredGraph":{`, 1),
		"missing tests":              strings.Replace(valid, `"tests":[`, `"ignoredTests":[`, 1),
		"malformed Golden backtrace": string(readGolden(t, "show-only-malformed.json")),
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseShowOnlyJSON([]byte(encoded), DefaultLimits()); !errors.Is(err, ErrInvalidSnapshot) {
				t.Fatalf("ParseShowOnlyJSON() error = %v, want ErrInvalidSnapshot", err)
			}
		})
	}
}

func TestParseShowOnlyJSONEnforcesEveryLimit(t *testing.T) {
	valid := `{
		"kind":"ctestInfo",
		"version":{"major":1,"minor":0},
		"backtraceGraph":{
			"commands":["add_test"],
			"files":["CMakeLists.txt"],
			"nodes":[{"file":0,"command":0}]
		},
		"tests":[{
			"name":"case",
			"command":["runner","--flag"],
			"backtrace":0,
			"properties":[{"name":"LABELS","value":["fast","unit"]}]
		}]
	}`
	defaults := DefaultLimits()
	cases := map[string]Limits{
		"document bytes":        withLimit(defaults, func(value *Limits) { value.MaxDocumentBytes = len(valid) - 1 }),
		"tests":                 withLimit(defaults, func(value *Limits) { value.MaxTests = 0 }),
		"command arguments":     withLimit(defaults, func(value *Limits) { value.MaxCommandArguments = 1 }),
		"properties":            withLimit(defaults, func(value *Limits) { value.MaxPropertiesPerTest = 0 }),
		"backtrace commands":    withLimit(defaults, func(value *Limits) { value.MaxBacktraceCommands = 0 }),
		"backtrace files":       withLimit(defaults, func(value *Limits) { value.MaxBacktraceFiles = 0 }),
		"backtrace nodes":       withLimit(defaults, func(value *Limits) { value.MaxBacktraceNodes = 0 }),
		"property string array": withLimit(defaults, func(value *Limits) { value.MaxPropertyStrings = 1 }),
		"string bytes":          withLimit(defaults, func(value *Limits) { value.MaxStringBytes = 3 }),
	}
	for name, limits := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseShowOnlyJSON([]byte(valid), limits); !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("ParseShowOnlyJSON() error = %v, want ErrLimitExceeded", err)
			}
		})
	}
}

func FuzzParseShowOnlyJSON(f *testing.F) {
	for _, name := range []string{
		"show-only-linux.json",
		"show-only-windows.json",
		"show-only-multiconfig.json",
		"show-only-malformed.json",
	} {
		encoded, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(encoded)
	}
	f.Fuzz(func(t *testing.T, encoded []byte) {
		_, _ = ParseShowOnlyJSON(encoded, DefaultLimits())
	})
}

func parseGolden(t *testing.T, name string) Snapshot {
	t.Helper()
	snapshot, err := ParseShowOnlyJSON(readGolden(t, name), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	encoded, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func withLimit(value Limits, change func(*Limits)) Limits {
	change(&value)
	return value
}
