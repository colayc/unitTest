package unity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/testframework"
	"unit-test-ide.local/test-service/internal/unityrunner"
)

func TestParserCommitsOnlyNewlineFlushedRecords(t *testing.T) {
	fixture := newAdapterFixture(t)
	parser := parserForCase(t, fixture, 0)
	record := runRecord(
		t,
		fixture.manifest,
		fixture.manifest.Cases[0],
		testframework.CasePassed,
	)
	withoutNewline := record[:len(record)-1]
	events, err := parser.Feed(
		testframework.StreamControl,
		withoutNewline,
	)
	if err != nil || len(events) != 0 {
		t.Fatalf("partial Feed() = %#v, %v", events, err)
	}
	events, err = parser.Feed(testframework.StreamControl, []byte{'\n'})
	if err != nil || len(events) != 1 ||
		events[0].Case.Status != testframework.CasePassed {
		t.Fatalf("flushed Feed() = %#v, %v", events, err)
	}
	result, err := parser.Finish(testframework.ProcessResult{
		ExitCode: 0, Termination: testframework.ProcessExited,
	})
	if err != nil || !result.Complete || len(result.Cases) != 1 ||
		result.Cases[0].Partial ||
		result.Cases[0].SourceLocation == nil ||
		result.Cases[0].SourceLocation.Path != "testdata/basic.c" ||
		result.Cases[0].SourceLocation.Line != 16 {
		t.Fatalf("Finish() = %#v, %v", result, err)
	}
}

func TestParserNormalizesPassedFailedAndSkipped(t *testing.T) {
	tests := []struct {
		name     string
		status   testframework.CaseStatus
		exitCode int
	}{
		{name: "passed", status: testframework.CasePassed, exitCode: 0},
		{name: "failed", status: testframework.CaseFailed, exitCode: 1},
		{name: "skipped", status: testframework.CaseSkipped, exitCode: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAdapterFixture(t)
			parser := parserForCase(t, fixture, 0)
			record := runRecord(
				t,
				fixture.manifest,
				fixture.manifest.Cases[0],
				test.status,
			)
			if test.status == testframework.CaseFailed {
				record = readUnityTestdata(t, "fail.jsonl")
			}
			events, err := parser.Feed(
				testframework.StreamControl,
				record,
			)
			if err != nil || len(events) != 1 {
				t.Fatalf("Feed() = %#v, %v", events, err)
			}
			result, err := parser.Finish(testframework.ProcessResult{
				ExitCode: test.exitCode, Termination: testframework.ProcessExited,
			})
			if err != nil || !result.Complete ||
				len(result.Cases) != 1 ||
				result.Cases[0].Status != test.status ||
				result.Cases[0].DurationMS != 2 {
				t.Fatalf("Finish() = %#v, %v", result, err)
			}
			if test.status == testframework.CaseFailed {
				if result.Cases[0].Category != "assertion_failure" ||
					result.Cases[0].Message == "" ||
					len(result.Cases[0].FailureDetails) != 1 {
					t.Fatalf("failure result = %#v", result.Cases[0])
				}
			}
		})
	}
}

func TestParserRejectsExitStatusInconsistency(t *testing.T) {
	tests := []struct {
		name     string
		status   testframework.CaseStatus
		exitCode int
	}{
		{name: "failed with zero", status: testframework.CaseFailed, exitCode: 0},
		{name: "passed with nonzero", status: testframework.CasePassed, exitCode: 1},
		{name: "skipped with nonzero", status: testframework.CaseSkipped, exitCode: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAdapterFixture(t)
			parser := parserForCase(t, fixture, 0)
			if _, err := parser.Feed(
				testframework.StreamControl,
				runRecord(
					t,
					fixture.manifest,
					fixture.manifest.Cases[0],
					test.status,
				),
			); err != nil {
				t.Fatal(err)
			}
			result, err := parser.Finish(testframework.ProcessResult{
				ExitCode: test.exitCode, Termination: testframework.ProcessExited,
			})
			if err != nil || result.Complete ||
				len(result.Diagnostics) != 1 ||
				result.Diagnostics[0].Category != "inconsistent_exit_status" ||
				!result.Cases[0].Partial {
				t.Fatalf("Finish() = %#v, %v", result, err)
			}
		})
	}
}

