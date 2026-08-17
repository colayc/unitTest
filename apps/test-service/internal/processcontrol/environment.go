package processcontrol

import (
	"os"
	"sort"
	"strings"
)

// environSnapshot is deliberately consulted at launch time by
// SanitizeEnvironment. Tests replace it with a barrier to prove the policy is
// applied in the same critical section as the environment snapshot.
var environSnapshot = os.Environ

func hostileEnvironmentKey(key string) bool {
	upper := strings.ToUpper(key)
	return strings.HasPrefix(upper, "PYTHON") ||
		strings.HasPrefix(upper, "PIP_") ||
		strings.HasPrefix(upper, "CONDA_") ||
		upper == "VIRTUAL_ENV" ||
		strings.HasSuffix(upper, "_PROXY") ||
		upper == "HTTP_PROXY" || upper == "HTTPS_PROXY" || upper == "ALL_PROXY" ||
		upper == "LANG" || upper == "LANGUAGE" || strings.HasPrefix(upper, "LC_")
}

func serviceOwnedEnvironmentKey(key string) bool {
	upper := strings.ToUpper(key)
	return strings.HasPrefix(upper, "UTIDE_") ||
		strings.HasPrefix(upper, "UNIT_TEST_IDE_") ||
		upper == "UNIT_TEST_SERVICE_TOKEN"
}

// SanitizeEnvironment snapshots the host environment at invocation and
// removes hostile families case-insensitively, including all original key
// spellings. Service-owned control variables are retained.
func SanitizeEnvironment(extra, unset []string) []string {
	removed := make(map[string]struct{}, len(unset))
	for _, key := range unset {
		removed[strings.ToUpper(key)] = struct{}{}
	}
	overrides := make(map[string]string, len(extra))
	for _, entry := range extra {
		key, _, ok := strings.Cut(entry, "=")
		if ok && !hostileEnvironmentKey(key) && !serviceOwnedEnvironmentKey(key) {
			overrides[strings.ToUpper(key)] = entry
		}
	}
	inherited := environSnapshot()
	result := make([]string, 0, len(inherited)+len(extra))
	seen := make(map[string]struct{}, len(inherited)+len(extra))
	for _, entry := range inherited {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || hostileEnvironmentKey(key) || serviceOwnedEnvironmentKey(key) {
			continue
		}
		upper := strings.ToUpper(key)
		if _, ok := removed[upper]; ok {
			continue
		}
		if override, ok := overrides[upper]; ok {
			result = append(result, override)
			seen[upper] = struct{}{}
			continue
		}
		if _, ok := seen[upper]; ok {
			continue
		}
		seen[upper] = struct{}{}
		result = append(result, entry)
	}
	for _, entry := range extra {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || hostileEnvironmentKey(key) || serviceOwnedEnvironmentKey(key) {
			continue
		}
		upper := strings.ToUpper(key)
		if _, ok := removed[upper]; ok {
			continue
		}
		if _, ok := seen[upper]; ok {
			continue
		}
		seen[upper] = struct{}{}
		result = append(result, entry)
	}
	sort.SliceStable(result, func(i, j int) bool { return strings.ToUpper(result[i]) < strings.ToUpper(result[j]) })
	return result
}
