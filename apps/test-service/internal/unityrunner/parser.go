package unityrunner

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"unit-test-ide.local/test-service/internal/workspace"
)

type SourceDiagnostic struct {
	Code    string
	Path    string
	Line    int
	Message string
	cause   error
}

func (diagnostic *SourceDiagnostic) Error() string {
	if diagnostic == nil {
		return "<nil>"
	}
	location := diagnostic.Path
	if diagnostic.Line > 0 {
		location = fmt.Sprintf("%s:%d", location, diagnostic.Line)
	}
	return fmt.Sprintf("%s: %s: %s", location, diagnostic.Code, diagnostic.Message)
}

func (diagnostic *SourceDiagnostic) Unwrap() error {
	if diagnostic == nil {
		return nil
	}
	return diagnostic.cause
}

type sourceEntry struct {
	displayPath string
	nativePath  string
	info        os.FileInfo
}

type parsedSource struct {
	setUp    *SourceLocation
	tearDown *SourceLocation
	cases    []TestCase
}

type parserState struct {
	limits             Limits
	caseCount          int
	parameterInstances int
}

func ParseSources(root string, sources []string, limits Limits) (Manifest, error) {
	if err := limits.Validate(); err != nil {
		return Manifest{}, err
	}
	if len(sources) == 0 {
		return Manifest{}, fmt.Errorf("%w: no sources were declared", ErrInvalidSourcePath)
	}
	if len(sources) > limits.MaxSources {
		return Manifest{}, fmt.Errorf("%w: source count %d exceeds %d", ErrLimitExceeded, len(sources), limits.MaxSources)
	}
	canonicalRoot, err := workspace.OpenRoot(root)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrInvalidSourcePath, err)
	}

	entries := make([]sourceEntry, 0, len(sources))
	seenSources := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		entry, err := resolveSource(canonicalRoot, source)
		if err != nil {
			return Manifest{}, err
		}
		if _, exists := seenSources[entry.displayPath]; exists {
			return Manifest{}, fmt.Errorf("%w: duplicate declared source %q", ErrInvalidSourcePath, entry.displayPath)
		}
		for _, previous := range entries {
			if os.SameFile(previous.info, entry.info) {
				return Manifest{}, fmt.Errorf(
					"%w: %q and %q name the same file",
					ErrInvalidSourcePath, previous.displayPath, entry.displayPath,
				)
			}
		}
		seenSources[entry.displayPath] = struct{}{}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].displayPath < entries[right].displayPath
	})

	state := parserState{limits: limits}
	manifest := Manifest{
		Sources: make([]string, 0, len(entries)),
		Cases:   make([]TestCase, 0),
	}
	seenIdentities := make(map[string]SourceLocation)
	for _, entry := range entries {
		manifest.Sources = append(manifest.Sources, entry.displayPath)
		data, err := readSource(entry, limits.MaxSourceBytes)
		if err != nil {
			return Manifest{}, err
		}
		parsed, err := parseSource(entry.displayPath, data, &state)
		if err != nil {
			return Manifest{}, err
		}
		if parsed.setUp != nil {
			if manifest.SetUp != nil {
				return Manifest{}, sourceError(
					ErrDuplicateIdentity, "duplicate_hook", parsed.setUp.Path, parsed.setUp.Line,
					fmt.Sprintf("setUp was already declared at %s:%d", manifest.SetUp.Path, manifest.SetUp.Line),
				)
			}
			manifest.SetUp = parsed.setUp
		}
		if parsed.tearDown != nil {
			if manifest.TearDown != nil {
				return Manifest{}, sourceError(
					ErrDuplicateIdentity, "duplicate_hook", parsed.tearDown.Path, parsed.tearDown.Line,
					fmt.Sprintf("tearDown was already declared at %s:%d", manifest.TearDown.Path, manifest.TearDown.Line),
				)
			}
			manifest.TearDown = parsed.tearDown
		}
		for _, testCase := range parsed.cases {
			if previous, exists := seenIdentities[testCase.Identity]; exists {
				return Manifest{}, sourceError(
					ErrDuplicateIdentity, "duplicate_test_identity",
					testCase.Location.Path, testCase.Location.Line,
					fmt.Sprintf("%q was already declared at %s:%d", testCase.Identity, previous.Path, previous.Line),
				)
			}
			seenIdentities[testCase.Identity] = testCase.Location
			manifest.Cases = append(manifest.Cases, testCase)
		}
	}
	sortManifestCases(manifest.Cases)
	return sealManifest(manifest)
}

