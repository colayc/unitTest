package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/discovery"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

type WorkspaceInspector interface {
	Inspect(context.Context) (discovery.Snapshot, error)
}

type TaskStarter interface {
	Start(context.Context, task.StartRequest) (task.Task, error)
}

type ConfigurationStore interface {
	GetBuildConfiguration(context.Context, string, string, string) (taskstore.BuildConfiguration, error)
	PutBuildConfiguration(context.Context, taskstore.BuildConfiguration) error
}

type CoordinatorConfig struct {
	Inspector       WorkspaceInspector
	Tasks           TaskStarter
	Configurations  ConfigurationStore
	Installation    cmake.Installation
	WorkspaceRoot   workspace.Root
	ServiceDataRoot string
	Locks           *DirectoryLocks
}

type coordinatorDependencies struct {
	readReply  func(string, []string, ...cmake.BuildProfile) (cmake.FileAPIReply, error)
	writeQuery func(string) error
	now        func() time.Time
}

type Coordinator struct {
	config       CoordinatorConfig
	dependencies coordinatorDependencies
}

func NewCoordinator(config CoordinatorConfig) (*Coordinator, error) {
	return newCoordinator(config, coordinatorDependencies{
		readReply: cmake.ReadReply, writeQuery: cmake.WriteQuery,
		now: func() time.Time { return time.Now().UTC() },
	})
}

func newCoordinator(
	config CoordinatorConfig,
	dependencies coordinatorDependencies,
) (*Coordinator, error) {
	if nilInterface(config.Inspector) || nilInterface(config.Tasks) ||
		nilInterface(config.Configurations) || config.Installation.Executable == "" ||
		config.Installation.Identity == "" || config.WorkspaceRoot.NativePath == "" ||
		config.WorkspaceRoot.ID == "" || config.ServiceDataRoot == "" ||
		config.Locks == nil || dependencies.readReply == nil ||
		dependencies.writeQuery == nil || dependencies.now == nil {
		return nil, task.ErrInvalidArgument
	}
	boundary, err := newExecutionBoundary(
		config.Installation, config.WorkspaceRoot, config.ServiceDataRoot, nil,
	)
	if err != nil {
		return nil, err
	}
	if err := boundary.Release(); err != nil {
		return nil, err
	}
	return &Coordinator{config: config, dependencies: dependencies}, nil
}

func (c *Coordinator) Inspect(ctx context.Context) (discovery.Snapshot, error) {
	if c == nil || ctx == nil {
		return discovery.Snapshot{}, task.ErrInvalidArgument
	}
	return c.config.Inspector.Inspect(ctx)
}

func (c *Coordinator) Targets(
	ctx context.Context,
	request TargetsRequest,
) ([]cmake.Target, error) {
	snapshot, project, profile, instance, err := c.resolve(ctx, request.WorkspaceGeneration, request.ProjectID, request.BuildProfileID)
	if err != nil {
		return nil, err
	}
	previous, err := c.config.Configurations.GetBuildConfiguration(
		ctx, c.config.WorkspaceRoot.ID, project.ID, profile.ID,
	)
	if errors.Is(err, task.ErrNotFound) {
		return nil, ErrConfigureRequired
	}
	if err != nil {
		return nil, err
	}
	reply, err := c.readReply(profile, instance, snapshot.Toolchains)
	if err != nil {
		return nil, ErrConfigureRequired
	}
	toolchainIdentity := effectiveToolchainIdentity(profile, toolchain.Instance{}, reply)
	current := fingerprintInput(
		snapshot.Generation, profile, c.config.Installation.Identity,
		toolchainIdentity, reply, c.config.Installation.UnityRunnerGenerator.Identity,
	)
	if cmake.NeedsConfigure(cmake.BuildConfiguration{
		Fingerprint: previous.Fingerprint, Succeeded: true,
	}, current) {
		return nil, ErrConfigureRequired
	}
	return cloneTargets(reply.Targets), nil
}

