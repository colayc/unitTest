package cpputest

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

type ResultLimits struct {
	MaxOutputBytes  int
	MaxLineBytes    int
	MaxMessageBytes int
	MaxCases        int
}

func DefaultResultLimits() ResultLimits {
	return ResultLimits{
		MaxOutputBytes:  16 * 1024 * 1024,
		MaxLineBytes:    256 * 1024,
		MaxMessageBytes: 1024 * 1024,
		MaxCases:        100_000,
	}
}

func (limits ResultLimits) valid() bool {
	return limits.MaxOutputBytes > 0 &&
		limits.MaxLineBytes > 0 &&
		limits.MaxMessageBytes > 0 &&
		limits.MaxCases > 0
}

type activeRecord struct {
	identity       grammarCase
	hadFailure     bool
	failureRecords int
	messageLines   []string
	messageBytes   int
	source         *testframework.ParsedSourceLocation
}

type Parser struct {
	limits        ResultLimits
	expected      map[string]testframework.RunItem
	expectedOrder []testframework.RunItem
	seen          map[string]struct{}
	stdout        []byte
	stderr        []byte
	outputBytes   int
	active        *activeRecord
	summary       *grammarSummary
	cases         []testframework.ParsedCaseResult
	ordinary      int
	ignored       int
	failures      int
	invalid       bool
	finished      bool
	feedErr       error
}

func NewParser(input testframework.ParseInput, limits ResultLimits) (*Parser, error) {
	if !limits.valid() || len(input.Items) == 0 || len(input.Items) > limits.MaxCases {
		return nil, ErrInvalidResult
	}
	parser := &Parser{
		limits:        limits,
		expected:      make(map[string]testframework.RunItem, len(input.Items)),
		expectedOrder: append([]testframework.RunItem(nil), input.Items...),
		seen:          make(map[string]struct{}, len(input.Items)),
		cases:         make([]testframework.ParsedCaseResult, 0, len(input.Items)),
	}
	for _, item := range input.Items {
		if !testdomain.ValidID(item.ItemID) ||
			!validResultIdentity(item.ParentLogicalName) ||
			!validResultIdentity(item.LogicalName) {
			return nil, ErrInvalidResult
		}
		key := resultIdentityKey(item.ParentLogicalName, item.LogicalName)
		if _, duplicate := parser.expected[key]; duplicate {
			return nil, ErrInvalidResult
		}
		item.Parameters = append([]testdomain.Parameter(nil), item.Parameters...)
		parser.expected[key] = item
	}
	return parser, nil
}

func validResultIdentity(value string) bool {
	return value != "" &&
		len(value) <= 64*1024 &&
		utf8.ValidString(value) &&
		!strings.ContainsAny(value, "\x00\r\n,)")
}

func resultIdentityKey(group, name string) string {
	return group + "\x00" + name
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
	if stream != testframework.StreamStdout && stream != testframework.StreamStderr {
		return nil, parser.fail(fmt.Errorf("%w: unknown output stream", ErrInvalidResult))
	}
	if len(data) > parser.limits.MaxOutputBytes-parser.outputBytes {
		return nil, parser.fail(ErrResultLimitExceeded)
	}
	parser.outputBytes += len(data)
	var buffer *[]byte
	if stream == testframework.StreamStdout {
		buffer = &parser.stdout
	} else {
		buffer = &parser.stderr
	}
	searchFrom := len(*buffer)
	*buffer = append(*buffer, data...)
	var events []testframework.ResultEvent
	for {
		relativeNewline := bytes.IndexByte((*buffer)[searchFrom:], '\n')
		if relativeNewline < 0 {
			if len(*buffer) > parser.limits.MaxLineBytes {
				return events, parser.fail(ErrResultLimitExceeded)
			}
			break
		}
		newline := searchFrom + relativeNewline
		if newline > parser.limits.MaxLineBytes {
			return events, parser.fail(ErrResultLimitExceeded)
		}
		line := (*buffer)[:newline]
		*buffer = (*buffer)[newline+1:]
		searchFrom = 0
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		normalized, err := stripANSILine(line)
		if err != nil {
			return events, parser.fail(err)
		}
		if stream == testframework.StreamStdout {
			event, emitted, err := parser.consumeStdoutLine(normalized)
			if err != nil {
				return events, parser.fail(err)
			}
			if emitted {
				events = append(events, event)
			}
		}
	}
	return events, nil
}

func (parser *Parser) fail(err error) error {
	parser.feedErr = err
	return err
}

