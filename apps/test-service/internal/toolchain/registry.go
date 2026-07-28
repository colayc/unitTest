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
				results <- adapterResult{index: index, instances: cloneInstances(instances), err: err}
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
	sort.Slice(results, func(left, right int) bool {
		return results[left].index < results[right].index
	})
	instances := make([]Instance, 0)
	issues := make([]Issue, 0)
	byID := make(map[string]int)
	byDescriptor := make(map[string]int)

	for _, result := range results {
		if result.err != nil {
			var carried issueCarrier
			if reflect.TypeOf(result.err) != nil && asIssueCarrier(result.err, &carried) {
				for _, issue := range carried.ToolchainIssues() {
					appendIssue(&issues, issue)
				}
			} else {
				appendIssue(&issues, Issue{
					Code:     "TOOLCHAIN_DISCOVERY_FAILED",
					Message:  fmt.Sprintf("toolchain adapter %d failed", result.index),
					Blocking: false,
				})
			}
		}
		for _, instance := range result.instances {
			if len(instances) >= maxRegistryResults {
				appendIssue(&issues, Issue{
					Code:     "TOOLCHAIN_LIMIT_EXCEEDED",
					Message:  fmt.Sprintf("toolchain discovery exceeded %d instances", maxRegistryResults),
					Blocking: true,
				})
				break
			}
			normalized, ok := normalizeInstance(instance)
			if !ok {
				appendIssue(&issues, Issue{
					Code:     "TOOLCHAIN_INVALID",
					Message:  fmt.Sprintf("toolchain adapter %d returned an invalid descriptor", result.index),
					Blocking: false,
				})
				continue
			}
			descriptor := descriptorKey(normalized)
			if existing, duplicate := byID[normalized.ID]; duplicate {
				if descriptorKey(instances[existing]) != descriptor {
					appendIssue(&issues, Issue{
						Code:     "TOOLCHAIN_ID_CONFLICT",
						Message:  fmt.Sprintf("toolchain id %q has conflicting descriptors", normalized.ID),
						Blocking: true,
					})
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
	return cloneInstances(instances), append([]Issue(nil), issues...)
}

func normalizeInstance(instance Instance) (Instance, bool) {
	if !validInstanceID(instance.ID) || !validFamily(instance.Family) ||
		instance.CCompiler == "" || instance.CXXCompiler == "" ||
		instance.Version == "" || instance.TargetTriple == "" ||
		instance.HostArchitecture == "" || instance.TargetArchitecture == "" {
		return Instance{}, false
	}
	instance.Environment = sortedUnique(instance.Environment)
	instance.Generators = sortedUnique(instance.Generators)
	for _, value := range instance.Environment {
		if value == "" || strings.IndexByte(value, 0) >= 0 || !strings.Contains(value, "=") {
			return Instance{}, false
		}
	}
	for _, value := range instance.Generators {
		if value != "Ninja" && value != "Unix Makefiles" &&
			value != "Visual Studio 17 2022" && value != "NMake Makefiles" {
			return Instance{}, false
		}
	}
	return instance, true
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
	}
	return strings.Join(values, "\x00")
}

func appendIssue(issues *[]Issue, issue Issue) {
	if len(*issues) >= maxRegistryIssues || issue.Code == "" || issue.Message == "" ||
		strings.IndexByte(issue.Code, 0) >= 0 || strings.IndexByte(issue.Message, 0) >= 0 {
		return
	}
	*issues = append(*issues, issue)
}

func validFamily(family Family) bool {
	return family == FamilyMSVC || family == FamilyClangCL || family == FamilyGCC || family == FamilyClang
}

func validInstanceID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
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
