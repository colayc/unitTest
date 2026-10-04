package runtime

import (
	"context"
	"reflect"
	"sort"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenvalidate"
	"unit-test-ide.local/test-service/internal/workspace"
)

type productionManagedSelectionRunReader interface {
	GetGeneration(context.Context, string) (testgendomain.Run, error)
}

type productionManagedSelectionIndexReader interface {
	ReadCurrentCoverageIndex(context.Context, coveragedetail.CurrentIndexQuery) (coveragedetail.Index, error)
}

type productionManagedSelectionAuthorityConfig struct {
	root     workspace.Root
	store    productionManagedSelectionRunReader
	resolver productionGenerationSnapshotResolver
	current  productionManagedSelectionIndexReader
}

// productionManagedSelectionAuthority reconstructs the selected-output
// validator context from current service-owned state. The selected files are
// intentionally not an input here: the publisher and validator bind those
// bytes independently through SelectedOutputDigest.
type productionManagedSelectionAuthority struct {
	config productionManagedSelectionAuthorityConfig
}

func newProductionManagedSelectionAuthority(config productionManagedSelectionAuthorityConfig) (*productionManagedSelectionAuthority, error) {
	if config.root.NativePath == "" || config.root.ID == "" || config.store == nil || config.resolver == nil || config.current == nil {
		return nil, task.ErrStorageUnavailable
	}
	return &productionManagedSelectionAuthority{config: config}, nil
}

func (authority *productionManagedSelectionAuthority) Resolve(ctx context.Context, runID string) (testgenvalidate.SelectionContext, error) {
	if authority == nil || ctx == nil || ctx.Err() != nil || !validProductionObjectID(runID) {
		return testgenvalidate.SelectionContext{}, errProductionManagedUnavailable
	}
	run, err := authority.config.store.GetGeneration(ctx, runID)
	if err != nil {
		return testgenvalidate.SelectionContext{}, err
	}
	if testgendomain.ValidateRun(run) != nil || run.ID != runID || run.State != testgendomain.StateAwaitingConfirmation ||
		run.Request.ManagedSelectionID() == "" || run.Record.SnapshotDigest != testgendomain.NewGenerationRecord(run.Request).SnapshotDigest {
		return testgenvalidate.SelectionContext{}, errProductionManagedUnavailable
	}
	target, err := authority.config.resolver.ResolveRequest(ctx, run.Request)
	if err != nil || !target.valid() || !target.managed || !reflect.DeepEqual(target.request, run.Request) {
		return testgenvalidate.SelectionContext{}, errProductionManagedUnavailable
	}
	index, err := authority.config.current.ReadCurrentCoverageIndex(ctx, coveragedetail.CurrentIndexQuery{
		ProjectID: run.Request.ProjectID, ReportID: run.Request.CoverageReportID, WorkspaceGeneration: run.Request.WorkspaceGeneration,
	})
	if err != nil {
		return testgenvalidate.SelectionContext{}, err
	}
	if index.ProjectID != run.Request.ProjectID || index.ReportID != run.Request.CoverageReportID ||
		index.WorkspaceGeneration != run.Request.WorkspaceGeneration || !validProductionDigest(index.ToolchainID) {
		return testgenvalidate.SelectionContext{}, errProductionManagedUnavailable
	}
	selected, err := productionSelectedCoverage(index)
	if err != nil {
		return testgenvalidate.SelectionContext{}, err
	}
	sourceDigest, err := testgenvalidate.WorkspaceSnapshotDigest(authority.config.root.NativePath)
	if err != nil || !validProductionDigest(sourceDigest) {
		return testgenvalidate.SelectionContext{}, errProductionManagedUnavailable
	}
	return testgenvalidate.SelectionContext{
		SnapshotDigest: run.Record.SnapshotDigest,
		ToolchainID:    index.ToolchainID,
		SourceDigest:   sourceDigest,
		Baseline:       selected,
	}, nil
}