func TestParserDiscardsPartialFinalJSONAndKeepsFlushedCrashEvidence(t *testing.T) {
	t.Run("only partial record", func(t *testing.T) {
		fixture := newAdapterFixture(t)
		parser := parserForCase(t, fixture, 0)
		partial := []byte(`{"magic":"unit-test-ide","protocol":"utide.runner.v1"`)
		if events, err := parser.Feed(
			testframework.StreamControl,
			partial,
		); err != nil || len(events) != 0 {
			t.Fatalf("Feed() = %#v, %v", events, err)
		}
		result, err := parser.Finish(testframework.ProcessResult{
			ExitCode: -1, Termination: testframework.ProcessCrashed,
		})
		if err != nil || result.Complete ||
			result.Cases[0].Status != testframework.CaseNotRun ||
			!result.Cases[0].Partial {
			t.Fatalf("Finish() = %#v, %v", result, err)
		}
	})

	t.Run("flushed record before crash", func(t *testing.T) {
		fixture := newAdapterFixture(t)
		parser := parserForCase(t, fixture, 0)
		events, err := parser.Feed(
			testframework.StreamControl,
			bytes.TrimSuffix(
				readUnityTestdata(t, "crash-partial.jsonl"),
				[]byte{'\n'},
			),
		)
		if err != nil || len(events) != 1 {
			t.Fatalf("Feed() = %#v, %v", events, err)
		}
		result, err := parser.Finish(testframework.ProcessResult{
			ExitCode: -1, Termination: testframework.ProcessCrashed,
		})
		if err != nil || result.Complete ||
			result.Cases[0].Status != testframework.CasePassed ||
			!result.Cases[0].Partial ||
			result.Diagnostics[0].Category != "test_process_crash" {
			t.Fatalf("Finish() = %#v, %v", result, err)
		}
	})
}

func TestParserRejectsMalformedMagicVersionIdentityAndRecordLimit(t *testing.T) {
	fixture := newAdapterFixture(t)
	valid := runRecord(
		t,
		fixture.manifest,
		fixture.manifest.Cases[0],
		testframework.CasePassed,
	)
	tests := map[string][]byte{
		"malformed fixture": readUnityTestdata(t, "malformed.jsonl"),
		"magic": []byte(strings.Replace(
			string(valid), `"magic":"unit-test-ide"`, `"magic":"other"`, 1,
		)),
		"identity": []byte(strings.Replace(
			string(valid),
			`"identity":"test_adds_numbers"`,
			`"identity":"test_handles_zero"`,
			1,
		)),
		"manifest fingerprint": []byte(strings.Replace(
			string(valid),
			fixture.manifest.SHA256,
			strings.Repeat("0", 64),
			1,
		)),
		"runner fingerprint": []byte(strings.Replace(
			string(valid),
			`"generatorVersion":"1.0.0"`,
			`"generatorVersion":"2.0.0"`,
			1,
		)),
		"duplicate key": []byte(strings.Replace(
			string(valid),
			`{"magic":"unit-test-ide"`,
			`{"magic":"unit-test-ide","magic":"unit-test-ide"`,
			1,
		)),
		"overlong record": append(
			[]byte(strings.Repeat("x", maxRecordBytes+1)),
			'\n',
		),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			parser := parserForCase(t, fixture, 0)
			if _, err := parser.Feed(
				testframework.StreamControl,
				data,
			); err == nil {
				t.Fatal("Feed() error = nil")
			}
		})
	}
}

func TestParserUsesOnlyControlStreamForStatus(t *testing.T) {
	fixture := newAdapterFixture(t)
	parser := parserForCase(t, fixture, 0)
	valid := runRecord(
		t,
		fixture.manifest,
		fixture.manifest.Cases[0],
		testframework.CasePassed,
	)
	if events, err := parser.Feed(
		testframework.StreamStdout,
		append([]byte("Unity log\n"), valid...),
	); err != nil || len(events) != 0 {
		t.Fatalf("stdout Feed() = %#v, %v", events, err)
	}
	if events, err := parser.Feed(
		testframework.StreamControl,
		valid,
	); err != nil || len(events) != 1 {
		t.Fatalf("control Feed() = %#v, %v", events, err)
	}
}

func TestParserReverifiesExecutableAndManifestAtFinish(t *testing.T) {
	tests := map[string]func(t *testing.T, fixture adapterFixture){
		"executable": func(t *testing.T, fixture adapterFixture) {
			t.Helper()
			if err := os.WriteFile(
				fixture.descriptor.Executable.Path,
				[]byte("replacement executable"),
				0o700,
			); err != nil {
				t.Fatal(err)
			}
		},
		"manifest": func(t *testing.T, fixture adapterFixture) {
			t.Helper()
			if err := os.WriteFile(
				fixture.manifestPath,
				append(fixture.manifestJSON, ' '),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newAdapterFixture(t)
			parser := parserForCase(t, fixture, 0)
			if _, err := parser.Feed(
				testframework.StreamControl,
				runRecord(
					t,
					fixture.manifest,
					fixture.manifest.Cases[0],
					testframework.CasePassed,
				),
			); err != nil {
				t.Fatal(err)
			}
			mutate(t, fixture)
			if _, err := parser.Finish(testframework.ProcessResult{
				ExitCode: 0, Termination: testframework.ProcessExited,
			}); err == nil {
				t.Fatal("Finish() error = nil")
			}
		})
	}
}

