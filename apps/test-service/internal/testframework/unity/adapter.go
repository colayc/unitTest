package unity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
	"unit-test-ide.local/test-service/internal/unityrunner"
	"unit-test-ide.local/test-service/internal/workspace"
)

const (
	ContractVersion         = testframework.UnityRunnerV1
	defaultDiscoveryTimeout = 5 * time.Second
	maxManifestBytes        = 8 * 1024 * 1024
	maxControlFileBytes     = 64 * 1024 * 1024
	maxCapturedOutputBytes  = 16 * 1024 * 1024
)

var (
	ErrInvalidAdapter         = errors.New("invalid Unity adapter")
	ErrDiscoveryFailed        = errors.New("Unity discovery failed")
	ErrInvalidManifest        = errors.New("invalid Unity adapter manifest")
	ErrInvalidProtocol        = errors.New("invalid utide.runner.v1 record")
	ErrProtocolLimit          = errors.New("utide.runner.v1 limit exceeded")
	ErrInvalidRunPlan         = errors.New("invalid Unity run plan")
	ErrIncompatibleDescriptor = errors.New("incompatible CTest execution descriptor")
	ErrReservedArguments      = errors.New("reserved Unity control argument conflict")
	ErrInvalidResult          = errors.New("invalid Unity result")
)

type Adapter struct {
	runner           probe.Runner
	allocator        testframework.ControlFileAllocator
	discoveryTimeout time.Duration
}

var _ testframework.Adapter = (*Adapter)(nil)

func NewAdapter(
	runner probe.Runner,
	allocator testframework.ControlFileAllocator,
) (*Adapter, error) {
	if nilInterface(runner) || nilInterface(allocator) {
		return nil, ErrInvalidAdapter
	}
	return &Adapter{
		runner:           runner,
		allocator:        allocator,
		discoveryTimeout: defaultDiscoveryTimeout,
	}, nil
}

func (*Adapter) Framework() testdomain.Framework {
	return testdomain.FrameworkUnity
}

func (*Adapter) ContractVersion() string {
	return ContractVersion
}

func (adapter *Adapter) Verify(
	ctx context.Context,
	descriptor ctest.ExecutionDescriptor,
) (testframework.Capabilities, error) {
	if !adapter.valid() || ctx == nil {
		return testframework.Capabilities{}, ErrInvalidAdapter
	}
	if err := ctx.Err(); err != nil {
		return testframework.Capabilities{}, err
	}
	if err := verifyDescriptor(descriptor); err != nil {
		return testframework.Capabilities{}, err
	}
	if _, err := processEnvironment(descriptor); err != nil {
		return testframework.Capabilities{}, err
	}
	if _, err := loadManifestEvidence(descriptor); err != nil {
		return testframework.Capabilities{}, err
	}
	return testframework.Capabilities{
		CanRunContainer:         true,
		CanDiscoverCases:        true,
		CanRunCase:              true,
		CanReportSkipped:        true,
		CanReportSourceLocation: true,
	}, nil
}

func (adapter *Adapter) Discover(
	ctx context.Context,
	descriptor ctest.ExecutionDescriptor,
) (testframework.DiscoveryResult, error) {
	if _, err := adapter.Verify(ctx, descriptor); err != nil {
		return testframework.DiscoveryResult{}, err
	}
	evidence, err := loadManifestEvidence(descriptor)
	if err != nil {
		return testframework.DiscoveryResult{}, err
	}
	control, err := adapter.allocator.Allocate(ctx)
	if err != nil {
		return testframework.DiscoveryResult{}, errors.Join(ErrDiscoveryFailed, err)
	}
	if err := validateControlFile(control); err != nil {
		return testframework.DiscoveryResult{}, err
	}
	environment, err := processEnvironment(descriptor)
	if err != nil {
		return testframework.DiscoveryResult{}, err
	}
	arguments := []string{
		"--utide-protocol", ContractVersion,
		"--utide-mode", "list",
		"--utide-result", control.Path(),
	}
	result, runErr := adapter.runner.Run(ctx, probe.Spec{
		Executable: descriptor.Executable.Path,
		Args:       arguments,
		Env:        environment,
		Dir:        descriptor.WorkingDirectory,
		Timeout:    adapter.discoveryTimeout,
		MaxOutput:  maxCapturedOutputBytes,
	})
	if verifyErr := descriptor.ValidateExecutable(); verifyErr != nil {
		return testframework.DiscoveryResult{}, verifyErr
	}
	if verifyErr := evidence.verify(); verifyErr != nil {
		return testframework.DiscoveryResult{}, verifyErr
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return testframework.DiscoveryResult{}, contextErr
	}
	if runErr != nil || result.ExitCode != 0 {
		return testframework.DiscoveryResult{}, errors.Join(
			ErrDiscoveryFailed,
			runErr,
		)
	}
	data, err := control.Read(ctx, maxControlFileBytes)
	if err != nil {
		return testframework.DiscoveryResult{}, errors.Join(ErrDiscoveryFailed, err)
	}
	if len(data) > maxControlFileBytes {
		return testframework.DiscoveryResult{}, ErrProtocolLimit
	}
	records, err := parseList(data, evidence.manifest)
	if err != nil {
		return testframework.DiscoveryResult{}, errors.Join(ErrDiscoveryFailed, err)
	}
	if verifyErr := evidence.verify(); verifyErr != nil {
		return testframework.DiscoveryResult{}, verifyErr
	}
	return testframework.DiscoveryResult{
		Items:       discoveredItems(records, evidence),
		Diagnostics: []testdomain.Diagnostic{},
	}, nil
}

