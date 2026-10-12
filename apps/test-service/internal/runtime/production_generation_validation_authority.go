package runtime

import (
	"context"
	"os"
	"reflect"
	"sort"
	"sync"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	coverage "unit-test-ide.local/test-service/internal/coveragemodel/v1"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenvalidate"
	"unit-test-ide.local/test-service/internal/workspace"
)

type productionCoverageBaselineReader interface {
	ReadCoverageBaseline(context.Context, string) ([]byte, error)
}

type productionValidationAuthorityConfig struct {
	root     workspace.Root
	resolver productionResolverAuthority
	baseline productionCoverageBaselineReader
}

type productionValidationAuthorityRecord struct {
	binding       productionValidationBinding
	resolved      testgenvalidate.ResolvedCandidate
	baseline      []byte
	receiptDigest string
}

type runtimeProductionValidationAuthority struct {
	config  productionValidationAuthorityConfig
	mu      sync.Mutex
	records map[string]productionValidationAuthorityRecord
}

func newRuntimeProductionValidationAuthority(config productionValidationAuthorityConfig) (*runtimeProductionValidationAuthority, error) {
	if config.root.NativePath == "" || config.root.ID == "" || config.resolver == nil || config.baseline == nil {
		return nil, task.ErrStorageUnavailable
	}
	return &runtimeProductionValidationAuthority{config: config, records: make(map[string]productionValidationAuthorityRecord)}, nil
}

func (authority *runtimeProductionValidationAuthority) Ready() bool {
	return authority != nil && authority.config.root.NativePath != "" && authority.config.root.ID != "" && authority.config.resolver != nil && authority.config.baseline != nil
}

func validProductionValidationBinding(binding productionValidationBinding) bool {
	sealed := binding
	sealed.ValidationID = ""
	if !validProductionDigest(binding.ValidationID) || productionValidationDigest(sealed) != binding.ValidationID ||
		!validProductionObjectID(binding.RunID) || !validProductionObjectID(binding.TaskID) ||
		binding.TaskDigest != productionBytesDigest([]byte(binding.TaskID)) || !validProductionDigest(binding.SnapshotDigest) ||
		!validProductionDigest(binding.EditDigest) || !validProductionDigest(binding.AssertionDigest) ||
		binding.ProjectID == "" || len(binding.ProjectID) > 128 || !validProductionDigest(binding.WorkspaceGeneration) ||
		!validProductionObjectID(binding.CoverageReportID) || !validProductionDigest(binding.BaselineReportDigest) ||
		binding.SourceRelativePath == "" || !validProductionDigest(binding.SourceDigest) || binding.TargetSymbol == "" ||
		len(binding.TargetFunctionIDs) == 0 || len(binding.TargetFunctionIDs) != len(binding.CoverageFunctionIDs) {
		return false
	}
	if binding.Kind != testgendomain.KindVerified && binding.Kind != testgendomain.KindCharacterization {
		return false
	}
	for index, id := range binding.TargetFunctionIDs {
		if !validProductionDigest(id) || index > 0 && binding.TargetFunctionIDs[index-1] >= id {
			return false
		}
	}
	for index, id := range binding.CoverageFunctionIDs {
		if !validProductionObjectID(id) || index > 0 && binding.CoverageFunctionIDs[index-1] >= id {
			return false
		}
	}
	return true
}

func (authority *runtimeProductionValidationAuthority) Prepare(ctx context.Context, binding productionValidationBinding) (productionValidationInput, error) {
	record, err := authority.resolveCurrent(ctx, binding)
	if err != nil {
		return productionValidationInput{}, err
	}
	authority.mu.Lock()
	if existing, ok := authority.records[binding.ValidationID]; ok &&
		(!reflect.DeepEqual(existing.binding, record.binding) || !reflect.DeepEqual(existing.resolved, record.resolved) ||
			!reflect.DeepEqual(existing.baseline, record.baseline)) {
		authority.mu.Unlock()
		return productionValidationInput{}, errProductionValidationUnavailable
	}
	authority.records[binding.ValidationID] = record
	authority.mu.Unlock()
	return productionValidationInput{BaselineCoverage: append([]byte(nil), record.baseline...), Metrics: testgenvalidate.Metrics{Lines: true}}, nil
}