func (parser *Parser) consumeStdoutLine(
	line string,
) (testframework.ResultEvent, bool, error) {
	if parser.summary != nil {
		if line != "" {
			parser.invalid = true
		}
		return testframework.ResultEvent{}, false, nil
	}
	if parser.active == nil {
		if line == "" {
			return testframework.ResultEvent{}, false, nil
		}
		if summary, ok := parseSummary(line); ok {
			parser.summary = &summary
			return testframework.ResultEvent{}, false, nil
		}
		if record, ok := parseCompleteCase(line); ok {
			return parser.completeRecord(record, nil)
		}
		if start, ok := parseCaseStart(line); ok {
			parser.active = &activeRecord{identity: start}
			return testframework.ResultEvent{}, false, nil
		}
		parser.invalid = true
		return testframework.ResultEvent{}, false, nil
	}

	if duration, ok := parseCaseEnd(line); ok {
		record := parser.active.identity
		record.duration = duration
		active := parser.active
		parser.active = nil
		return parser.completeRecord(record, active)
	}
	if failure, ok := parseFailure(line); ok {
		parser.active.hadFailure = true
		parser.active.failureRecords++
		if failure.macro != parser.active.identity.macro ||
			failure.group != parser.active.identity.group ||
			failure.name != parser.active.identity.name {
			parser.invalid = true
		}
		if parser.active.source == nil {
			parser.active.source = &testframework.ParsedSourceLocation{
				Path: failure.path,
				Line: failure.line,
			}
		}
		return testframework.ResultEvent{}, false, nil
	}
	if _, ok := parseCaseStart(line); ok {
		parser.invalid = true
	}
	if err := parser.appendMessageLine(line); err != nil {
		return testframework.ResultEvent{}, false, err
	}
	return testframework.ResultEvent{}, false, nil
}

func (parser *Parser) appendMessageLine(line string) error {
	added := len(line)
	if len(parser.active.messageLines) != 0 {
		added++
	}
	if added > parser.limits.MaxMessageBytes-parser.active.messageBytes {
		return ErrResultLimitExceeded
	}
	parser.active.messageBytes += added
	parser.active.messageLines = append(parser.active.messageLines, line)
	return nil
}