func resolveSource(root workspace.Root, source string) (sourceEntry, error) {
	if source == "" || strings.IndexByte(source, 0) >= 0 || !utf8.ValidString(source) {
		return sourceEntry{}, fmt.Errorf("%w: malformed path", ErrInvalidSourcePath)
	}
	slashPath := strings.ReplaceAll(source, "\\", "/")
	if strings.HasPrefix(slashPath, "/") || strings.HasPrefix(slashPath, "//") ||
		hasPortableVolume(slashPath) {
		return sourceEntry{}, fmt.Errorf("%w: path %q must be workspace-relative", ErrInvalidSourcePath, source)
	}
	nativeRelative := filepath.Clean(filepath.FromSlash(slashPath))
	if nativeRelative == "." || filepath.IsAbs(nativeRelative) || filepath.VolumeName(nativeRelative) != "" {
		return sourceEntry{}, fmt.Errorf("%w: path %q must name a file", ErrInvalidSourcePath, source)
	}
	canonicalSlash := filepath.ToSlash(nativeRelative)
	if canonicalSlash == ".." || strings.HasPrefix(canonicalSlash, "../") {
		return sourceEntry{}, fmt.Errorf("%w: path %q escapes the workspace", ErrInvalidSourcePath, source)
	}
	provisionalDisplayPath := norm.NFC.String(canonicalSlash)
	if !validManifestPath(provisionalDisplayPath) {
		return sourceEntry{}, fmt.Errorf("%w: path %q is not canonical", ErrInvalidSourcePath, source)
	}
	if err := rejectSourceLinks(root.NativePath, nativeRelative); err != nil {
		return sourceEntry{}, fmt.Errorf("%w: %q: %v", ErrInvalidSourcePath, provisionalDisplayPath, err)
	}
	resolved, err := root.ResolveRelative(nativeRelative)
	if err != nil {
		return sourceEntry{}, fmt.Errorf("%w: %q: %v", ErrInvalidSourcePath, provisionalDisplayPath, err)
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return sourceEntry{}, fmt.Errorf("%w: %q: %v", ErrInvalidSourcePath, provisionalDisplayPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return sourceEntry{}, fmt.Errorf("%w: %q is not a regular non-link file", ErrInvalidSourcePath, provisionalDisplayPath)
	}
	resolvedRelative, err := filepath.Rel(root.NativePath, resolved)
	if err != nil {
		return sourceEntry{}, fmt.Errorf("%w: %q: %v", ErrInvalidSourcePath, provisionalDisplayPath, err)
	}
	displayPath := norm.NFC.String(filepath.ToSlash(resolvedRelative))
	if !validManifestPath(displayPath) {
		return sourceEntry{}, fmt.Errorf("%w: resolved path %q is not canonical", ErrInvalidSourcePath, displayPath)
	}
	return sourceEntry{displayPath: displayPath, nativePath: resolved, info: info}, nil
}

func hasPortableVolume(value string) bool {
	return len(value) >= 2 && isASCIILetter(value[0]) && value[1] == ':'
}

func rejectSourceLinks(root, relative string) error {
	current := root
	for _, component := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("source path contains a symbolic link or junction")
		}
	}
	return nil
}

func readSource(entry sourceEntry, maximum int64) ([]byte, error) {
	file, err := os.Open(entry.nativePath)
	if err != nil {
		return nil, sourceError(ErrInvalidSource, "source_read_failed", entry.displayPath, 0, err.Error())
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, sourceError(ErrInvalidSource, "source_stat_failed", entry.displayPath, 0, err.Error())
	}
	if !info.Mode().IsRegular() {
		return nil, sourceError(ErrInvalidSourcePath, "source_not_regular", entry.displayPath, 0, "source is not a regular file")
	}
	if info.Size() > maximum {
		return nil, sourceError(
			ErrLimitExceeded, "source_too_large", entry.displayPath, 0,
			fmt.Sprintf("source size %d exceeds %d bytes", info.Size(), maximum),
		)
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, sourceError(ErrInvalidSource, "source_read_failed", entry.displayPath, 0, err.Error())
	}
	if int64(len(data)) > maximum {
		return nil, sourceError(ErrLimitExceeded, "source_too_large", entry.displayPath, 0, "source grew beyond the byte limit while reading")
	}
	if !utf8.Valid(data) {
		return nil, sourceError(ErrInvalidSource, "invalid_utf8", entry.displayPath, 1, "source must be valid UTF-8")
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, sourceError(ErrInvalidSource, "source_contains_nul", entry.displayPath, 1, "source must not contain NUL")
	}
	return data, nil
}

