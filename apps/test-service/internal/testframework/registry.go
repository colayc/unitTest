package testframework

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/testdomain"
)

var (
	ErrInvalidAdapter                 = errors.New("invalid test framework adapter")
	ErrInvalidSelection               = errors.New("invalid test framework selection")
	ErrFrameworkConfigurationConflict = errors.New("test framework configuration conflict")
)

type Source string

const (
	SourceHelper    Source = "helper"
	SourceWorkspace Source = "workspace"
	SourceOpaque    Source = "opaque"
)

const UnityRunnerV1 = "utide.runner.v1"

const (
	DegradedNoMetadata          = "framework_metadata_missing"
	DegradedDescriptor          = "ctest_descriptor_incompatible"
	DegradedAdapterUnavailable  = "framework_adapter_unavailable"
	DegradedAdapterVerification = "framework_adapter_verification_failed"
)

type Declaration struct {
	CTestName       string
	Framework       testdomain.Framework
	ContractVersion string
}

type Mapping struct {
	CTestName string
	Framework testdomain.Framework
}

type SelectionInput struct {
	Descriptor ctest.ExecutionDescriptor
	Helper     *Declaration
	Mappings   []Mapping
}

type Selection struct {
	Framework      testdomain.Framework
	Adapter        Adapter
	Capabilities   Capabilities
	Source         Source
	DegradedReason string
}

type adapterKey struct {
	framework testdomain.Framework
	version   string
}

type Registry struct {
	adapters  map[adapterKey]Adapter
	framework map[testdomain.Framework][]Adapter
}

func NewRegistry(adapters ...Adapter) (*Registry, error) {
	registry := &Registry{
		adapters:  make(map[adapterKey]Adapter, len(adapters)),
		framework: make(map[testdomain.Framework][]Adapter),
	}
	for _, adapter := range adapters {
		if adapterIsNil(adapter) {
			return nil, ErrInvalidAdapter
		}
		framework := adapter.Framework()
		version := adapter.ContractVersion()
		if !selectableFramework(framework) || !validContractVersion(version) {
			return nil, ErrInvalidAdapter
		}
		key := adapterKey{framework: framework, version: version}
		if _, duplicate := registry.adapters[key]; duplicate {
			return nil, fmt.Errorf(
				"%w: duplicate %s adapter contract %q",
				ErrInvalidAdapter,
				framework,
				version,
			)
		}
		registry.adapters[key] = adapter
		registry.framework[framework] = append(registry.framework[framework], adapter)
	}
	return registry, nil
}

func (registry *Registry) Select(
	ctx context.Context,
	input SelectionInput,
) (Selection, error) {
	if registry == nil || ctx == nil || input.Descriptor.LogicalName == "" {
		return Selection{}, ErrInvalidSelection
	}
	declaration, source, declared, err := resolveDeclaration(input)
	if err != nil {
		return Selection{}, err
	}
	if !declared {
		return opaqueSelection(DegradedNoMetadata), nil
	}
	if input.Descriptor.Blocked || !input.Descriptor.Compatibility.CaseLevel {
		return opaqueSelection(DegradedDescriptor), nil
	}

	adapter := registry.resolveAdapter(declaration)
	if adapter == nil {
		return opaqueSelection(DegradedAdapterUnavailable), nil
	}
	capabilities, err := adapter.Verify(ctx, input.Descriptor)
	if err != nil || capabilities.validate() != nil {
		return opaqueSelection(DegradedAdapterVerification), nil
	}
	return Selection{
		Framework:    declaration.Framework,
		Adapter:      adapter,
		Capabilities: capabilities,
		Source:       source,
	}, nil
}

func resolveDeclaration(input SelectionInput) (Declaration, Source, bool, error) {
	name := input.Descriptor.LogicalName
	var mapping *Mapping
	for index := range input.Mappings {
		candidate := input.Mappings[index]
		if candidate.CTestName != name {
			continue
		}
		if !selectableFramework(candidate.Framework) {
			return Declaration{}, "", false, ErrInvalidSelection
		}
		if mapping != nil && mapping.Framework != candidate.Framework {
			return Declaration{}, "", false, ErrFrameworkConfigurationConflict
		}
		copy := candidate
		mapping = &copy
	}

	if input.Helper != nil && input.Helper.CTestName == name {
		helper := *input.Helper
		if !selectableFramework(helper.Framework) ||
			!validContractVersion(helper.ContractVersion) {
			return Declaration{}, "", false, ErrInvalidSelection
		}
		if mapping != nil && mapping.Framework != helper.Framework {
			return Declaration{}, "", false, ErrFrameworkConfigurationConflict
		}
		return helper, SourceHelper, true, nil
	}
	if mapping != nil {
		return Declaration{
			CTestName: name,
			Framework: mapping.Framework,
		}, SourceWorkspace, true, nil
	}
	return Declaration{}, "", false, nil
}

func (registry *Registry) resolveAdapter(declaration Declaration) Adapter {
	if declaration.ContractVersion != "" {
		return registry.adapters[adapterKey{
			framework: declaration.Framework,
			version:   declaration.ContractVersion,
		}]
	}
	candidates := registry.framework[declaration.Framework]
	if len(candidates) != 1 {
		return nil
	}
	return candidates[0]
}

func opaqueSelection(reason string) Selection {
	return Selection{
		Framework:      testdomain.FrameworkOpaqueCTest,
		Capabilities:   opaqueCapabilities(),
		Source:         SourceOpaque,
		DegradedReason: reason,
	}
}

func selectableFramework(framework testdomain.Framework) bool {
	return framework == testdomain.FrameworkCppUTest ||
		framework == testdomain.FrameworkUnity
}

func validContractVersion(version string) bool {
	if version == "" || len(version) > 128 || !utf8.ValidString(version) ||
		strings.ContainsRune(version, '\x00') {
		return false
	}
	for _, character := range version {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func adapterIsNil(adapter Adapter) bool {
	if adapter == nil {
		return true
	}
	value := reflect.ValueOf(adapter)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