func (c *Coordinator) Start(
	ctx context.Context,
	request StartRequest,
) (task.Task, error) {
	prepared, err := c.prepare(ctx, request)
	if err != nil {
		return task.Task{}, err
	}
	defer prepared.releaseUnlessAdopted()
	return c.config.Tasks.Start(ctx, prepared.request)
}

type PreparedPlan struct {
	prepared *preparedBuild
}

func (c *Coordinator) PreparePlan(
	ctx context.Context,
	request StartRequest,
) (*PreparedPlan, error) {
	prepared, err := c.prepare(ctx, request)
	if err != nil {
		return nil, err
	}
	return &PreparedPlan{prepared: prepared}, nil
}

func (plan *PreparedPlan) Plan() task.ExecutionPlan {
	if plan == nil || plan.prepared == nil {
		return task.ExecutionPlan{}
	}
	return cloneBuildExecutionPlan(plan.prepared.request.Plan)
}

func (plan *PreparedPlan) Boundary() task.ExecutionBoundary {
	if plan == nil || plan.prepared == nil {
		return nil
	}
	return plan.prepared.request.Boundary
}

func (plan *PreparedPlan) WorkspaceGeneration() string {
	if plan == nil || plan.prepared == nil {
		return ""
	}
	return plan.prepared.snapshot.Generation
}

func (plan *PreparedPlan) Project() workspace.ProjectConfig {
	if plan == nil || plan.prepared == nil {
		return workspace.ProjectConfig{}
	}
	result := plan.prepared.project
	result.Fallback.Configurations = append(
		[]string(nil),
		plan.prepared.project.Fallback.Configurations...,
	)
	result.Tests.Containers = append(
		[]workspace.TestContainerMapping(nil),
		plan.prepared.project.Tests.Containers...,
	)
	return result
}

func (plan *PreparedPlan) Profile() cmake.BuildProfile {
	if plan == nil || plan.prepared == nil {
		return cmake.BuildProfile{}
	}
	return plan.prepared.profile
}

func (plan *PreparedPlan) Toolchain() toolchain.Instance {
	if plan == nil || plan.prepared == nil {
		return toolchain.Instance{}
	}
	result := plan.prepared.toolchain
	result.Environment = append(
		[]string(nil),
		plan.prepared.toolchain.Environment...,
	)
	result.Generators = append(
		[]string(nil),
		plan.prepared.toolchain.Generators...,
	)
	return result
}

func (plan *PreparedPlan) Targets() []cmake.Target {
	if plan == nil || plan.prepared == nil {
		return nil
	}
	return cloneTargets(plan.prepared.targets)
}

func (plan *PreparedPlan) AllowTestExecutable(
	state cmake.FingerprintFile,
) error {
	if plan == nil || plan.prepared == nil ||
		plan.prepared.boundary == nil {
		return task.ErrInvalidArgument
	}
	return plan.prepared.boundary.allowTestExecutable(state)
}

func (plan *PreparedPlan) ReleaseIfUnadopted() {
	if plan != nil && plan.prepared != nil {
		plan.prepared.releaseUnlessAdopted()
	}
}

type queuedTaskResumer interface {
	ResumeQueued(context.Context, task.ResumeRequest) (task.Task, error)
}