type tokenKind uint8

const (
	tokenIdentifier tokenKind = iota + 1
	tokenNumber
	tokenString
	tokenCharacter
	tokenPunctuation
)

type sourceToken struct {
	kind tokenKind
	text string
	line int
}

func lexSource(path string, data []byte) ([]sourceToken, error) {
	tokens := make([]sourceToken, 0, len(data)/4)
	line := 1
	lineHasOnlyWhitespace := true
	for index := 0; index < len(data); {
		character := data[index]
		switch {
		case character == '\n':
			line++
			index++
			lineHasOnlyWhitespace = true
		case character == '\r' || character == ' ' || character == '\t' || character == '\f' || character == '\v':
			index++
		case character == '/' && index+1 < len(data) && data[index+1] == '/':
			index += 2
			for index < len(data) && data[index] != '\n' {
				index++
			}
		case character == '/' && index+1 < len(data) && data[index+1] == '*':
			startLine := line
			index += 2
			closed := false
			for index < len(data) {
				if data[index] == '\n' {
					line++
					lineHasOnlyWhitespace = true
					index++
					continue
				}
				if data[index] == '*' && index+1 < len(data) && data[index+1] == '/' {
					index += 2
					closed = true
					break
				}
				index++
			}
			if !closed {
				return nil, sourceError(ErrInvalidSource, "unterminated_comment", path, startLine, "unterminated block comment")
			}
		case character == '#' && lineHasOnlyWhitespace:
			directiveLine := line
			index++
			for index < len(data) && (data[index] == ' ' || data[index] == '\t') {
				index++
			}
			start := index
			for index < len(data) && isIdentifierContinue(data[index]) {
				index++
			}
			directive := string(data[start:index])
			switch directive {
			case "if", "ifdef", "ifndef", "elif", "else", "endif":
				return nil, sourceError(
					ErrUnsupportedSyntax, "conditional_preprocessor", path, directiveLine,
					fmt.Sprintf("#%s is unsupported in a declared Unity test source", directive),
				)
			}
			for index < len(data) {
				if data[index] == '\\' && index+1 < len(data) && data[index+1] == '\n' {
					index += 2
					line++
					continue
				}
				if data[index] == '\n' {
					break
				}
				index++
			}
			lineHasOnlyWhitespace = true
		case isIdentifierStart(character):
			start := index
			tokenLine := line
			index++
			for index < len(data) && isIdentifierContinue(data[index]) {
				index++
			}
			tokens = append(tokens, sourceToken{kind: tokenIdentifier, text: string(data[start:index]), line: tokenLine})
			lineHasOnlyWhitespace = false
		case isASCIIDigit(character) || character == '.' && index+1 < len(data) && isASCIIDigit(data[index+1]):
			start := index
			tokenLine := line
			if data[index] == '.' {
				index++
			}
			for index < len(data) {
				current := data[index]
				if isASCIILetter(current) || isASCIIDigit(current) || current == '_' || current == '.' {
					index++
					continue
				}
				if (current == '+' || current == '-') && index > start &&
					(data[index-1] == 'e' || data[index-1] == 'E' || data[index-1] == 'p' || data[index-1] == 'P') {
					index++
					continue
				}
				break
			}
			tokens = append(tokens, sourceToken{kind: tokenNumber, text: string(data[start:index]), line: tokenLine})
			lineHasOnlyWhitespace = false
		case character == '"' || character == '\'':
			kind := tokenString
			if character == '\'' {
				kind = tokenCharacter
			}
			start := index
			tokenLine := line
			quote := character
			index++
			escaped := false
			closed := false
			for index < len(data) {
				current := data[index]
				if current == '\n' || current == '\r' {
					break
				}
				index++
				if escaped {
					escaped = false
					continue
				}
				if current == '\\' {
					escaped = true
					continue
				}
				if current == quote {
					closed = true
					break
				}
			}
			if !closed {
				return nil, sourceError(ErrInvalidSource, "unterminated_literal", path, tokenLine, "unterminated string or character literal")
			}
			tokens = append(tokens, sourceToken{kind: kind, text: string(data[start:index]), line: tokenLine})
			lineHasOnlyWhitespace = false
		case character >= utf8.RuneSelf:
			_, size := utf8.DecodeRune(data[index:])
			return nil, sourceError(
				ErrUnsupportedSyntax, "non_ascii_source_token", path, line,
				fmt.Sprintf("non-ASCII token %q is outside the supported Unity grammar", string(data[index:index+size])),
			)
		default:
			punctuation := cPunctuator(data[index:])
			tokens = append(tokens, sourceToken{kind: tokenPunctuation, text: punctuation, line: line})
			index += len(punctuation)
			lineHasOnlyWhitespace = false
		}
	}
	return tokens, nil
}

