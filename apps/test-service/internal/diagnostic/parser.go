package diagnostic

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/workspace"
)

const (
	maxLineBytes         = 64 * 1024
	maxDiagnosticBytes   = 1024 * 1024
	maxRelatedRecords    = 32
	maxDiagnostics       = 4096
	maxRetainedTextBytes = 8 * 1024 * 1024
)

type Family string

const (
	FamilyCMake  Family = "cmake"
	FamilyMSVC   Family = "msvc"
	FamilyGNU    Family = "gnu"
	FamilyLinker Family = "linker"
)

type Options struct {
	Root             workspace.Root
	WorkingDirectory string
	TaskID           string
	StepID           string
	ToolchainID      string
}

type streamState struct {
	buffer         []byte
	pending        *Diagnostic
	discardingLine bool
}

type parser struct {
	family             Family
	options            Options
	streams            map[string]*streamState
	occurrences        map[string]int
	notices            map[string]bool
	count              int
	retained           int
	retentionExhausted bool
	closed             bool
}

func NewParser(family Family, options Options) (Parser, error) {
	if family != FamilyCMake && family != FamilyMSVC &&
		family != FamilyGNU && family != FamilyLinker {
		return nil, errors.New("unsupported diagnostic parser family")
	}
	if options.Root.NativePath == "" || options.Root.ID == "" || options.Root.URI == "" {
		return nil, errors.New("invalid workspace root")
	}
	if options.WorkingDirectory == "" || !filepath.IsAbs(options.WorkingDirectory) ||
		!options.Root.Contains(options.WorkingDirectory) {
		return nil, errors.New("invalid diagnostic working directory")
	}
	return &parser{
		family:      family,
		options:     options,
		streams:     map[string]*streamState{"stdout": {}, "stderr": {}},
		occurrences: make(map[string]int),
		notices:     make(map[string]bool),
	}, nil
}

func (p *parser) Feed(stream string, data []byte) []Diagnostic {
	if p == nil || p.closed {
		return nil
	}
	state, ok := p.streams[stream]
	if !ok {
		return nil
	}
	var result []Diagnostic
	for len(data) != 0 {
		if state.discardingLine {
			index := bytes.IndexByte(data, '\n')
			if index < 0 {
				return cloneDiagnostics(result)
			}
			state.discardingLine = false
			data = data[index+1:]
			continue
		}
		index := bytes.IndexByte(data, '\n')
		segment := data
		complete := false
		if index >= 0 {
			segment = data[:index]
			data = data[index+1:]
			complete = true
		} else {
			data = nil
		}
		if logicalLineTooLong(state.buffer, segment, complete) {
			state.buffer = nil
			if !complete {
				state.discardingLine = true
			}
			result = append(result, p.flush(state)...)
			result = append(result, p.notice(
				"DIAGNOSTIC_TRUNCATED",
				"Diagnostic output was truncated",
			)...)
			continue
		}
		state.buffer = append(state.buffer, segment...)
		if complete {
			line := bytes.TrimSuffix(state.buffer, []byte{'\r'})
			state.buffer = nil
			result = append(result, p.consumeBytes(state, line)...)
		}
	}
	return cloneDiagnostics(result)
}

func logicalLineTooLong(buffer, segment []byte, complete bool) bool {
	size := len(buffer) + len(segment)
	trailingCR := len(segment) != 0 && segment[len(segment)-1] == '\r' ||
		len(segment) == 0 && len(buffer) != 0 && buffer[len(buffer)-1] == '\r'
	if complete && trailingCR {
		size--
	}
	if !complete && size == maxLineBytes+1 && trailingCR {
		return false
	}
	return size > maxLineBytes
}

func (p *parser) Close() []Diagnostic {
	if p == nil || p.closed {
		return nil
	}
	p.closed = true
	var result []Diagnostic
	for _, stream := range []string{"stdout", "stderr"} {
		state := p.streams[stream]
		if len(state.buffer) != 0 {
			line := bytes.TrimSuffix(state.buffer, []byte{'\r'})
			result = append(result, p.consumeBytes(state, line)...)
			state.buffer = nil
		}
		result = append(result, p.flush(state)...)
	}
	return cloneDiagnostics(result)
}

func (p *parser) consumeBytes(state *streamState, line []byte) []Diagnostic {
	if !utf8.Valid(line) || bytes.IndexByte(line, 0) >= 0 {
		result := p.flush(state)
		return append(result, p.notice(
			"DIAGNOSTIC_INPUT_INVALID",
			"Diagnostic input was invalid",
		)...)
	}
	return p.consumeLine(state, string(line))
}

