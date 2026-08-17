package cpputest

import (
	"os"
	"runtime"
	"sort"
	"strings"

	"unit-test-ide.local/test-service/internal/ctest"
)

type environmentValue struct {
	name  string
	value string
}

func discoveryEnvironment(
	descriptor ctest.ExecutionDescriptor,
) ([]string, error) {
	current := make(map[string]environmentValue)
	for _, encoded := range os.Environ() {
		name, value, ok := strings.Cut(encoded, "=")
		if !ok || !validEnvironmentEntry(name, value) ||
			reservedEnvironmentName(name) {
			continue
		}
		key := environmentKey(name)
		entry := environmentValue{name: name, value: value}
		current[key] = entry
	}
	seen := make(map[string]struct{}, len(descriptor.Environment))
	for _, entry := range descriptor.Environment {
		if !validEnvironmentEntry(entry.Name, entry.Value) ||
			reservedEnvironmentName(entry.Name) {
			return nil, ErrIncompatibleDescriptor
		}
		key := environmentKey(entry.Name)
		if _, duplicate := seen[key]; duplicate {
			return nil, ErrIncompatibleDescriptor
		}
		seen[key] = struct{}{}
		current[key] = environmentValue{name: entry.Name, value: entry.Value}
	}
	// CTest applies ENVIRONMENT first. A later reset operation restores this
	// baseline and discards only earlier ENVIRONMENT_MODIFICATION operations.
	resetBaseline := cloneEnvironment(current)
	for _, modification := range descriptor.EnvironmentChanges {
		if !validEnvironmentEntry(modification.Name, modification.Value) ||
			reservedEnvironmentName(modification.Name) {
			return nil, ErrIncompatibleDescriptor
		}
		key := environmentKey(modification.Name)
		entry := current[key]
		if entry.name == "" {
			entry.name = modification.Name
		}
		switch modification.Operation {
		case "reset":
			if modification.Value != "" {
				return nil, ErrIncompatibleDescriptor
			}
			if value, exists := resetBaseline[key]; exists {
				current[key] = value
			} else {
				delete(current, key)
			}
		case "set":
			entry.value = modification.Value
			current[key] = entry
		case "unset":
			if modification.Value != "" {
				return nil, ErrIncompatibleDescriptor
			}
			delete(current, key)
		case "string_append":
			entry.value += modification.Value
			current[key] = entry
		case "string_prepend":
			entry.value = modification.Value + entry.value
			current[key] = entry
		case "path_list_append":
			entry.value = appendEnvironmentList(
				entry.value,
				modification.Value,
				string(os.PathListSeparator),
			)
			current[key] = entry
		case "path_list_prepend":
			entry.value = prependEnvironmentList(
				entry.value,
				modification.Value,
				string(os.PathListSeparator),
			)
			current[key] = entry
		case "cmake_list_append":
			entry.value = appendEnvironmentList(entry.value, modification.Value, ";")
			current[key] = entry
		case "cmake_list_prepend":
			entry.value = prependEnvironmentList(entry.value, modification.Value, ";")
			current[key] = entry
		default:
			return nil, ErrIncompatibleDescriptor
		}
	}
	keys := make([]string, 0, len(current))
	for key := range current {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		entry := current[key]
		result = append(result, entry.name+"="+entry.value)
	}
	return result, nil
}

func cloneEnvironment(
	value map[string]environmentValue,
) map[string]environmentValue {
	result := make(map[string]environmentValue, len(value))
	for key, entry := range value {
		result[key] = entry
	}
	return result
}

func validEnvironmentEntry(name, value string) bool {
	if name == "" || strings.ContainsAny(name, "=\x00") ||
		strings.ContainsRune(value, '\x00') {
		return false
	}
	for index, character := range []byte(name) {
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
		strings.HasPrefix(upper, "UNIT_TEST_IDE_") ||
		upper == "UNIT_TEST_SERVICE_TOKEN"
}

func environmentKey(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

func appendEnvironmentList(current, value, separator string) string {
	if current == "" {
		return value
	}
	if value == "" {
		return current
	}
	return current + separator + value
}

func prependEnvironmentList(current, value, separator string) string {
	if current == "" {
		return value
	}
	if value == "" {
		return current
	}
	return value + separator + current
}
