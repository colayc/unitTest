package coveragebundle

import (
	"errors"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"

	"unit-test-ide.local/test-service/internal/task"
)

// Execution is the runtime-only handoff from the coverage bundle package to
// build.ExecutionBoundary. It intentionally exposes only a fixed ProcessSpec
// and verification/ownership methods; no protocol type carries these fields.
type Execution interface {
	ProcessSpec() task.ProcessSpec
	Verify() error
	VerifyAfter() error
	ValidateProcessTarget(executable string, args, env, envUnset []string, dir string) error
	Close() error
}

type PreparedExecution struct {
	mu             sync.Mutex
	pin            Pin
	install        Installation
	descriptor     *OwnedDescriptor
	descriptorPath string
	spec           task.ProcessSpec
	closed         bool
}

func PrepareRunner(pin Pin, coverageRoot, taskID string, input DescriptorInput, capabilities DescriptorCapabilities) (*PreparedExecution, error) {
	if isNilPin(pin) {
		return nil, ErrBundleIntegrity
	}
	if err := pin.Verify(); err != nil {
		return nil, err
	}
	install := pin.Installation()
	if err := validateInstallation(install); err != nil {
		return nil, err
	}
	descriptor, err := NewDescriptor(input.Root, input.ObjectDirectory, input.GcovExecutable, input.OutputPath)
	if err != nil {
		return nil, err
	}
	owned, err := descriptor.WriteAtomic(coverageRoot, taskID, capabilities)
	if err != nil {
		closeDescriptorCapabilities(capabilities)
		return nil, err
	}
	spec := task.ProcessSpec{
		Executable: install.Python,
		Args:       []string{"-I", "-S", install.Runner, owned.Path()},
		EnvUnset:   fixedRunnerEnvUnset(),
		Dir:        owned.TaskRoot(),
	}
	execution := &PreparedExecution{pin: pin, install: install, descriptor: owned, descriptorPath: owned.Path(), spec: spec}
	if err := execution.Verify(); err != nil {
		_ = execution.Close()
		return nil, err
	}
	return execution, nil
}

func closeDescriptorCapabilities(capabilities DescriptorCapabilities) {
	if capabilities.GcovExecutable != nil {
		_ = capabilities.GcovExecutable.Close()
	}
	if capabilities.ObjectDirectory != nil {
		_ = capabilities.ObjectDirectory.Close()
	}
	if capabilities.Root != nil {
		_ = capabilities.Root.Close()
	}
	if capabilities.CoverageRoot != nil {
		_ = capabilities.CoverageRoot.Close()
	}
	if capabilities.Provenance != nil {
		_ = capabilities.Provenance.Close()
	}
}

func (execution *PreparedExecution) ProcessSpec() task.ProcessSpec {
	if execution == nil {
		return task.ProcessSpec{}
	}
	execution.mu.Lock()
	defer execution.mu.Unlock()
	spec := cloneProcessSpec(execution.spec)
	// Recompute the hostile-variable deny-list at launch/validation time;
	// environment policy is not a prepare-time snapshot.
	spec.EnvUnset = fixedRunnerEnvUnset()
	execution.spec.EnvUnset = append([]string(nil), spec.EnvUnset...)
	return spec
}

func (execution *PreparedExecution) DescriptorPath() string {
	if execution == nil {
		return ""
	}
	execution.mu.Lock()
	defer execution.mu.Unlock()
	return execution.descriptorPath
}

func (execution *PreparedExecution) TaskRoot() string {
	if execution == nil {
		return ""
	}
	execution.mu.Lock()
	defer execution.mu.Unlock()
	if execution.descriptor == nil {
		return ""
	}
	return execution.descriptor.TaskRoot()
}

func (execution *PreparedExecution) Descriptor() Descriptor {
	if execution == nil {
		return Descriptor{}
	}
	execution.mu.Lock()
	defer execution.mu.Unlock()
	if execution.descriptor == nil {
		return Descriptor{}
	}
	return execution.descriptor.Descriptor()
}

func (execution *PreparedExecution) Verify() error {
	if execution == nil {
		return ErrBundleIntegrity
	}
	execution.mu.Lock()
	defer execution.mu.Unlock()
	if execution.closed || execution.pin == nil || execution.descriptor == nil {
		return ErrBundleIntegrity
	}
	if err := execution.pin.Verify(); err != nil {
		return err
	}
	if err := execution.descriptor.Verify(); err != nil {
		return err
	}
	install := execution.pin.Installation()
	if install != execution.install {
		return ErrBundleIntegrity
	}
	if execution.spec.Executable != install.Python || len(execution.spec.Args) != 4 ||
		execution.spec.Args[0] != "-I" || execution.spec.Args[1] != "-S" ||
		execution.spec.Args[2] != install.Runner || execution.spec.Args[3] != execution.descriptor.Path() ||
		execution.spec.Dir != execution.descriptor.TaskRoot() || len(execution.spec.Env) != 0 || len(execution.spec.Batch) != 0 ||
		!reflect.DeepEqual(execution.spec.EnvUnset, fixedRunnerEnvUnset()) {
		return ErrBundleIntegrity
	}
	return nil
}