func (p *parser) consumeLine(state *streamState, line string) []Diagnostic {
	switch p.family {
	case FamilyCMake:
		return p.consumeCMakeLine(state, line)
	case FamilyMSVC:
		return p.consumeMSVCLine(state, line)
	case FamilyGNU:
		return p.consumeGNULine(state, line)
	case FamilyLinker:
		return p.consumeLinkerLine(state, line)
	default:
		return nil
	}
}

func (p *parser) consumeCMakeLine(state *streamState, line string) []Diagnostic {
	if match := cmakeLocationPattern.FindStringSubmatch(line); match != nil {
		result := p.flush(state)
		severity := strings.ToLower(match[1])
		code := "CMAKE_" + strings.ToUpper(match[1])
		location := p.location(match[2], parsePositive(match[3]), 0)
		state.pending = &Diagnostic{
			TaskID: p.options.TaskID, StepID: p.options.StepID,
			Source: "cmake", Severity: severity, Code: code,
			FileURI: location.uri, Range: location.rangeValue,
			External: location.external,
		}
		return result
	}
	if line == "CMake Generate step failed. Build files cannot be regenerated correctly." {
		result := p.flush(state)
		state.pending = &Diagnostic{
			TaskID: p.options.TaskID, StepID: p.options.StepID,
			Source: "cmake", Severity: "error", Code: "CMAKE_GENERATE_FAILED",
			Message: "CMake generate step failed",
		}
		return result
	}
	if cmakeSourceDirectoryErrorPattern.MatchString(line) {
		result := p.flush(state)
		state.pending = &Diagnostic{
			TaskID: p.options.TaskID, StepID: p.options.StepID,
			Source: "cmake", Severity: "error", Code: "CMAKE_CONFIGURE_FAILED",
			Message: "CMake source directory is invalid",
		}
		return result
	}
	if line == "-- Configuring incomplete, errors occurred!" {
		result := p.flush(state)
		state.pending = &Diagnostic{
			TaskID: p.options.TaskID, StepID: p.options.StepID,
			Source: "cmake", Severity: "error", Code: "CMAKE_CONFIGURE_FAILED",
			Message: "CMake configure failed",
		}
		return result
	}
	if state.pending != nil {
		if strings.TrimSpace(line) == "" {
			return p.flush(state)
		}
		if len(line) != 0 && (line[0] == ' ' || line[0] == '\t') {
			text := strings.TrimSpace(line)
			if text != "" {
				addition := len(text)
				if state.pending.Message != "" {
					addition++
				}
				if diagnosticTextBytes(*state.pending)+addition > maxDiagnosticBytes {
					return p.notice(
						"DIAGNOSTIC_TRUNCATED",
						"Diagnostic output was truncated",
					)
				}
				if state.pending.Message != "" {
					state.pending.Message += "\n"
				}
				state.pending.Message += text
			}
			return nil
		}
		return p.flush(state)
	}
	return nil
}

func (p *parser) consumeMSVCLine(state *streamState, line string) []Diagnostic {
	if match := msvcNotePattern.FindStringSubmatch(line); match != nil &&
		state.pending != nil && state.pending.Severity != "note" {
		location := p.location(match[1], parsePositive(match[2]), parsePositive(match[3]))
		message := msvcProjectSuffixPattern.ReplaceAllString(match[4], "")
		if len(state.pending.Related) >= maxRelatedRecords ||
			diagnosticTextBytes(*state.pending)+len(message)+len(location.uri) > maxDiagnosticBytes {
			return p.notice(
				"DIAGNOSTIC_TRUNCATED",
				"Diagnostic output was truncated",
			)
		}
		state.pending.Related = append(state.pending.Related, Related{
			Message: message, FileURI: location.uri, Range: location.rangeValue,
		})
		return nil
	}
	if match := msvcLocationPattern.FindStringSubmatch(line); match != nil {
		result := p.flush(state)
		severity := match[4]
		if severity == "fatal error" {
			severity = "error"
		}
		location := p.location(match[1], parsePositive(match[2]), parsePositive(match[3]))
		message := msvcProjectSuffixPattern.ReplaceAllString(match[6], "")
		code := match[5]
		if code == "" {
			message, code = compilerMessageAndCode(message, severity)
		}
		state.pending = &Diagnostic{
			TaskID: p.options.TaskID, StepID: p.options.StepID,
			Source: "compiler", ToolchainID: p.options.ToolchainID,
			Severity: severity, Code: code, Message: message,
			FileURI: location.uri, Range: location.rangeValue,
			External: location.external,
		}
		return result
	}
	return p.consumeLinkerLine(state, line)
}

