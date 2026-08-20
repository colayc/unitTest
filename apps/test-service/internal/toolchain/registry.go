package toolchain

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
)

const (
	maxRegistryAdapters = 16
	maxRegistryWorkers  = 4
	maxRegistryResults  = 256
	maxRegistryIssues   = 256

	maxRegistryAdapterResults        = 256
	maxRegistryIDBytes               = 128
	maxRegistryPathBytes             = 4096
	maxRegistryVersionBytes          = 128
	maxRegistryTripleBytes           = 256
	maxRegistryArchitectureBytes     = 16
	maxRegistryEnvironmentEntries    = 256
	maxRegistryEnvironmentEntryBytes = 4096
	maxRegistryEnvironmentTotalBytes = 64 * 1024
	maxRegistryGeneratorEntries      = 4
	maxRegistryGeneratorEntryBytes   = 64
	maxRegistryGeneratorTotalBytes   = 256
	maxRegistryInstanceTotalBytes    = 128 * 1024
	maxRegistryIssueCodeBytes        = 64
	maxRegistryIssueMessageBytes     = 256
)

type Registry struct {
	adapters []Adapter
}

type adapterResult struct {
	index     int
	instances []Instance
	err       error
}

type issueCarrier interface {
	ToolchainIssues() []Issue
}

func NewRegistry(adapters ...Adapter) (*Registry, error) {
	if len(adapters) > maxRegistryAdapters {
		return nil, fmt.Errorf("%w: got %d adapters, maximum is %d", ErrInvalidRegistry, len(adapters), maxRegistryAdapters)
	}
	owned := make([]Adapter, len(adapters))
	seen := make(map[Adapter]struct{}, len(adapters))
	for index, adapter := range adapters {
		if nilAdapter(adapter) {
			return nil, fmt.Errorf("%w: adapter %d is nil", ErrInvalidRegistry, index)
		}
		if reflect.TypeOf(adapter).Comparable() {
			if _, duplicate := seen[adapter]; duplicate {
				return nil, fmt.Errorf("%w: adapter %d is duplicated", ErrInvalidRegistry, index)
			}
			seen[adapter] = struct{}{}
		}
		owned[index] = adapter
	}
	return &Registry{adapters: owned}, nil
}

