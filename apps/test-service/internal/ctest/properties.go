package ctest

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

const (
	maxDescriptorEnvironmentEntries = 256
	maxDescriptorLabels             = 256
)

type Reason string

const (
	ReasonCommandNotTarget         Reason = "command_not_file_api_target"
	ReasonConfigurationMismatch    Reason = "configuration_mismatch"
	ReasonReservedArgument         Reason = "reserved_argument"
	ReasonExternalCommand          Reason = "blocked_external_command"
	ReasonUnsafeExecutable         Reason = "unsafe_executable"
	ReasonExternalWorkingDirectory Reason = "blocked_external_working_directory"
	ReasonMissingWorkingDirectory  Reason = "missing_working_directory"
	ReasonUnsupportedProperty      Reason = "unsupported_property"
	ReasonInvalidProperty          Reason = "invalid_property"
	ReasonDuplicateProperty        Reason = "duplicate_property"
)

type Compatibility struct {
	CaseLevel bool
	Reasons   []Reason
	RunSerial bool
}

type EnvironmentEntry struct {
	Name  string
	Value string
}

type EnvironmentModification struct {
	Name      string
	Operation string
	Value     string
}

type PropertySettings struct {
	WorkingDirectory         string
	Environment              []EnvironmentEntry
	EnvironmentModifications []EnvironmentModification
	TimeoutSeconds           *float64
	Labels                   []string
	Disabled                 bool
	SkipReturnCode           *int
}

func ClassifyProperties(properties []Property) (PropertySettings, Compatibility) {
	settings := PropertySettings{
		Environment:              []EnvironmentEntry{},
		EnvironmentModifications: []EnvironmentModification{},
		Labels:                   []string{},
	}
	compatibility := Compatibility{CaseLevel: true, Reasons: []Reason{}}
	seen := make(map[string]struct{}, len(properties))
	for _, property := range properties {
		if _, duplicate := seen[property.Name]; duplicate {
			addReason(&compatibility, ReasonDuplicateProperty)
			continue
		}
		seen[property.Name] = struct{}{}
		switch property.Name {
		case "WORKING_DIRECTORY":
			if property.Value.Kind != PropertyString || property.Value.String == "" {
				addReason(&compatibility, ReasonInvalidProperty)
				continue
			}
			settings.WorkingDirectory = property.Value.String
		case "ENVIRONMENT":
			entries, ok := parseEnvironment(property.Value)
			if !ok {
				addReason(&compatibility, ReasonInvalidProperty)
				continue
			}
			settings.Environment = entries
		case "ENVIRONMENT_MODIFICATION":
			modifications, ok := parseEnvironmentModifications(property.Value)
			if !ok {
				addReason(&compatibility, ReasonInvalidProperty)
				continue
			}
			settings.EnvironmentModifications = modifications
		case "TIMEOUT":
			value, ok := propertyFloat(property.Value)
			if !ok || value <= 0 {
				addReason(&compatibility, ReasonInvalidProperty)
				continue
			}
			settings.TimeoutSeconds = &value
		case "LABELS":
			if property.Value.Kind != PropertyStrings ||
				len(property.Value.Strings) > maxDescriptorLabels {
				addReason(&compatibility, ReasonInvalidProperty)
				continue
			}
			settings.Labels = sortedUnique(property.Value.Strings)
		case "DISABLED":
			if property.Value.Kind != PropertyBoolean {
				addReason(&compatibility, ReasonInvalidProperty)
				continue
			}
			settings.Disabled = property.Value.Boolean
		case "SKIP_RETURN_CODE":
			value, ok := propertyInteger(property.Value)
			if !ok || value < 0 || value > 255 {
				addReason(&compatibility, ReasonInvalidProperty)
				continue
			}
			settings.SkipReturnCode = &value
		case "RUN_SERIAL":
			if property.Value.Kind != PropertyBoolean {
				addReason(&compatibility, ReasonInvalidProperty)
				continue
			}
			compatibility.RunSerial = property.Value.Boolean
		default:
			addReason(&compatibility, ReasonUnsupportedProperty)
		}
	}
	finalizeCompatibility(&compatibility)
	return settings, compatibility
}