func (parser *Parser) completeRecord(
	record grammarCase,
	active *activeRecord,
) (testframework.ResultEvent, bool, error) {
	key := resultIdentityKey(record.group, record.name)
	item, expected := parser.expected[key]
	if !expected {
		parser.invalid = true
	}
	if _, duplicate := parser.seen[key]; duplicate {
		parser.invalid = true
		expected = false
	}

	status := testframework.CasePassed
	category := ""
	message := ""
	var source *testframework.ParsedSourceLocation
	failureRecords := 0
	if record.macro == "IGNORE_TEST" {
		status = testframework.CaseSkipped
		parser.ignored++
	} else {
		parser.ordinary++
	}
	if active != nil && active.hadFailure {
		if record.macro != "TEST" {
			parser.invalid = true
		}
		status = testframework.CaseFailed
		category = "assertion_failure"
		message = strings.TrimSpace(strings.Join(active.messageLines, "\n"))
		source = active.source
		failureRecords = active.failureRecords
		if failureRecords == 0 {
			failureRecords = 1
		}
		parser.failures += failureRecords
	}
	if !expected {
		return testframework.ResultEvent{}, false, nil
	}
	parser.seen[key] = struct{}{}
	result := testframework.ParsedCaseResult{
		ItemID:            item.ItemID,
		ParentLogicalName: item.ParentLogicalName,
		LogicalName:       item.LogicalName,
		Status:            status,
		DurationMS:        record.duration,
		Category:          category,
		Message:           message,
		SourceLocation:    cloneParsedLocation(source),
	}
	parser.cases = append(parser.cases, result)
	return testframework.ResultEvent{Case: cloneParsedCase(result)}, true, nil
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
	if err := parser.flushFinalLines(); err != nil {
		return testframework.ParseResult{}, err
	}
	if !validTermination(process.Termination) {
		return testframework.ParseResult{}, fmt.Errorf(
			"%w: unknown process termination",
			ErrInvalidResult,
		)
	}

	diagnostics := make([]testdomain.Diagnostic, 0, 2)
	complete := true
	abnormal := process.Termination != testframework.ProcessExited
	switch process.Termination {
	case testframework.ProcessTimedOut:
		complete = false
		diagnostics = append(diagnostics, resultDiagnostic(
			"test_timeout",
			"test.cpputest.timeout",
			"CppUTest 进程运行超时，结果不完整。",
		))
	case testframework.ProcessCrashed:
		complete = false
		diagnostics = append(diagnostics, resultDiagnostic(
			"test_process_crash",
			"test.cpputest.crash",
			"CppUTest 进程异常终止，结果不完整。",
		))
	case testframework.ProcessCancelled:
		complete = false
		diagnostics = append(diagnostics, resultDiagnostic(
			"cancelled",
			"test.cpputest.cancelled",
			"CppUTest 运行已取消，结果不完整。",
		))
	}

	outputInvalid := parser.invalid
	if !abnormal {
		if parser.active != nil || parser.summary == nil {
			outputInvalid = true
		}
		if parser.summary != nil && !parser.summaryConsistent() {
			outputInvalid = true
		}
	}
	if outputInvalid {
		complete = false
		diagnostics = append(diagnostics, resultDiagnostic(
			"framework_output_invalid",
			"test.cpputest.output_invalid",
			"CppUTest 输出 grammar 或 summary 与已观察 record 不一致。",
		))
	}

	if process.Termination == testframework.ProcessExited {
		switch {
		case process.ExitCode != 0 && parser.failures == 0:
			complete = false
			diagnostics = append(diagnostics, resultDiagnostic(
				"unexpected_exit",
				"test.cpputest.unexpected_exit",
				fmt.Sprintf("CppUTest 未提供 assertion evidence，但进程以 %d 退出。", process.ExitCode),
			))
		case process.ExitCode == 0 && parser.failures > 0:
			complete = false
			diagnostics = append(diagnostics, resultDiagnostic(
				"inconsistent_exit_status",
				"test.cpputest.inconsistent_exit",
				"CppUTest 提供 assertion failure evidence，但进程退出码为 0。",
			))
		case parser.summary != nil &&
			((parser.summary.ok && process.ExitCode != 0) ||
				(!parser.summary.ok && process.ExitCode == 0)):
			complete = false
			diagnostics = append(diagnostics, resultDiagnostic(
				"inconsistent_exit_status",
				"test.cpputest.inconsistent_exit",
				"CppUTest summary 与进程退出码不一致。",
			))
		}
	}
	if len(parser.seen) != len(parser.expected) {
		complete = false
		if !abnormal && !outputInvalid {
			diagnostics = append(diagnostics, resultDiagnostic(
				"framework_output_invalid",
				"test.cpputest.missing_cases",
				"CppUTest 未返回全部预期 case record。",
			))
		}
	}

	cases := make([]testframework.ParsedCaseResult, 0, len(parser.expected))
	for _, item := range parser.cases {
		copy := cloneParsedCase(item)
		copy.Partial = !complete
		cases = append(cases, copy)
	}
	if !complete {
		for _, item := range parser.expectedOrder {
			key := resultIdentityKey(item.ParentLogicalName, item.LogicalName)
			if _, found := parser.seen[key]; found {
				continue
			}
			cases = append(cases, testframework.ParsedCaseResult{
				ItemID:            item.ItemID,
				ParentLogicalName: item.ParentLogicalName,
				LogicalName:       item.LogicalName,
				Status:            testframework.CaseNotRun,
				Partial:           true,
			})
		}
	}
	return testframework.ParseResult{
		Cases:       cases,
		Diagnostics: diagnostics,
		Complete:    complete,
	}, nil
}

func (parser *Parser) flushFinalLines() error {
	for _, stream := range []struct {
		kind   testframework.Stream
		buffer *[]byte
	}{
		{testframework.StreamStdout, &parser.stdout},
		{testframework.StreamStderr, &parser.stderr},
	} {
		if len(*stream.buffer) == 0 {
			continue
		}
		if len(*stream.buffer) > parser.limits.MaxLineBytes {
			return ErrResultLimitExceeded
		}
		line := *stream.buffer
		*stream.buffer = nil
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		normalized, err := stripANSILine(line)
		if err != nil {
			return err
		}
		if stream.kind == testframework.StreamStdout {
			if _, _, err := parser.consumeStdoutLine(normalized); err != nil {
				return err
			}
		}
	}
	return nil
}

func (parser *Parser) summaryConsistent() bool {
	summary := parser.summary
	if summary == nil {
		return false
	}
	if summary.tests != summary.ran+summary.ignored+summary.filteredOut ||
		summary.ran != parser.ordinary ||
		summary.ignored != parser.ignored ||
		summary.failures != parser.failures {
		return false
	}
	if summary.ok {
		return summary.failures == 0
	}
	return summary.failures > 0
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

func cloneParsedLocation(
	value *testframework.ParsedSourceLocation,
) *testframework.ParsedSourceLocation {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneParsedCase(
	value testframework.ParsedCaseResult,
) testframework.ParsedCaseResult {
	value.SourceLocation = cloneParsedLocation(value.SourceLocation)
	return value
}
