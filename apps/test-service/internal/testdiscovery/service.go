package testdiscovery

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"time"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

var ErrInvalidService = errors.New("invalid test discovery service")

type CTestExecutor interface {
	Execute(context.Context, task.ExecutionStep, int) ([]byte, error)
}

type CatalogArtifactWriter interface {
	CommitTestCatalog(
		context.Context,
		string,
		string,
		time.Time,
		testdomain.Catalog,
	) (task.Artifact, error)
}

type CatalogPublisher interface {
	PublishCatalog(context.Context, testdomain.Catalog, task.Artifact) error
}

type ServiceConfig struct {
	Runner    *ctest.Runner
	Executor  CTestExecutor
	Registry  *testframework.Registry
	Builder   *Builder
	Artifacts CatalogArtifactWriter
	Catalogs  CatalogPublisher
	Limits    ctest.Limits
	Now       func() time.Time
}

type Service struct {
	config ServiceConfig
}

func NewService(config ServiceConfig) (*Service, error) {
	if config.Runner == nil || nilPort(config.Executor) ||
		config.Registry == nil || config.Builder == nil ||
		nilPort(config.Artifacts) || nilPort(config.Catalogs) ||
		config.Now == nil {
		return nil, ErrInvalidService
	}
	if config.Limits == (ctest.Limits{}) {
		config.Limits = ctest.DefaultLimits()
	}
	if !config.Limits.Valid() || config.Limits.MaxDocumentBytes < 1 {
		return nil, ErrInvalidService
	}
	return &Service{config: config}, nil
}

type DiscoveryInput struct {
	TaskID      string
	ArtifactID  string
	Profile     cmake.BuildProfile
	Targets     []cmake.Target
	Helpers     map[string]testframework.Declaration
	Mappings    []testframework.Mapping
	Fingerprint Fingerprint
}

func (service *Service) DiscoverAfterBuild(
	ctx context.Context,
	input DiscoveryInput,
) (testdomain.Catalog, error) {
	if service == nil || ctx == nil || !validOpaqueID(input.TaskID) ||
		!validOpaqueID(input.ArtifactID) ||
		input.Profile.ID == "" || input.Profile.ProjectID == "" {
		return testdomain.Catalog{}, ErrInvalidService
	}
	step, err := service.config.Runner.ShowOnlyPlan(input.Profile)
	if err != nil {
		return testdomain.Catalog{}, err
	}
	encoded, err := service.config.Executor.Execute(
		ctx,
		step,
		service.config.Limits.MaxDocumentBytes,
	)
	if err != nil {
		return testdomain.Catalog{}, err
	}
	snapshot, err := ctest.ParseShowOnlyJSON(encoded, service.config.Limits)
	if err != nil {
		return testdomain.Catalog{}, err
	}

	containerInputs := make([]ContainerInput, 0, len(snapshot.Tests))
	executables := make([]cmake.FingerprintFile, 0, len(snapshot.Tests))
	for _, raw := range snapshot.Tests {
		if err := ctx.Err(); err != nil {
			return testdomain.Catalog{}, err
		}
		descriptor, err := ctest.BuildDescriptor(raw, input.Profile, input.Targets)
		if err != nil {
			return testdomain.Catalog{}, err
		}
		selectionInput := testframework.SelectionInput{
			Descriptor: descriptor,
			Mappings:   append([]testframework.Mapping(nil), input.Mappings...),
		}
		if helper, exists := input.Helpers[raw.Name]; exists {
			copy := helper
			selectionInput.Helper = &copy
		}
		selection, err := service.config.Registry.Select(ctx, selectionInput)
		if err != nil {
			return testdomain.Catalog{}, err
		}
		containerInputs = append(containerInputs, ContainerInput{
			Descriptor: descriptor,
			Selection:  selection,
		})
		if descriptor.TargetID != "" {
			executables = append(executables, descriptor.Executable)
		}
	}
	fingerprint := cloneFingerprint(input.Fingerprint)
	fingerprint.CTestSemanticSHA256 = ctest.SemanticHash(snapshot)
	fingerprint.Executables = uniqueExecutableFingerprints(executables)
	catalog, err := service.config.Builder.Build(ctx, BuildInput{
		ProjectID:   input.Profile.ProjectID,
		ProfileID:   input.Profile.ID,
		GeneratedAt: service.config.Now().UTC(),
		Fingerprint: fingerprint,
		Containers:  containerInputs,
	})
	if err != nil {
		return testdomain.Catalog{}, err
	}
	artifact, err := service.config.Artifacts.CommitTestCatalog(
		ctx,
		input.TaskID,
		input.ArtifactID,
		catalog.GeneratedAt,
		catalog,
	)
	if err != nil {
		return testdomain.Catalog{}, err
	}
	if err := service.config.Catalogs.PublishCatalog(ctx, catalog, artifact); err != nil {
		return testdomain.Catalog{}, err
	}
	return catalog, nil
}

func uniqueExecutableFingerprints(
	values []cmake.FingerprintFile,
) []cmake.FingerprintFile {
	result := append([]cmake.FingerprintFile(nil), values...)
	sort.Slice(result, func(first, second int) bool {
		if result[first].Path != result[second].Path {
			return result[first].Path < result[second].Path
		}
		if result[first].Identity != result[second].Identity {
			return result[first].Identity < result[second].Identity
		}
		return result[first].SHA256 < result[second].SHA256
	})
	if len(result) == 0 {
		return []cmake.FingerprintFile{}
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

func validOpaqueID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

func nilPort(value any) bool {
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