func (adapter *Adapter) PlanRun(
	ctx context.Context,
	input testframework.RunInput,
) (testframework.RunPlan, error) {
	if _, err := adapter.Verify(ctx, input.Descriptor); err != nil {
		return testframework.RunPlan{}, err
	}
	evidence, err := loadManifestEvidence(input.Descriptor)
	if err != nil {
		return testframework.RunPlan{}, err
	}
	return buildRunPlan(ctx, input, evidence, adapter.allocator)
}

func (adapter *Adapter) NewParser(
	input testframework.ParseInput,
) (testframework.ResultParser, error) {
	if !adapter.valid() {
		return nil, ErrInvalidAdapter
	}
	if err := verifyDescriptor(input.Descriptor); err != nil {
		return nil, err
	}
	evidence, err := loadManifestEvidence(input.Descriptor)
	if err != nil {
		return nil, err
	}
	return newParser(input, evidence)
}

func (adapter *Adapter) valid() bool {
	return adapter != nil &&
		!nilInterface(adapter.runner) &&
		!nilInterface(adapter.allocator) &&
		adapter.discoveryTimeout > 0
}

func discoveredItems(
	records []controlRecord,
	evidence *manifestEvidence,
) []testframework.DiscoveredItem {
	items := make([]testframework.DiscoveredItem, 0, len(records)*2)
	groups := make(map[string]struct{})
	for index, record := range records {
		testCase := evidence.manifest.Cases[index]
		if _, exists := groups[record.Suite]; !exists {
			groups[record.Suite] = struct{}{}
			items = append(items, testframework.DiscoveredItem{
				Kind:        testdomain.ItemGroup,
				LogicalName: record.Suite,
				DisplayName: record.Suite,
				Labels:      []string{},
				Parameters:  []testdomain.Parameter{},
			})
		}
		location := evidence.sourceLocations[testCase.Location.Path]
		location.Line = testCase.Location.Line
		items = append(items, testframework.DiscoveredItem{
			Kind:              testdomain.ItemCase,
			ParentKind:        testdomain.ItemGroup,
			ParentLogicalName: record.Suite,
			LogicalName:       record.Identity,
			DisplayName:       record.Case,
			Labels:            []string{},
			SourceLocation:    &location,
			Parameters:        caseParameters(testCase),
		})
	}
	return items
}

type manifestEvidence struct {
	manifest        unityrunner.Manifest
	path            string
	file            os.FileInfo
	fileSHA256      string
	sourceLocations map[string]testdomain.SourceLocation
}

func loadManifestEvidence(
	descriptor ctest.ExecutionDescriptor,
) (*manifestEvidence, error) {
	buildRoot, err := workspace.OpenRoot(descriptor.TestDirectory)
	if err != nil {
		return nil, errors.Join(ErrInvalidManifest, err)
	}
	sum := sha256.Sum256([]byte(descriptor.LogicalName))
	relative := filepath.Join(
		".unit-test-ide",
		hex.EncodeToString(sum[:]),
		"manifest.json",
	)
	path, err := resolveDirectPath(buildRoot, relative)
	if err != nil {
		return nil, errors.Join(ErrInvalidManifest, err)
	}
	data, info, digest, err := readStableRegularFile(path, maxManifestBytes)
	if err != nil {
		return nil, errors.Join(ErrInvalidManifest, err)
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return nil, errors.Join(ErrInvalidManifest, err)
	}
	var manifest unityrunner.Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, errors.Join(ErrInvalidManifest, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, errors.Join(ErrInvalidManifest, err)
	}
	if manifest.GeneratorVersion == "" {
		return nil, fmt.Errorf("%w: generator version is missing", ErrInvalidManifest)
	}
	if err := manifest.Verify(); err != nil {
		return nil, errors.Join(ErrInvalidManifest, err)
	}

	sourceRoot, err := workspace.OpenRoot(descriptor.SourceDirectory)
	if err != nil {
		return nil, errors.Join(ErrInvalidManifest, err)
	}
	locations := make(map[string]testdomain.SourceLocation, len(manifest.Sources))
	for _, source := range manifest.Sources {
		native, err := resolveDirectPath(sourceRoot, filepath.FromSlash(source))
		if err != nil {
			return nil, errors.Join(ErrInvalidManifest, err)
		}
		locations[source] = testdomain.SourceLocation{
			URI:        fileURI(native),
			Navigable:  true,
			Provenance: "framework-manifest",
		}
	}
	return &manifestEvidence{
		manifest:        manifest,
		path:            path,
		file:            info,
		fileSHA256:      digest,
		sourceLocations: locations,
	}, nil
}

