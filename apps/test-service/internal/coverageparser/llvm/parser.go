package llvm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const exportType = "llvm.coverage.json.export"

type boundedReader struct {
	reader    io.Reader
	remaining int64
	exceeded  bool
}

func (reader *boundedReader) Read(destination []byte) (int, error) {
	if reader.remaining == 0 {
		var probe [1]byte
		read, err := reader.reader.Read(probe[:])
		if read > 0 {
			reader.exceeded = true
			return 0, ErrLimitExceeded
		}
		return 0, err
	}
	if int64(len(destination)) > reader.remaining {
		destination = destination[:reader.remaining]
	}
	read, err := reader.reader.Read(destination)
	reader.remaining -= int64(read)
	return read, err
}

type parser struct {
	decoder *json.Decoder
	limits  Limits
	depth   int64

	files     int64
	functions int64
	lineItems int64
	branches  int64
}

type rawFile struct {
	path     string
	segments []segment
	branches []branch
}

type segment struct {
	line, column          int64
	count                 int64
	hasCount, region, gap bool
}

type region struct {
	lineStart, columnStart int64
	lineEnd, columnEnd     int64
	count                  int64
	fileID, expandedFileID int64
	kind                   int64
}

type branch struct {
	lineStart, columnStart int64
	lineEnd, columnEnd     int64
	trueCount, falseCount  int64
	fileID, expandedFileID int64
	kind                   int64
}

type rawFunction struct {
	name      string
	count     int64
	filenames []string
	regions   []region
}

type rawExport struct {
	version   string
	typeName  string
	files     []rawFile
	functions []rawFunction
}

// Parse consumes one bounded LLVM 18 JSON export. It uses Decoder.Token so
// duplicate fields and every nested type are checked before a model exists.
func Parse(source io.Reader, limits Limits) (Export, error) {
	if err := limits.validate(); err != nil {
		return Export{}, err
	}
	if source == nil {
		return Export{}, ErrInvalidExport
	}
	bounded := &boundedReader{reader: source, remaining: limits.MaxInputBytes}
	var captured bytes.Buffer
	decoder := json.NewDecoder(io.TeeReader(bounded, &captured))
	decoder.UseNumber()
	p := &parser{decoder: decoder, limits: limits}
	raw, parseErr := p.parseRoot()
	if parseErr == nil {
		if _, err := p.token(); !errors.Is(err, io.EOF) {
			parseErr = errors.New("trailing JSON value")
		}
	}
	if bounded.exceeded {
		return Export{}, ErrLimitExceeded
	}
	if !utf8.Valid(captured.Bytes()) {
		return Export{}, fmt.Errorf("%w: invalid UTF-8", ErrInvalidExport)
	}
	if parseErr != nil {
		if errors.Is(parseErr, ErrLimitExceeded) || errors.Is(parseErr, ErrInvalidLimits) {
			return Export{}, parseErr
		}
		return Export{}, fmt.Errorf("%w: %v", ErrInvalidExport, parseErr)
	}
	result, err := p.reduce(raw)
	if err != nil {
		if errors.Is(err, ErrLimitExceeded) {
			return Export{}, err
		}
		return Export{}, fmt.Errorf("%w: %v", ErrInvalidExport, err)
	}
	return result, nil
}

func (p *parser) parseRoot() (rawExport, error) {
	var result rawExport
	err := p.object(map[string]func() error{
		"version": func() error {
			value, err := p.string()
			result.version = value
			return err
		},
		"type": func() error {
			value, err := p.string()
			result.typeName = value
			return err
		},
		"data": func() error {
			return p.array(func() error {
				files, functions, err := p.parseData()
				result.files = append(result.files, files...)
				result.functions = append(result.functions, functions...)
				return err
			})
		},
	}, "version", "type", "data")
	if err != nil {
		return rawExport{}, err
	}
	if result.typeName != exportType || !supportedVersion(result.version) {
		return rawExport{}, errors.New("unsupported type or version")
	}
	return result, nil
}

func supportedVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		if _, err := strconv.ParseUint(part, 10, 31); err != nil {
			return false
		}
	}
	return parts[0] == "2"
}

