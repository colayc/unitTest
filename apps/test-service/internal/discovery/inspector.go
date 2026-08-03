package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/diagnostic"
	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

const maxProfiles = 4096

var ErrInspectorInvariant = errors.New("workspace inspector invariant failed")

type Snapshot struct {
	WorkspaceID  string
	WorkspaceURI string
	Generation   string
	Projects     []workspace.ProjectConfig
	Profiles     []cmake.BuildProfile
	Toolchains   []toolchain.Instance
	Diagnostics  []diagnostic.Diagnostic
}

type toolchainDiscovery interface {
	Discover(context.Context) ([]toolchain.Instance, []toolchain.Issue)
}

type inspectorDependencies struct {
	loadConfig func(workspace.Root) (workspace.LoadResult, error)
	resolve    func(
		context.Context, probe.Runner, cmake.ResolverConfig,
	) (cmake.Installation, error)
	discoverPresets func(
		context.Context, probe.Runner, cmake.Installation,
		workspace.Root, workspace.ProjectConfig,
	) (cmake.PresetDiscovery, error)
}

type Inspector struct {
	root           workspace.Root
	runner         probe.Runner
	resolverConfig cmake.ResolverConfig
	toolchains     toolchainDiscovery
	buildRoot      string
	dependencies   inspectorDependencies
}

func NewInspector(
	root workspace.Root,
	runner probe.Runner,
	resolverConfig cmake.ResolverConfig,
	registry *toolchain.Registry,
	buildRoot string,
) (*Inspector, error) {
	return newInspector(
		root,
		runner,
		resolverConfig,
		registry,
		buildRoot,
		inspectorDependencies{
			loadConfig:      workspace.LoadConfig,
			resolve:         cmake.Resolve,
			discoverPresets: cmake.DiscoverPresets,
		},
	)
}

func newInspector(
	root workspace.Root,
	runner probe.Runner,
	resolverConfig cmake.ResolverConfig,
	toolchains toolchainDiscovery,
	buildRoot string,
	dependencies inspectorDependencies,
) (*Inspector, error) {
	if root.NativePath == "" || root.ID == "" || root.URI == "" ||
		!filepath.IsAbs(root.NativePath) {
		return nil, errors.New("invalid workspace root")
	}
	if nilInterface(runner) {
		return nil, errors.New("diagnostic inspector probe runner is nil")
	}
	if nilInterface(toolchains) {
		return nil, errors.New("diagnostic inspector toolchain registry is nil")
	}
	if dependencies.loadConfig == nil || dependencies.resolve == nil ||
		dependencies.discoverPresets == nil {
		return nil, errors.New("diagnostic inspector dependency is nil")
	}
	if buildRoot == "" || !utf8.ValidString(buildRoot) ||
		strings.IndexByte(buildRoot, 0) >= 0 ||
		!filepath.IsAbs(buildRoot) || filepath.Clean(buildRoot) != buildRoot {
		return nil, errors.New("invalid trusted build root descriptor")
	}
	if lexicallyWithinRoot(root.NativePath, buildRoot) {
		return nil, errors.New("trusted build root must be outside the workspace")
	}
	return &Inspector{
		root: root, runner: runner, resolverConfig: resolverConfig,
		toolchains: toolchains, buildRoot: buildRoot, dependencies: dependencies,
	}, nil
}