func (execution *PreparedExecution) VerifyAfter() error {
	if err := execution.Verify(); err != nil {
		return err
	}
	execution.mu.Lock()
	if execution.closed || execution.descriptor == nil {
		execution.mu.Unlock()
		return ErrBundleIntegrity
	}
	descriptor := execution.descriptor
	execution.mu.Unlock()
	return descriptor.VerifyOutputAfter()
}

func (execution *PreparedExecution) ValidateProcessTarget(executable string, args, env, envUnset []string, dir string) error {
	if execution == nil {
		return ErrBundleIntegrity
	}
	if err := execution.Verify(); err != nil {
		return err
	}
	want := execution.ProcessSpec()
	if executable != want.Executable || !reflect.DeepEqual(args, want.Args) ||
		!reflect.DeepEqual(env, want.Env) || !reflect.DeepEqual(envUnset, want.EnvUnset) || dir != want.Dir {
		return ErrBundleIntegrity
	}
	return nil
}

func (execution *PreparedExecution) Close() error {
	if execution == nil {
		return nil
	}
	execution.mu.Lock()
	if execution.closed {
		execution.mu.Unlock()
		return nil
	}
	execution.closed = true
	descriptor := execution.descriptor
	pin := execution.pin
	execution.descriptor = nil
	execution.pin = nil
	execution.mu.Unlock()
	var result error
	if descriptor != nil {
		result = errors.Join(result, descriptor.Close())
	}
	if pin != nil {
		result = errors.Join(result, pin.Close())
	}
	return result
}

func validateInstallation(install Installation) error {
	for label, path := range map[string]string{
		"bundle root": install.Root, "python": install.Python, "runner": install.Runner,
	} {
		if err := validateAbsolutePath(path); err != nil {
			return integrityError(label, err)
		}
	}
	if install.PythonVersion == "" || install.GcovrVersion != "8.6" || len(install.ManifestSHA256) != 64 {
		return integrityError("installation identity", errors.New("invalid pinned identity"))
	}
	return nil
}

func isNilPin(pin Pin) bool {
	if pin == nil {
		return true
	}
	value := reflect.ValueOf(pin)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func fixedRunnerEnvUnset() []string {
	keys := map[string]struct{}{
		"PYTHONPATH": {}, "PYTHONHOME": {}, "PYTHONSTARTUP": {}, "PYTHONUSERBASE": {},
		"PYTHONWARNINGS": {}, "PYTHONBREAKPOINT": {}, "PYTHONINSPECT": {}, "PYTHONHASHSEED": {},
		"PYTHONUTF8": {}, "PIP_CONFIG_FILE": {}, "PIP_INDEX_URL": {}, "PIP_EXTRA_INDEX_URL": {},
		"PIP_TRUSTED_HOST": {}, "VIRTUAL_ENV": {}, "CONDA_PREFIX": {}, "CONDA_DEFAULT_ENV": {},
		"HTTP_PROXY": {}, "HTTPS_PROXY": {}, "ALL_PROXY": {}, "NO_PROXY": {},
		"LANG": {}, "LANGUAGE": {},
	}
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		upper := strings.ToUpper(key)
		if strings.HasPrefix(upper, "PYTHON") || strings.HasPrefix(upper, "PIP_") || strings.HasPrefix(upper, "CONDA_") ||
			strings.HasSuffix(upper, "_PROXY") || upper == "VIRTUAL_ENV" || upper == "LANG" || upper == "LANGUAGE" || strings.HasPrefix(upper, "LC_") {
			// Keep every original spelling.  Process environments on Unix can
			// contain both PYTHONPATH and pYtHoNpAtH; folding them would leave
			// one hostile entry available to Python.
			keys[key] = struct{}{}
		}
	}
	result := make([]string, 0, len(keys))
	for key := range keys {
		result = append(result, key)
	}
	sort.SliceStable(result, func(left, right int) bool {
		upperLeft, upperRight := strings.ToUpper(result[left]), strings.ToUpper(result[right])
		if upperLeft != upperRight {
			return upperLeft < upperRight
		}
		return result[left] < result[right]
	})
	return result
}

func cloneProcessSpec(spec task.ProcessSpec) task.ProcessSpec {
	return task.ProcessSpec{
		Executable: spec.Executable,
		Args:       append([]string(nil), spec.Args...), Env: append([]string(nil), spec.Env...),
		EnvUnset: append([]string(nil), spec.EnvUnset...), Dir: spec.Dir,
		Batch: append([]task.ProcessBatchItem(nil), spec.Batch...),
	}
}
