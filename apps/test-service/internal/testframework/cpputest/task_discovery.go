package cpputest

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

var _ testframework.TaskDiscoveryAdapter = (*Adapter)(nil)

func (adapter *Adapter) PrepareDiscovery(
	ctx context.Context,
	descriptor ctest.ExecutionDescriptor,
) (testframework.DiscoveryExecution, error) {
	if _, err := adapter.Verify(ctx, descriptor); err != nil {
		return testframework.DiscoveryExecution{}, err
	}
	environment, unset, err := discoveryEnvironmentDelta(descriptor)
	if err != nil {
		return testframework.DiscoveryExecution{}, err
	}
	arguments := append([]string(nil), descriptor.Arguments...)
	arguments = append(arguments, "-ln")
	return testframework.DiscoveryExecution{
		Process: task.ProcessSpec{
			Executable: descriptor.Executable.Path,
			Args:       arguments,
			Env:        environment,
			EnvUnset:   unset,
			Dir:        descriptor.WorkingDirectory,
		},
		Public: task.CommandSummary{
			Executable: filepath.Base(descriptor.Executable.Path),
			Args:       []string{"<service-owned-discovery-invocation>"},
		},
		Parser: &taskDiscoveryParser{
			descriptor: descriptor,
			limits:     adapter.listLimits,
		},
	}, nil
}

type taskDiscoveryParser struct {
	descriptor ctest.ExecutionDescriptor
	limits     Limits
	stdout     bytes.Buffer
	stderr     bytes.Buffer
	failed     bool
}

func (parser *taskDiscoveryParser) Feed(
	stream testframework.Stream,
	data []byte,
) error {
	if parser == nil || parser.failed {
		return ErrDiscoveryFailed
	}
	var target *bytes.Buffer
	switch stream {
	case testframework.StreamStdout:
		target = &parser.stdout
	case testframework.StreamStderr:
		target = &parser.stderr
	default:
		parser.failed = true
		return ErrDiscoveryFailed
	}
	if target.Len()+len(data) > parser.limits.MaxDocumentBytes {
		parser.failed = true
		return ErrDiscoveryFailed
	}
	_, _ = target.Write(data)
	return nil
}

func (parser *taskDiscoveryParser) Finish(
	ctx context.Context,
	result testframework.ProcessResult,
) (testframework.DiscoveryResult, error) {
	if parser == nil || parser.failed ||
		ctx == nil ||
		result.Termination != testframework.ProcessExited ||
		result.ExitCode != 0 ||
		len(bytes.TrimSpace(parser.stderr.Bytes())) != 0 {
		return testframework.DiscoveryResult{}, ErrDiscoveryFailed
	}
	if err := ctx.Err(); err != nil {
		return testframework.DiscoveryResult{}, err
	}
	if err := parser.descriptor.ValidateExecutable(); err != nil {
		return testframework.DiscoveryResult{}, err
	}
	identities, err := ParseList(
		bytes.NewReader(parser.stdout.Bytes()),
		parser.limits,
	)
	if err != nil {
		return testframework.DiscoveryResult{}, err
	}
	return testframework.DiscoveryResult{
		Items:       discoveredItems(identities),
		Diagnostics: []testdomain.Diagnostic{},
	}, nil
}

func discoveryEnvironmentDelta(
	descriptor ctest.ExecutionDescriptor,
) ([]string, []string, error) {
	final, err := discoveryEnvironment(descriptor)
	if err != nil {
		return nil, nil, err
	}
	baseline := make(map[string]environmentValue)
	for _, encoded := range os.Environ() {
		name, value, found := strings.Cut(encoded, "=")
		if !found || !validEnvironmentEntry(name, value) ||
			reservedEnvironmentName(name) {
			continue
		}
		baseline[environmentKey(name)] = environmentValue{
			name: name, value: value,
		}
	}
	current := make(map[string]environmentValue, len(final))
	for _, encoded := range final {
		name, value, found := strings.Cut(encoded, "=")
		if !found || reservedEnvironmentName(name) {
			continue
		}
		current[environmentKey(name)] = environmentValue{
			name: name, value: value,
		}
	}
	environment := make([]string, 0)
	for key, value := range current {
		if inherited, exists := baseline[key]; exists &&
			inherited.value == value.value {
			continue
		}
		environment = append(
			environment,
			value.name+"="+value.value,
		)
	}
	sort.Strings(environment)
	unset := make([]string, 0)
	for key, value := range baseline {
		if _, exists := current[key]; !exists {
			unset = append(unset, value.name)
		}
	}
	sort.Strings(unset)
	return environment, unset, nil
}