func (c *Coordinator) Resume(
	ctx context.Context,
	persisted task.Task,
) (task.Task, error) {
	if c == nil || ctx == nil || persisted.ID == "" ||
		persisted.IdempotencyKey == "" || persisted.Kind != task.KindCMakeBuild ||
		persisted.Status != task.StatusQueued ||
		persisted.WorkspaceGeneration == "" {
		return task.Task{}, task.ErrInvalidArgument
	}
	var payload struct {
		ProjectID      string   `json:"projectId"`
		BuildProfileID string   `json:"buildProfileId"`
		TargetIDs      []string `json:"targetIds"`
		Jobs           int      `json:"jobs"`
		TimeoutMS      int64    `json:"timeoutMs"`
	}
	if err := strictJSON(persisted.Request, &payload); err != nil ||
		payload.TimeoutMS != persisted.Timeout.Milliseconds() {
		return task.Task{}, task.ErrInvalidArgument
	}
	prepared, err := c.prepare(ctx, StartRequest{
		IdempotencyKey:      persisted.IdempotencyKey,
		WorkspaceGeneration: persisted.WorkspaceGeneration,
		ProjectID:           payload.ProjectID, BuildProfileID: payload.BuildProfileID,
		TargetIDs: append([]string(nil), payload.TargetIDs...),
		Jobs:      payload.Jobs, Timeout: persisted.Timeout,
	})
	if err != nil {
		return task.Task{}, err
	}
	defer prepared.releaseUnlessAdopted()
	resumer, ok := c.config.Tasks.(queuedTaskResumer)
	if !ok {
		return task.Task{}, task.ErrStorageUnavailable
	}
	return resumer.ResumeQueued(ctx, task.ResumeRequest{
		Task: persisted, Plan: prepared.request.Plan,
		Boundary: prepared.request.Boundary,
	})
}

type preparedBuild struct {
	request   task.StartRequest
	boundary  *executionBoundary
	snapshot  discovery.Snapshot
	project   workspace.ProjectConfig
	profile   cmake.BuildProfile
	toolchain toolchain.Instance
	targets   []cmake.Target
}

func (p *preparedBuild) releaseUnlessAdopted() {
	if p != nil && p.boundary != nil && !p.boundary.adopted() {
		_ = p.boundary.Release()
	}
}