func parserForCase(
	t *testing.T,
	fixture adapterFixture,
	index int,
) testframework.ResultParser {
	t.Helper()
	adapter, err := NewAdapter(
		&fakeRunner{},
		&fakeControlAllocator{root: fixture.controlDir},
	)
	if err != nil {
		t.Fatal(err)
	}
	items := fixture.runItems()
	parser, err := adapter.NewParser(testframework.ParseInput{
		Descriptor: fixture.descriptor,
		Items:      items[index : index+1],
	})
	if err != nil {
		t.Fatal(err)
	}
	return parser
}

func runRecord(
	t *testing.T,
	manifest unityrunner.Manifest,
	testCase unityrunner.TestCase,
	status testframework.CaseStatus,
) []byte {
	t.Helper()
	type source struct {
		Path string `json:"path"`
		Line int    `json:"line"`
	}
	record := struct {
		Magic               string   `json:"magic"`
		Protocol            string   `json:"protocol"`
		Record              string   `json:"record"`
		Suite               string   `json:"suite"`
		Case                string   `json:"case"`
		Identity            string   `json:"identity"`
		Arguments           []string `json:"arguments"`
		Source              source   `json:"source"`
		Status              string   `json:"status"`
		DurationNanoseconds int64    `json:"durationNanoseconds"`
		FailureMessage      string   `json:"failureMessage,omitempty"`
		GeneratorVersion    string   `json:"generatorVersion"`
		ManifestSHA256      string   `json:"manifestSha256"`
	}{
		Magic:               protocolMagic,
		Protocol:            ContractVersion,
		Record:              recordFinished,
		Suite:               testCase.Location.Path,
		Case:                testCase.Name,
		Identity:            testCase.Identity,
		Arguments:           append([]string{}, testCase.Arguments...),
		Source:              source(testCase.Location),
		Status:              string(status),
		DurationNanoseconds: 2_500_000,
		GeneratorVersion:    manifest.GeneratorVersion,
		ManifestSHA256:      manifest.SHA256,
	}
	if status == testframework.CaseFailed {
		record.FailureMessage = "Unity assertion failed; see stdout/stderr"
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return append(encoded, '\n')
}

func TestListParserRejectsPartialAndMalformedCompleteRecords(t *testing.T) {
	fixture := newAdapterFixture(t)
	list := readUnityTestdata(t, "list.jsonl")
	if records, err := parseList(list, fixture.manifest); err != nil ||
		len(records) != 2 {
		t.Fatalf("parseList() = %#v, %v", records, err)
	}
	partial := append(
		append([]byte(nil), list...),
		[]byte(`{"magic":"unit-test-ide"`)...,
	)
	if records, err := parseList(partial, fixture.manifest); err != nil ||
		len(records) != 2 {
		t.Fatalf("parseList(partial) = %#v, %v", records, err)
	}
	malformed := append(
		append([]byte(nil), list...),
		[]byte("{bad}\n")...,
	)
	if _, err := parseList(malformed, fixture.manifest); err == nil {
		t.Fatal("parseList(malformed) error = nil")
	}
}

func TestNewParserRequiresOneExactManifestItem(t *testing.T) {
	fixture := newAdapterFixture(t)
	adapter, err := NewAdapter(
		&fakeRunner{},
		&fakeControlAllocator{root: fixture.controlDir},
	)
	if err != nil {
		t.Fatal(err)
	}
	items := fixture.runItems()
	for name, selected := range map[string][]testframework.RunItem{
		"none":     nil,
		"multiple": items,
		"wrong identity": {{
			ItemID:            items[0].ItemID,
			ParentLogicalName: items[0].ParentLogicalName,
			LogicalName:       "client-identity",
		}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := adapter.NewParser(testframework.ParseInput{
				Descriptor: fixture.descriptor,
				Items:      selected,
			}); !errors.Is(err, ErrInvalidResult) {
				t.Fatalf("NewParser() error = %v", err)
			}
		})
	}
}

func TestAdapterConstructorRejectsTypedNilDependencies(t *testing.T) {
	var runner *fakeRunner
	var allocator *fakeControlAllocator
	if _, err := NewAdapter(runner, &fakeControlAllocator{}); !errors.Is(
		err,
		ErrInvalidAdapter,
	) {
		t.Fatalf("nil runner error = %v", err)
	}
	if _, err := NewAdapter(&fakeRunner{}, allocator); !errors.Is(
		err,
		ErrInvalidAdapter,
	) {
		t.Fatalf("nil allocator error = %v", err)
	}
}

func TestControlCapabilityReadHonorsContext(t *testing.T) {
	contextValue, cancel := context.WithCancel(context.Background())
	cancel()
	file := &fakeControlFile{path: "unused"}
	if _, err := file.Read(contextValue, 1); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("Read() error = %v", err)
	}
}