func (p *parser) consumeGNULine(state *streamState, line string) []Diagnostic {
	match := gnuLineColumnPattern.FindStringSubmatch(line)
	if match == nil {
		lineMatch := gnuLinePattern.FindStringSubmatch(line)
		if lineMatch == nil {
			return p.consumeLinkerLine(state, line)
		}
		match = []string{
			lineMatch[0], lineMatch[1], lineMatch[2], "",
			lineMatch[3], lineMatch[4],
		}
	}
	lineNumber := parsePositive(match[2])
	column := parsePositive(match[3])
	severity := match[4]
	if severity == "fatal error" {
		severity = "error"
	}
	location := p.location(match[1], lineNumber, column)
	message, code := compilerMessageAndCode(match[5], severity)
	if severity == "note" && state.pending != nil && state.pending.Severity != "note" {
		if len(state.pending.Related) >= maxRelatedRecords ||
			diagnosticTextBytes(*state.pending)+len(message)+len(location.uri) > maxDiagnosticBytes {
			return p.notice(
				"DIAGNOSTIC_TRUNCATED",
				"Diagnostic output was truncated",
			)
		}
		state.pending.Related = append(state.pending.Related, Related{
			Message: message, FileURI: location.uri, Range: location.rangeValue,
		})
		return nil
	}
	result := p.flush(state)
	state.pending = &Diagnostic{
		TaskID: p.options.TaskID, StepID: p.options.StepID,
		Source: "compiler", ToolchainID: p.options.ToolchainID,
		Severity: severity, Code: code, Message: message,
		FileURI: location.uri, Range: location.rangeValue,
		External: location.external,
	}
	return result
}

func (p *parser) consumeLinkerLine(state *streamState, line string) []Diagnostic {
	result := p.flush(state)
	var code, message string
	switch {
	case msvcLinkPattern.MatchString(line):
		match := msvcLinkPattern.FindStringSubmatch(line)
		code = match[1]
		message = msvcProjectSuffixPattern.ReplaceAllString(match[2], "")
	case gnuUndefinedReferencePattern.MatchString(line):
		match := gnuUndefinedReferencePattern.FindStringSubmatch(line)
		code, message = "LD_UNDEFINED_REFERENCE", match[1]+": undefined reference to "+match[2]
	case lldUndefinedSymbolPattern.MatchString(line):
		match := lldUndefinedSymbolPattern.FindStringSubmatch(line)
		code, message = "LLD_UNDEFINED_SYMBOL", "undefined symbol: "+match[1]
	case lldLinkErrorPattern.MatchString(line):
		match := lldLinkErrorPattern.FindStringSubmatch(line)
		code, message = "LLD_LINK_ERROR", match[1]
	case collect2ErrorPattern.MatchString(line):
		match := collect2ErrorPattern.FindStringSubmatch(line)
		code, message = "LD_ERROR", match[1]
	default:
		return result
	}
	state.pending = &Diagnostic{
		TaskID: p.options.TaskID, StepID: p.options.StepID,
		Source: "linker", ToolchainID: p.options.ToolchainID,
		Severity: "error", Code: code, Message: message,
	}
	return result
}

func compilerMessageAndCode(message, severity string) (string, string) {
	code := "COMPILER_" + strings.ToUpper(severity)
	if strings.HasSuffix(message, "]") {
		index := strings.LastIndex(message, " [-W")
		if index >= 0 {
			code = message[index+2 : len(message)-1]
			message = message[:index]
		}
	}
	return message, code
}

func (p *parser) flush(state *streamState) []Diagnostic {
	if state.pending == nil {
		return nil
	}
	if p.retentionExhausted {
		state.pending = nil
		return nil
	}
	value := cloneDiagnostic(*state.pending)
	state.pending = nil
	valueBytes := diagnosticTextBytes(value)
	retainedLimit := maxRetainedTextBytes - p.noticeReservationBytes()
	if valueBytes > maxDiagnosticBytes {
		return p.notice(
			"DIAGNOSTIC_TRUNCATED",
			"Diagnostic output was truncated",
		)
	}
	if p.retained+valueBytes > retainedLimit {
		p.retentionExhausted = true
		return p.notice(
			"DIAGNOSTIC_TRUNCATED",
			"Diagnostic output was truncated",
		)
	}
	countLimit := maxDiagnostics
	if !p.notices["DIAGNOSTIC_TRUNCATED"] {
		countLimit--
		if !p.notices["DIAGNOSTIC_INPUT_INVALID"] {
			countLimit--
		}
	}
	if p.count >= countLimit {
		return p.notice(
			"DIAGNOSTIC_TRUNCATED",
			"Diagnostic output was truncated",
		)
	}
	value.ID = p.diagnosticID(value)
	p.count++
	p.retained += valueBytes
	return []Diagnostic{value}
}