func (p *parser) parseData() ([]rawFile, []rawFunction, error) {
	var files []rawFile
	var functions []rawFunction
	err := p.object(map[string]func() error{
		"files": func() error {
			return p.array(func() error {
				if err := p.increment(&p.files, p.limits.MaxFiles); err != nil {
					return err
				}
				file, err := p.parseFile()
				files = append(files, file)
				return err
			})
		},
		"functions": func() error {
			return p.array(func() error {
				if err := p.increment(&p.functions, p.limits.MaxFunctions); err != nil {
					return err
				}
				function, err := p.parseFunction()
				functions = append(functions, function)
				return err
			})
		},
		"totals": p.summary,
	}, "files", "functions", "totals")
	return files, functions, err
}

func (p *parser) parseFile() (rawFile, error) {
	var result rawFile
	err := p.object(map[string]func() error{
		"filename": func() error {
			value, err := p.string()
			result.path = value
			return err
		},
		"segments": func() error {
			return p.array(func() error {
				if err := p.increment(&p.lineItems, p.limits.MaxLines); err != nil {
					return err
				}
				value, err := p.segment()
				result.segments = append(result.segments, value)
				return err
			})
		},
		"branches": func() error {
			return p.array(func() error {
				if err := p.increment(&p.branches, p.limits.MaxBranches); err != nil {
					return err
				}
				value, err := p.branch()
				result.branches = append(result.branches, value)
				return err
			})
		},
		"mcdc_records": func() error { return p.mcdcRecords() },
		"expansions":   func() error { return p.expansions() },
		"summary":      p.summary,
	}, "filename", "segments", "branches", "mcdc_records", "summary")
	if err == nil && result.path == "" {
		err = errors.New("empty filename")
	}
	return result, err
}

func (p *parser) parseFunction() (rawFunction, error) {
	var result rawFunction
	err := p.object(map[string]func() error{
		"name": func() error {
			value, err := p.string()
			result.name = value
			return err
		},
		"count": func() error {
			value, err := p.integer()
			result.count = value
			return err
		},
		"filenames": func() error {
			return p.stringArray(&result.filenames, p.limits.MaxFiles)
		},
		"regions": func() error {
			return p.regionArray(&result.regions)
		},
		"branches":     func() error { return p.branchArray() },
		"mcdc_records": func() error { return p.mcdcRecords() },
	}, "name", "count", "filenames", "regions", "branches", "mcdc_records")
	if err == nil && (result.name == "" || len(result.filenames) == 0 || len(result.regions) == 0) {
		err = errors.New("incomplete function identity")
	}
	return result, err
}

func (p *parser) segment() (segment, error) {
	var result segment
	if err := p.open('['); err != nil {
		return result, err
	}
	var err error
	if result.line, err = p.integer(); err == nil {
		result.column, err = p.integer()
	}
	if err == nil {
		result.count, err = p.integer()
	}
	if err == nil {
		result.hasCount, err = p.boolean()
	}
	if err == nil {
		result.region, err = p.boolean()
	}
	if err == nil {
		result.gap, err = p.boolean()
	}
	if err != nil {
		return segment{}, err
	}
	if result.line < 1 || result.column < 1 {
		return segment{}, errors.New("invalid segment location")
	}
	if err := p.close(']'); err != nil {
		return segment{}, err
	}
	return result, nil
}

func (p *parser) region() (region, error) {
	var result region
	if err := p.open('['); err != nil {
		return result, err
	}
	values := []*int64{&result.lineStart, &result.columnStart, &result.lineEnd, &result.columnEnd,
		&result.count, &result.fileID, &result.expandedFileID, &result.kind}
	for _, destination := range values {
		value, err := p.integer()
		if err != nil {
			return region{}, err
		}
		*destination = value
	}
	if !validSourceRange(result.lineStart, result.columnStart, result.lineEnd, result.columnEnd) {
		return region{}, errors.New("invalid region range")
	}
	if result.kind > 6 {
		return region{}, errors.New("unsupported region kind")
	}
	if err := p.close(']'); err != nil {
		return region{}, err
	}
	return result, nil
}

