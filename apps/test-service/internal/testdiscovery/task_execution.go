package testdiscovery

import (
	"context"
	"strconv"
	"sync"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

const maxTaskDiscoveryBatch = 256

type TaskProgress struct {
	Steps    []task.ExecutionStep
	Pins     []cmake.FingerprintFile
	Snapshot *DiscoverySnapshot
}

func (progress TaskProgress) Clone() TaskProgress {
	result := TaskProgress{
		Steps: cloneTaskDiscoverySteps(progress.Steps),
		Pins:  append([]cmake.FingerprintFile(nil), progress.Pins...),
	}
	if progress.Snapshot != nil {
		snapshot := progress.Snapshot.Clone()
		result.Snapshot = &snapshot
	}
	return result
}

type TaskExecution struct {
	mu sync.Mutex

	service *Service
	input   DiscoveryInput

	ctestStepID string
	ctestOutput []byte
	ctestFailed bool
	ctestDone   bool

	containers []ContainerInput
	bindings   []RuntimeBinding
	steps      []task.ExecutionStep
	parsers    map[string]taskDiscoveryBinding
	pins       map[string]cmake.FingerprintFile
	next       int
	lastIssued string
	complete   bool
}

type taskDiscoveryBinding struct {
	containerIndex int
	parser         testframework.DiscoveryParser
	failed         bool
	finished       bool
	continued      bool
}

func (service *Service) PrepareTaskDiscovery(
	ctx context.Context,
	input DiscoveryInput,
) (*TaskExecution, TaskProgress, error) {
	if service == nil || ctx == nil || !validOpaqueID(input.TaskID) ||
		!validOpaqueID(input.ArtifactID) ||
		input.Profile.ID == "" || input.Profile.ProjectID == "" {
		return nil, TaskProgress{}, ErrInvalidService
	}
	if err := ctx.Err(); err != nil {
		return nil, TaskProgress{}, err
	}
	step, err := service.config.Runner.ShowOnlyPlan(input.Profile)
	if err != nil {
		return nil, TaskProgress{}, err
	}
	execution := &TaskExecution{
		service:     service,
		input:       cloneDiscoveryInput(input),
		ctestStepID: step.ID,
		parsers:     make(map[string]taskDiscoveryBinding),
		pins:        make(map[string]cmake.FingerprintFile),
	}
	return execution, TaskProgress{
		Steps: []task.ExecutionStep{step},
	}, nil
}

func (execution *TaskExecution) ObserveOutput(
	ctx context.Context,
	step task.ExecutionStep,
	output task.ProcessOutput,
) error {
	if execution == nil || ctx == nil {
		return task.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	execution.mu.Lock()
	defer execution.mu.Unlock()
	if execution.complete {
		return task.ErrConflict
	}
	if step.ID == execution.ctestStepID {
		if output.Stream != string(testframework.StreamStdout) {
			return nil
		}
		limit := execution.service.config.Limits.MaxDocumentBytes
		if len(output.Data) > limit-len(execution.ctestOutput) {
			execution.ctestFailed = true
			return nil
		}
		execution.ctestOutput = append(
			execution.ctestOutput,
			output.Data...,
		)
		return nil
	}
	binding, exists := execution.parsers[step.ID]
	if !exists || binding.finished {
		return task.ErrInvalidArgument
	}
	if binding.failed {
		return nil
	}
	stream := testframework.Stream(output.Stream)
	if err := binding.parser.Feed(stream, output.Data); err != nil {
		binding.failed = true
		execution.parsers[step.ID] = binding
	}
	return nil
}

func (execution *TaskExecution) Interpret(
	ctx context.Context,
	step task.ExecutionStep,
	result task.ProcessResult,
) (task.StepVerdict, error) {
	if execution == nil || ctx == nil {
		return task.StepVerdictDefault, task.ErrInvalidArgument
	}
	execution.mu.Lock()
	defer execution.mu.Unlock()
	if execution.complete {
		return task.StepVerdictDefault, task.ErrConflict
	}
	if step.ID == execution.ctestStepID {
		if execution.ctestDone {
			return task.StepVerdictDefault, task.ErrConflict
		}
		if execution.ctestFailed {
			return task.StepVerdictFailed, nil
		}
		if result.Err != nil || result.ExitCode != 0 {
			return task.StepVerdictDefault, nil
		}
		execution.ctestDone = true
		return task.StepVerdictSucceeded, nil
	}
	binding, exists := execution.parsers[step.ID]
	if !exists || binding.finished {
		return task.StepVerdictDefault, task.ErrInvalidArgument
	}
	if result.Err != nil {
		return task.StepVerdictDefault, nil
	}
	var (
		discovery testframework.DiscoveryResult
		err       error
	)
	if !binding.failed {
		discovery, err = binding.parser.Finish(
			ctx,
			testframework.ProcessResult{
				ExitCode:    result.ExitCode,
				Termination: testframework.ProcessExited,
			},
		)
	}
	container := &execution.containers[binding.containerIndex]
	if err != nil || binding.failed {
		container.DiscoveryFailed = true
		container.Discovery = nil
	} else {
		discovery = cloneDiscoveryResult(discovery)
		container.Discovery = &discovery
		container.DiscoveryFailed = false
	}
	binding.finished = true
	execution.parsers[step.ID] = binding
	return task.StepVerdictSucceeded, nil
}

func (execution *TaskExecution) AfterStep(
	ctx context.Context,
	step task.ExecutionStep,
) (TaskProgress, error) {
	if execution == nil || ctx == nil {
		return TaskProgress{}, task.ErrInvalidArgument
	}
	execution.mu.Lock()
	defer execution.mu.Unlock()
	if execution.complete {
		return TaskProgress{}, task.ErrConflict
	}
	if step.ID == execution.ctestStepID {
		if !execution.ctestDone || execution.next != 0 ||
			len(execution.steps) != 0 {
			return TaskProgress{}, task.ErrConflict
		}
		if err := execution.prepareFrameworkSteps(ctx); err != nil {
			return TaskProgress{}, err
		}
		return execution.nextProgress(ctx)
	}
	binding, exists := execution.parsers[step.ID]
	if !exists || !binding.finished || binding.continued {
		return TaskProgress{}, task.ErrInvalidArgument
	}
	binding.continued = true
	execution.parsers[step.ID] = binding
	if step.ID != execution.lastIssued {
		return TaskProgress{}, nil
	}
	return execution.nextProgress(ctx)
}

func (execution *TaskExecution) prepareFrameworkSteps(
	ctx context.Context,
) error {
	snapshot, err := ctest.ParseShowOnlyJSON(
		execution.ctestOutput,
		execution.service.config.Limits,
	)
	if err != nil {
		return err
	}
	execution.containers = make(
		[]ContainerInput,
		0,
		len(snapshot.Tests),
	)
	execution.bindings = make(
		[]RuntimeBinding,
		0,
		len(snapshot.Tests),
	)
	executables := make(
		[]cmake.FingerprintFile,
		0,
		len(snapshot.Tests),
	)
	for index, raw := range snapshot.Tests {
		if err := ctx.Err(); err != nil {
			return err
		}
		descriptor, err := ctest.BuildDescriptor(
			raw,
			execution.input.Profile,
			execution.input.Targets,
		)
		if err != nil {
			return err
		}
		selectionInput := testframework.SelectionInput{
			Descriptor: descriptor,
			Mappings: append(
				[]testframework.Mapping(nil),
				execution.input.Mappings...,
			),
		}
		if helper, exists :=
			execution.input.Helpers[raw.Name]; exists {
			copy := helper
			selectionInput.Helper = &copy
		}
		selection, err := execution.service.config.Registry.Select(
			ctx,
			selectionInput,
		)
		if err != nil {
			return err
		}
		container := ContainerInput{
			Descriptor: descriptor,
			Selection:  selection,
		}
		containerID, err := testdomain.ContainerID(
			execution.input.Profile.ProjectID,
			raw.Name,
		)
		if err != nil {
			return err
		}
		execution.bindings = append(
			execution.bindings,
			RuntimeBinding{
				ContainerID: containerID,
				Descriptor:  cloneRuntimeDescriptor(descriptor),
				Adapter:     selection.Adapter,
			},
		)
		if descriptor.TargetID != "" {
			executables = append(
				executables,
				descriptor.Executable,
			)
		}
		if selection.Framework !=
			testdomain.FrameworkOpaqueCTest {
			adapter, supported := selection.Adapter.(testframework.TaskDiscoveryAdapter)
			if !supported {
				container.DiscoveryFailed = true
			} else {
				prepared, prepareErr := adapter.PrepareDiscovery(
					ctx,
					cloneRuntimeDescriptor(descriptor),
				)
				if prepareErr != nil ||
					prepared.Parser == nil {
					container.DiscoveryFailed = true
				} else {
					stepID := taskDiscoveryStepID(index)
					execution.steps = append(
						execution.steps,
						task.ExecutionStep{
							ID:      stepID,
							Kind:    task.StepTestDiscovery,
							Process: prepared.Process,
							Public:  prepared.Public,
						},
					)
					execution.parsers[stepID] =
						taskDiscoveryBinding{
							containerIndex: index,
							parser:         prepared.Parser,
						}
					execution.pins[stepID] =
						descriptor.Executable
				}
			}
		}
		execution.containers = append(
			execution.containers,
			container,
		)
	}
	fingerprint := cloneFingerprint(execution.input.Fingerprint)
	fingerprint.CTestSemanticSHA256 = ctest.SemanticHash(snapshot)
	fingerprint.Executables =
		uniqueExecutableFingerprints(executables)
	execution.input.Fingerprint = fingerprint
	return nil
}

func (execution *TaskExecution) nextProgress(
	ctx context.Context,
) (TaskProgress, error) {
	if execution.next < len(execution.steps) {
		end := min(
			execution.next+maxTaskDiscoveryBatch,
			len(execution.steps),
		)
		steps := cloneTaskDiscoverySteps(
			execution.steps[execution.next:end],
		)
		execution.next = end
		execution.lastIssued = steps[len(steps)-1].ID
		pins := make(
			[]cmake.FingerprintFile,
			0,
			len(steps),
		)
		for _, step := range steps {
			pins = append(pins, execution.pins[step.ID])
		}
		return TaskProgress{
			Steps: steps,
			Pins:  uniqueExecutableFingerprints(pins),
		}, nil
	}
	snapshot, err := execution.finalize(ctx)
	if err != nil {
		return TaskProgress{}, err
	}
	execution.complete = true
	execution.lastIssued = ""
	return TaskProgress{Snapshot: &snapshot}, nil
}

func (execution *TaskExecution) finalize(
	ctx context.Context,
) (DiscoverySnapshot, error) {
	catalog, err := execution.service.config.Builder.Build(
		ctx,
		BuildInput{
			ProjectID:   execution.input.Profile.ProjectID,
			ProfileID:   execution.input.Profile.ID,
			GeneratedAt: execution.service.config.Now().UTC(),
			Fingerprint: execution.input.Fingerprint,
			Containers:  execution.containers,
		},
	)
	if err != nil {
		return DiscoverySnapshot{}, err
	}
	artifact, err :=
		execution.service.config.Artifacts.CommitTestCatalog(
			ctx,
			execution.input.TaskID,
			execution.input.ArtifactID,
			catalog.GeneratedAt,
			catalog,
		)
	if err != nil {
		return DiscoverySnapshot{}, err
	}
	if err := execution.service.config.Catalogs.PublishCatalog(
		ctx,
		catalog,
		artifact,
	); err != nil {
		return DiscoverySnapshot{}, err
	}
	frameworks := make(
		map[testdomain.ID]testdomain.Framework,
		len(catalog.Containers),
	)
	for _, container := range catalog.Containers {
		frameworks[container.ID] = container.Framework
	}
	for index := range execution.bindings {
		if frameworks[execution.bindings[index].ContainerID] ==
			testdomain.FrameworkOpaqueCTest {
			execution.bindings[index].Adapter = nil
		}
	}
	return DiscoverySnapshot{
		Catalog:  catalog,
		Bindings: execution.bindings,
	}.Clone(), nil
}

func taskDiscoveryStepID(index int) string {
	return "framework-discovery-" +
		leftPadDiscoveryIndex(index+1)
}

func leftPadDiscoveryIndex(value int) string {
	result := strconv.Itoa(value)
	for len(result) < 6 {
		result = "0" + result
	}
	return result
}

func cloneDiscoveryInput(value DiscoveryInput) DiscoveryInput {
	result := value
	result.Targets = make([]cmake.Target, len(value.Targets))
	for index := range value.Targets {
		result.Targets[index] = value.Targets[index]
		result.Targets[index].Artifacts = append(
			[]string(nil),
			value.Targets[index].Artifacts...,
		)
	}
	result.Helpers = make(
		map[string]testframework.Declaration,
		len(value.Helpers),
	)
	for key, declaration := range value.Helpers {
		result.Helpers[key] = declaration
	}
	result.Mappings = append(
		[]testframework.Mapping(nil),
		value.Mappings...,
	)
	result.Fingerprint = cloneFingerprint(value.Fingerprint)
	return result
}

func cloneTaskDiscoverySteps(
	values []task.ExecutionStep,
) []task.ExecutionStep {
	result := make([]task.ExecutionStep, len(values))
	for index, value := range values {
		result[index] = value
		result[index].Process.Args = append(
			[]string(nil),
			value.Process.Args...,
		)
		result[index].Process.Env = append(
			[]string(nil),
			value.Process.Env...,
		)
		result[index].Process.EnvUnset = append(
			[]string(nil),
			value.Process.EnvUnset...,
		)
		result[index].Public.Args = append(
			[]string(nil),
			value.Public.Args...,
		)
		result[index].State = append(
			[]byte(nil),
			value.State...,
		)
	}
	return result
}