func (authority *runtimeProductionValidationAuthority) ResolveCandidate(ctx context.Context, candidateID string) (testgenvalidate.ResolvedCandidate, error) {
	if authority == nil || ctx == nil || ctx.Err() != nil || !validProductionDigest(candidateID) {
		return testgenvalidate.ResolvedCandidate{}, errProductionValidationUnavailable
	}
	authority.mu.Lock()
	record, ok := authority.records[candidateID]
	authority.mu.Unlock()
	if !ok {
		return testgenvalidate.ResolvedCandidate{}, errProductionValidationUnavailable
	}
	result := record.resolved
	result.TargetLines = append([]int64(nil), result.TargetLines...)
	result.TargetFunctionIDs = append([]string(nil), result.TargetFunctionIDs...)
	return result, nil
}

func (authority *runtimeProductionValidationAuthority) VerifyEvidence(ctx context.Context, candidateID string, evidence testgenvalidate.AssertionEvidence) bool {
	if authority == nil || ctx == nil || ctx.Err() != nil {
		return false
	}
	authority.mu.Lock()
	record, ok := authority.records[candidateID]
	authority.mu.Unlock()
	if !ok || record.binding.Kind != evidence.Kind {
		return false
	}
	if evidence.Kind == testgendomain.KindVerified {
		return evidence.IndependentProofDigest == record.binding.AssertionDigest && evidence.ObservedOutputReceipt == ""
	}
	return evidence.ObservedOutputReceipt == record.binding.AssertionDigest && evidence.IndependentProofDigest == ""
}

func (authority *runtimeProductionValidationAuthority) Verify(ctx context.Context, binding productionValidationBinding, receipts []testgenvalidate.StageReceipt) error {
	if !validProductionReceipts(receipts) {
		return errProductionValidationUnavailable
	}
	current, err := authority.resolveCurrent(ctx, binding)
	if err != nil {
		return err
	}
	receiptDigest := productionValidationDigest(receipts)
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if existing, ok := authority.records[binding.ValidationID]; ok {
		if !reflect.DeepEqual(existing.binding, current.binding) || !reflect.DeepEqual(existing.resolved, current.resolved) ||
			!reflect.DeepEqual(existing.baseline, current.baseline) || existing.receiptDigest != "" && existing.receiptDigest != receiptDigest {
			return errProductionValidationUnavailable
		}
		current.receiptDigest = receiptDigest
	}
	authority.records[binding.ValidationID] = current
	return nil
}