func (c *Coordinator) prepare(
	ctx context.Context,
	request StartRequest,
) (*preparedBuild, error) {
	if c == nil || ctx == nil || request.IdempotencyKey == "" ||
		request.Jobs < 1 || request.Jobs > 256 ||
		request.Timeout < time.Millisecond || request.Timeout > 24*time.Hour {
		return nil, task.ErrInvalidArgument
	}
	snapshot, project, profile, instance, err := c.resolve(
		ctx, request.WorkspaceGeneration, request.ProjectID, request.BuildProfileID,
	)
	if err != nil {
		return nil, err
	}
	if err := c.ensureBuildDirectory(profile.BinaryDir); err != nil {
		return nil, err
	}
	lock, err := c.config.Locks.Acquire(profile.BinaryDir)
	if err != nil {
		return nil, err
	}
	lockOwned := true
	defer func() {
		if lockOwned {
			_ = lock.Release()
		}
	}()

	reply, replyErr := c.readReply(profile, instance, snapshot.Toolchains)
	targets := []cmake.Target{}
	if replyErr == nil {
		targets = reply.Targets
	}
	if _, err := resolveTargetNames(targets, request.TargetIDs); err != nil {
		if replyErr != nil && len(request.TargetIDs) != 0 {
			return nil, ErrConfigureRequired
		}
		return nil, err
	}

	needsConfigure := true
	previous, configurationErr := c.config.Configurations.GetBuildConfiguration(
		ctx, c.config.WorkspaceRoot.ID, project.ID, profile.ID,
	)
	if configurationErr != nil && !errors.Is(configurationErr, task.ErrNotFound) {
		return nil, configurationErr
	}
	toolchainIdentity := effectiveToolchainIdentity(profile, instance, reply)
	if configurationErr == nil && replyErr == nil {
		needsConfigure = cmake.NeedsConfigure(
			cmake.BuildConfiguration{Fingerprint: previous.Fingerprint, Succeeded: true},
			fingerprintInput(
				snapshot.Generation, profile, c.config.Installation.Identity,
				toolchainIdentity, reply, c.config.Installation.UnityRunnerGenerator.Identity,
			),
		)
	}
	state, err := c.configureState(
		snapshot, project, profile, instance, reply, request.TargetIDs,
	)
	if err != nil {
		return nil, err
	}
	if needsConfigure {
		if err := c.dependencies.writeQuery(profile.BinaryDir); err != nil {
			return nil, ErrConfigureRequired
		}
	}
	plan, err := Plan(PlanInput{
		Installation: c.config.Installation, WorkspaceRoot: c.config.WorkspaceRoot,
		Project: project, Profile: profile, Toolchain: instance,
		Targets: targets, TargetIDs: request.TargetIDs, Jobs: request.Jobs,
		Configure: needsConfigure, ConfigureState: state,
	})
	if err != nil {
		return nil, err
	}
	boundary, err := newExecutionBoundary(
		c.config.Installation, c.config.WorkspaceRoot, c.config.ServiceDataRoot, lock,
	)
	if err != nil {
		return nil, err
	}
	lockOwned = false
	boundaryOwned := true
	defer func() {
		if boundaryOwned {
			_ = boundary.Release()
		}
	}()
	requestJSON, err := json.Marshal(struct {
		ProjectID      string   `json:"projectId"`
		BuildProfileID string   `json:"buildProfileId"`
		TargetIDs      []string `json:"targetIds"`
		Jobs           int      `json:"jobs"`
		TimeoutMS      int64    `json:"timeoutMs"`
	}{
		ProjectID: request.ProjectID, BuildProfileID: request.BuildProfileID,
		TargetIDs: append([]string{}, request.TargetIDs...),
		Jobs:      request.Jobs, TimeoutMS: request.Timeout.Milliseconds(),
	})
	if err != nil {
		return nil, task.ErrInvalidArgument
	}
	internalRequest := task.StartRequest{
		IdempotencyKey: request.IdempotencyKey, Kind: task.KindCMakeBuild,
		Request: requestJSON, WorkspaceGeneration: request.WorkspaceGeneration,
		Timeout: request.Timeout, Plan: plan, Boundary: boundary,
	}
	boundaryOwned = false
	preparedToolchain := instance
	if preparedToolchain.ID == "" {
		preparedToolchain.ID = effectiveToolchainIdentity(
			profile,
			instance,
			reply,
		)
	}
	return &preparedBuild{
		request: internalRequest, boundary: boundary,
		snapshot: snapshot, project: project, profile: profile,
		toolchain: preparedToolchain,
		targets:   cloneTargets(targets),
	}, nil
}

func cloneBuildExecutionPlan(value task.ExecutionPlan) task.ExecutionPlan {
	result := value
	result.Steps = make([]task.ExecutionStep, len(value.Steps))
	for index, step := range value.Steps {
		result.Steps[index] = step
		result.Steps[index].Process.Args = append(
			[]string(nil),
			step.Process.Args...,
		)
		result.Steps[index].Process.Env = append(
			[]string(nil),
			step.Process.Env...,
		)
		result.Steps[index].Process.EnvUnset = append(
			[]string(nil),
			step.Process.EnvUnset...,
		)
		result.Steps[index].Public.Args = append(
			[]string(nil),
			step.Public.Args...,
		)
		result.Steps[index].State = append(
			json.RawMessage(nil),
			step.State...,
		)
	}
	return result
}