func cPunctuator(data []byte) string {
	for _, candidate := range []string{
		">>=", "<<=", "...", "->", "++", "--", "<<", ">>", "<=", ">=", "==", "!=",
		"&&", "||", "*=", "/=", "%=", "+=", "-=", "&=", "^=", "|=", "##",
	} {
		if len(data) >= len(candidate) && string(data[:len(candidate)]) == candidate {
			return candidate
		}
	}
	return string(data[:1])
}

func isIdentifierStart(value byte) bool {
	return value == '_' || isASCIILetter(value)
}

func isIdentifierContinue(value byte) bool {
	return isIdentifierStart(value) || isASCIIDigit(value)
}

func isASCIILetter(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isASCIIDigit(value byte) bool {
	return value >= '0' && value <= '9'
}

type annotation struct {
	kind   string
	tokens []sourceToken
	line   int
}

type sourceFunction struct {
	name            string
	line            int
	parameterTokens []sourceToken
	parameters      string
	bodyEnd         int
}

func parseSource(path string, data []byte, state *parserState) (parsedSource, error) {
	tokens, err := lexSource(path, data)
	if err != nil {
		return parsedSource{}, err
	}
	result := parsedSource{}
	var pending []annotation
	braceDepth := 0
	parenthesisDepth := 0
	bracketDepth := 0
	for index := 0; index < len(tokens); {
		current := tokens[index]
		if braceDepth > 0 {
			switch current.text {
			case "{":
				braceDepth++
			case "}":
				braceDepth--
			}
			index++
			continue
		}
		if parenthesisDepth > 0 || bracketDepth > 0 {
			switch current.text {
			case "(":
				parenthesisDepth++
			case ")":
				parenthesisDepth--
			case "[":
				bracketDepth++
			case "]":
				bracketDepth--
			}
			index++
			continue
		}
		switch current.text {
		case "{":
			braceDepth = 1
			index++
			continue
		case "(":
			parenthesisDepth = 1
			index++
			continue
		case "[":
			bracketDepth = 1
			index++
			continue
		case "}":
			return parsedSource{}, sourceError(
				ErrUnsupportedSyntax, "unbalanced_file_scope_brace", path, current.line,
				"unexpected closing brace at file scope",
			)
		}
		if current.kind == tokenIdentifier &&
			(current.text == "TEST_CASE" || current.text == "TEST_RANGE" || current.text == "TEST_MATRIX") {
			if current.text == "TEST_MATRIX" {
				return parsedSource{}, sourceError(
					ErrUnsupportedSyntax, "unsupported_parameter_macro", path, current.line,
					"TEST_MATRIX is outside the Phase 4 Unity grammar",
				)
			}
			parsed, next, err := parseAnnotation(path, tokens, index)
			if err != nil {
				return parsedSource{}, err
			}
			pending = append(pending, parsed)
			index = next
			continue
		}
		if current.kind == tokenIdentifier && current.text == "void" {
			function, definition, next, err := parseFunction(path, tokens, index)
			if err != nil {
				return parsedSource{}, err
			}
			if !definition {
				if function.name == "setUp" || function.name == "tearDown" ||
					strings.HasPrefix(function.name, "test_") || len(pending) > 0 {
					return parsedSource{}, sourceError(
						ErrUnsupportedSyntax, "test_without_definition", path, function.line,
						fmt.Sprintf("%s must be a function definition", function.name),
					)
				}
				index = next
				continue
			}
			if (function.name == "setUp" || function.name == "tearDown" || strings.HasPrefix(function.name, "test_")) &&
				len(pending) == 0 && hasDeclarationPrefix(tokens, index) {
				return parsedSource{}, sourceError(
					ErrUnsupportedSyntax, "qualified_test_function", path, function.line,
					"test functions must use the unqualified void declaration form",
				)
			}
			switch {
			case function.name == "setUp":
				if len(pending) > 0 || parameterCount(function.parameterTokens) != 0 {
					return parsedSource{}, sourceError(ErrUnsupportedSyntax, "invalid_setup", path, function.line, "setUp must have a void parameter list")
				}
				if result.setUp != nil {
					return parsedSource{}, sourceError(ErrDuplicateIdentity, "duplicate_hook", path, function.line, "setUp is declared more than once")
				}
				result.setUp = &SourceLocation{Path: path, Line: function.line}
			case function.name == "tearDown":
				if len(pending) > 0 || parameterCount(function.parameterTokens) != 0 {
					return parsedSource{}, sourceError(ErrUnsupportedSyntax, "invalid_teardown", path, function.line, "tearDown must have a void parameter list")
				}
				if result.tearDown != nil {
					return parsedSource{}, sourceError(ErrDuplicateIdentity, "duplicate_hook", path, function.line, "tearDown is declared more than once")
				}
				result.tearDown = &SourceLocation{Path: path, Line: function.line}
			case strings.HasPrefix(function.name, "test_"):
				cases, err := buildCases(path, function, pending, state)
				if err != nil {
					return parsedSource{}, err
				}
				result.cases = append(result.cases, cases...)
			default:
				if len(pending) > 0 {
					return parsedSource{}, sourceError(
						ErrUnsupportedSyntax, "annotation_without_test", path, pending[0].line,
						"parameter annotation must immediately precede a test_* function",
					)
				}
			}
			pending = nil
			index = function.bodyEnd + 1
			continue
		}
		if len(pending) > 0 {
			return parsedSource{}, sourceError(
				ErrUnsupportedSyntax, "annotation_without_test", path, pending[0].line,
				"parameter annotation must immediately precede a test_* function",
			)
		}
		index++
	}
	if len(pending) > 0 {
		return parsedSource{}, sourceError(
			ErrUnsupportedSyntax, "annotation_without_test", path, pending[0].line,
			"parameter annotation has no following test_* function",
		)
	}
	return result, nil
}

func parseAnnotation(path string, tokens []sourceToken, start int) (annotation, int, error) {
	name := tokens[start]
	if start+1 >= len(tokens) || tokens[start+1].text != "(" {
		return annotation{}, start, sourceError(
			ErrUnsupportedSyntax, "malformed_parameter_macro", path, name.line,
			fmt.Sprintf("%s must be followed by parentheses", name.text),
		)
	}
	close, err := matchingToken(tokens, start+1, "(", ")")
	if err != nil {
		return annotation{}, start, sourceError(ErrUnsupportedSyntax, "malformed_parameter_macro", path, name.line, err.Error())
	}
	if close == start+2 {
		return annotation{}, start, sourceError(ErrUnsupportedSyntax, "empty_parameter_macro", path, name.line, name.text+" must not be empty")
	}
	return annotation{kind: name.text, tokens: append([]sourceToken(nil), tokens[start+2:close]...), line: name.line}, close + 1, nil
}

func parseFunction(path string, tokens []sourceToken, start int) (sourceFunction, bool, int, error) {
	line := tokens[start].line
	if start+1 >= len(tokens) || tokens[start+1].kind != tokenIdentifier {
		return sourceFunction{line: line}, false, start + 1, nil
	}
	function := sourceFunction{name: tokens[start+1].text, line: tokens[start+1].line}
	if start+2 >= len(tokens) || tokens[start+2].text != "(" {
		return function, false, start + 2, nil
	}
	close, err := matchingToken(tokens, start+2, "(", ")")
	if err != nil {
		return sourceFunction{}, false, start, sourceError(ErrUnsupportedSyntax, "malformed_function", path, function.line, err.Error())
	}
	function.parameterTokens = append([]sourceToken(nil), tokens[start+3:close]...)
	function.parameters = canonicalTokens(function.parameterTokens)
	if parameterCount(function.parameterTokens) == 0 {
		function.parameters = "void"
	}
	next := close + 1
	if next >= len(tokens) || tokens[next].text != "{" {
		return function, false, next, nil
	}
	bodyEnd, err := matchingToken(tokens, next, "{", "}")
	if err != nil {
		return sourceFunction{}, false, start, sourceError(ErrUnsupportedSyntax, "malformed_function_body", path, function.line, err.Error())
	}
	function.bodyEnd = bodyEnd
	return function, true, bodyEnd + 1, nil
}

func hasDeclarationPrefix(tokens []sourceToken, voidIndex int) bool {
	for index := voidIndex - 1; index >= 0; index-- {
		switch tokens[index].text {
		case ";", "{", "}":
			return false
		default:
			return true
		}
	}
	return false
}

func matchingToken(tokens []sourceToken, start int, open, close string) (int, error) {
	if start >= len(tokens) || tokens[start].text != open {
		return 0, fmt.Errorf("expected %s", open)
	}
	depth := 0
	for index := start; index < len(tokens); index++ {
		switch tokens[index].text {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return index, nil
			}
		}
	}
	return 0, fmt.Errorf("missing closing %s", close)
}