func (p *parser) branch() (branch, error) {
	var result branch
	if err := p.open('['); err != nil {
		return result, err
	}
	values := []*int64{&result.lineStart, &result.columnStart, &result.lineEnd, &result.columnEnd,
		&result.trueCount, &result.falseCount, &result.fileID, &result.expandedFileID, &result.kind}
	for _, destination := range values {
		value, err := p.integer()
		if err != nil {
			return branch{}, err
		}
		*destination = value
	}
	if !validSourceRange(result.lineStart, result.columnStart, result.lineEnd, result.columnEnd) {
		return branch{}, errors.New("invalid branch range")
	}
	if result.kind != 4 && result.kind != 6 {
		return branch{}, errors.New("unsupported branch kind")
	}
	if err := p.close(']'); err != nil {
		return branch{}, err
	}
	return result, nil
}

func (p *parser) regionArray(destination *[]region) error {
	return p.array(func() error {
		if err := p.increment(&p.lineItems, p.limits.MaxLines); err != nil {
			return err
		}
		value, err := p.region()
		*destination = append(*destination, value)
		return err
	})
}

func (p *parser) branchArray() error {
	return p.array(func() error {
		if err := p.increment(&p.branches, p.limits.MaxBranches); err != nil {
			return err
		}
		_, err := p.branch()
		return err
	})
}

func (p *parser) mcdcRecords() error {
	return p.array(func() error {
		if err := p.increment(&p.branches, p.limits.MaxBranches); err != nil {
			return err
		}
		if err := p.open('['); err != nil {
			return err
		}
		values := make([]int64, 6)
		for index := range values {
			value, err := p.integer()
			if err != nil {
				return err
			}
			values[index] = value
		}
		if !validSourceRange(values[0], values[1], values[2], values[3]) {
			return errors.New("invalid MC/DC range")
		}
		if values[5] != 5 {
			return errors.New("unsupported MC/DC kind")
		}
		conditions := int64(0)
		if err := p.array(func() error {
			if err := p.increment(&conditions, p.limits.MaxBranches); err != nil {
				return err
			}
			_, err := p.boolean()
			return err
		}); err != nil {
			return err
		}
		return p.close(']')
	})
}

func (p *parser) expansions() error {
	count := int64(0)
	return p.array(func() error {
		if err := p.increment(&count, p.limits.MaxLines); err != nil {
			return err
		}
		return p.object(map[string]func() error{
			"filenames": func() error {
				var ignored []string
				return p.stringArray(&ignored, p.limits.MaxFiles)
			},
			"source_region": func() error {
				if err := p.increment(&p.lineItems, p.limits.MaxLines); err != nil {
					return err
				}
				_, err := p.region()
				return err
			},
			"target_regions": func() error {
				var ignored []region
				return p.regionArray(&ignored)
			},
			"branches": p.branchArray,
		}, "filenames", "source_region", "target_regions", "branches")
	})
}

func (p *parser) summary() error {
	return p.object(map[string]func() error{
		"lines":          func() error { return p.summaryMetric(false) },
		"functions":      func() error { return p.summaryMetric(false) },
		"instantiations": func() error { return p.summaryMetric(false) },
		"regions":        func() error { return p.summaryMetric(true) },
		"branches":       func() error { return p.summaryMetric(true) },
		"mcdc":           func() error { return p.summaryMetric(true) },
	}, "lines", "functions", "instantiations", "regions", "branches", "mcdc")
}

func (p *parser) summaryMetric(notCovered bool) error {
	var count, covered, uncovered int64
	handlers := map[string]func() error{
		"count": func() error {
			value, err := p.integer()
			count = value
			return err
		},
		"covered": func() error {
			value, err := p.integer()
			covered = value
			return err
		},
		"percent": p.percentage,
	}
	required := []string{"count", "covered", "percent"}
	if notCovered {
		handlers["notcovered"] = func() error {
			value, err := p.integer()
			uncovered = value
			return err
		}
		required = append(required, "notcovered")
	}
	if err := p.object(handlers, required...); err != nil {
		return err
	}
	if covered > count || (notCovered && uncovered != count-covered) {
		return errors.New("inconsistent coverage summary")
	}
	return nil
}