func (p *parser) diagnosticID(value Diagnostic) string {
	identity := cloneDiagnostic(value)
	identity.ID = ""
	identity.FileURI = p.identityURI(identity.FileURI)
	for index := range identity.Related {
		identity.Related[index].FileURI = p.identityURI(identity.Related[index].FileURI)
	}
	encoded, _ := json.Marshal(identity)
	fingerprint := sha256.Sum256(append([]byte("diagnostic-v1\x00"), encoded...))
	key := hex.EncodeToString(fingerprint[:])
	ordinal := p.occurrences[key]
	p.occurrences[key] = ordinal + 1
	identityText := fmt.Sprintf("diagnostic-v1\x00%s\x00%d", key, ordinal)
	sum := sha256.Sum256([]byte(identityText))
	return hex.EncodeToString(sum[:])
}

func (p *parser) identityURI(value string) string {
	if value == "" {
		return ""
	}
	root := strings.TrimSuffix(p.options.Root.URI, "/")
	if identity, ok := windowsWorkspaceIdentityURI(root, value); ok {
		return identity
	}
	if value == root {
		return "workspace:///"
	}
	if strings.HasPrefix(value, root+"/") {
		return "workspace:///" + strings.TrimPrefix(value, root+"/")
	}
	return value
}

func (p *parser) PublicURI(value string) string {
	if p == nil || value == "" {
		return value
	}
	parsed, err := url.Parse(value)
	if err != nil || !strings.EqualFold(parsed.Scheme, "file") ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return value
	}
	native, ok := fileURIPath(parsed)
	if !ok {
		return value
	}
	relative, err := p.options.Root.RelativePath(native)
	if err != nil {
		if runtime.GOOS == "windows" {
			return "workspace:///"
		}
		return value
	}
	if relative == "." {
		return "workspace:///"
	}
	return (&url.URL{Scheme: "workspace", Path: "/" + filepath.ToSlash(relative)}).String()
}