func (authority *runtimeProductionValidationAuthority) resolveCurrent(ctx context.Context, binding productionValidationBinding) (productionValidationAuthorityRecord, error) {
	if authority == nil || ctx == nil || ctx.Err() != nil || !authority.Ready() || !validProductionValidationBinding(binding) {
		return productionValidationAuthorityRecord{}, errProductionValidationUnavailable
	}
	resolvedContext, err := authority.config.resolver.ResolveProductionContext(ctx, binding.ProjectID, binding.WorkspaceGeneration, binding.CoverageReportID)
	if err != nil {
		return productionValidationAuthorityRecord{}, err
	}
	if resolvedContext.baselineReportDigest != binding.BaselineReportDigest || resolvedContext.index.ProjectID != binding.ProjectID ||
		resolvedContext.index.WorkspaceGeneration != binding.WorkspaceGeneration || resolvedContext.index.ReportID != binding.CoverageReportID {
		return productionValidationAuthorityRecord{}, errProductionValidationUnavailable
	}
	file, lines, err := productionValidationLines(resolvedContext.index, binding)
	if err != nil {
		return productionValidationAuthorityRecord{}, err
	}
	baseline, err := authority.config.baseline.ReadCoverageBaseline(ctx, binding.CoverageReportID)
	if err != nil || len(baseline) == 0 || len(baseline) > 32<<20 {
		return productionValidationAuthorityRecord{}, errProductionValidationUnavailable
	}
	document, err := coverage.Decode(baseline)
	if err != nil || document.Completeness.Outcome != coverage.Available || !productionBaselineMatchesIndex(document, file) {
		return productionValidationAuthorityRecord{}, errProductionValidationUnavailable
	}
	sourcePath, err := authority.config.root.ResolveRelative(binding.SourceRelativePath)
	if err != nil {
		return productionValidationAuthorityRecord{}, errProductionValidationUnavailable
	}
	info, err := os.Lstat(sourcePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 0 || info.Size() > 1<<20 {
		return productionValidationAuthorityRecord{}, errProductionValidationUnavailable
	}
	source, err := os.ReadFile(sourcePath)
	if err != nil || productionBytesDigest(source) != binding.SourceDigest {
		return productionValidationAuthorityRecord{}, errProductionValidationUnavailable
	}
	snapshotDigest, err := testgenvalidate.WorkspaceSnapshotDigest(authority.config.root.NativePath)
	if err != nil || !validProductionDigest(snapshotDigest) {
		return productionValidationAuthorityRecord{}, errProductionValidationUnavailable
	}
	resolved := testgenvalidate.ResolvedCandidate{
		ID: binding.ValidationID, Kind: binding.Kind, TargetSymbol: binding.TargetSymbol,
		TargetFileURI: binding.SourceRelativePath, TargetLines: lines,
		TargetFunctionIDs: append([]string(nil), binding.TargetFunctionIDs...),
		BaselineSHA256:    productionBytesDigest(baseline), SourceSnapshotDigest: snapshotDigest, AssertionDigest: binding.AssertionDigest,
	}
	return productionValidationAuthorityRecord{binding: cloneProductionValidationBinding(binding), resolved: resolved, baseline: append([]byte(nil), baseline...)}, nil
}

func productionValidationLines(index coveragedetail.Index, binding productionValidationBinding) (coveragedetail.File, []int64, error) {
	var selected *coveragedetail.File
	for fileIndex := range index.Files {
		file := &index.Files[fileIndex]
		if file.RelativePath != binding.SourceRelativePath {
			continue
		}
		if selected != nil || file.ID == "" || file.SourceSHA256 != binding.SourceDigest || file.Status != coveragedetail.StatusCurrent {
			return coveragedetail.File{}, nil, errProductionValidationUnavailable
		}
		selected = file
	}
	if selected == nil {
		return coveragedetail.File{}, nil, errProductionValidationUnavailable
	}
	wantsFile := binding.TargetSymbol == "file:"+selected.ID
	if !wantsFile && (len(binding.CoverageFunctionIDs) != 1 || binding.TargetSymbol != "fn:"+binding.TargetFunctionIDs[0]) {
		return coveragedetail.File{}, nil, errProductionValidationUnavailable
	}
	wanted := make(map[string]struct{}, len(binding.CoverageFunctionIDs))
	for _, id := range binding.CoverageFunctionIDs {
		wanted[id] = struct{}{}
	}
	currentFunctionIDs := make([]string, 0, len(selected.Functions))
	lineSet := make(map[int64]struct{})
	for _, function := range selected.Functions {
		if function.Status != coveragedetail.StatusCurrent {
			return coveragedetail.File{}, nil, errProductionValidationUnavailable
		}
		currentFunctionIDs = append(currentFunctionIDs, function.ID)
		if _, ok := wanted[function.ID]; !ok {
			continue
		}
		delete(wanted, function.ID)
		for _, line := range function.Lines {
			if line.Line <= 0 {
				return coveragedetail.File{}, nil, errProductionValidationUnavailable
			}
			lineSet[line.Line] = struct{}{}
		}
	}
	sort.Strings(currentFunctionIDs)
	if len(wanted) != 0 || wantsFile && !reflect.DeepEqual(currentFunctionIDs, binding.CoverageFunctionIDs) || len(lineSet) == 0 {
		return coveragedetail.File{}, nil, errProductionValidationUnavailable
	}
	lines := make([]int64, 0, len(lineSet))
	for line := range lineSet {
		lines = append(lines, line)
	}
	sort.Slice(lines, func(left, right int) bool { return lines[left] < lines[right] })
	return *selected, lines, nil
}

func productionBaselineMatchesIndex(document coverage.CoverageDocumentV1, indexed coveragedetail.File) bool {
	found := false
	for _, file := range document.Files {
		if file.URI != indexed.RelativePath {
			continue
		}
		if found || file.Sha256 != indexed.SourceSHA256 || len(file.Lines) != len(indexed.Lines) {
			return false
		}
		found = true
		for index := range file.Lines {
			if file.Lines[index].Line != indexed.Lines[index].Line || file.Lines[index].Count != indexed.Lines[index].Count {
				return false
			}
		}
	}
	return found
}

func cloneProductionValidationBinding(value productionValidationBinding) productionValidationBinding {
	value.TargetFunctionIDs = append([]string(nil), value.TargetFunctionIDs...)
	value.CoverageFunctionIDs = append([]string(nil), value.CoverageFunctionIDs...)
	return value
}

var _ productionValidationAuthority = (*runtimeProductionValidationAuthority)(nil)