func productionSelectedCoverage(index coveragedetail.Index) (testgenvalidate.SelectedCoverage, error) {
	if index.Project.Status != coveragedetail.StatusCurrent {
		return testgenvalidate.SelectedCoverage{}, errProductionManagedUnavailable
	}
	if _, err := coveragedomain.NewSummary(index.Project.Summary); err != nil || len(index.Files) == 0 || len(index.Files) > 10000 {
		return testgenvalidate.SelectedCoverage{}, errProductionManagedUnavailable
	}
	result := testgenvalidate.SelectedCoverage{Project: index.Project.Summary}
	seenFiles := make(map[string]bool, len(index.Files))
	seenFunctions := map[string]bool{}
	var project coveragedomain.Summary
	for _, file := range index.Files {
		if !validProductionObjectID(file.ID) || seenFiles[file.ID] || file.Status != coveragedetail.StatusCurrent ||
			!validProductionDigest(file.SourceSHA256) || len(file.Functions) > 100000 {
			return testgenvalidate.SelectedCoverage{}, errProductionManagedUnavailable
		}
		if _, err := coveragedomain.NewSummary(file.Summary); err != nil {
			return testgenvalidate.SelectedCoverage{}, errProductionManagedUnavailable
		}
		var err error
		project, err = coveragedomain.AddSummary(project, file.Summary)
		if err != nil {
			return testgenvalidate.SelectedCoverage{}, errProductionManagedUnavailable
		}
		seenFiles[file.ID] = true
		result.Files = append(result.Files, testgenvalidate.SelectedFileCoverage{ID: file.ID, Summary: file.Summary})
		var functionMetric coveragedomain.Metric
		for _, function := range file.Functions {
			if !validProductionObjectID(function.ID) || seenFunctions[function.ID] || function.Status != coveragedetail.StatusCurrent {
				return testgenvalidate.SelectedCoverage{}, errProductionManagedUnavailable
			}
			if _, err := coveragedomain.NewSummary(function.Summary); err != nil || !productionCoverageWithin(function.Summary, file.Summary) ||
				functionMetric.Covered > coveragedomain.MaxSafeInteger-function.Summary.Functions.Covered ||
				functionMetric.Total > coveragedomain.MaxSafeInteger-function.Summary.Functions.Total {
				return testgenvalidate.SelectedCoverage{}, errProductionManagedUnavailable
			}
			functionMetric.Covered += function.Summary.Functions.Covered
			functionMetric.Total += function.Summary.Functions.Total
			seenFunctions[function.ID] = true
			result.Functions = append(result.Functions, testgenvalidate.SelectedFunctionCoverage{
				ID: function.ID, FileID: file.ID, Summary: function.Summary,
			})
		}
		if functionMetric != file.Summary.Functions {
			return testgenvalidate.SelectedCoverage{}, errProductionManagedUnavailable
		}
	}
	if project != index.Project.Summary || len(result.Functions) == 0 || len(result.Functions) > 100000 {
		return testgenvalidate.SelectedCoverage{}, errProductionManagedUnavailable
	}
	sort.Slice(result.Files, func(left, right int) bool { return result.Files[left].ID < result.Files[right].ID })
	sort.Slice(result.Functions, func(left, right int) bool { return result.Functions[left].ID < result.Functions[right].ID })
	return result, nil
}

func productionCoverageWithin(value, parent coveragedomain.Summary) bool {
	return value.Functions.Covered <= parent.Functions.Covered && value.Functions.Total <= parent.Functions.Total &&
		value.Lines.Covered <= parent.Lines.Covered && value.Lines.Total <= parent.Lines.Total &&
		value.Branches.Covered <= parent.Branches.Covered && value.Branches.Total <= parent.Branches.Total
}

var _ testgenvalidate.SelectionResolver = (&productionManagedSelectionAuthority{}).Resolve
