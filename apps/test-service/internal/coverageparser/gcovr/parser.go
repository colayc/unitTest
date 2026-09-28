package gcovr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/coveragedomain"
)

const formatVersion = "0.14"

type boundedReader struct {
	reader io.Reader
	left   int64
	over   bool
}

func (r *boundedReader) Read(dst []byte) (int, error) {
	if r.left == 0 {
		var probe [1]byte
		n, err := r.reader.Read(probe[:])
		if n > 0 {
			r.over = true
			return 0, ErrLimitExceeded
		}
		return 0, err
	}
	if int64(len(dst)) > r.left {
		dst = dst[:r.left]
	}
	n, err := r.reader.Read(dst)
	r.left -= int64(n)
	return n, err
}

type parser struct {
	decoder                           *json.Decoder
	limits                            Limits
	depth                             int64
	files, functions, lines, branches int64
}

type rawFile struct {
	path      string
	lines     []Line
	functions Metric
}

type rawLineObservation struct {
	name     string
	line     coveragedomain.LineObservation
	branches []coveragedomain.BranchObservation
}

// Parse consumes exactly one gcovr 8.6 JSON export. It validates every token
// before constructing evidence, so malformed input cannot yield partial output.
func Parse(source io.Reader, limits Limits) (Export, error) {
	if err := limits.validate(); err != nil {
		return Export{}, err
	}
	if source == nil {
		return Export{}, ErrInvalidExport
	}
	bounded := &boundedReader{reader: source, left: limits.MaxInputBytes}
	var captured bytes.Buffer
	decoder := json.NewDecoder(io.TeeReader(bounded, &captured))
	decoder.UseNumber()
	p := parser{decoder: decoder, limits: limits}
	value, err := p.root()
	if err == nil {
		if _, trailing := p.token(); !errors.Is(trailing, io.EOF) {
			err = errors.New("trailing JSON value")
		}
	}
	if bounded.over {
		return Export{}, ErrLimitExceeded
	}
	if !utf8.Valid(captured.Bytes()) {
		return Export{}, fmt.Errorf("%w: invalid UTF-8", ErrInvalidExport)
	}
	if err != nil {
		if errors.Is(err, ErrLimitExceeded) || errors.Is(err, ErrInvalidLimits) {
			return Export{}, err
		}
		return Export{}, fmt.Errorf("%w: %v", ErrInvalidExport, err)
	}
	return value, nil
}

func (p *parser) root() (Export, error) {
	var result Export
	var files []File
	err := p.object(map[string]func() error{
		"gcovr/format_version": func() error { value, err := p.string(); result.FormatVersion = value; return err },
		"files": func() error {
			return p.array(func() error {
				if err := p.increment(&p.files, p.limits.MaxFiles); err != nil {
					return err
				}
				file, err := p.file()
				files = append(files, file)
				return err
			})
		},
	}, "gcovr/format_version", "files")
	if err != nil {
		return Export{}, err
	}
	if result.FormatVersion != formatVersion {
		return Export{}, errors.New("unsupported gcovr format version")
	}
	result.Files = files
	return result, nil
}

