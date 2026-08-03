package coveragemodelv1

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	maxSafeInteger     int64 = 9_007_199_254_740_991
	maxSafeIntegerText       = "9007199254740991"
)

var ErrInvalidDocument = errors.New("invalid Coverage JSON v1")

// Decode parses, validates, and defensively clones a Coverage JSON v1 document.
func Decode(data []byte) (CoverageDocumentV1, error) {
	if err := validateJSONStructure(data); err != nil {
		return CoverageDocumentV1{}, err
	}
	normalized, err := normalizeJSONNumbers(data)
	if err != nil {
		return CoverageDocumentV1{}, err
	}
	var value CoverageDocumentV1
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return CoverageDocumentV1{}, fmt.Errorf("%w: decode: %v", ErrInvalidDocument, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return CoverageDocumentV1{}, invalid("trailing JSON value")
	}
	if err := Validate(value); err != nil {
		return CoverageDocumentV1{}, err
	}
	return Clone(value), nil
}

func normalizeJSONNumbers(data []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, invalid("number")
	}
	if err := normalizeJSONValue(value); err != nil {
		return nil, err
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return nil, invalid("number")
	}
	return normalized, nil
}

func normalizeJSONValue(value any) error {
	switch current := value.(type) {
	case map[string]any:
		for name, child := range current {
			if number, ok := child.(json.Number); ok {
				integer, err := exactSafeInteger(number)
				if err != nil {
					return err
				}
				current[name] = integer
				continue
			}
			if err := normalizeJSONValue(child); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range current {
			if number, ok := child.(json.Number); ok {
				integer, err := exactSafeInteger(number)
				if err != nil {
					return err
				}
				current[index] = integer
				continue
			}
			if err := normalizeJSONValue(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func exactSafeInteger(number json.Number) (int64, error) {
	text := number.String()
	negative := strings.HasPrefix(text, "-")
	if negative {
		text = text[1:]
	}

	mantissa := text
	exponentText := "0"
	if exponentIndex := strings.IndexAny(text, "eE"); exponentIndex >= 0 {
		mantissa = text[:exponentIndex]
		exponentText = text[exponentIndex+1:]
	}
	fractionDigits := 0
	digits := mantissa
	if decimalIndex := strings.IndexByte(mantissa, '.'); decimalIndex >= 0 {
		fractionDigits = len(mantissa) - decimalIndex - 1
		digits = mantissa[:decimalIndex] + mantissa[decimalIndex+1:]
	}
	if strings.Trim(digits, "0") == "" {
		return 0, nil
	}
	if negative {
		return 0, invalid("number")
	}

	exponent, ok := new(big.Int).SetString(exponentText, 10)
	if !ok {
		return 0, invalid("number")
	}
	shift := new(big.Int).Sub(exponent, big.NewInt(int64(fractionDigits)))
	integerDigits := digits
	if shift.Sign() >= 0 {
		if !shift.IsInt64() || shift.Int64() > int64(len(maxSafeIntegerText)) {
			return 0, invalid("number")
		}
		integerDigits += strings.Repeat("0", int(shift.Int64()))
	} else {
		division := new(big.Int).Neg(shift)
		if !division.IsInt64() || division.Int64() >= int64(len(digits)) {
			return 0, invalid("number")
		}
		divisionDigits := int(division.Int64())
		for _, digit := range digits[len(digits)-divisionDigits:] {
			if digit != '0' {
				return 0, invalid("number")
			}
		}
		integerDigits = digits[:len(digits)-divisionDigits]
	}

	integerDigits = strings.TrimLeft(integerDigits, "0")
	if len(integerDigits) == 0 {
		return 0, nil
	}
	if len(integerDigits) > len(maxSafeIntegerText) ||
		(len(integerDigits) == len(maxSafeIntegerText) && integerDigits > maxSafeIntegerText) {
		return 0, invalid("number")
	}
	integer, err := strconv.ParseInt(integerDigits, 10, 64)
	if err != nil {
		return 0, invalid("number")
	}
	return integer, nil
}

type rawValidator func(*json.Decoder) error

func validateJSONStructure(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := validateRawDocument(decoder); err != nil {
		return invalid("structure")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return invalid("trailing JSON value")
	}
	return nil
}

func validateRawDocument(decoder *json.Decoder) error {
	return validateRawObject(decoder, map[string]rawValidator{
		"schemaVersion": validateRawString,
		"provenance":    validateRawProvenance,
		"completeness":  validateRawCompleteness,
		"summary":       validateRawSummary,
		"files":         validateRawFiles,
	})
}

func validateRawProvenance(decoder *json.Decoder) error {
	return validateRawObject(decoder, map[string]rawValidator{
		"platform":                     validateRawString,
		"architecture":                 validateRawString,
		"compiler":                     validateRawCompiler,
		"driver":                       validateRawDriver,
		"collector":                    validateRawCollector,
		"normalizerVersion":            validateRawString,
		"instrumentationFingerprint":   validateRawString,
	})
}

func validateRawCompiler(decoder *json.Decoder) error {
	return validateRawObject(decoder, map[string]rawValidator{"family": validateRawString, "version": validateRawString})
}

func validateRawDriver(decoder *json.Decoder) error {
	return validateRawObject(decoder, map[string]rawValidator{"name": validateRawString, "version": validateRawString})
}

func validateRawCollector(decoder *json.Decoder) error {
	return validateRawObject(decoder, map[string]rawValidator{"name": validateRawString, "version": validateRawString})
}

func validateRawCompleteness(decoder *json.Decoder) error {
	return validateRawObject(decoder, map[string]rawValidator{"outcome": validateRawString, "reasons": validateRawReasons})
}

func validateRawReasons(decoder *json.Decoder) error {
	return validateRawArray(decoder, validateRawString)
}

func validateRawSummary(decoder *json.Decoder) error {
	return validateRawObject(decoder, map[string]rawValidator{
		"lines": validateRawMetric, "branches": validateRawMetric, "functions": validateRawMetric,
	})
}

func validateRawMetric(decoder *json.Decoder) error {
	return validateRawObject(decoder, map[string]rawValidator{"covered": validateRawNumber, "total": validateRawNumber})
}

func validateRawFiles(decoder *json.Decoder) error {
	return validateRawArray(decoder, validateRawFile)
}

func validateRawFile(decoder *json.Decoder) error {
	return validateRawObject(decoder, map[string]rawValidator{
		"uri": validateRawString, "sha256": validateRawString, "summary": validateRawSummary, "lines": validateRawLines,
	})
}

func validateRawLines(decoder *json.Decoder) error {
	return validateRawArray(decoder, validateRawLine)
}

func validateRawLine(decoder *json.Decoder) error {
	return validateRawObject(decoder, map[string]rawValidator{
		"line": validateRawNumber, "count": validateRawNumber, "branches": validateRawMetric,
	})
}

func validateRawObject(decoder *json.Decoder, fields map[string]rawValidator) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return errors.New("expected object")
	}
	seen := make(map[string]struct{}, len(fields))
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return err
		}
		name, ok := token.(string)
		if !ok {
			return errors.New("expected field name")
		}
		validator, ok := fields[name]
		if !ok {
			return errors.New("unknown field")
		}
		if _, duplicate := seen[name]; duplicate {
			return errors.New("duplicate field")
		}
		seen[name] = struct{}{}
		if err := validator(decoder); err != nil {
			return err
		}
	}
	token, err = decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '}' || len(seen) != len(fields) {
		return errors.New("missing required field")
	}
	return nil
}

func validateRawArray(decoder *json.Decoder, element rawValidator) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '[' {
		return errors.New("expected array")
	}
	for decoder.More() {
		if err := element(decoder); err != nil {
			return err
		}
	}
	token, err = decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != ']' {
		return errors.New("expected array end")
	}
	return nil
}