func (i *Inspector) Inspect(ctx context.Context) (Snapshot, error) {
	if i == nil || ctx == nil {
		return Snapshot{}, errors.New("invalid workspace inspector")
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	loaded, err := i.loadConfig()
	if err != nil {
		switch {
		case errors.Is(err, workspace.ErrInvalidConfig):
			loaded = workspace.LoadResult{
				Config: workspace.Config{Version: 1},
				Issues: []workspace.Issue{{
					Code:     "workspace.invalid-config",
					Message:  "workspace configuration is invalid",
					Blocking: true,
				}},
			}
		case errors.Is(err, workspace.ErrConfigTooLarge):
			loaded = workspace.LoadResult{
				Config: workspace.Config{Version: 1},
				Issues: []workspace.Issue{{
					Code:     "workspace.config-too-large",
					Message:  "workspace configuration is too large",
					Blocking: true,
				}},
			}
		default:
			return Snapshot{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}

	resolverConfig := i.resolverConfig
	if loaded.Config.CMake.Executable != "" {
		resolverConfig.Override = loaded.Config.CMake.Executable
	}
	installation, cmakeErr, instances, toolchainIssues, err := i.discoverFoundations(
		ctx, resolverConfig,
	)
	if err != nil {
		return Snapshot{}, err
	}

	projects := cloneProjects(loaded.Config.Projects)
	diagnostics := make([]diagnostic.Diagnostic, 0)
	for _, issue := range loaded.Issues {
		diagnostics = append(diagnostics, inspectorDiagnostic(
			"workspace", issueSeverity(issue.Blocking),
			workspaceIssueCode(issue.Code), "Workspace configuration reported an issue", "",
		))
	}
	for _, issue := range toolchainIssues {
		diagnostics = append(diagnostics, inspectorDiagnostic(
			"toolchain", issueSeverity(issue.Blocking),
			safeToolchainIssueCode(issue.Code), "Toolchain discovery reported an issue", "",
		))
	}
	if cmakeErr != nil {
		diagnostics = append(diagnostics, inspectorDiagnostic(
			"cmake", "error", "CMAKE_UNAVAILABLE", "CMake is unavailable", "",
		))
	}

	validProjects := make([]workspace.ProjectConfig, 0, len(projects))
	for _, project := range projects {
		uri, validationErr := i.validateProject(project)
		if validationErr != nil {
			diagnostics = append(diagnostics, inspectorDiagnostic(
				"workspace", "error", "PROJECT_INVALID",
				"Project does not contain a valid CMakeLists.txt", uri,
			))
			continue
		}
		validProjects = append(validProjects, project)
	}

	profiles := make([]cmake.BuildProfile, 0)
	inputGenerations := make([]string, 0, len(validProjects))
	if cmakeErr == nil {
		presetResults, err := i.discoverProjectPresets(ctx, installation, validProjects)
		if err != nil {
			return Snapshot{}, err
		}
		for _, result := range presetResults {
			project := result.project
			discovered, discoverErr := result.discovery, result.err
			if discoverErr != nil {
				diagnostics = append(diagnostics, inspectorDiagnostic(
					"cmake", "error", "CMAKE_PRESET_INVALID",
					"CMake preset discovery failed", projectURI(i.root, project),
				))
				continue
			}
			inputGenerations = append(inputGenerations, discovered.InputGeneration)
			blocked := false
			for _, issue := range discovered.Issues {
				blocked = blocked || issue.Blocking
				diagnostics = append(diagnostics, inspectorDiagnostic(
					"cmake", issueSeverity(issue.Blocking),
					"CMAKE_PRESET_INVALID", "CMake preset discovery reported an issue",
					projectURI(i.root, project),
				))
			}
			if blocked {
				continue
			}
			if len(discovered.Inputs) != 0 {
				if !appendProfiles(&profiles, discovered.Profiles) {
					diagnostics = append(diagnostics, inspectorDiagnostic(
						"workspace", "error", "PROFILE_LIMIT_EXCEEDED",
						"Build profile limit exceeded", projectURI(i.root, project),
					))
				}
				continue
			}
			generated, localDiagnostics := i.generateProfiles(project, instances, len(profiles))
			diagnostics = append(diagnostics, localDiagnostics...)
			if !appendProfiles(&profiles, generated) {
				diagnostics = append(diagnostics, inspectorDiagnostic(
					"workspace", "error", "PROFILE_LIMIT_EXCEEDED",
					"Build profile limit exceeded", projectURI(i.root, project),
				))
			}
		}
	}

	sortProjects(projects)
	sortProfiles(profiles)
	if err := cmake.ValidateCoverageProfileReferences(loaded.Config.CoverageProfiles, profiles); err != nil {
		diagnostics = append(diagnostics, inspectorDiagnostic(
			"workspace",
			"error",
			"COVERAGE_PROFILE_INVALID",
			"Coverage profile base build profile is unavailable",
			"",
		))
	}
	instances = cloneToolchains(instances)
	sortToolchains(instances)
	diagnostics = boundDiagnostics(diagnostics)
	assignDiagnosticIDs(diagnostics, i.root.URI)
	sortDiagnostics(diagnostics)
	toolchainDescriptors := toolchainGenerationDescriptors(instances)
	generation := cmake.WorkspaceGeneration(
		loaded.Config, installation, profiles, toolchainDescriptors, inputGenerations...,
	)
	return Snapshot{
		WorkspaceID: i.root.ID, WorkspaceURI: i.root.URI, Generation: generation,
		Projects: projects, Profiles: cloneProfiles(profiles),
		Toolchains: cloneToolchains(instances), Diagnostics: cloneDiagnostics(diagnostics),
	}, nil
}

type cmakeDiscoveryResult struct {
	installation cmake.Installation
	err          error
}

type toolchainDiscoveryResult struct {
	instances []toolchain.Instance
	issues    []toolchain.Issue
	err       error
}

func (i *Inspector) discoverFoundations(
	ctx context.Context,
	resolverConfig cmake.ResolverConfig,
) (
	cmake.Installation,
	error,
	[]toolchain.Instance,
	[]toolchain.Issue,
	error,
) {
	cmakeResults := make(chan cmakeDiscoveryResult, 1)
	toolchainResults := make(chan toolchainDiscoveryResult, 1)
	go func() {
		result := i.resolve(ctx, resolverConfig)
		select {
		case cmakeResults <- result:
		case <-ctx.Done():
		}
	}()
	go func() {
		result := i.discoverToolchains(ctx)
		select {
		case toolchainResults <- result:
		case <-ctx.Done():
		}
	}()

	var cmakeResult cmakeDiscoveryResult
	var toolchainResult toolchainDiscoveryResult
	for cmakeResults != nil || toolchainResults != nil {
		select {
		case <-ctx.Done():
			return cmake.Installation{}, nil, nil, nil, ctx.Err()
		case value := <-cmakeResults:
			cmakeResult = value
			cmakeResults = nil
		case value := <-toolchainResults:
			toolchainResult = value
			toolchainResults = nil
		}
	}
	if errors.Is(cmakeResult.err, context.Canceled) ||
		errors.Is(cmakeResult.err, context.DeadlineExceeded) {
		return cmake.Installation{}, nil, nil, nil, cmakeResult.err
	}
	if errors.Is(cmakeResult.err, ErrInspectorInvariant) {
		return cmake.Installation{}, nil, nil, nil, cmakeResult.err
	}
	if toolchainResult.err != nil {
		return cmake.Installation{}, nil, nil, nil, toolchainResult.err
	}
	if err := ctx.Err(); err != nil {
		return cmake.Installation{}, nil, nil, nil, err
	}
	return cmakeResult.installation, cmakeResult.err,
		toolchainResult.instances, toolchainResult.issues, nil
}

func (i *Inspector) loadConfig() (result workspace.LoadResult, err error) {
	defer func() {
		if recover() != nil {
			result = workspace.LoadResult{}
			err = ErrInspectorInvariant
		}
	}()
	return i.dependencies.loadConfig(i.root)
}

func (i *Inspector) resolve(
	ctx context.Context,
	config cmake.ResolverConfig,
) (result cmakeDiscoveryResult) {
	defer func() {
		if recover() != nil {
			result = cmakeDiscoveryResult{err: ErrInspectorInvariant}
		}
	}()
	result.installation, result.err = i.dependencies.resolve(ctx, i.runner, config)
	return result
}

func (i *Inspector) discoverToolchains(
	ctx context.Context,
) (result toolchainDiscoveryResult) {
	defer func() {
		if recover() != nil {
			result = toolchainDiscoveryResult{err: ErrInspectorInvariant}
		}
	}()
	result.instances, result.issues = i.toolchains.Discover(ctx)
	return result
}

type projectPresetResult struct {
	index     int
	project   workspace.ProjectConfig
	discovery cmake.PresetDiscovery
	err       error
}

func (i *Inspector) discoverProjectPresets(
	ctx context.Context,
	installation cmake.Installation,
	projects []workspace.ProjectConfig,
) ([]projectPresetResult, error) {
	if len(projects) == 0 {
		return []projectPresetResult{}, nil
	}
	jobs := make(chan int)
	results := make(chan projectPresetResult, len(projects))
	workers := min(4, len(projects))
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			for index := range jobs {
				discovered, err := i.discoverPreset(ctx, installation, projects[index])
				select {
				case results <- projectPresetResult{
					index: index, project: projects[index],
					discovery: discovered, err: err,
				}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index := range projects {
			select {
			case jobs <- index:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wait.Wait()
		close(results)
	}()

	collected := make([]projectPresetResult, 0, len(projects))
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result, open := <-results:
			if !open {
				sort.Slice(collected, func(left, right int) bool {
					return collected[left].index < collected[right].index
				})
				for _, value := range collected {
					if errors.Is(value.err, context.Canceled) ||
						errors.Is(value.err, context.DeadlineExceeded) {
						return nil, value.err
					}
					if errors.Is(value.err, ErrInspectorInvariant) {
						return nil, value.err
					}
				}
				return collected, nil
			}
			collected = append(collected, result)
		}
	}
}

func (i *Inspector) discoverPreset(
	ctx context.Context,
	installation cmake.Installation,
	project workspace.ProjectConfig,
) (result cmake.PresetDiscovery, err error) {
	defer func() {
		if recover() != nil {
			result = cmake.PresetDiscovery{}
			err = ErrInspectorInvariant
		}
	}()
	return i.dependencies.discoverPresets(
		ctx, i.runner, installation, i.root, project,
	)
}

func (i *Inspector) validateProject(project workspace.ProjectConfig) (string, error) {
	source, err := i.root.ResolveRelative(project.SourceDir)
	if err != nil {
		return "", err
	}
	sourceInfo, err := os.Stat(source)
	if err != nil || !sourceInfo.IsDir() || !i.root.Contains(source) {
		return projectURI(i.root, project), errors.New("invalid project source directory")
	}
	cmakeLists := filepath.Join(source, "CMakeLists.txt")
	info, err := os.Lstat(cmakeLists)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		!i.root.Contains(cmakeLists) {
		return fileURI(cmakeLists), errors.New("invalid project CMakeLists.txt")
	}
	return fileURI(cmakeLists), nil
}

func (i *Inspector) generateProfiles(
	project workspace.ProjectConfig,
	instances []toolchain.Instance,
	existing int,
) ([]cmake.BuildProfile, []diagnostic.Diagnostic) {
	configurations := project.Fallback.Configurations
	if len(configurations) == 0 {
		return nil, []diagnostic.Diagnostic{inspectorDiagnostic(
			"workspace", "warning", "PROJECT_HAS_NO_BUILD_PROFILE",
			"Project has no preset or fallback configuration", projectURI(i.root, project),
		)}
	}
	if len(instances) == 0 {
		return nil, []diagnostic.Diagnostic{inspectorDiagnostic(
			"toolchain", "warning", "TOOLCHAIN_NOT_FOUND",
			"No verified toolchain is available", projectURI(i.root, project),
		)}
	}
	if len(instances) != 0 &&
		(len(configurations) > maxProfiles ||
			len(instances) > (maxProfiles-existing)/len(configurations)) {
		return nil, []diagnostic.Diagnostic{inspectorDiagnostic(
			"workspace", "error", "PROFILE_LIMIT_EXCEEDED",
			"Build profile limit exceeded", projectURI(i.root, project),
		)}
	}
	result := make([]cmake.BuildProfile, 0, len(instances)*len(configurations))
	diagnostics := make([]diagnostic.Diagnostic, 0)
	for _, instance := range instances {
		generator := preferredGenerator(project.Fallback.PreferredGenerator, instance)
		if generator == "" {
			diagnostics = append(diagnostics, inspectorDiagnostic(
				"toolchain", "warning", "GENERATOR_UNAVAILABLE",
				"Toolchain has no compatible verified generator", projectURI(i.root, project),
			))
			continue
		}
		for _, configuration := range configurations {
			profile, err := cmake.NewGeneratedProfile(cmake.GeneratedProfileSpec{
				ProjectID: project.ID, ToolchainID: instance.ID,
				Generator: generator, Configuration: configuration,
				BuildRoot: i.buildRoot,
			})
			if err != nil {
				diagnostics = append(diagnostics, inspectorDiagnostic(
					"workspace", "error", "PROFILE_INVALID",
					"Build profile could not be constructed", projectURI(i.root, project),
				))
				continue
			}
			result = append(result, profile)
		}
	}
	return result, diagnostics
}

func preferredGenerator(explicit string, instance toolchain.Instance) string {
	has := func(value string) bool {
		for _, candidate := range instance.Generators {
			if candidate == value {
				return true
			}
		}
		return false
	}
	if explicit != "" {
		if has(explicit) {
			return explicit
		}
		return ""
	}
	var policy []string
	switch instance.Family {
	case toolchain.FamilyGCC, toolchain.FamilyClang:
		policy = []string{"Ninja", "Unix Makefiles"}
	case toolchain.FamilyMSVC:
		policy = []string{"Visual Studio 18 2026", "Visual Studio 17 2022", "Ninja"}
	case toolchain.FamilyClangCL:
		policy = []string{"Ninja"}
	default:
		return ""
	}
	for _, value := range policy {
		if has(value) {
			return value
		}
	}
	return ""
}

func appendProfiles(destination *[]cmake.BuildProfile, values []cmake.BuildProfile) bool {
	if len(values) > maxProfiles-len(*destination) {
		return false
	}
	*destination = append(*destination, cloneProfiles(values)...)
	return true
}

func toolchainGenerationDescriptors(instances []toolchain.Instance) []string {
	result := make([]string, 0, len(instances))
	for _, instance := range instances {
		environment := append([]string(nil), instance.Environment...)
		generators := append([]string(nil), instance.Generators...)
		sort.Strings(environment)
		sort.Strings(generators)
		payload := struct {
			ID                 string                       `json:"id"`
			Family             toolchain.Family             `json:"family"`
			CCompiler          string                       `json:"cCompiler"`
			CXXCompiler        string                       `json:"cxxCompiler"`
			Version            string                       `json:"version"`
			TargetTriple       string                       `json:"targetTriple"`
			HostArchitecture   string                       `json:"hostArchitecture"`
			TargetArchitecture string                       `json:"targetArchitecture"`
			Sysroot            string                       `json:"sysroot"`
			Environment        []string                     `json:"environment"`
			Generators         []string                     `json:"generators"`
			Coverage           toolchain.CoverageCapability `json:"coverage"`
		}{
			ID: instance.ID, Family: instance.Family,
			CCompiler:   canonicalPath(instance.CCompiler),
			CXXCompiler: canonicalPath(instance.CXXCompiler),
			Version:     instance.Version, TargetTriple: instance.TargetTriple,
			HostArchitecture:   instance.HostArchitecture,
			TargetArchitecture: instance.TargetArchitecture,
			Sysroot:            canonicalPath(instance.Sysroot),
			Environment:        environment, Generators: generators,
			Coverage: toolchain.CoverageCapability{
				LLVMProfdata: canonicalPath(instance.Coverage.LLVMProfdata),
				LLVMCov:      canonicalPath(instance.Coverage.LLVMCov),
				GCov:         canonicalPath(instance.Coverage.GCov),
			},
		}
		encoded, _ := json.Marshal(payload)
		sum := sha256.Sum256(append([]byte("toolchain-generation-v1\x00"), encoded...))
		result = append(result, hex.EncodeToString(sum[:]))
	}
	sort.Strings(result)
	return result
}

func canonicalPath(value string) string {
	if value == "" {
		return ""
	}
	portable := strings.ReplaceAll(value, `\`, "/")
	unc := strings.HasPrefix(portable, "//") &&
		len(portable) > 2 && portable[2] != '/'
	value = path.Clean(portable)
	if unc && strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") {
		value = "/" + value
	}
	if runtime.GOOS == "windows" {
		value = strings.ToLower(value)
	}
	return value
}

func inspectorDiagnostic(source, severity, code, message, fileURI string) diagnostic.Diagnostic {
	return diagnostic.Diagnostic{
		Source: source, Severity: severity, Code: code, Message: message, FileURI: fileURI,
	}
}

func assignDiagnosticIDs(values []diagnostic.Diagnostic, rootURI string) {
	occurrences := make(map[string]int)
	for index := range values {
		copyValue := cloneDiagnostics(values[index : index+1])[0]
		copyValue.ID = ""
		copyValue.FileURI = diagnosticIdentityURI(copyValue.FileURI, rootURI)
		for relatedIndex := range copyValue.Related {
			copyValue.Related[relatedIndex].FileURI = diagnosticIdentityURI(
				copyValue.Related[relatedIndex].FileURI, rootURI,
			)
		}
		encoded, _ := json.Marshal(copyValue)
		fingerprint := sha256.Sum256(append([]byte("inspector-diagnostic-v1\x00"), encoded...))
		key := hex.EncodeToString(fingerprint[:])
		ordinal := occurrences[key]
		occurrences[key]++
		sum := sha256.Sum256([]byte(fmt.Sprintf(
			"inspector-diagnostic-v1\x00%s\x00%d", key, ordinal,
		)))
		values[index].ID = hex.EncodeToString(sum[:])
	}
}

func diagnosticIdentityURI(value, rootURI string) string {
	if value == "" {
		return ""
	}
	rootURI = strings.TrimSuffix(rootURI, "/")
	if value == rootURI {
		return "workspace:///"
	}
	if strings.HasPrefix(value, rootURI+"/") {
		return "workspace:///" + strings.TrimPrefix(value, rootURI+"/")
	}
	return value
}

func sortProjects(values []workspace.ProjectConfig) {
	sort.Slice(values, func(left, right int) bool {
		if values[left].ID != values[right].ID {
			return values[left].ID < values[right].ID
		}
		return values[left].SourceDir < values[right].SourceDir
	})
}

func sortProfiles(values []cmake.BuildProfile) {
	sort.Slice(values, func(left, right int) bool {
		return profileKey(values[left]) < profileKey(values[right])
	})
}

func profileKey(value cmake.BuildProfile) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func sortToolchains(values []toolchain.Instance) {
	sort.Slice(values, func(left, right int) bool {
		return toolchainKey(values[left]) < toolchainKey(values[right])
	})
}

func toolchainKey(value toolchain.Instance) string {
	encoded, _ := json.Marshal(toolchainGenerationDescriptors([]toolchain.Instance{value}))
	return string(encoded)
}

func sortDiagnostics(values []diagnostic.Diagnostic) {
	severityRank := func(value string) int {
		switch value {
		case "error":
			return 0
		case "warning":
			return 1
		case "note":
			return 2
		default:
			return 3
		}
	}
	sort.SliceStable(values, func(left, right int) bool {
		a, b := values[left], values[right]
		leftKey := fmt.Sprintf(
			"%s\x00%d\x00%s\x00%s\x00%v\x00%s\x00%s",
			a.Source, severityRank(a.Severity), a.Code, a.FileURI, a.Range, a.Message, a.ID,
		)
		rightKey := fmt.Sprintf(
			"%s\x00%d\x00%s\x00%s\x00%v\x00%s\x00%s",
			b.Source, severityRank(b.Severity), b.Code, b.FileURI, b.Range, b.Message, b.ID,
		)
		return leftKey < rightKey
	})
}

func boundDiagnostics(values []diagnostic.Diagnostic) []diagnostic.Diagnostic {
	const (
		maximumCount = 4096
		maximumBytes = 8 * 1024 * 1024
	)
	result := make([]diagnostic.Diagnostic, 0, min(len(values), maximumCount))
	total := 0
	limited := false
	for _, value := range values {
		size := inspectorDiagnosticBytes(value)
		if len(result) >= maximumCount || size > maximumBytes ||
			total > maximumBytes-size {
			limited = true
			break
		}
		result = append(result, value)
		total += size
	}
	if !limited {
		return result
	}
	limit := inspectorDiagnostic(
		"workspace", "error", "DIAGNOSTIC_LIMIT_EXCEEDED",
		"Workspace diagnostic limit exceeded", "",
	)
	if len(result) >= maximumCount {
		total -= inspectorDiagnosticBytes(result[maximumCount-1])
		result = result[:maximumCount-1]
	}
	for len(result) != 0 &&
		total+inspectorDiagnosticBytes(limit) > maximumBytes {
		total -= inspectorDiagnosticBytes(result[len(result)-1])
		result = result[:len(result)-1]
	}
	return append(result, limit)
}

func lexicallyWithinRoot(root, candidate string) bool {
	if root == "" || candidate == "" ||
		!filepath.IsAbs(root) || !filepath.IsAbs(candidate) {
		return false
	}
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative == "." ||
		relative != ".." && !strings.HasPrefix(
			relative, ".."+string(filepath.Separator),
		)
}

func inspectorDiagnosticBytes(value diagnostic.Diagnostic) int {
	total := len(value.Source) + len(value.Severity) + len(value.Code) +
		len(value.Message) + len(value.FileURI) + len(value.ToolchainID) +
		len(value.TaskID) + len(value.StepID)
	for _, related := range value.Related {
		total += len(related.Message) + len(related.FileURI)
	}
	return total
}

func projectURI(root workspace.Root, project workspace.ProjectConfig) string {
	source, err := root.ResolveRelative(project.SourceDir)
	if err != nil {
		return root.URI
	}
	return fileURI(filepath.Join(source, "CMakeLists.txt"))
}

func fileURI(value string) string {
	portable := strings.ReplaceAll(value, `\`, "/")
	unc := strings.HasPrefix(portable, "//") &&
		len(portable) > 2 && portable[2] != '/'
	clean := path.Clean(portable)
	if unc && strings.HasPrefix(clean, "/") && !strings.HasPrefix(clean, "//") {
		clean = "/" + clean
	}
	if strings.HasPrefix(clean, "//") {
		parts := strings.SplitN(strings.TrimPrefix(clean, "//"), "/", 2)
		uriPath := "/"
		if len(parts) == 2 {
			uriPath += parts[1]
		}
		return (&url.URL{Scheme: "file", Host: parts[0], Path: uriPath}).String()
	}
	if len(clean) >= 2 && clean[1] == ':' {
		clean = "/" + clean
	}
	return (&url.URL{Scheme: "file", Path: clean}).String()
}

func issueSeverity(blocking bool) string {
	if blocking {
		return "error"
	}
	return "warning"
}

func workspaceIssueCode(value string) string {
	value = strings.NewReplacer(".", "_", "-", "_").Replace(value)
	value = strings.ToUpper(value)
	if value == "" {
		return "WORKSPACE_ISSUE"
	}
	return value
}

func safeToolchainIssueCode(value string) string {
	if value == "" || len(value) > 64 {
		return "TOOLCHAIN_DISCOVERY_FAILED"
	}
	for _, character := range value {
		if character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '_' {
			continue
		}
		return "TOOLCHAIN_DISCOVERY_FAILED"
	}
	return value
}

func cloneProjects(values []workspace.ProjectConfig) []workspace.ProjectConfig {
	result := append([]workspace.ProjectConfig(nil), values...)
	for index := range result {
		result[index].Fallback.Configurations = append(
			[]string(nil), values[index].Fallback.Configurations...,
		)
	}
	return result
}

func cloneProfiles(values []cmake.BuildProfile) []cmake.BuildProfile {
	return append([]cmake.BuildProfile(nil), values...)
}

func cloneToolchains(values []toolchain.Instance) []toolchain.Instance {
	result := append([]toolchain.Instance(nil), values...)
	for index := range result {
		result[index].Environment = append([]string(nil), values[index].Environment...)
		result[index].Generators = append([]string(nil), values[index].Generators...)
	}
	return result
}

func cloneDiagnostics(values []diagnostic.Diagnostic) []diagnostic.Diagnostic {
	result := append([]diagnostic.Diagnostic(nil), values...)
	for index := range result {
		if values[index].Range != nil {
			rangeCopy := *values[index].Range
			result[index].Range = &rangeCopy
		}
		result[index].Related = append([]diagnostic.Related(nil), values[index].Related...)
		for relatedIndex := range result[index].Related {
			if values[index].Related[relatedIndex].Range != nil {
				rangeCopy := *values[index].Related[relatedIndex].Range
				result[index].Related[relatedIndex].Range = &rangeCopy
			}
		}
	}
	return result
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
