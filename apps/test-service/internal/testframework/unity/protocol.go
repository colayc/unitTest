package unity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
	"unit-test-ide.local/test-service/internal/unityrunner"
)

const (
	protocolMagic  = "unit-test-ide"
	recordCase     = "case"
	recordFinished = "testFinished"
	maxRecordBytes = 256 * 1024
	maxRecords     = 50_000
)

type controlSource struct {
	Path string
	Line int
}

type controlRecord struct {
	Magic               string
	Protocol            string
	Record              string
	Suite               string
	Case                string
	Identity            string
	Arguments           []string
	Source              controlSource
	Status              string
	DurationNanoseconds int64
	FailureMessage      string
	GeneratorVersion    string
	ManifestSHA256      string
}

type wireSource struct {
	Path *string `json:"path"`
	Line *int    `json:"line"`
}

type wireRecord struct {
	Magic               *string     `json:"magic"`
	Protocol            *string     `json:"protocol"`
	Record              *string     `json:"record"`
	Suite               *string     `json:"suite"`
	Case                *string     `json:"case"`
	Identity            *string     `json:"identity"`
	Arguments           *[]string   `json:"arguments"`
	Source              *wireSource `json:"source"`
	Status              *string     `json:"status"`
	DurationNanoseconds *int64      `json:"durationNanoseconds"`
	FailureMessage      *string     `json:"failureMessage"`
	GeneratorVersion    *string     `json:"generatorVersion"`
	ManifestSHA256      *string     `json:"manifestSha256"`
}

