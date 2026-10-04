package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
	"time"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/discovery"
	generationv15 "unit-test-ide.local/test-service/internal/protocolmodel/v1_5/testgeneration"
	generationv16 "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/testgeneration"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgenanalysis"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenrender"
	"unit-test-ide.local/test-service/internal/testgensolver"
	"unit-test-ide.local/test-service/internal/workspace"
)

type productionResolverContext struct {
	snapshot             discovery.Snapshot
	project              workspace.ProjectConfig
	profile              cmake.BuildProfile
	targets              []cmake.Target
	index                coveragedetail.Index
	baselineReportDigest string
}

type productionResolverAuthority interface {
	ResolveProductionContext(context.Context, string, string, string) (productionResolverContext, error)
}

type productionTestLayout struct {
	framework       testgendomain.Framework
	frameworkDigest string
	ctestName       string
	renderTarget    testgenrender.TargetMetadata
}

type productionTestLayoutAuthority interface {
	ResolveProductionTestLayout(context.Context, productionResolverContext, productionCompileBinding, testgendomain.Framework) (productionTestLayout, error)
}

type productionTargetResolverConfig struct {
	root                 workspace.Root
	authority            productionResolverAuthority
	layout               productionTestLayoutAuthority
	analyzerBundleDigest string
	analyzerVersion      string
	processOwnerDigest   string
}

type productionGenerationTargetResolver struct {
	config productionTargetResolverConfig
}

func newProductionGenerationTargetResolver(config productionTargetResolverConfig) (*productionGenerationTargetResolver, error) {
	if config.root.NativePath == "" || config.root.ID == "" || config.authority == nil || config.layout == nil ||
		!validProductionDigest(config.analyzerBundleDigest) || config.analyzerVersion == "" || len(config.analyzerVersion) > 128 ||
		!validProductionDigest(config.processOwnerDigest) {
		return nil, task.ErrStorageUnavailable
	}
	return &productionGenerationTargetResolver{config: config}, nil
}

func (resolver *productionGenerationTargetResolver) Targets(context.Context, generationv15.TestGenerationTargetListRequestV15) (generationv15.TestGenerationTargetListV15, error) {
	// Production generation is report-bound. The v1.5 target-list shape cannot
	// carry that identity, so it intentionally advertises no unbound targets.
	return generationv15.TestGenerationTargetListV15{Items: []generationv15.TestGenerationTargetV15{}}, nil
}

func (resolver *productionGenerationTargetResolver) ResolveStart(context.Context, generationv15.TestGenerationStartRequestV15) (generationTarget, error) {
	return generationTarget{}, errProductionGenerationUnavailable
}

func (resolver *productionGenerationTargetResolver) ResolveManagedStart(ctx context.Context, owner string, input generationv16.TestGenerationStartRequestV16, selected testgendomain.ManagedTarget) (generationTarget, error) {
	if resolver == nil || ctx == nil || !validProductionDigest(owner) || input.CoverageReportID == nil || *input.CoverageReportID == "" {
		return generationTarget{}, errProductionGenerationUnavailable
	}
	scope, selectionID, ok := productionManagedProtocolSelection(input)
	if !ok || selectionID != selected.SelectionID(scope) || selected.SourceDigest == "" {
		return generationTarget{}, errProductionGenerationUnavailable
	}
	framework, ok := productionDomainFramework(input.Framework)
	if !ok {
		return generationTarget{}, errProductionGenerationUnavailable
	}
	request := testgendomain.Request{
		IdempotencyKey: input.IdempotencyKey, WorkspaceGeneration: input.WorkspaceGeneration,
		ProjectID: input.ProjectID, Scope: scope, CoverageReportID: *input.CoverageReportID,
		ManagedTargetID: selectionID, ManagedGapID: "", Framework: framework,
		Goals:              testgendomain.Goals{FunctionPercent: input.Goals.FunctionPercent, LinePercent: input.Goals.LinePercent, BranchPercent: input.Goals.BranchPercent},
		Budgets:            testgendomain.Budgets{WallTimeMS: input.Budgets.WallTimeMS, CandidateCount: input.Budgets.CandidateCount, MemoryMiB: input.Budgets.MemoryMiB, Concurrency: input.Budgets.Concurrency},
		SessionOwnerDigest: owner,
	}
	if scope == testgendomain.ScopeCoverageGap {
		request.ManagedTargetID, request.ManagedGapID = "", selectionID
	}
	return resolver.resolve(ctx, request, selected)
}

