package coveragemodelv1

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const maxSafeInteger int64 = 9_007_199_254_740_991

var ErrInvalidDocument = errors.New("invalid Coverage JSON v1")

// Decode parses, validates, and defensively clones a Coverage JSON v1 document.
func Decode(data []byte) (CoverageDocumentV1, error) {
	var value CoverageDocumentV1
	decoder := json.NewDecoder(bytes.NewReader(data))
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
