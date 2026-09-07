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
	err := p.object(map[string]func() error{
		"file": func() error { value, err := p.string(); result.RelativePath = value; return err },
		"lines": func() error {
			return p.array(func() error {
				if err := p.increment(&p.lines, p.limits.MaxLines); err != nil {
					return err
				}
				line, err := p.line()
				result.Lines = append(result.Lines, line)
				return err
			})
		},
		"functions": func() error {
			return p.array(func() error {
				if err := p.increment(&p.functions, p.limits.MaxFunctions); err != nil {
					return err
				}
				covered, err := p.function()
				if covered {
					result.Functions.Covered++
				}
				result.Functions.Total++
				return err
			})
		},
		"gcovr/data_sources": p.skipStringArray,
	}, "file", "lines", "functions")
	if err != nil {
		return File{}, err
	}
	if !validRelativePath(result.RelativePath) {
		return File{}, errors.New("invalid source path")
	}
	seen := make(map[int64]struct{}, len(result.Lines))
	for _, line := range result.Lines {
		if _, ok := seen[line.Number]; ok {
			return File{}, errors.New("duplicate line")
		}
		seen[line.Number] = struct{}{}
	}
	return result, nil
}

func (p *parser) line() (Line, error) {
	var result Line
	err := p.object(map[string]func() error{
		"line_number": func() error { value, err := p.integer(); result.Number = value; return err },
		"count":       func() error { value, err := p.integer(); result.Count = value; return err },
		"branches": func() error {
			return p.array(func() error {
				if err := p.increment(&p.branches, p.limits.MaxBranches); err != nil {
					return err
				}
				covered, err := p.branch()
				if covered {
					result.Branches.Covered++
				}
				result.Branches.Total++
				return err
			})
		},
		"gcovr/noncode":      func() error { _, err := p.boolean(); return err },
		"function_name":      func() error { _, err := p.string(); return err },
		"block_ids":          p.skipIntegerArray,
		"conditions":         p.skipValue,
		"gcovr/decision":     p.skipValue,
		"calls":              p.skipValue,
		"gcovr/md5":          func() error { _, err := p.string(); return err },
		"gcovr/excluded":     func() error { _, err := p.boolean(); return err },
		"gcovr/data_sources": p.skipStringArray,
	}, "line_number", "count", "branches")
	if err != nil {
		return Line{}, err
	}
	if result.Number < 1 {
		return Line{}, errors.New("invalid line number")
	}
	return result, nil
}

func (p *parser) branch() (bool, error) {
	var count int64
	err := p.object(map[string]func() error{
		"count":           func() error { value, err := p.integer(); count = value; return err },
		"fallthrough":     func() error { _, err := p.boolean(); return err },
		"throw":           func() error { _, err := p.boolean(); return err },
		"branchno":        func() error { _, err := p.integer(); return err },
		"source_block_id": func() error { _, err := p.integer(); return err },
	}, "count", "fallthrough", "throw")
	return count > 0, err
}

func (p *parser) function() (bool, error) {
	var name string
	var line, count int64
	var percent float64
	hasCount := false
	err := p.object(map[string]func() error{
		"name": func() error { value, err := p.string(); name = value; return err },
		"demangled_name": func() error {
			value, err := p.string()
			if name == "" {
				name = value
			}
			return err
		},
		"lineno":             func() error { value, err := p.integer(); line = value; return err },
		"execution_count":    func() error { value, err := p.integer(); count = value; hasCount = true; return err },
		"blocks_percent":     func() error { value, err := p.number(); percent = value; return err },
		"pos":                p.skipStringArray,
		"gcovr/excluded":     func() error { _, err := p.boolean(); return err },
		"gcovr/data_sources": p.skipStringArray,
	}, "lineno", "blocks_percent")
	if err != nil {
		return false, err
	}
	if name == "" || line < 1 || percent < 0 || percent > 100 {
		return false, errors.New("invalid function")
	}
	if !hasCount {
		count = 0
	}
	return count > 0, nil
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
func (p *parser) skipStringArray() error {
	return p.array(func() error { _, err := p.string(); return err })
}
func (p *parser) skipIntegerArray() error {
	return p.array(func() error { _, err := p.integer(); return err })
}
func (p *parser) skipValue() error {
	token, err := p.token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	p.depth++
	if p.depth > p.limits.MaxDepth {
		return ErrLimitExceeded
	}
	for p.decoder.More() {
		if err := p.skipValue(); err != nil {
			return err
		}
	}
	end, err := p.token()
	if err != nil {
		return err
	}
	want := json.Delim('}')
	if delim == '[' {
		want = ']'
	}
	if actual, ok := end.(json.Delim); !ok || actual != want {
		return fmt.Errorf("expected %q", want)
	}
	p.depth--
	return nil
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