func (c *Coordinator) Succeeded(
	ctx context.Context,
	_ task.Task,
	step task.ExecutionStep,
) error {
	if c == nil || ctx == nil {
		return task.ErrInvalidArgument
	}
	if step.Kind != task.StepConfigure {
		return nil
	}
	if len(step.State) == 0 {
		return task.ErrInvalidArgument
	}
	var state configureStepState
	if err := strictJSON(step.State, &state); err != nil {
		return task.ErrInvalidArgument
	}
	reply, err := c.dependencies.readReply(
		state.Profile.BinaryDir, state.AllowedRoots, state.Profile,
	)
	if err != nil {
		return ErrConfigureRequired
	}
	byID := make(map[string]string, len(reply.Targets))
	for _, target := range reply.Targets {
		byID[target.ID] = target.Name
	}
	for _, id := range state.TargetIDs {
		if byID[id] == "" || byID[id] != state.TargetNames[id] {
			return ErrTargetNotFound
		}
	}
	toolchainIdentity := effectiveToolchainIdentity(
		state.Profile, toolchain.Instance{ID: state.ToolchainID}, reply,
	)
	fingerprint := configureFingerprint(
		state.WorkspaceGeneration, state.Profile, state.CMakeIdentity,
		toolchainIdentity, reply, state.UnityRunnerGeneratorIdentity,
	)
	if fingerprint == "" {
		return ErrConfigureRequired
	}
	return c.config.Configurations.PutBuildConfiguration(
		ctx,
		taskstore.BuildConfiguration{
			WorkspaceID: state.WorkspaceID, ProjectID: state.ProjectID,
			ProfileID: state.Profile.ID, Fingerprint: fingerprint,
			BuildDirectory:  state.BuildDirectory,
			CMakeIdentity:   state.CMakeIdentity,
			FileAPIIdentity: fileAPIIdentity(reply),
			ConfiguredAt:    c.dependencies.now(),
		},
	)
}

type configureStepState struct {
	WorkspaceID                  string             `json:"workspaceId"`
	WorkspaceGeneration          string             `json:"workspaceGeneration"`
	ProjectID                    string             `json:"projectId"`
	Profile                      cmake.BuildProfile `json:"profile"`
	ToolchainID                  string             `json:"toolchainId"`
	CMakeIdentity                string             `json:"cmakeIdentity"`
	UnityRunnerGeneratorIdentity string             `json:"unityRunnerGeneratorIdentity,omitempty"`
	BuildDirectory               string             `json:"buildDirectory"`
	AllowedRoots                 []string           `json:"allowedRoots"`
	TargetIDs                    []string           `json:"targetIds"`
	TargetNames                  map[string]string  `json:"targetNames"`
}

func (c *Coordinator) configureState(
	snapshot discovery.Snapshot,
	project workspace.ProjectConfig,
	profile cmake.BuildProfile,
	instance toolchain.Instance,
	reply cmake.FileAPIReply,
	targetIDs []string,
) (json.RawMessage, error) {
	targetNames := make(map[string]string, len(targetIDs))
	byID := make(map[string]string, len(reply.Targets))
	for _, target := range reply.Targets {
		byID[target.ID] = target.Name
	}
	for _, id := range targetIDs {
		if byID[id] == "" {
			return nil, ErrTargetNotFound
		}
		targetNames[id] = byID[id]
	}
	buildDirectory, err := c.buildDirectoryIdentity(profile.BinaryDir)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(configureStepState{
		WorkspaceID:         c.config.WorkspaceRoot.ID,
		WorkspaceGeneration: snapshot.Generation,
		ProjectID:           project.ID, Profile: profile, ToolchainID: instance.ID,
		CMakeIdentity:                c.config.Installation.Identity,
		UnityRunnerGeneratorIdentity: c.config.Installation.UnityRunnerGenerator.Identity,
		BuildDirectory:               buildDirectory,
		AllowedRoots:                 c.fileAPIAllowedRoots(profile, instance, snapshot.Toolchains),
		TargetIDs:                    append([]string{}, targetIDs...), TargetNames: targetNames,
	})
	if err != nil {
		return nil, task.ErrInvalidArgument
	}
	return encoded, nil
}