func (resolver *productionGenerationTargetResolver) ResolveRequest(ctx context.Context, request testgendomain.Request) (generationTarget, error) {
	if resolver == nil || ctx == nil || testgendomain.ValidateRequest(request) != nil || request.ManagedSelectionID() == "" {
		return generationTarget{}, errProductionGenerationUnavailable
	}
	contextValue, err := resolver.config.authority.ResolveProductionContext(ctx, request.ProjectID, request.WorkspaceGeneration, request.CoverageReportID)
	if err != nil {
		return generationTarget{}, err
	}
	selected, err := testgendomain.ResolveManagedTarget(testgendomain.ManagedSelector{
		ProjectID: request.ProjectID, WorkspaceGeneration: request.WorkspaceGeneration,
		CoverageReportID: request.CoverageReportID, Scope: request.Scope, ID: request.ManagedSelectionID(),
	}, contextValue.index)
	if err != nil {
		return generationTarget{}, err
	}
	target, err := resolver.resolveContext(ctx, request, selected, contextValue)
	if err != nil || !reflect.DeepEqual(target.request, request) {
		return generationTarget{}, testgendomain.ErrStaleSnapshot
	}
	return target, nil
}

func (resolver *productionGenerationTargetResolver) resolve(ctx context.Context, request testgendomain.Request, selected testgendomain.ManagedTarget) (generationTarget, error) {
	contextValue, err := resolver.config.authority.ResolveProductionContext(ctx, request.ProjectID, request.WorkspaceGeneration, request.CoverageReportID)
	if err != nil {
		return generationTarget{}, err
	}
	current, err := testgendomain.ResolveManagedTarget(testgendomain.ManagedSelector{
		ProjectID: request.ProjectID, WorkspaceGeneration: request.WorkspaceGeneration,
		CoverageReportID: request.CoverageReportID, Scope: request.Scope, ID: request.ManagedSelectionID(),
	}, contextValue.index)
	if err != nil || !reflect.DeepEqual(current, selected) {
		return generationTarget{}, testgendomain.ErrStaleSnapshot
	}
	return resolver.resolveContext(ctx, request, selected, contextValue)
}

func (resolver *productionGenerationTargetResolver) resolveContext(ctx context.Context, request testgendomain.Request, selected testgendomain.ManagedTarget, contextValue productionResolverContext) (generationTarget, error) {
	if err := ctx.Err(); err != nil {
		return generationTarget{}, err
	}
	if contextValue.snapshot.Generation != request.WorkspaceGeneration || contextValue.project.ID != request.ProjectID ||
		contextValue.profile.ID == "" || contextValue.profile.ProjectID != request.ProjectID ||
		contextValue.index.WorkspaceGeneration != request.WorkspaceGeneration || contextValue.index.ProjectID != request.ProjectID ||
		contextValue.index.ReportID != request.CoverageReportID || contextValue.index.ToolchainID == "" ||
		!validProductionDigest(contextValue.baselineReportDigest) || selected.SourceDigest == "" {
		return generationTarget{}, testgendomain.ErrStaleSnapshot
	}
	binding, err := resolveProductionCompileBinding(resolver.config.root, selected.File, contextValue.targets, contextValue.index.ToolchainID)
	if err != nil {
		return generationTarget{}, err
	}
	layout, err := resolver.config.layout.ResolveProductionTestLayout(ctx, contextValue, binding, request.Framework)
	if err != nil || layout.framework == testgendomain.FrameworkAuto || !validProductionDigest(layout.frameworkDigest) || !validProductionCTestName(layout.ctestName) {
		return generationTarget{}, errProductionGenerationUnavailable
	}
	if request.Framework != testgendomain.FrameworkAuto && request.Framework != layout.framework {
		return generationTarget{}, errProductionGenerationUnavailable
	}
	coverageDigest, err := productionCanonicalDigest("coverage-index-v1", contextValue.index)
	if err != nil {
		return generationTarget{}, errProductionGenerationUnavailable
	}
	request.Framework = layout.framework
	request.CompileSnapshotDigest = binding.compileSnapshotDigest
	request.CoverageSnapshotDigest = coverageDigest
	request.SourceDigest = selected.SourceDigest
	request.CMakeTargetDigest = binding.cmakeTargetDigest
	request.FrameworkBundleDigest = layout.frameworkDigest
	request.AnalyzerBundleDigest = resolver.config.analyzerBundleDigest
	request.BaselineReportDigest = contextValue.baselineReportDigest
	request.ProcessOwnerDigest = resolver.config.processOwnerDigest
	if testgendomain.ValidateRequest(request) != nil {
		return generationTarget{}, errProductionGenerationUnavailable
	}
	functions, err := productionSelectedFunctions(contextValue.index, selected, request.Scope, binding.compileSnapshotDigest, resolver.config.analyzerVersion)
	if err != nil || len(functions) == 0 || len(functions) > int(request.Budgets.CandidateCount) {
		return generationTarget{}, errProductionGenerationUnavailable
	}
	analysisTimeout := time.Duration(request.Budgets.WallTimeMS) * time.Millisecond
	if analysisTimeout > 30*time.Second {
		analysisTimeout = 30 * time.Second
	}
	target := generationTarget{
		request: request, projectID: request.ProjectID, workspaceGeneration: request.WorkspaceGeneration,
		fileID: selected.FileID, coverageReportID: request.CoverageReportID, sourceDigest: selected.SourceDigest,
		compileSnapshotDigest: binding.compileSnapshotDigest, cmakeTargetDigest: binding.cmakeTargetDigest,
		toolchainID: contextValue.index.ToolchainID, framework: string(layout.framework), frameworkDigest: layout.frameworkDigest,
		ctestName: layout.ctestName, analyzerBundleDigest: resolver.config.analyzerBundleDigest, language: binding.language, headerPath: binding.headerRelative,
		managed: true, sourceRelativePath: selected.File,
		analysis: testgenanalysis.AnalysisRequest{
			WorkspaceRoot: resolver.config.root.NativePath, SourceRelative: selected.File, SourceDigest: selected.SourceDigest,
			CompileSnapshotDigest: binding.compileSnapshotDigest, Arguments: append([]string(nil), binding.arguments...), Timeout: analysisTimeout,
		},
		renderTarget: layout.renderTarget, wallTime: time.Duration(request.Budgets.WallTimeMS) * time.Millisecond,
		candidateLimit: int(request.Budgets.CandidateCount), memoryBytes: min(request.Budgets.MemoryMiB, int64(1024)) << 20,
		concurrency: min(int(request.Budgets.Concurrency), 16),
	}
	if request.Scope == testgendomain.ScopeFile {
		target.functions = functions
	} else {
		target.functionID, target.linkageName, target.gap = functions[0].functionID, functions[0].linkageName, functions[0].gap
	}
	if !target.valid() {
		return generationTarget{}, errProductionGenerationUnavailable
	}
	return target, nil
}