func (registry *Registry) Discover(ctx context.Context) ([]Instance, []Issue) {
	if registry == nil || ctx == nil || ctx.Err() != nil {
		return nil, nil
	}
	if len(registry.adapters) == 0 {
		return []Instance{}, []Issue{}
	}

	jobs := make(chan int)
	results := make(chan adapterResult, len(registry.adapters))
	workerCount := min(len(registry.adapters), maxRegistryWorkers)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for index := range jobs {
				instances, err := registry.adapters[index].Discover(ctx)
				results <- adapterResult{index: index, instances: instances, err: err}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index := range registry.adapters {
			select {
			case jobs <- index:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()

	collected := make([]adapterResult, 0, len(registry.adapters))
	for {
		select {
		case <-ctx.Done():
			return nil, nil
		case result, open := <-results:
			if !open {
				return normalizeRegistryResults(collected)
			}
			collected = append(collected, result)
		}
	}
}

func normalizeRegistryResults(results []adapterResult) ([]Instance, []Issue) {
	for _, result := range results {
		if isContextError(result.err) {
			return nil, nil
		}
	}
	sort.Slice(results, func(left, right int) bool {
		return results[left].index < results[right].index
	})
	instances := make([]Instance, 0)
	issues := make([]Issue, 0)
	byID := make(map[string]int)
	byDescriptor := make(map[string]int)

	resultLimitReached := false
	issueLimitReached := false
	for _, result := range results {
		if result.err != nil {
			var carried issueCarrier
			if reflect.TypeOf(result.err) != nil && asIssueCarrier(result.err, &carried) {
				for _, issue := range carried.ToolchainIssues() {
					if !appendIssue(&issues, sanitizeCarrierIssue(result.index, issue)) {
						issueLimitReached = true
						break
					}
				}
			} else {
				if !appendIssue(&issues, Issue{
					Code:     "TOOLCHAIN_DISCOVERY_FAILED",
					Message:  fmt.Sprintf("toolchain adapter %d failed", result.index),
					Blocking: false,
				}) {
					issueLimitReached = true
				}
			}
		}
		if issueLimitReached {
			break
		}
		adapterInstances := result.instances
		if len(adapterInstances) > maxRegistryAdapterResults {
			adapterInstances = adapterInstances[:maxRegistryAdapterResults]
			if !appendIssue(&issues, Issue{
				Code:     "TOOLCHAIN_LIMIT_EXCEEDED",
				Message:  fmt.Sprintf("toolchain adapter %d result limit exceeded", result.index),
				Blocking: true,
			}) {
				break
			}
		}
		for _, instance := range adapterInstances {
			if len(instances) >= maxRegistryResults {
				appendIssue(&issues, Issue{
					Code:     "TOOLCHAIN_LIMIT_EXCEEDED",
					Message:  "toolchain result limit exceeded",
					Blocking: true,
				})
				resultLimitReached = true
				break
			}
			normalized, ok := normalizeInstance(instance)
			if !ok {
				if !appendIssue(&issues, Issue{
					Code:     "TOOLCHAIN_INVALID",
					Message:  fmt.Sprintf("toolchain adapter %d returned an invalid descriptor", result.index),
					Blocking: false,
				}) {
					issueLimitReached = true
					break
				}
				continue
			}
			descriptor := descriptorKey(normalized)
			if existing, duplicate := byID[normalized.ID]; duplicate {
				if descriptorKey(instances[existing]) != descriptor {
					if !appendIssue(&issues, Issue{
						Code:     "TOOLCHAIN_ID_CONFLICT",
						Message:  fmt.Sprintf("toolchain id %q has conflicting descriptors", normalized.ID),
						Blocking: true,
					}) {
						issueLimitReached = true
						break
					}
				}
				continue
			}
			if existing, duplicate := byDescriptor[descriptor]; duplicate {
				if normalized.ID < instances[existing].ID {
					delete(byID, instances[existing].ID)
					instances[existing] = normalized
					byID[normalized.ID] = existing
				}
				continue
			}
			byID[normalized.ID] = len(instances)
			byDescriptor[descriptor] = len(instances)
			instances = append(instances, normalized)
		}
		if resultLimitReached || issueLimitReached {
			break
		}
	}

	sort.Slice(instances, func(left, right int) bool {
		a, b := instances[left], instances[right]
		return lessStrings(
			[]string{
				string(a.Family), a.TargetTriple, a.Version, identityPath(a.CCompiler),
				identityPath(a.Coverage.LLVMProfdata), identityPath(a.Coverage.LLVMCov),
				identityPath(a.Coverage.GCov), a.ID,
			},
			[]string{
				string(b.Family), b.TargetTriple, b.Version, identityPath(b.CCompiler),
				identityPath(b.Coverage.LLVMProfdata), identityPath(b.Coverage.LLVMCov),
				identityPath(b.Coverage.GCov), b.ID,
			},
		)
	})
	sort.Slice(issues, func(left, right int) bool {
		a, b := issues[left], issues[right]
		return lessStrings(
			[]string{a.Code, a.Message, fmt.Sprint(a.Blocking)},
			[]string{b.Code, b.Message, fmt.Sprint(b.Blocking)},
		)
	})
	return instances, append([]Issue(nil), issues...)
}

func normalizeInstance(instance Instance) (Instance, bool) {
	if !boundedString(instance.ID, maxRegistryIDBytes) ||
		!validInstanceID(instance.ID) || !validFamily(instance.Family) ||
		instance.CCompiler == "" || instance.CXXCompiler == "" ||
		instance.Version == "" || instance.TargetTriple == "" ||
		instance.HostArchitecture == "" || instance.TargetArchitecture == "" ||
		!boundedString(instance.CCompiler, maxRegistryPathBytes) ||
		!boundedString(instance.CXXCompiler, maxRegistryPathBytes) ||
		!boundedString(instance.Version, maxRegistryVersionBytes) ||
		!boundedString(instance.TargetTriple, maxRegistryTripleBytes) ||
		!boundedString(instance.HostArchitecture, maxRegistryArchitectureBytes) ||
		!boundedString(instance.TargetArchitecture, maxRegistryArchitectureBytes) ||
		!boundedString(instance.Sysroot, maxRegistryPathBytes) ||
		!boundedString(instance.Coverage.LLVMProfdata, maxRegistryPathBytes) ||
		!boundedString(instance.Coverage.LLVMCov, maxRegistryPathBytes) ||
		!boundedString(instance.Coverage.GCov, maxRegistryPathBytes) ||
		!boundedString(instance.Coverage.CompilerEvidence.FileIdentity, 128) ||
		!boundedString(instance.Coverage.ProfdataEvidence.FileIdentity, 128) ||
		!boundedString(instance.Coverage.CovEvidence.FileIdentity, 128) ||
		!boundedString(instance.Coverage.CompilerEvidence.SHA256, 64) ||
		!boundedString(instance.Coverage.ProfdataEvidence.SHA256, 64) ||
		!boundedString(instance.Coverage.CovEvidence.SHA256, 64) ||
		!boundedString(instance.Coverage.ToolsetIdentity, 64) {
		return Instance{}, false
	}
	if instance.Family == FamilyClangCL && (instance.Coverage.LLVMProfdata != "" || instance.Coverage.LLVMCov != "") {
		paths := []string{instance.CCompiler, instance.Coverage.LLVMProfdata, instance.Coverage.LLVMCov}
		evidence := []ExecutableEvidence{instance.Coverage.CompilerEvidence, instance.Coverage.ProfdataEvidence, instance.Coverage.CovEvidence}
		if instance.Coverage.ToolsetIdentity == "" || LLVMToolsetIdentity(instance.Version, paths, evidence) != instance.Coverage.ToolsetIdentity {
			return Instance{}, false
		}
	}
	totalBytes := len(instance.ID) + len(instance.CCompiler) + len(instance.CXXCompiler) +
		len(instance.Version) + len(instance.TargetTriple) +
		len(instance.HostArchitecture) + len(instance.TargetArchitecture) +
		len(instance.Sysroot) + len(instance.Coverage.LLVMProfdata) +
		len(instance.Coverage.LLVMCov) + len(instance.Coverage.GCov) +
		len(instance.Coverage.CompilerEvidence.FileIdentity) + len(instance.Coverage.CompilerEvidence.SHA256) +
		len(instance.Coverage.ProfdataEvidence.FileIdentity) + len(instance.Coverage.ProfdataEvidence.SHA256) +
		len(instance.Coverage.CovEvidence.FileIdentity) + len(instance.Coverage.CovEvidence.SHA256) +
		len(instance.Coverage.ToolsetIdentity)
	environmentBytes, environmentOK := registryEnvironmentBytes(instance.Environment)
	if !environmentOK || len(instance.Generators) > maxRegistryGeneratorEntries {
		return Instance{}, false
	}
	generatorBytes := 0
	for _, value := range instance.Generators {
		if !boundedString(value, maxRegistryGeneratorEntryBytes) ||
			value != "Ninja" && value != "Unix Makefiles" &&
				value != "Visual Studio 17 2022" &&
				value != "Visual Studio 18 2026" &&
				value != "NMake Makefiles" {
			return Instance{}, false
		}
		generatorBytes += len(value)
		if generatorBytes > maxRegistryGeneratorTotalBytes {
			return Instance{}, false
		}
	}
	if totalBytes+environmentBytes+generatorBytes > maxRegistryInstanceTotalBytes {
		return Instance{}, false
	}
	instance.Environment = sortedUnique(instance.Environment)
	instance.Generators = sortedUnique(instance.Generators)
	return instance, true
}

func registryEnvironmentBytes(environment []string) (int, bool) {
	if len(environment) > maxRegistryEnvironmentEntries {
		return 0, false
	}
	total := 0
	for _, value := range environment {
		if !boundedString(value, maxRegistryEnvironmentEntryBytes) ||
			value == "" || !strings.Contains(value, "=") {
			return 0, false
		}
		total += len(value)
		if total > maxRegistryEnvironmentTotalBytes {
			return 0, false
		}
	}
	return total, true
}

func cloneInstances(instances []Instance) []Instance {
	if instances == nil {
		return nil
	}
	owned := make([]Instance, len(instances))
	for index := range instances {
		owned[index] = instances[index]
		owned[index].Environment = append([]string(nil), instances[index].Environment...)
		owned[index].Generators = append([]string(nil), instances[index].Generators...)
	}
	return owned
}

func descriptorKey(instance Instance) string {
	values := []string{
		string(instance.Family),
		identityPath(instance.CCompiler),
		identityPath(instance.CXXCompiler),
		instance.Version,
		instance.TargetTriple,
		instance.HostArchitecture,
		instance.TargetArchitecture,
		identityPath(instance.Sysroot),
		strings.Join(instance.Environment, "\x1f"),
		strings.Join(instance.Generators, "\x1f"),
		identityPath(instance.Coverage.LLVMProfdata),
		identityPath(instance.Coverage.LLVMCov),
		identityPath(instance.Coverage.GCov),
		instance.Coverage.CompilerEvidence.FileIdentity,
		instance.Coverage.CompilerEvidence.SHA256,
		instance.Coverage.ProfdataEvidence.FileIdentity,
		instance.Coverage.ProfdataEvidence.SHA256,
		instance.Coverage.CovEvidence.FileIdentity,
		instance.Coverage.CovEvidence.SHA256,
		instance.Coverage.ToolsetIdentity,
	}
	return strings.Join(values, "\x00")
}

func appendIssue(issues *[]Issue, issue Issue) bool {
	if len(*issues) >= maxRegistryIssues-1 {
		appendIssueLimit(issues)
		return false
	}
	if !boundedString(issue.Code, maxRegistryIssueCodeBytes) ||
		!boundedString(issue.Message, maxRegistryIssueMessageBytes) ||
		issue.Code == "" || issue.Message == "" {
		return true
	}
	*issues = append(*issues, issue)
	return true
}

func appendIssueLimit(issues *[]Issue) {
	limit := Issue{
		Code:     "TOOLCHAIN_LIMIT_EXCEEDED",
		Message:  "toolchain issue limit exceeded",
		Blocking: true,
	}
	if len(*issues) < maxRegistryIssues {
		*issues = append(*issues, limit)
		return
	}
	(*issues)[maxRegistryIssues-1] = limit
}

func boundedString(value string, maximum int) bool {
	return len(value) <= maximum && strings.IndexByte(value, 0) < 0
}

func sanitizeCarrierIssue(adapterIndex int, issue Issue) Issue {
	switch {
	case !validIssueCode(issue.Code):
		return invalidCarrierIssue(adapterIndex)
	case issue.Code == "BUILD_TOOL_NOT_FOUND":
		return Issue{
			Code:     issue.Code,
			Message:  "toolchain candidate has no verified Ninja or Make build tool",
			Blocking: false,
		}
	case issue.Code == "TOOLCHAIN_PROBE_FAILED":
		return Issue{
			Code:     issue.Code,
			Message:  "toolchain candidate probe failed",
			Blocking: false,
		}
	case issue.Code == "TOOLCHAIN_PAIR_MISMATCH":
		return Issue{
			Code:     issue.Code,
			Message:  "toolchain compiler pair is incompatible",
			Blocking: false,
		}
	case issue.Code == "TOOLCHAIN_LIMIT_EXCEEDED":
		return Issue{
			Code:     issue.Code,
			Message:  "toolchain discovery limit exceeded",
			Blocking: true,
		}
	case issue.Code == "TOOLCHAIN_ENVIRONMENT_INVALID":
		return Issue{
			Code:     issue.Code,
			Message:  "Windows toolchain environment is invalid",
			Blocking: false,
		}
	case issue.Code == "TOOLCHAIN_MANUAL_SELECTION_FAILED":
		return Issue{
			Code:     issue.Code,
			Message:  "manual Windows toolchain selection was not found",
			Blocking: false,
		}
	case issue.Code == "WINDOWS_BUILD_TOOL_NOT_FOUND":
		return Issue{
			Code:     issue.Code,
			Message:  "Windows toolchain has no verified build generator",
			Blocking: false,
		}
	default:
		return invalidCarrierIssue(adapterIndex)
	}
}

func invalidCarrierIssue(adapterIndex int) Issue {
	return Issue{
		Code:     "TOOLCHAIN_DISCOVERY_FAILED",
		Message:  fmt.Sprintf("toolchain adapter %d returned an invalid issue", adapterIndex),
		Blocking: false,
	}
}

func validIssueCode(code string) bool {
	if len(code) == 0 || len(code) > 64 || code[0] < 'A' || code[0] > 'Z' {
		return false
	}
	for _, character := range code[1:] {
		if character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '_' {
			continue
		}
		return false
	}
	return true
}

func validFamily(family Family) bool {
	return family == FamilyMSVC || family == FamilyClangCL || family == FamilyGCC || family == FamilyClang
}

func validInstanceID(value string) bool {
	if len(value) == 0 || len(value) > maxRegistryIDBytes {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func sortedUnique(values []string) []string {
	owned := append([]string(nil), values...)
	sort.Strings(owned)
	result := owned[:0]
	for _, value := range owned {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func lessStrings(left, right []string) bool {
	for index := range left {
		if left[index] != right[index] {
			return left[index] < right[index]
		}
	}
	return false
}

func identityPath(value string) string {
	if value == "" {
		return ""
	}
	normalized := filepath.ToSlash(filepath.Clean(value))
	if runtime.GOOS == "windows" {
		normalized = strings.ToLower(normalized)
	}
	return normalized
}

func nilAdapter(adapter Adapter) bool {
	if adapter == nil {
		return true
	}
	value := reflect.ValueOf(adapter)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func asIssueCarrier(err error, destination *issueCarrier) bool {
	for err != nil {
		if carrier, ok := err.(issueCarrier); ok {
			*destination = carrier
			return true
		}
		type unwrapper interface{ Unwrap() error }
		next, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = next.Unwrap()
	}
	return false
}