func (evidence *manifestEvidence) verify() error {
	if evidence == nil || evidence.path == "" || evidence.file == nil {
		return ErrInvalidManifest
	}
	data, info, digest, err := readStableRegularFile(
		evidence.path,
		maxManifestBytes,
	)
	if err != nil {
		return errors.Join(ErrInvalidManifest, err)
	}
	if !os.SameFile(evidence.file, info) ||
		digest != evidence.fileSHA256 ||
		len(data) == 0 {
		return ErrInvalidManifest
	}
	return nil
}

func readStableRegularFile(
	path string,
	limit int64,
) ([]byte, os.FileInfo, string, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, "", err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, nil, "", errors.New("path is not a direct regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, nil, "", errors.New("file identity changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, nil, "", err
	}
	if int64(len(data)) > limit {
		return nil, nil, "", errors.New("file exceeds size limit")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(opened, after) {
		return nil, nil, "", errors.New("file identity changed while reading")
	}
	sum := sha256.Sum256(data)
	return data, after, hex.EncodeToString(sum[:]), nil
}

func resolveDirectPath(root workspace.Root, relative string) (string, error) {
	if relative == "" {
		return "", workspace.ErrInvalidRelativePath
	}
	resolved, err := root.ResolveRelative(relative)
	if err != nil {
		return "", err
	}
	direct := filepath.Clean(filepath.Join(root.NativePath, relative))
	if !samePath(direct, resolved) {
		return "", errors.New("path contains a link or reparse point")
	}
	return resolved, nil
}

func samePath(first, second string) bool {
	first = filepath.Clean(first)
	second = filepath.Clean(second)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(first, second)
	}
	return first == second
}

func fileURI(path string) string {
	slash := filepath.ToSlash(filepath.Clean(path))
	if strings.HasPrefix(slash, "//") {
		withoutPrefix := strings.TrimPrefix(slash, "//")
		host, uriPath, found := strings.Cut(withoutPrefix, "/")
		if found {
			return (&url.URL{
				Scheme: "file",
				Host:   host,
				Path:   "/" + uriPath,
			}).String()
		}
	}
	if runtime.GOOS == "windows" {
		slash = "/" + slash
	}
	return (&url.URL{Scheme: "file", Path: slash}).String()
}

func verifyDescriptor(descriptor ctest.ExecutionDescriptor) error {
	if descriptor.Blocked || !descriptor.Compatibility.CaseLevel {
		return ErrIncompatibleDescriptor
	}
	if descriptor.LogicalName == "" || descriptor.TargetID == "" ||
		!absoluteCleanPath(descriptor.TestDirectory) ||
		!absoluteCleanPath(descriptor.SourceDirectory) ||
		!absoluteCleanPath(descriptor.Executable.Path) ||
		!absoluteCleanPath(descriptor.WorkingDirectory) ||
		!hasLabel(descriptor.Labels, "utide.framework.unity") ||
		!hasLabel(descriptor.Labels, ContractVersion) {
		return ErrIncompatibleDescriptor
	}
	if hasReservedArguments(descriptor.Arguments) {
		return ErrReservedArguments
	}
	if len(descriptor.Arguments) != 0 {
		return ErrIncompatibleDescriptor
	}
	if descriptor.TimeoutSeconds != nil &&
		(*descriptor.TimeoutSeconds <= 0 ||
			math.IsNaN(*descriptor.TimeoutSeconds) ||
			math.IsInf(*descriptor.TimeoutSeconds, 0)) {
		return ErrIncompatibleDescriptor
	}
	if err := descriptor.ValidateExecutable(); err != nil {
		return err
	}
	return nil
}

func absoluteCleanPath(value string) bool {
	return value != "" && filepath.IsAbs(value) &&
		filepath.Clean(value) == value &&
		!strings.ContainsRune(value, '\x00')
}

func hasLabel(labels []string, expected string) bool {
	for _, label := range labels {
		if label == expected {
			return true
		}
	}
	return false
}

func hasReservedArguments(arguments []string) bool {
	for _, argument := range arguments {
		switch argument {
		case "--utide-protocol", "--utide-mode", "--utide-case", "--utide-result":
			return true
		}
		if strings.HasPrefix(argument, "--utide-protocol=") ||
			strings.HasPrefix(argument, "--utide-mode=") ||
			strings.HasPrefix(argument, "--utide-case=") ||
			strings.HasPrefix(argument, "--utide-result=") {
			return true
		}
	}
	return false
}

func validateControlFile(control testframework.ControlFile) error {
	if nilInterface(control) || !absoluteCleanPath(control.Path()) {
		return ErrInvalidRunPlan
	}
	return nil
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

type environmentValue struct {
	name  string
	value string
}

func processEnvironment(
	descriptor ctest.ExecutionDescriptor,
) ([]string, error) {
	current := make(map[string]environmentValue)
	for _, encoded := range os.Environ() {
		name, value, ok := strings.Cut(encoded, "=")
		if !ok || !validEnvironmentEntry(name, value) {
			continue
		}
		current[environmentKey(name)] = environmentValue{name: name, value: value}
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
		strings.HasPrefix(upper, "UNIT_TEST_IDE_")
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