func buildCases(path string, function sourceFunction, annotations []annotation, state *parserState) ([]TestCase, error) {
	if len(function.name) > state.limits.MaxCaseNameBytes {
		return nil, sourceError(
			ErrLimitExceeded, "case_name_too_long", path, function.line,
			fmt.Sprintf("case name exceeds %d UTF-8 bytes", state.limits.MaxCaseNameBytes),
		)
	}
	parameterCount := parameterCount(function.parameterTokens)
	if len(annotations) == 0 {
		if parameterCount != 0 {
			return nil, sourceError(
				ErrUnsupportedSyntax, "unannotated_parameters", path, function.line,
				"test_* functions with parameters require TEST_CASE or TEST_RANGE",
			)
		}
		if err := state.reserveCases(path, function.line, 1, false); err != nil {
			return nil, err
		}
		return []TestCase{{
			Name: function.name, Identity: function.name, Parameters: "void",
			Location: SourceLocation{Path: path, Line: function.line},
		}}, nil
	}
	if parameterCount == 0 {
		return nil, sourceError(
			ErrUnsupportedSyntax, "annotation_without_parameters", path, function.line,
			"parameter annotations require a function parameter list",
		)
	}

	var instances [][]string
	for _, current := range annotations {
		var expanded [][]string
		var err error
		switch current.kind {
		case "TEST_CASE":
			arguments, splitErr := splitArguments(current.tokens)
			if splitErr != nil {
				err = splitErr
			} else {
				expanded = [][]string{arguments}
			}
		case "TEST_RANGE":
			remaining := state.limits.MaxParameterInstances - state.parameterInstances - len(instances)
			if remaining <= 0 {
				return nil, sourceError(
					ErrLimitExceeded, "too_many_parameter_instances", path, current.line,
					fmt.Sprintf("parameter instance count exceeds %d", state.limits.MaxParameterInstances),
				)
			}
			expanded, err = expandRanges(current.tokens, remaining)
		default:
			err = fmt.Errorf("unsupported annotation %s", current.kind)
		}
		if err != nil {
			if errors.Is(err, errRangeLimit) {
				return nil, sourceError(
					ErrLimitExceeded, "too_many_parameter_instances", path, current.line,
					fmt.Sprintf("parameter instance count exceeds %d", state.limits.MaxParameterInstances),
				)
			}
			return nil, sourceError(ErrUnsupportedSyntax, "invalid_parameter_annotation", path, current.line, err.Error())
		}
		instances = append(instances, expanded...)
	}
	if len(instances) == 0 {
		return nil, sourceError(ErrUnsupportedSyntax, "empty_parameter_expansion", path, function.line, "parameter annotations produced no cases")
	}
	if err := state.reserveCases(path, function.line, len(instances), true); err != nil {
		return nil, err
	}

	cases := make([]TestCase, 0, len(instances))
	for _, arguments := range instances {
		if len(arguments) != parameterCount {
			return nil, sourceError(
				ErrUnsupportedSyntax, "parameter_arity_mismatch", path, function.line,
				fmt.Sprintf("function has %d parameters but annotation supplies %d arguments", parameterCount, len(arguments)),
			)
		}
		argumentBytes := 0
		for _, argument := range arguments {
			argumentBytes += len(argument)
		}
		if argumentBytes > state.limits.MaxParameterBytes {
			return nil, sourceError(
				ErrLimitExceeded, "parameter_value_too_large", path, function.line,
				fmt.Sprintf("parameter arguments exceed %d bytes", state.limits.MaxParameterBytes),
			)
		}
		identity := function.name + "(" + strings.Join(arguments, ", ") + ")"
		cases = append(cases, TestCase{
			Name: function.name, Identity: identity, Parameters: function.parameters,
			Arguments: append([]string(nil), arguments...),
			Location:  SourceLocation{Path: path, Line: function.line},
		})
	}
	return cases, nil
}