func fileURIPath(value *url.URL) (string, bool) {
	if value == nil {
		return "", false
	}
	if runtime.GOOS == "windows" {
		if value.Host != "" {
			native := `\\` + value.Host + `\` +
				strings.TrimPrefix(filepath.FromSlash(value.Path), `\`)
			if !filepath.IsAbs(native) {
				return "", false
			}
			return filepath.Clean(native), true
		}
		native := filepath.FromSlash(strings.TrimPrefix(value.Path, "/"))
		if !filepath.IsAbs(native) {
			return "", false
		}
		return filepath.Clean(native), true
	}
	if value.Host != "" {
		return "", false
	}
	native := filepath.Clean(value.Path)
	if !filepath.IsAbs(native) {
		return "", false
	}
	return native, true
}

func windowsWorkspaceIdentityURI(root, value string) (string, bool) {
	if runtime.GOOS != "windows" {
		return "", false
	}
	rootURL, rootErr := url.Parse(root)
	valueURL, valueErr := url.Parse(value)
	if rootErr != nil || valueErr != nil ||
		!strings.EqualFold(rootURL.Scheme, "file") ||
		!strings.EqualFold(valueURL.Scheme, "file") ||
		rootURL.RawQuery != "" || rootURL.Fragment != "" ||
		valueURL.RawQuery != "" || valueURL.Fragment != "" ||
		!strings.EqualFold(rootURL.Host, valueURL.Host) {
		return "", false
	}
	rootParts := fileURIPathParts(rootURL.Path)
	valueParts := fileURIPathParts(valueURL.Path)
	if rootURL.Host == "" &&
		(len(rootParts) == 0 || len(rootParts[0]) != 2 || rootParts[0][1] != ':') {
		return "", false
	}
	if len(valueParts) < len(rootParts) {
		return "", false
	}
	for index := range rootParts {
		if !strings.EqualFold(rootParts[index], valueParts[index]) {
			return "", false
		}
	}
	relative := strings.ToLower(strings.Join(valueParts[len(rootParts):], "/"))
	return "workspace:///" + relative, true
}

func fileURIPathParts(value string) []string {
	value = strings.Trim(value, "/")
	if value == "" {
		return nil
	}
	return strings.Split(value, "/")
}

func (p *parser) notice(code, message string) []Diagnostic {
	if p.notices[code] || p.count >= maxDiagnostics {
		return nil
	}
	value := Diagnostic{
		TaskID: p.options.TaskID, StepID: p.options.StepID,
		Source: "parser", ToolchainID: p.options.ToolchainID,
		Severity: "info", Code: code, Message: message,
	}
	valueBytes := diagnosticTextBytes(value)
	if p.retained+valueBytes > maxRetainedTextBytes {
		return nil
	}
	p.notices[code] = true
	value.ID = p.diagnosticID(value)
	p.count++
	p.retained += valueBytes
	return []Diagnostic{value}
}

func (p *parser) noticeReservationBytes() int {
	total := 0
	if !p.notices["DIAGNOSTIC_TRUNCATED"] {
		total += p.noticeBytes(
			"DIAGNOSTIC_TRUNCATED",
			"Diagnostic output was truncated",
		)
	}
	if !p.notices["DIAGNOSTIC_INPUT_INVALID"] {
		total += p.noticeBytes(
			"DIAGNOSTIC_INPUT_INVALID",
			"Diagnostic input was invalid",
		)
	}
	return total
}

func (p *parser) noticeBytes(code, message string) int {
	return diagnosticTextBytes(Diagnostic{
		TaskID: p.options.TaskID, StepID: p.options.StepID,
		Source: "parser", ToolchainID: p.options.ToolchainID,
		Severity: "info", Code: code, Message: message,
	})
}

func diagnosticTextBytes(value Diagnostic) int {
	total := len(value.TaskID) + len(value.StepID) + len(value.Source) +
		len(value.ToolchainID) + len(value.Severity) + len(value.Code) +
		len(value.Message) + len(value.FileURI)
	for _, related := range value.Related {
		total += len(related.Message) + len(related.FileURI)
	}
	return total
}

type mappedLocation struct {
	uri        string
	rangeValue *Range
	external   bool
}

func (p *parser) location(path string, line, column int) mappedLocation {
	native, external := p.resolvePath(path)
	result := mappedLocation{uri: fileURI(native), external: external}
	if line > 0 {
		start := Position{Line: line - 1}
		end := start
		if column > 0 {
			start.Character = column - 1
			end.Character = column
		}
		result.rangeValue = &Range{Start: start, End: end}
	}
	return result
}

func (p *parser) resolvePath(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if portableAbsolute(value) {
		if filepath.IsAbs(value) {
			clean := filepath.Clean(value)
			return clean, !p.options.Root.Contains(clean)
		}
		return portableClean(value), true
	}
	native := filepath.Clean(filepath.Join(p.options.WorkingDirectory, filepath.FromSlash(value)))
	return native, !p.options.Root.Contains(native)
}

func portableAbsolute(value string) bool {
	return strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\\`) ||
		len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') ||
			(value[0] >= 'a' && value[0] <= 'z')) &&
			value[1] == ':' && (value[2] == '/' || value[2] == '\\')
}

func portableClean(value string) string {
	return strings.ReplaceAll(value, `\`, "/")
}

func fileURI(value string) string {
	portable := portableClean(value)
	if strings.HasPrefix(portable, "//") {
		parts := strings.SplitN(strings.TrimPrefix(portable, "//"), "/", 2)
		path := "/"
		if len(parts) == 2 {
			path += parts[1]
		}
		return (&url.URL{Scheme: "file", Host: parts[0], Path: path}).String()
	}
	if len(portable) >= 2 && portable[1] == ':' {
		portable = "/" + portable
	}
	return (&url.URL{Scheme: "file", Path: portable}).String()
}

func parsePositive(value string) int {
	number, _ := strconv.Atoi(value)
	if number < 1 {
		return 0
	}
	return number
}

func cloneDiagnostics(values []Diagnostic) []Diagnostic {
	if values == nil {
		return nil
	}
	result := make([]Diagnostic, len(values))
	for index := range values {
		result[index] = cloneDiagnostic(values[index])
	}
	return result
}

func cloneDiagnostic(value Diagnostic) Diagnostic {
	if value.Range != nil {
		copyRange := *value.Range
		value.Range = &copyRange
	}
	value.Related = append([]Related(nil), value.Related...)
	for index := range value.Related {
		if value.Related[index].Range != nil {
			copyRange := *value.Related[index].Range
			value.Related[index].Range = &copyRange
		}
	}
	return value
}