func parseList(
	data []byte,
	manifest unityrunner.Manifest,
) ([]controlRecord, error) {
	records := make([]controlRecord, 0, len(manifest.Cases))
	err := forEachCompleteRecord(data, func(line []byte) error {
		if len(records) >= maxRecords {
			return ErrProtocolLimit
		}
		record, err := decodeControlRecord(line)
		if err != nil {
			return err
		}
		if record.Record != recordCase {
			return fmt.Errorf("%w: list contains %q", ErrInvalidProtocol, record.Record)
		}
		records = append(records, record)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(records) != len(manifest.Cases) {
		return nil, fmt.Errorf(
			"%w: list contains %d cases, manifest contains %d",
			ErrInvalidProtocol,
			len(records),
			len(manifest.Cases),
		)
	}
	for index, record := range records {
		if err := validateRecordBinding(
			record,
			manifest.Cases[index],
			manifest,
		); err != nil {
			return nil, err
		}
	}
	return records, nil
}

func forEachCompleteRecord(
	data []byte,
	consume func([]byte) error,
) error {
	for len(data) > 0 {
		newline := bytes.IndexByte(data, '\n')
		if newline < 0 {
			if len(data) > maxRecordBytes {
				return ErrProtocolLimit
			}
			return nil
		}
		if newline > maxRecordBytes {
			return ErrProtocolLimit
		}
		line := data[:newline]
		data = data[newline+1:]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if len(line) == 0 {
			return fmt.Errorf("%w: empty record", ErrInvalidProtocol)
		}
		if err := consume(line); err != nil {
			return err
		}
	}
	return nil
}

func decodeControlRecord(data []byte) (controlRecord, error) {
	if len(data) == 0 || len(data) > maxRecordBytes || !utf8.Valid(data) {
		if len(data) > maxRecordBytes {
			return controlRecord{}, ErrProtocolLimit
		}
		return controlRecord{}, ErrInvalidProtocol
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return controlRecord{}, errors.Join(ErrInvalidProtocol, err)
	}
	var wire wireRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return controlRecord{}, errors.Join(ErrInvalidProtocol, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return controlRecord{}, errors.Join(ErrInvalidProtocol, err)
	}
	if wire.Magic == nil || wire.Protocol == nil || wire.Record == nil ||
		wire.Suite == nil || wire.Case == nil || wire.Identity == nil ||
		wire.Arguments == nil || wire.Source == nil ||
		wire.Source.Path == nil || wire.Source.Line == nil ||
		wire.GeneratorVersion == nil || wire.ManifestSHA256 == nil {
		return controlRecord{}, fmt.Errorf("%w: required field is missing", ErrInvalidProtocol)
	}
	record := controlRecord{
		Magic:            *wire.Magic,
		Protocol:         *wire.Protocol,
		Record:           *wire.Record,
		Suite:            *wire.Suite,
		Case:             *wire.Case,
		Identity:         *wire.Identity,
		Arguments:        append([]string(nil), (*wire.Arguments)...),
		Source:           controlSource{Path: *wire.Source.Path, Line: *wire.Source.Line},
		GeneratorVersion: *wire.GeneratorVersion,
		ManifestSHA256:   *wire.ManifestSHA256,
	}
	switch record.Record {
	case recordCase:
		if wire.Status != nil || wire.DurationNanoseconds != nil ||
			wire.FailureMessage != nil {
			return controlRecord{}, fmt.Errorf(
				"%w: list record contains run-only fields",
				ErrInvalidProtocol,
			)
		}
	case recordFinished:
		if wire.Status == nil || wire.DurationNanoseconds == nil {
			return controlRecord{}, fmt.Errorf(
				"%w: result fields are missing",
				ErrInvalidProtocol,
			)
		}
		record.Status = *wire.Status
		record.DurationNanoseconds = *wire.DurationNanoseconds
		if wire.FailureMessage != nil {
			record.FailureMessage = *wire.FailureMessage
		}
		if record.DurationNanoseconds < 0 {
			return controlRecord{}, fmt.Errorf(
				"%w: duration is negative",
				ErrInvalidProtocol,
			)
		}
		switch record.Status {
		case string(testframework.CasePassed), string(testframework.CaseSkipped):
			if wire.FailureMessage != nil {
				return controlRecord{}, fmt.Errorf(
					"%w: non-failed result has a failure message",
					ErrInvalidProtocol,
				)
			}
		case string(testframework.CaseFailed):
			if wire.FailureMessage == nil ||
				!validProtocolMessage(record.FailureMessage) {
				return controlRecord{}, fmt.Errorf(
					"%w: failed result has no valid failure message",
					ErrInvalidProtocol,
				)
			}
		default:
			return controlRecord{}, fmt.Errorf(
				"%w: unsupported status %q",
				ErrInvalidProtocol,
				record.Status,
			)
		}
	default:
		return controlRecord{}, fmt.Errorf(
			"%w: unsupported record %q",
			ErrInvalidProtocol,
			record.Record,
		)
	}
	if record.Magic != protocolMagic || record.Protocol != ContractVersion {
		return controlRecord{}, fmt.Errorf(
			"%w: magic or protocol mismatch",
			ErrInvalidProtocol,
		)
	}
	return record, nil
}

func validateRecordBinding(
	record controlRecord,
	testCase unityrunner.TestCase,
	manifest unityrunner.Manifest,
) error {
	if record.Suite != testCase.Location.Path ||
		record.Case != testCase.Name ||
		record.Identity != testCase.Identity ||
		record.Source.Path != testCase.Location.Path ||
		record.Source.Line != testCase.Location.Line ||
		record.GeneratorVersion != manifest.GeneratorVersion ||
		record.ManifestSHA256 != manifest.SHA256 ||
		!equalStrings(record.Arguments, testCase.Arguments) {
		return fmt.Errorf(
			"%w: record %q does not match manifest",
			ErrInvalidProtocol,
			record.Identity,
		)
	}
	return nil
}

func equalStrings(first, second []string) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func validProtocolMessage(value string) bool {
	return value != "" && len(value) <= maxRecordBytes &&
		utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("malformed JSON object")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("malformed JSON array")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

type Parser struct {
	descriptor  ctest.ExecutionDescriptor
	evidence    *manifestEvidence
	expected    testframework.RunItem
	testCase    unityrunner.TestCase
	buffer      []byte
	outputBytes int
	stdout      []byte
	stderr      []byte
	result      *testframework.ParsedCaseResult
	finished    bool
	feedErr     error
}

func newParser(
	input testframework.ParseInput,
	evidence *manifestEvidence,
) (*Parser, error) {
	if evidence == nil || len(input.Items) != 1 {
		return nil, ErrInvalidResult
	}
	item := input.Items[0]
	testCase, ok := findManifestCase(evidence.manifest, item.LogicalName)
	if !ok || !testdomain.ValidID(item.ItemID) ||
		item.ParentLogicalName != testCase.Location.Path ||
		!reflect.DeepEqual(item.Parameters, caseParameters(testCase)) {
		return nil, ErrInvalidResult
	}
	item.Parameters = append([]testdomain.Parameter(nil), item.Parameters...)
	return &Parser{
		descriptor: input.Descriptor,
		evidence:   evidence,
		expected:   item,
		testCase:   testCase,
	}, nil
}

func (parser *Parser) Feed(
	stream testframework.Stream,
	data []byte,
) ([]testframework.ResultEvent, error) {
	if parser.finished {
		return nil, fmt.Errorf("%w: parser already finished", ErrInvalidResult)
	}
	if parser.feedErr != nil {
		return nil, parser.feedErr
	}
	if stream != testframework.StreamStdout &&
		stream != testframework.StreamStderr &&
		stream != testframework.StreamControl {
		return nil, parser.fail(fmt.Errorf("%w: unknown stream", ErrInvalidResult))
	}
	if len(data) > maxControlFileBytes-parser.outputBytes {
		return nil, parser.fail(ErrProtocolLimit)
	}
	parser.outputBytes += len(data)
	if stream == testframework.StreamStdout {
		if len(data) > maxCapturedOutputBytes-len(parser.stdout) {
			return nil, parser.fail(ErrProtocolLimit)
		}
		parser.stdout = append(parser.stdout, data...)
		return nil, nil
	}
	if stream == testframework.StreamStderr {
		if len(data) > maxCapturedOutputBytes-len(parser.stderr) {
			return nil, parser.fail(ErrProtocolLimit)
		}
		parser.stderr = append(parser.stderr, data...)
		return nil, nil
	}
	parser.buffer = append(parser.buffer, data...)
	events := make([]testframework.ResultEvent, 0, 1)
	for {
		newline := bytes.IndexByte(parser.buffer, '\n')
		if newline < 0 {
			if len(parser.buffer) > maxRecordBytes {
				return events, parser.fail(ErrProtocolLimit)
			}
			return events, nil
		}
		if newline > maxRecordBytes {
			return events, parser.fail(ErrProtocolLimit)
		}
		line := parser.buffer[:newline]
		parser.buffer = parser.buffer[newline+1:]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if len(line) == 0 || parser.result != nil {
			return events, parser.fail(ErrInvalidProtocol)
		}
		record, err := decodeControlRecord(line)
		if err != nil {
			return events, parser.fail(err)
		}
		if record.Record != recordFinished ||
			validateRecordBinding(record, parser.testCase, parser.evidence.manifest) != nil {
			return events, parser.fail(ErrInvalidProtocol)
		}
		result := parsedCase(record, parser.expected)
		parser.result = &result
		events = append(events, testframework.ResultEvent{
			Case: cloneParsedCase(result),
		})
	}
}

func (parser *Parser) Finish(
	process testframework.ProcessResult,
) (testframework.ParseResult, error) {
	if parser.finished {
		return testframework.ParseResult{}, fmt.Errorf(
			"%w: parser already finished",
			ErrInvalidResult,
		)
	}
	parser.finished = true
	if parser.feedErr != nil {
		return testframework.ParseResult{}, parser.feedErr
	}
	// An unterminated final JSON fragment was not flushed by the runner and is
	// therefore deliberately discarded.
	parser.buffer = nil
	if err := parser.descriptor.ValidateExecutable(); err != nil {
		return testframework.ParseResult{}, err
	}
	if err := parser.evidence.verify(); err != nil {
		return testframework.ParseResult{}, err
	}
	if !validTermination(process.Termination) {
		return testframework.ParseResult{}, ErrInvalidResult
	}

	diagnostics := make([]testdomain.Diagnostic, 0, 2)
	complete := process.Termination == testframework.ProcessExited
	switch process.Termination {
	case testframework.ProcessTimedOut:
		diagnostics = append(diagnostics, resultDiagnostic(
			"test_timeout",
			"test.unity.timeout",
			"Unity 测试进程运行超时，结果不完整。",
		))
	case testframework.ProcessCrashed:
		diagnostics = append(diagnostics, resultDiagnostic(
			"test_process_crash",
			"test.unity.crash",
			"Unity 测试进程异常终止，结果不完整。",
		))
	case testframework.ProcessCancelled:
		diagnostics = append(diagnostics, resultDiagnostic(
			"cancelled",
			"test.unity.cancelled",
			"Unity 测试运行已取消，结果不完整。",
		))
	}
	if process.Termination == testframework.ProcessExited {
		switch {
		case parser.result == nil:
			complete = false
			diagnostics = append(diagnostics, resultDiagnostic(
				"framework_output_invalid",
				"test.unity.missing_record",
				"Unity runner 未写入完整的 testFinished record。",
			))
		case parser.result.Status == testframework.CaseFailed && process.ExitCode == 0:
			complete = false
			diagnostics = append(diagnostics, resultDiagnostic(
				"inconsistent_exit_status",
				"test.unity.inconsistent_exit",
				"Unity runner 报告失败，但进程退出码为 0。",
			))
		case parser.result.Status != testframework.CaseFailed && process.ExitCode != 0:
			complete = false
			diagnostics = append(diagnostics, resultDiagnostic(
				"inconsistent_exit_status",
				"test.unity.inconsistent_exit",
				fmt.Sprintf(
					"Unity runner 报告 %s，但进程退出码为 %d。",
					parser.result.Status,
					process.ExitCode,
				),
			))
		}
	}
	if parser.result != nil {
		detail, observed, invalid := parser.enrichFromOutput()
		if invalid {
			complete = false
			diagnostics = append(diagnostics, resultDiagnostic(
				"framework_output_invalid",
				"test.unity.output_inconsistent",
				"Unity stdout 与 control-file testFinished record 不一致。",
			))
		} else if observed && detail != nil {
			parser.result.Message = detail.Message
			parser.result.FailureDetails = []testframework.ParsedFailureDetail{
				*detail,
			}
		}
	}

	cases := make([]testframework.ParsedCaseResult, 0, 1)
	if parser.result == nil {
		cases = append(cases, testframework.ParsedCaseResult{
			ItemID:            parser.expected.ItemID,
			ParentLogicalName: parser.expected.ParentLogicalName,
			LogicalName:       parser.expected.LogicalName,
			Status:            testframework.CaseNotRun,
			Partial:           true,
		})
	} else {
		result := cloneParsedCase(*parser.result)
		result.Partial = !complete
		cases = append(cases, result)
	}
	return testframework.ParseResult{
		Cases:       cases,
		Diagnostics: diagnostics,
		Complete:    complete,
	}, nil
}

func (parser *Parser) enrichFromOutput() (
	*testframework.ParsedFailureDetail,
	bool,
	bool,
) {
	records := parseUnityOutput(parser.stdout)
	if len(records) == 0 {
		return nil, false, false
	}
	var matched *unityOutputRecord
	for index := range records {
		record := &records[index]
		if record.identity != parser.expected.LogicalName ||
			!manifestContainsSource(parser.evidence.manifest, record.path) ||
			matched != nil {
			return nil, true, true
		}
		matched = record
	}
	if matched == nil || parser.result == nil ||
		matched.status != parser.result.Status {
		return nil, true, true
	}
	if matched.status != testframework.CaseFailed {
		return nil, true, false
	}
	detail := outputFailureDetail(*matched)
	return &detail, true, false
}

func manifestContainsSource(
	manifest unityrunner.Manifest,
	path string,
) bool {
	for _, source := range manifest.Sources {
		if source == path {
			return true
		}
	}
	return false
}

func (parser *Parser) fail(err error) error {
	parser.feedErr = err
	return err
}

func parsedCase(
	record controlRecord,
	item testframework.RunItem,
) testframework.ParsedCaseResult {
	location := testframework.ParsedSourceLocation{
		Path:       record.Source.Path,
		Line:       record.Source.Line,
		Provenance: "framework-manifest",
	}
	result := testframework.ParsedCaseResult{
		ItemID:            item.ItemID,
		ParentLogicalName: item.ParentLogicalName,
		LogicalName:       item.LogicalName,
		Status:            testframework.CaseStatus(record.Status),
		DurationMS:        record.DurationNanoseconds / 1_000_000,
		SourceLocation:    &location,
		FailureDetails:    []testframework.ParsedFailureDetail{},
	}
	if result.Status == testframework.CaseFailed {
		result.Category = "assertion_failure"
		result.Message = record.FailureMessage
		result.FailureDetails = append(
			result.FailureDetails,
			testframework.ParsedFailureDetail{
				Category:  "assertion_failure",
				Message:   record.FailureMessage,
				Locations: []testframework.ParsedSourceLocation{location},
			},
		)
	}
	return result
}

func cloneParsedCase(
	value testframework.ParsedCaseResult,
) testframework.ParsedCaseResult {
	result := value
	if value.SourceLocation != nil {
		location := *value.SourceLocation
		result.SourceLocation = &location
	}
	result.FailureDetails = make(
		[]testframework.ParsedFailureDetail,
		len(value.FailureDetails),
	)
	for index, detail := range value.FailureDetails {
		result.FailureDetails[index] = detail
		result.FailureDetails[index].Locations = append(
			[]testframework.ParsedSourceLocation(nil),
			detail.Locations...,
		)
	}
	return result
}

func findManifestCase(
	manifest unityrunner.Manifest,
	identity string,
) (unityrunner.TestCase, bool) {
	for _, testCase := range manifest.Cases {
		if testCase.Identity == identity {
			return testCase, true
		}
	}
	return unityrunner.TestCase{}, false
}

func caseParameters(testCase unityrunner.TestCase) []testdomain.Parameter {
	result := make([]testdomain.Parameter, len(testCase.Arguments))
	for index, argument := range testCase.Arguments {
		result[index] = testdomain.Parameter{
			Name:  fmt.Sprintf("argument[%d]", index),
			Value: argument,
		}
	}
	return result
}

func validTermination(value testframework.ProcessTermination) bool {
	switch value {
	case testframework.ProcessExited,
		testframework.ProcessTimedOut,
		testframework.ProcessCrashed,
		testframework.ProcessCancelled:
		return true
	default:
		return false
	}
}

func resultDiagnostic(category, code, message string) testdomain.Diagnostic {
	return testdomain.Diagnostic{
		Severity: "error",
		Category: category,
		Code:     code,
		Message:  message,
	}
}