func productionSelectedFunctions(index coveragedetail.Index, selected testgendomain.ManagedTarget, scope testgendomain.Scope, compileDigest, analyzerVersion string) ([]generationFunctionTarget, error) {
	if scope == testgendomain.ScopeCoverageGap {
		return nil, errProductionGenerationUnavailable
	}
	result := []generationFunctionTarget{}
	for _, file := range index.Files {
		if file.ID != selected.FileID || file.RelativePath != selected.File || file.SourceSHA256 != selected.SourceDigest || file.Status != coveragedetail.StatusCurrent {
			continue
		}
		for _, function := range file.Functions {
			if scope == testgendomain.ScopeSymbol && function.ID != selected.FunctionID {
				continue
			}
			if function.Status != coveragedetail.StatusCurrent || !validProductionObjectID(function.ID) || !validProductionLinkage(function.LinkageName) {
				return nil, errProductionGenerationUnavailable
			}
			result = append(result, generationFunctionTarget{
				functionID: function.ID, linkageName: function.LinkageName,
				gap: testgensolver.CoverageGap{Kind: testgensolver.GapFunction, CompileSnapshot: compileDigest, AnalyzerVersion: analyzerVersion},
			})
		}
		break
	}
	sort.Slice(result, func(left, right int) bool { return result[left].functionID < result[right].functionID })
	if scope == testgendomain.ScopeSymbol && len(result) != 1 || scope == testgendomain.ScopeFile && len(result) == 0 {
		return nil, errProductionGenerationUnavailable
	}
	return result, nil
}

func productionManagedProtocolSelection(input generationv16.TestGenerationStartRequestV16) (testgendomain.Scope, string, bool) {
	if input.CoverageReportID == nil || input.TargetID != nil {
		return "", "", false
	}
	switch input.Scope {
	case generationv16.Symbol:
		if input.FileID != nil || input.CoverageGapID != nil {
			return "", "", false
		}
		value, ok := productionOptionalString(input.FunctionID)
		return testgendomain.ScopeSymbol, value, ok
	case generationv16.File:
		if input.FunctionID != nil || input.CoverageGapID != nil {
			return "", "", false
		}
		value, ok := productionOptionalString(input.FileID)
		return testgendomain.ScopeFile, value, ok
	case generationv16.TestGenerationScopeV16CoverageGap:
		if input.FileID != nil || input.FunctionID != nil {
			return "", "", false
		}
		value, ok := productionOptionalString(input.CoverageGapID)
		return testgendomain.ScopeCoverageGap, value, ok
	default:
		return "", "", false
	}
}

func productionOptionalString(value *string) (string, bool) {
	if value == nil || *value == "" {
		return "", false
	}
	return *value, true
}

func productionDomainFramework(value generationv16.TestGenerationFrameworkV16) (testgendomain.Framework, bool) {
	switch value {
	case generationv16.Auto:
		return testgendomain.FrameworkAuto, true
	case generationv16.TestGenerationFrameworkV16Cpputest:
		return testgendomain.FrameworkCppUTest, true
	case generationv16.TestGenerationFrameworkV16Unity:
		return testgendomain.FrameworkUnity, true
	default:
		return "", false
	}
}

func productionCanonicalDigest(domain string, value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte(domain+"\x00"), encoded...))
	return hex.EncodeToString(sum[:]), nil
}

var _ productionTargetResolver = (*productionGenerationTargetResolver)(nil)
var _ productionManagedTargetResolver = (*productionGenerationTargetResolver)(nil)