func (state *parserState) reserveCases(path string, line, count int, parameterized bool) error {
	if count < 0 || state.caseCount > state.limits.MaxCases-count {
		return sourceError(
			ErrLimitExceeded, "too_many_cases", path, line,
			fmt.Sprintf("case count exceeds %d", state.limits.MaxCases),
		)
	}
	if parameterized && (count > state.limits.MaxParameterInstances-state.parameterInstances) {
		return sourceError(
			ErrLimitExceeded, "too_many_parameter_instances", path, line,
			fmt.Sprintf("parameter instance count exceeds %d", state.limits.MaxParameterInstances),
		)
	}
	state.caseCount += count
	if parameterized {
		state.parameterInstances += count
	}
	return nil
}

func parameterCount(tokens []sourceToken) int {
	if len(tokens) == 0 || len(tokens) == 1 && tokens[0].kind == tokenIdentifier && tokens[0].text == "void" {
		return 0
	}
	parts, err := splitTokenGroups(tokens)
	if err != nil {
		return -1
	}
	return len(parts)
}

func splitArguments(tokens []sourceToken) ([]string, error) {
	parts, err := splitTokenGroups(tokens)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		value := canonicalTokens(part)
		if value == "" {
			return nil, errors.New("empty TEST_CASE argument")
		}
		result = append(result, value)
	}
	return result, nil
}