func parseEnvironment(value PropertyValue) ([]EnvironmentEntry, bool) {
	if value.Kind != PropertyStrings || len(value.Strings) > maxDescriptorEnvironmentEntries {
		return nil, false
	}
	result := make([]EnvironmentEntry, 0, len(value.Strings))
	seen := make(map[string]struct{}, len(value.Strings))
	for _, encoded := range value.Strings {
		name, contents, ok := strings.Cut(encoded, "=")
		if !ok || !validEnvironmentName(name) || reservedEnvironmentName(name) {
			return nil, false
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, false
		}
		seen[name] = struct{}{}
		result = append(result, EnvironmentEntry{Name: name, Value: contents})
	}
	return result, true
}

func parseEnvironmentModifications(value PropertyValue) ([]EnvironmentModification, bool) {
	if value.Kind != PropertyStrings || len(value.Strings) > maxDescriptorEnvironmentEntries {
		return nil, false
	}
	result := make([]EnvironmentModification, 0, len(value.Strings))
	for _, encoded := range value.Strings {
		name, operationAndValue, ok := strings.Cut(encoded, "=")
		if !ok || !validEnvironmentName(name) || reservedEnvironmentName(name) {
			return nil, false
		}
		operation, contents, ok := strings.Cut(operationAndValue, ":")
		if !ok || !validEnvironmentOperation(operation) ||
			(operation == "reset" || operation == "unset") && contents != "" {
			return nil, false
		}
		result = append(result, EnvironmentModification{
			Name: name, Operation: operation, Value: contents,
		})
	}
	return result, true
}

func validEnvironmentName(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range []byte(value) {
		if character >= 'A' && character <= 'Z' ||
			character >= 'a' && character <= 'z' ||
			index > 0 && character >= '0' && character <= '9' ||
			character == '_' {
			continue
		}
		return false
	}
	return true
}

func reservedEnvironmentName(value string) bool {
	upper := strings.ToUpper(value)
	return strings.HasPrefix(upper, "UTIDE_") ||
		strings.HasPrefix(upper, "UNIT_TEST_IDE_")
}

func validEnvironmentOperation(value string) bool {
	switch value {
	case "reset", "set", "unset", "string_append", "string_prepend",
		"path_list_append", "path_list_prepend",
		"cmake_list_append", "cmake_list_prepend":
		return true
	default:
		return false
	}
}

func propertyFloat(value PropertyValue) (float64, bool) {
	if value.Kind != PropertyNumber {
		return 0, false
	}
	result, err := strconv.ParseFloat(value.Number, 64)
	return result, err == nil && !math.IsInf(result, 0) && !math.IsNaN(result)
}

func propertyInteger(value PropertyValue) (int, bool) {
	if value.Kind != PropertyNumber || strings.ContainsAny(value.Number, ".eE") {
		return 0, false
	}
	result, err := strconv.ParseInt(value.Number, 10, 32)
	return int(result), err == nil
}

func sortedUnique(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	if len(result) == 0 {
		return []string{}
	}
	write := 1
	for read := 1; read < len(result); read++ {
		if result[read] == result[write-1] {
			continue
		}
		result[write] = result[read]
		write++
	}
	return result[:write]
}

func addReason(compatibility *Compatibility, reason Reason) {
	if !slicesContainsReason(compatibility.Reasons, reason) {
		compatibility.Reasons = append(compatibility.Reasons, reason)
	}
	compatibility.CaseLevel = false
}

func finalizeCompatibility(compatibility *Compatibility) {
	sort.Slice(compatibility.Reasons, func(first, second int) bool {
		return compatibility.Reasons[first] < compatibility.Reasons[second]
	})
	compatibility.CaseLevel = len(compatibility.Reasons) == 0
}

func slicesContainsReason(values []Reason, expected Reason) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