func validateRawString(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if _, ok := token.(string); !ok {
		return errors.New("expected string")
	}
	return nil
}

func validateRawNumber(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if _, ok := token.(json.Number); !ok {
		return errors.New("expected number")
	}
	return nil
}

// Validate checks the schema-independent semantic rules for Coverage JSON v1.
func Validate(value CoverageDocumentV1) error {
	if value.SchemaVersion != The10 || !validProvenance(value.Provenance) ||
		!validCompleteness(value.Completeness) || len(value.Files) > 100_000 {
		return invalid("document header")
	}
	if err := validateSummary(value.Summary); err != nil {
		return err
	}

	aggregate := CoverageSummaryV1{}
	var previousURI string
	for index, file := range value.Files {
		if !canonicalURI(file.URI) || !lowerHex(file.Sha256, 64) ||
			(index > 0 && bytes.Compare([]byte(previousURI), []byte(file.URI)) >= 0) ||
			len(file.Lines) > 1_000_000 {
			return invalid("file identity or ordering")
		}
		previousURI = file.URI
		if err := validateSummary(file.Summary); err != nil {
			return err
		}

		var previousLine int64
		var coveredLines int64
		branches := CoverageMetricV1{}
		for _, line := range file.Lines {
			if line.Line < 1 || line.Line > maxSafeInteger || line.Line <= previousLine ||
				line.Count < 0 || line.Count > maxSafeInteger {
				return invalid("line identity or count")
			}
			previousLine = line.Line
			if line.Count > 0 {
				coveredLines++
			}
			if err := validateMetric(line.Branches); err != nil {
				return err
			}
			var err error
			branches, err = addMetric(branches, line.Branches)
			if err != nil {
				return err
			}
		}
		if file.Summary.Lines.Covered != coveredLines ||
			file.Summary.Lines.Total != int64(len(file.Lines)) ||
			file.Summary.Branches != branches {
			return invalid("file summary")
		}
		var err error
		aggregate, err = addSummary(aggregate, file.Summary)
		if err != nil {
			return err
		}
	}
	if aggregate != value.Summary {
		return invalid("document summary")
	}
	return nil
}