func (p *parser) file() (File, error) {
	var result File
	var observations []rawLineObservation
	err := p.object(map[string]func() error{
		"file": func() error { value, err := p.string(); result.RelativePath = value; return err },
		"lines": func() error {
			return p.array(func() error {
				if err := p.increment(&p.lines, p.limits.MaxLines); err != nil {
					return err
				}
				line, detail, included, err := p.line()
				if included {
					result.Lines = append(result.Lines, line)
					observations = append(observations, detail)
				}
				return err
			})
		},
		"functions": func() error {
			return p.array(func() error {
				if err := p.increment(&p.functions, p.limits.MaxFunctions); err != nil {
					return err
				}
				function, included, err := p.function()
				if included && function.ExecutionCount > 0 {
					result.Functions.Covered++
				}
				if included {
					result.Functions.Total++
					result.Observations = append(result.Observations, function)
				}
				return err
			})
		},
		"gcovr/data_sources": p.dataSources,
	}, "file", "lines", "functions")
	if err != nil {
		return File{}, err
	}
	if !validRelativePath(result.RelativePath) {
		return File{}, errors.New("invalid source path")
	}
	// A line's function_name is only a reliable association when it names
	// exactly one retained function in this file. Duplicate names remain
	// separate observations with no invented per-function line attribution.
	byName := make(map[string][]int, len(result.Observations))
	for index := range result.Observations {
		function := &result.Observations[index]
		function.File = result.RelativePath
		for _, name := range []string{function.LinkageName, function.QualifiedName} {
			if name == "" {
				continue
			}
			indices := byName[name]
			if len(indices) == 0 || indices[len(indices)-1] != index {
				byName[name] = append(indices, index)
			}
		}
	}
	for _, detail := range observations {
		indices := byName[detail.name]
		if len(indices) == 1 {
			function := &result.Observations[indices[0]]
			function.Lines = append(function.Lines, detail.line)
			function.Branches = append(function.Branches, detail.branches...)
		} else if len(indices) > 1 {
			for _, index := range indices {
				result.Observations[index].IncompleteReason = coveragedomain.ObservationIncompleteAttributionAmbiguous
			}
		}
	}
	ordinals := make(map[string]int64)
	for index := range result.Observations {
		function := &result.Observations[index]
		name := function.LinkageName
		if name == "" {
			name = function.QualifiedName
		}
		key := name + "\x00" + strconv.FormatInt(function.Start.Line, 10) + "\x00" + strconv.FormatInt(function.Start.Column, 10)
		function.InstantiationOrdinal = ordinals[key]
		ordinals[key]++
	}
	// gcovr can emit more than one record for a source line when the line is
	// represented by multiple functions or data sources. The public model is
	// line-based, so coalesce those records deterministically instead of
	// rejecting an otherwise valid export. Counts and branch metrics use the
	// maximum observed value to avoid double-counting overlapping records.
	coalesced := make([]Line, 0, len(result.Lines))
	indices := make(map[int64]int, len(result.Lines))
	for _, line := range result.Lines {
		if index, ok := indices[line.Number]; ok {
			current := &coalesced[index]
			if line.Count > current.Count {
				current.Count = line.Count
			}
			if line.Branches.Covered > current.Branches.Covered {
				current.Branches.Covered = line.Branches.Covered
			}
			if line.Branches.Total > current.Branches.Total {
				current.Branches.Total = line.Branches.Total
			}
			continue
		}
		indices[line.Number] = len(coalesced)
		coalesced = append(coalesced, line)
	}
	result.Lines = coalesced
	return result, nil
}

func (p *parser) line() (Line, rawLineObservation, bool, error) {
	var result Line
	var detail rawLineObservation
	excluded := false
	err := p.object(map[string]func() error{
		"line_number": func() error { value, err := p.integer(); result.Number = value; return err },
		"count":       func() error { value, err := p.integer(); result.Count = value; return err },
		"branches": func() error {
			return p.array(func() error {
				if err := p.increment(&p.branches, p.limits.MaxBranches); err != nil {
					return err
				}
				branch, included, err := p.branch()
				if included && branch.Count > 0 {
					result.Branches.Covered++
				}
				if included {
					result.Branches.Total++
					detail.branches = append(detail.branches, branch)
				}
				return err
			})
		},
		"function_name":      func() error { value, err := p.string(); detail.name = value; return err },
		"block_ids":          p.integerArray,
		"conditions":         p.conditions,
		"gcovr/decision":     p.decision,
		"calls":              p.calls,
		"gcovr/md5":          func() error { _, err := p.string(); return err },
		"gcovr/excluded":     func() error { value, err := p.boolean(); excluded = value; return err },
		"gcovr/data_sources": p.dataSources,
	}, "line_number", "count", "branches")
	if err != nil {
		return Line{}, rawLineObservation{}, false, err
	}
	if result.Number < 1 {
		return Line{}, rawLineObservation{}, false, errors.New("invalid line number")
	}
	detail.line = coveragedomain.LineObservation{Line: result.Number, Count: result.Count}
	for index := range detail.branches {
		detail.branches[index].Line = result.Number
	}
	return result, detail, !excluded, nil
}