func (c *Coordinator) resolve(
	ctx context.Context,
	generation string,
	projectID string,
	profileID string,
) (
	discovery.Snapshot,
	workspace.ProjectConfig,
	cmake.BuildProfile,
	toolchain.Instance,
	error,
) {
	snapshot, err := c.Inspect(ctx)
	if err != nil {
		return discovery.Snapshot{}, workspace.ProjectConfig{}, cmake.BuildProfile{}, toolchain.Instance{}, err
	}
	if snapshot.Generation != generation {
		return discovery.Snapshot{}, workspace.ProjectConfig{}, cmake.BuildProfile{}, toolchain.Instance{}, ErrWorkspaceChanged
	}
	var project workspace.ProjectConfig
	for _, candidate := range snapshot.Projects {
		if candidate.ID == projectID {
			project = candidate
			break
		}
	}
	if project.ID == "" {
		return discovery.Snapshot{}, workspace.ProjectConfig{}, cmake.BuildProfile{}, toolchain.Instance{}, ErrProjectNotFound
	}
	var profile cmake.BuildProfile
	for _, candidate := range snapshot.Profiles {
		if candidate.ID == profileID && candidate.ProjectID == projectID {
			profile = candidate
			break
		}
	}
	if profile.ID == "" {
		return discovery.Snapshot{}, workspace.ProjectConfig{}, cmake.BuildProfile{}, toolchain.Instance{}, ErrBuildProfileNotFound
	}
	var instance toolchain.Instance
	if profile.ToolchainID != "" {
		for _, candidate := range snapshot.Toolchains {
			if candidate.ID == profile.ToolchainID {
				instance = candidate
				break
			}
		}
		if instance.ID == "" {
			return discovery.Snapshot{}, workspace.ProjectConfig{}, cmake.BuildProfile{}, toolchain.Instance{}, ErrBuildProfileNotFound
		}
	}
	return snapshot, project, profile, instance, nil
}

func (c *Coordinator) readReply(
	profile cmake.BuildProfile,
	instance toolchain.Instance,
	discovered []toolchain.Instance,
) (cmake.FileAPIReply, error) {
	return c.dependencies.readReply(
		profile.BinaryDir, c.fileAPIAllowedRoots(profile, instance, discovered), profile,
	)
}

func (c *Coordinator) fileAPIAllowedRoots(
	profile cmake.BuildProfile,
	instance toolchain.Instance,
	discovered []toolchain.Instance,
) []string {
	candidates := []string{
		c.config.WorkspaceRoot.NativePath,
		c.config.ServiceDataRoot,
		c.config.Installation.Root,
	}
	if c.config.Installation.Root == "" {
		candidates[2] = filepath.Dir(c.config.Installation.Executable)
	}
	if c.config.Installation.UnityRunnerGenerator.Path != "" {
		candidates = append(
			candidates,
			filepath.Dir(c.config.Installation.UnityRunnerGenerator.Path),
		)
	}
	instances := []toolchain.Instance{instance}
	if profile.Origin == "preset" {
		instances = discovered
	}
	for _, candidate := range instances {
		if candidate.CCompiler != "" {
			candidates = append(candidates, filepath.Dir(candidate.CCompiler))
		}
		if candidate.CXXCompiler != "" {
			candidates = append(candidates, filepath.Dir(candidate.CXXCompiler))
		}
		if candidate.Sysroot != "" {
			candidates = append(candidates, candidate.Sysroot)
		}
	}
	result := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		clean := filepath.Clean(candidate)
		key := canonicalPortablePathForBoundary(clean)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, clean)
	}
	return result
}

func canonicalPortablePathForBoundary(value string) string {
	value = filepath.ToSlash(value)
	if filepath.Separator == '\\' {
		value = strings.ToLower(value)
	}
	return value
}

func (c *Coordinator) ensureBuildDirectory(path string) error {
	if path == "" {
		return task.ErrInvalidArgument
	}
	resolved, ok := resolveAllowedBuildPath(
		path, c.config.WorkspaceRoot.NativePath, c.config.ServiceDataRoot,
	)
	if !ok {
		return task.ErrInvalidArgument
	}
	if err := os.MkdirAll(resolved, 0o700); err != nil {
		return task.ErrInvalidArgument
	}
	boundary, err := newExecutionBoundary(
		c.config.Installation, c.config.WorkspaceRoot, c.config.ServiceDataRoot, nil,
	)
	if err != nil {
		return err
	}
	validationErr := boundary.ValidateWorkingDirectory(resolved)
	releaseErr := boundary.Release()
	return errors.Join(validationErr, releaseErr)
}