// Clone returns a document that shares no mutable slices with value.
func Clone(value CoverageDocumentV1) CoverageDocumentV1 {
	result := value
	result.Completeness.Reasons = slices.Clone(value.Completeness.Reasons)
	result.Files = make([]CoverageFileV1, len(value.Files))
	for index, file := range value.Files {
		result.Files[index] = file
		result.Files[index].Lines = slices.Clone(file.Lines)
	}
	return result
}

func validProvenance(value CoverageProvenanceV1) bool {
	return (value.Platform == Windows || value.Platform == Linux) &&
		(value.Architecture == X86 || value.Architecture == X64 || value.Architecture == Arm64) &&
		(value.Compiler.Family == GCC || value.Compiler.Family == Clang || value.Compiler.Family == ClangCl) &&
		(value.Driver.Name == Gcov || value.Driver.Name == FluffyLlvmCov) &&
		(value.Collector.Name == Gcovr || value.Collector.Name == PurpleLlvmCov) &&
		validVersion(value.Compiler.Version) && validVersion(value.Driver.Version) &&
		validVersion(value.Collector.Version) && validVersion(value.NormalizerVersion) &&
		lowerHex(value.InstrumentationFingerprint, 64)
}

func validCompleteness(value CoverageCompletenessV1) bool {
	if len(value.Reasons) > 64 {
		return false
	}
	seen := make(map[Reason]struct{}, len(value.Reasons))
	for _, reason := range value.Reasons {
		if reason != TestCrashed && reason != TestTimedOut && reason != ProfileMissingForFailedInvocation {
			return false
		}
		if _, duplicate := seen[reason]; duplicate {
			return false
		}
		seen[reason] = struct{}{}
	}
	return (value.Outcome == Available && len(value.Reasons) == 0) ||
		(value.Outcome == Partial && len(value.Reasons) > 0)
}

func validVersion(value string) bool {
	return len(value) >= 1 && len(value) <= 128 && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func validateSummary(value CoverageSummaryV1) error {
	for _, metric := range []CoverageMetricV1{value.Lines, value.Branches, value.Functions} {
		if err := validateMetric(metric); err != nil {
			return err
		}
	}
	return nil
}

func validateMetric(value CoverageMetricV1) error {
	if value.Covered < 0 || value.Total < 0 || value.Covered > value.Total || value.Total > maxSafeInteger {
		return invalid("metric")
	}
	return nil
}

func addMetric(first, second CoverageMetricV1) (CoverageMetricV1, error) {
	covered, err := addSafe(first.Covered, second.Covered)
	if err != nil {
		return CoverageMetricV1{}, err
	}
	total, err := addSafe(first.Total, second.Total)
	if err != nil {
		return CoverageMetricV1{}, err
	}
	result := CoverageMetricV1{Covered: covered, Total: total}
	if err := validateMetric(result); err != nil {
		return CoverageMetricV1{}, err
	}
	return result, nil
}

func addSummary(first, second CoverageSummaryV1) (CoverageSummaryV1, error) {
	lines, err := addMetric(first.Lines, second.Lines)
	if err != nil {
		return CoverageSummaryV1{}, err
	}
	branches, err := addMetric(first.Branches, second.Branches)
	if err != nil {
		return CoverageSummaryV1{}, err
	}
	functions, err := addMetric(first.Functions, second.Functions)
	if err != nil {
		return CoverageSummaryV1{}, err
	}
	return CoverageSummaryV1{Lines: lines, Branches: branches, Functions: functions}, nil
}

func addSafe(first, second int64) (int64, error) {
	if first < 0 || second < 0 || first > maxSafeInteger-second {
		return 0, invalid("unsafe aggregate")
	}
	return first + second, nil
}

func canonicalURI(value string) bool {
	if len(value) < 1 || len(value) > 4096 || !utf8.ValidString(value) || !norm.NFC.IsNormalString(value) ||
		strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\?#\x00") || strings.Contains(value, "//") ||
		windowsDrivePath(value) || uriScheme(value) {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func windowsDrivePath(value string) bool {
	return len(value) >= 2 && asciiLetter(value[0]) && value[1] == ':'
}

func uriScheme(value string) bool {
	if len(value) < 2 || !asciiLetter(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		if value[index] == ':' {
			return true
		}
		if !asciiLetter(value[index]) && (value[index] < '0' || value[index] > '9') && value[index] != '+' && value[index] != '-' && value[index] != '.' {
			return false
		}
	}
	return false
}

func asciiLetter(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func lowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' && (value[index] < 'a' || value[index] > 'f') {
			return false
		}
	}
	return true
}

func invalid(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidDocument, message)
}