func (p *parser) branch() (coveragedomain.BranchObservation, bool, error) {
	var result coveragedomain.BranchObservation
	excluded := false
	err := p.object(map[string]func() error{
		"count":                func() error { value, err := p.integer(); result.Count = value; return err },
		"fallthrough":          func() error { _, err := p.boolean(); return err },
		"throw":                func() error { _, err := p.boolean(); return err },
		"branchno":             func() error { value, err := p.integer(); result.Ordinal = value; result.HasOrdinal = true; return err },
		"source_block_id":      func() error { _, err := p.integer(); return err },
		"destination_block_id": func() error { _, err := p.integer(); return err },
		"gcovr/excluded":       func() error { value, err := p.boolean(); excluded = value; return err },
		"gcovr/data_sources":   p.dataSources,
	}, "count", "fallthrough", "throw")
	return result, !excluded, err
}

func (p *parser) function() (coveragedomain.FunctionObservation, bool, error) {
	var name, demangled string
	var line, count int64
	var start, end coveragedomain.SourceLocation
	var percent float64
	hasCount := false
	excluded := false
	err := p.object(map[string]func() error{
		"name": func() error { value, err := p.string(); name = value; return err },
		"demangled_name": func() error {
			value, err := p.string()
			demangled = value
			return err
		},
		"lineno":             func() error { value, err := p.integer(); line = value; return err },
		"execution_count":    func() error { value, err := p.integer(); count = value; hasCount = true; return err },
		"blocks_percent":     func() error { value, err := p.number(); percent = value; return err },
		"pos":                func() error { var err error; start, end, err = p.position(); return err },
		"gcovr/excluded":     func() error { value, err := p.boolean(); excluded = value; return err },
		"gcovr/data_sources": p.dataSources,
	})
	if err != nil {
		return coveragedomain.FunctionObservation{}, false, err
	}
	if name == "" && demangled == "" || percent < 0 || percent > 100 {
		return coveragedomain.FunctionObservation{}, false, errors.New("invalid function")
	}
	if !hasCount {
		// gcovr omits execution metrics for <unknown function>; retaining it
		// would fabricate an uncovered function in the public summary.
		return coveragedomain.FunctionObservation{}, false, nil
	}
	if start.Line == 0 {
		start.Line = line
	}
	if demangled == "" {
		demangled = name
	}
	return coveragedomain.FunctionObservation{
		QualifiedName: demangled, LinkageName: name,
		Start: start, End: end, ExecutionCount: count,
	}, !excluded, nil
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
		handler, ok := handlers[name]
		if !ok {
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
		if _, ok := seen[name]; !ok {
			return fmt.Errorf("missing field %q", name)
		}
	}
	return nil
}