func validSourceRange(startLine, startColumn, endLine, endColumn int64) bool {
	if startLine < 1 || startColumn < 1 || endLine < 1 || endColumn < 1 {
		return false
	}
	return endLine > startLine || (endLine == startLine && endColumn > startColumn)
}

func (p *parser) percentage() error {
	token, err := p.token()
	if err != nil {
		return err
	}
	number, ok := token.(json.Number)
	if !ok {
		return errors.New("expected percentage")
	}
	value, err := strconv.ParseFloat(number.String(), 64)
	if err != nil || value < 0 || value > 100 {
		return errors.New("invalid percentage")
	}
	return nil
}

func (p *parser) stringArray(destination *[]string, maximum int64) error {
	count := int64(0)
	return p.array(func() error {
		if err := p.increment(&count, maximum); err != nil {
			return err
		}
		value, err := p.string()
		*destination = append(*destination, value)
		return err
	})
}

func (p *parser) object(handlers map[string]func() error, required ...string) error {
	if err := p.open('{'); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(handlers))
	for p.decoder.More() {
		name, err := p.string()
		if err != nil {
			return err
		}
		handler, known := handlers[name]
		if !known {
			return fmt.Errorf("unknown field %q", name)
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("duplicate field %q", name)
		}
		seen[name] = struct{}{}
		if err := handler(); err != nil {
			return err
		}
	}
	if err := p.close('}'); err != nil {
		return err
	}
	for _, name := range required {
		if _, exists := seen[name]; !exists {
			return fmt.Errorf("missing field %q", name)
		}
	}
	return nil
}

func (p *parser) array(element func() error) error {
	if err := p.open('['); err != nil {
		return err
	}
	for p.decoder.More() {
		if err := element(); err != nil {
			return err
		}
	}
	return p.close(']')
}

func (p *parser) open(want json.Delim) error {
	token, err := p.token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != want {
		return fmt.Errorf("expected %q", want)
	}
	p.depth++
	if p.depth > p.limits.MaxDepth {
		return ErrLimitExceeded
	}
	return nil
}

func (p *parser) close(want json.Delim) error {
	if p.decoder.More() {
		return errors.New("unexpected tuple item")
	}
	token, err := p.token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != want {
		return fmt.Errorf("expected %q", want)
	}
	p.depth--
	return nil
}

func (p *parser) token() (json.Token, error) {
	token, err := p.decoder.Token()
	if err != nil {
		return nil, err
	}
	if value, ok := token.(string); ok && int64(len(value)) > p.limits.MaxStringBytes {
		return nil, ErrLimitExceeded
	}
	return token, nil
}

func (p *parser) string() (string, error) {
	token, err := p.token()
	if err != nil {
		return "", err
	}
	value, ok := token.(string)
	if !ok || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return "", errors.New("expected valid string")
	}
	return value, nil
}

func (p *parser) integer() (int64, error) {
	token, err := p.token()
	if err != nil {
		return 0, err
	}
	number, ok := token.(json.Number)
	if !ok {
		return 0, errors.New("expected integer")
	}
	value, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil || value < 0 || value > maxSafeInteger {
		return 0, errors.New("invalid integer")
	}
	return value, nil
}

func (p *parser) boolean() (bool, error) {
	token, err := p.token()
	if err != nil {
		return false, err
	}
	value, ok := token.(bool)
	if !ok {
		return false, errors.New("expected boolean")
	}
	return value, nil
}

func (p *parser) increment(value *int64, maximum int64) error {
	*value++
	if *value > maximum {
		return ErrLimitExceeded
	}
	return nil
}