func splitTokenGroups(tokens []sourceToken) ([][]sourceToken, error) {
	if len(tokens) == 0 {
		return nil, errors.New("empty token group")
	}
	var result [][]sourceToken
	start := 0
	parentheses, brackets, braces := 0, 0, 0
	for index, current := range tokens {
		switch current.text {
		case "(":
			parentheses++
		case ")":
			parentheses--
		case "[":
			brackets++
		case "]":
			brackets--
		case "{":
			braces++
		case "}":
			braces--
		case ",":
			if parentheses == 0 && brackets == 0 && braces == 0 {
				if index == start {
					return nil, errors.New("empty comma-separated value")
				}
				result = append(result, append([]sourceToken(nil), tokens[start:index]...))
				start = index + 1
			}
		}
		if parentheses < 0 || brackets < 0 || braces < 0 {
			return nil, errors.New("unbalanced delimiter")
		}
	}
	if parentheses != 0 || brackets != 0 || braces != 0 || start >= len(tokens) {
		return nil, errors.New("unbalanced or empty comma-separated value")
	}
	result = append(result, append([]sourceToken(nil), tokens[start:]...))
	return result, nil
}

func canonicalTokens(tokens []sourceToken) string {
	var builder strings.Builder
	for index, current := range tokens {
		if index > 0 {
			previous := tokens[index-1].text
			switch {
			case current.text == ",":
			case previous == ",":
				builder.WriteByte(' ')
			case current.text == ")" || current.text == "]" || current.text == ">":
			case previous == "(" || previous == "[" || previous == "<":
			case (previous == "-" || previous == "+") && unarySignAt(tokens, index-1):
			default:
				builder.WriteByte(' ')
			}
		}
		builder.WriteString(current.text)
	}
	return builder.String()
}

func unarySignAt(tokens []sourceToken, index int) bool {
	if index == 0 {
		return true
	}
	switch tokens[index-1].text {
	case "(", "[", "{", ",", "=", "==", "!=", "<", ">", "<=", ">=", "&&", "||",
		"+", "-", "*", "/", "%", "&", "|", "^", "!", "~", "?", ":":
		return true
	default:
		return false
	}
}

type numericValue struct {
	value *big.Rat
	scale int
}

var errRangeLimit = errors.New("TEST_RANGE expansion exceeds the parameter instance limit")