func (p *parser) array(item func() error) error {
	if err := p.open('['); err != nil {
		return err
	}
	for p.decoder.More() {
		if err := item(); err != nil {
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
	delim, ok := token.(json.Delim)
	if !ok || delim != want {
		return fmt.Errorf("expected %q", want)
	}
	p.depth++
	if p.depth > p.limits.MaxDepth {
		return ErrLimitExceeded
	}
	return nil
}
func (p *parser) close(want json.Delim) error {
	token, err := p.token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != want {
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
	if !ok || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
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
func (p *parser) number() (float64, error) {
	token, err := p.token()
	if err != nil {
		return 0, err
	}
	number, ok := token.(json.Number)
	if !ok {
		return 0, errors.New("expected number")
	}
	value, err := strconv.ParseFloat(number.String(), 64)
	if err != nil {
		return 0, errors.New("invalid number")
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
func (p *parser) dataSources() error {
	var count int64
	return p.array(func() error {
		if err := p.increment(&count, p.limits.MaxFiles); err != nil {
			return err
		}
		_, err := p.string()
		return err
	})
}

func (p *parser) integerArray() error {
	var count int64
	return p.array(func() error {
		if err := p.increment(&count, p.limits.MaxBranches); err != nil {
			return err
		}
		_, err := p.integer()
		return err
	})
}

func (p *parser) position() (coveragedomain.SourceLocation, coveragedomain.SourceLocation, error) {
	var count int64
	var positions [2]coveragedomain.SourceLocation
	err := p.array(func() error {
		if err := p.increment(&count, 2); err != nil {
			return err
		}
		value, err := p.string()
		if err != nil {
			return err
		}
		position, valid := parsePosition(value)
		if !valid {
			return errors.New("invalid function position")
		}
		positions[count-1] = position
		return nil
	})
	if err != nil {
		return coveragedomain.SourceLocation{}, coveragedomain.SourceLocation{}, err
	}
	if count != 2 || positions[1].Line < positions[0].Line ||
		positions[1].Line == positions[0].Line && positions[1].Column < positions[0].Column {
		return coveragedomain.SourceLocation{}, coveragedomain.SourceLocation{}, errors.New("invalid function position")
	}
	return positions[0], positions[1], nil
}

func (p *parser) conditions() error {
	return p.array(func() error {
		if err := p.increment(&p.branches, p.limits.MaxBranches); err != nil {
			return err
		}
		var count, covered int64
		err := p.object(map[string]func() error{
			"conditionno":        func() error { _, err := p.integer(); return err },
			"count":              func() error { value, err := p.integer(); count = value; return err },
			"covered":            func() error { value, err := p.integer(); covered = value; return err },
			"not_covered_false":  p.integerArray,
			"not_covered_true":   p.integerArray,
			"gcovr/excluded":     func() error { _, err := p.boolean(); return err },
			"gcovr/data_sources": p.dataSources,
		}, "conditionno", "count", "covered", "not_covered_false", "not_covered_true")
		if err != nil {
			return err
		}
		if covered > count {
			return errors.New("invalid condition")
		}
		return nil
	})
}

func (p *parser) decision() error {
	var kind string
	var trueCount, falseCount, count int64
	var hasTrue, hasFalse, hasCount bool
	err := p.object(map[string]func() error{
		"type":               func() error { value, err := p.string(); kind = value; return err },
		"count_true":         func() error { value, err := p.integer(); trueCount = value; hasTrue = true; return err },
		"count_false":        func() error { value, err := p.integer(); falseCount = value; hasFalse = true; return err },
		"count":              func() error { value, err := p.integer(); count = value; hasCount = true; return err },
		"gcovr/data_sources": p.dataSources,
	}, "type")
	if err != nil {
		return err
	}
	switch kind {
	case "uncheckable":
		if hasTrue || hasFalse || hasCount {
			return errors.New("invalid uncheckable decision")
		}
	case "conditional":
		if !hasTrue || !hasFalse || hasCount || trueCount > maxSafeInteger || falseCount > maxSafeInteger {
			return errors.New("invalid conditional decision")
		}
	case "switch":
		if !hasCount || hasTrue || hasFalse || count > maxSafeInteger {
			return errors.New("invalid switch decision")
		}
	default:
		return errors.New("unsupported decision")
	}
	return nil
}

func (p *parser) calls() error {
	return p.array(func() error {
		if err := p.increment(&p.branches, p.limits.MaxBranches); err != nil {
			return err
		}
		return p.object(map[string]func() error{
			"callno":               func() error { _, err := p.integer(); return err },
			"source_block_id":      func() error { _, err := p.integer(); return err },
			"destination_block_id": func() error { _, err := p.integer(); return err },
			"returned":             func() error { _, err := p.integer(); return err },
			"gcovr/excluded":       func() error { _, err := p.boolean(); return err },
			"gcovr/data_sources":   p.dataSources,
		}, "returned")
	})
}

func parsePosition(value string) (coveragedomain.SourceLocation, bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return coveragedomain.SourceLocation{}, false
	}
	var result coveragedomain.SourceLocation
	for index, part := range parts {
		number, err := strconv.ParseInt(part, 10, 64)
		if err != nil || number < 1 || number > maxSafeInteger {
			return coveragedomain.SourceLocation{}, false
		}
		if index == 0 {
			result.Line = number
		} else {
			result.Column = number
		}
	}
	return result, true
}
func (p *parser) increment(value *int64, maximum int64) error {
	*value++
	if *value > maximum {
		return ErrLimitExceeded
	}
	return nil
}

func validRelativePath(path string) bool {
	if path == "" || !utf8.ValidString(path) || strings.ContainsRune(path, 0) || strings.Contains(path, "\\") || strings.HasPrefix(path, "/") {
		return false
	}
	for _, component := range strings.Split(path, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}