func (p *parser) reduce(raw rawExport) (Export, error) {
	result := Export{Version: raw.version, Files: make([]File, len(raw.files))}
	pathIndex := make(map[string]int, len(raw.files))
	var outputLines int64
	for index, rawFile := range raw.files {
		lines, err := p.reduceLines(rawFile, p.limits.MaxLines-outputLines)
		if err != nil {
			return Export{}, err
		}
		outputLines += int64(len(lines))
		result.Files[index] = File{NativePath: rawFile.path, Lines: lines}
		if _, exists := pathIndex[rawFile.path]; !exists {
			pathIndex[rawFile.path] = index
		}
	}

	type functionState struct {
		file    int
		covered bool
	}
	functions := make(map[string]functionState, len(raw.functions))
	for _, function := range raw.functions {
		var identityRegion region
		hasCodeRegion := false
		for _, candidate := range function.regions {
			if candidate.kind == 0 {
				identityRegion = candidate
				hasCodeRegion = true
				break
			}
		}
		if !hasCodeRegion {
			return Export{}, errors.New("function has no code region")
		}
		if identityRegion.fileID < 0 || identityRegion.fileID >= int64(len(function.filenames)) {
			return Export{}, errors.New("function file ID")
		}
		path := function.filenames[identityRegion.fileID]
		fileIndex, exists := pathIndex[path]
		if !exists {
			return Export{}, errors.New("function source is absent")
		}
		key := path + "\x00" + strconv.FormatInt(identityRegion.lineStart, 10) + "\x00" + function.name
		state, duplicate := functions[key]
		if !duplicate {
			state.file = fileIndex
		}
		state.covered = state.covered || function.count > 0
		functions[key] = state
	}
	for _, function := range functions {
		metric := &result.Files[function.file].Functions
		metric.Total++
		if function.covered {
			metric.Covered++
		}
	}
	return result, nil
}

func (p *parser) reduceLines(file rawFile, lineBudget int64) ([]Line, error) {
	segments := append([]segment(nil), file.segments...)
	sort.SliceStable(segments, func(i, j int) bool {
		if segments[i].line != segments[j].line {
			return segments[i].line < segments[j].line
		}
		return segments[i].column < segments[j].column
	})
	counts := make(map[int64]int64)
	for index, current := range segments {
		if !current.hasCount || current.gap {
			continue
		}
		end := current.line
		if index+1 < len(segments) && segments[index+1].line > current.line {
			end = segments[index+1].line
			if segments[index+1].column == 1 {
				end--
			}
		}
		for line := current.line; line <= end; line++ {
			if int64(len(counts)) >= lineBudget {
				if _, exists := counts[line]; !exists {
					return nil, ErrLimitExceeded
				}
			}
			if current.count > counts[line] {
				counts[line] = current.count
			} else if _, exists := counts[line]; !exists {
				counts[line] = 0
			}
			if line == maxSafeInteger {
				break
			}
		}
	}

	type branchIdentity struct {
		lineStart, columnStart, lineEnd, columnEnd int64
		kind                                       int64
	}
	deduplicated := make(map[branchIdentity]branch)
	for _, value := range file.branches {
		identity := branchIdentity{value.lineStart, value.columnStart, value.lineEnd, value.columnEnd,
			value.kind}
		current := deduplicated[identity]
		if value.trueCount > current.trueCount {
			current.trueCount = value.trueCount
		}
		if value.falseCount > current.falseCount {
			current.falseCount = value.falseCount
		}
		current.lineStart = value.lineStart
		deduplicated[identity] = current
	}
	branchMetrics := make(map[int64]Metric)
	for _, value := range deduplicated {
		if _, exists := counts[value.lineStart]; !exists {
			return nil, errors.New("branch has no executable line")
		}
		metric := branchMetrics[value.lineStart]
		metric.Total += 2
		if value.trueCount > 0 {
			metric.Covered++
		}
		if value.falseCount > 0 {
			metric.Covered++
		}
		branchMetrics[value.lineStart] = metric
	}

	lineNumbers := make([]int64, 0, len(counts))
	for line := range counts {
		lineNumbers = append(lineNumbers, line)
	}
	sort.Slice(lineNumbers, func(i, j int) bool { return lineNumbers[i] < lineNumbers[j] })
	result := make([]Line, len(lineNumbers))
	for index, line := range lineNumbers {
		result[index] = Line{Number: line, Count: counts[line], Branches: branchMetrics[line]}
	}
	return result, nil
}