func expandRanges(tokens []sourceToken, maximum int) ([][]string, error) {
	if maximum <= 0 {
		return nil, errRangeLimit
	}
	var ranges [][]string
	for index := 0; index < len(tokens); {
		if len(ranges) > 0 {
			if tokens[index].text != "," {
				return nil, errors.New("TEST_RANGE groups must be comma-separated")
			}
			index++
		}
		if index >= len(tokens) || tokens[index].text != "[" && tokens[index].text != "<" {
			return nil, errors.New("TEST_RANGE expects [start, end, step] or <start, end, step>")
		}
		open := tokens[index].text
		close := "]"
		if open == "<" {
			close = ">"
		}
		end := index + 1
		for end < len(tokens) && tokens[end].text != close {
			end++
		}
		if end >= len(tokens) {
			return nil, fmt.Errorf("TEST_RANGE is missing %s", close)
		}
		values, err := expandSingleRange(tokens[index+1:end], open == "<", maximum)
		if err != nil {
			return nil, err
		}
		ranges = append(ranges, values)
		index = end + 1
	}
	if len(ranges) == 0 {
		return nil, errors.New("TEST_RANGE has no ranges")
	}
	combinations := [][]string{{}}
	for _, values := range ranges {
		if len(values) == 0 || len(combinations) > maximum/len(values) {
			return nil, errRangeLimit
		}
		next := make([][]string, 0, len(combinations)*len(values))
		for _, combination := range combinations {
			for _, value := range values {
				item := append(append([]string(nil), combination...), value)
				next = append(next, item)
			}
		}
		combinations = next
	}
	return combinations, nil
}

func expandSingleRange(tokens []sourceToken, exclusive bool, maximum int) ([]string, error) {
	parts, err := splitTokenGroups(tokens)
	if err != nil || len(parts) != 3 {
		return nil, errors.New("TEST_RANGE requires exactly start, end, and step")
	}
	numbers := make([]numericValue, 3)
	scale := 0
	for index, part := range parts {
		numbers[index], err = parseNumericValue(part)
		if err != nil {
			return nil, err
		}
		if numbers[index].scale > scale {
			scale = numbers[index].scale
		}
	}
	start, end, step := numbers[0].value, numbers[1].value, numbers[2].value
	if step.Sign() == 0 {
		return nil, errors.New("TEST_RANGE step must not be zero")
	}
	if start.Cmp(end) < 0 && step.Sign() < 0 || start.Cmp(end) > 0 && step.Sign() > 0 {
		return nil, errors.New("TEST_RANGE step moves away from the end")
	}
	result := make([]string, 0)
	current := new(big.Rat).Set(start)
	for {
		comparison := current.Cmp(end)
		if step.Sign() > 0 {
			if comparison > 0 || exclusive && comparison == 0 {
				break
			}
		} else if comparison < 0 || exclusive && comparison == 0 {
			break
		}
		if len(result) >= maximum {
			return nil, errRangeLimit
		}
		result = append(result, formatNumericValue(current, scale))
		current = new(big.Rat).Add(current, step)
	}
	if len(result) == 0 {
		return nil, errors.New("TEST_RANGE produced no values")
	}
	return result, nil
}

func parseNumericValue(tokens []sourceToken) (numericValue, error) {
	if len(tokens) == 0 || len(tokens) > 2 {
		return numericValue{}, errors.New("TEST_RANGE values must be decimal numbers")
	}
	value := ""
	if len(tokens) == 2 {
		if tokens[0].text != "-" && tokens[0].text != "+" {
			return numericValue{}, errors.New("TEST_RANGE values only allow an optional sign")
		}
		value = tokens[0].text
		tokens = tokens[1:]
	}
	if tokens[0].kind != tokenNumber {
		return numericValue{}, errors.New("TEST_RANGE values must be decimal numbers")
	}
	value += tokens[0].text
	for index := 0; index < len(tokens[0].text); index++ {
		character := tokens[0].text[index]
		if !isASCIIDigit(character) && character != '.' {
			return numericValue{}, errors.New("TEST_RANGE values must be plain decimal numbers")
		}
	}
	if strings.HasPrefix(tokens[0].text, ".") {
		return numericValue{}, errors.New("TEST_RANGE values require a digit before the decimal point")
	}
	rational, ok := new(big.Rat).SetString(value)
	if !ok {
		return numericValue{}, fmt.Errorf("invalid TEST_RANGE number %q", value)
	}
	scale := 0
	if decimal := strings.IndexByte(tokens[0].text, '.'); decimal >= 0 {
		scale = len(tokens[0].text) - decimal - 1
	}
	return numericValue{value: rational, scale: scale}, nil
}

func formatNumericValue(value *big.Rat, scale int) string {
	if value.IsInt() {
		return value.Num().String()
	}
	encoded := value.FloatString(scale)
	encoded = strings.TrimRight(encoded, "0")
	encoded = strings.TrimRight(encoded, ".")
	if encoded == "-0" {
		return "0"
	}
	return encoded
}

func sourceError(cause error, code, path string, line int, message string) error {
	return &SourceDiagnostic{cause: cause, Code: code, Path: path, Line: line, Message: message}
}