func (c *Coordinator) buildDirectoryIdentity(path string) (string, error) {
	roots := []struct {
		prefix string
		path   string
	}{
		{prefix: "service", path: c.config.ServiceDataRoot},
		{prefix: "workspace", path: c.config.WorkspaceRoot.NativePath},
	}
	for _, root := range roots {
		relative, err := filepath.Rel(root.path, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return root.prefix + "/" + filepath.ToSlash(relative), nil
		}
	}
	return "", task.ErrInvalidArgument
}

func fingerprintInput(
	generation string,
	profile cmake.BuildProfile,
	cmakeIdentity string,
	toolchainIdentity string,
	reply cmake.FileAPIReply,
	unityRunnerGeneratorIdentity string,
) cmake.ProfileFingerprintInput {
	return cmake.ProfileFingerprintInput{
		WorkspaceGeneration: generation, Profile: profile,
		CMakeIdentity:                cmakeIdentity,
		UnityRunnerGeneratorIdentity: unityRunnerGeneratorIdentity,
		ToolchainIdentity:            toolchainIdentity,
		CMakeInputStates:             reply.CMakeInputStates, Cache: reply.Cache,
		FileAPIState: reply.StateFiles,
	}
}

func configureFingerprint(
	generation string,
	profile cmake.BuildProfile,
	cmakeIdentity string,
	toolchainIdentity string,
	reply cmake.FileAPIReply,
	unityRunnerGeneratorIdentity string,
) string {
	return cmake.ConfigureFingerprint(fingerprintInput(
		generation, profile, cmakeIdentity, toolchainIdentity, reply,
		unityRunnerGeneratorIdentity,
	))
}

func effectiveToolchainIdentity(
	profile cmake.BuildProfile,
	instance toolchain.Instance,
	reply cmake.FileAPIReply,
) string {
	if instance.ID != "" {
		return instance.ID
	}
	if profile.ToolchainID != "" {
		return profile.ToolchainID
	}
	identities := append([]string(nil), reply.ToolchainIDs...)
	sort.Strings(identities)
	encoded, _ := json.Marshal(identities)
	sum := sha256.Sum256(append([]byte("file-api-toolchains-v1\x00"), encoded...))
	return hex.EncodeToString(sum[:])
}

func fileAPIIdentity(reply cmake.FileAPIReply) string {
	states := append([]cmake.FingerprintFile(nil), reply.StateFiles...)
	sort.Slice(states, func(left, right int) bool {
		if states[left].Path != states[right].Path {
			return states[left].Path < states[right].Path
		}
		if states[left].Identity != states[right].Identity {
			return states[left].Identity < states[right].Identity
		}
		return states[left].SHA256 < states[right].SHA256
	})
	encoded, _ := json.Marshal(states)
	sum := sha256.Sum256(append([]byte("file-api-state-v1\x00"), encoded...))
	return hex.EncodeToString(sum[:])
}

func cloneTargets(values []cmake.Target) []cmake.Target {
	result := make([]cmake.Target, len(values))
	for index := range values {
		result[index] = values[index]
		result[index].Artifacts = append([]string(nil), values[index].Artifacts...)
	}
	return result
}

func resolveAllowedBuildPath(path string, roots ...string) (string, bool) {
	candidate, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	for _, root := range roots {
		relative, err := filepath.Rel(root, candidate)
		if err == nil && relative != ".." &&
			!strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			opened, openErr := workspace.OpenRoot(root)
			if openErr != nil {
				continue
			}
			resolved, resolveErr := opened.ResolveRelative(relative)
			if resolveErr == nil {
				return resolved, true
			}
		}
	}
	return "", false
}

func strictJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return task.ErrInvalidArgument
	}
	return nil
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
