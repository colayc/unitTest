package testgenpublish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/managedtest"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgenrender"
)

type ManagedDecision struct {
	ReviewID, ReviewDigest string
	Resolutions            map[string]managedtest.ConflictChoice
}

type managedPublication struct {
	acceptances []managedtest.Acceptance
	retirements []managedtest.Retirement
	sources     []preparedSource
	readonly    []PlannedEdit
}

type preparedSource struct {
	path, digest string
	identity     os.FileInfo
}

func (p *Publisher) PlanManaged(ctx context.Context, candidate CandidateSet, decision ManagedDecision) (Plan, error) {
	if p == nil || ctx == nil || p.ManagedRegistry == nil || candidate.Managed == nil ||
		!validHex(candidate.Managed.ReviewID, 32) || !validHex(decision.ReviewID, 32) ||
		!validHex(decision.ReviewDigest, 64) || decision.ReviewID != candidate.Managed.ReviewID ||
		!validHex(candidate.Managed.ValidationReceiptDigest, 64) || len(candidate.Managed.ValidationReceipt) == 0 || len(candidate.Managed.ValidationReceipt) > 1<<20 || digest(candidate.Managed.ValidationReceipt) != candidate.Managed.ValidationReceiptDigest || candidate.Managed.ToolchainID == "" ||
		len(candidate.Managed.Inputs) == 0 || len(candidate.Managed.Inputs) > maxFiles ||
		len(candidate.Managed.Records) == 0 || len(candidate.Managed.Records) > 1000 || len(decision.Resolutions) > 200 {
		return Plan{}, ErrInvalidPlan
	}
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	if err := p.verify(ctx, candidate.SnapshotDigest); err != nil {
		return Plan{}, ErrConflict
	}
	staged := make(map[string]testgenrender.StagedFile, len(candidate.Files))
	for _, file := range candidate.Files {
		if !generatedTestPath(file.Path) && !cmakePath(file.Path) || staged[strings.ToLower(file.Path)].Path != "" {
			return Plan{}, ErrInvalidPlan
		}
		staged[strings.ToLower(file.Path)] = file
	}
	records := make(map[string]managedtest.Record, len(candidate.Managed.Records))
	sources := make(map[string]preparedSource)
	for _, record := range candidate.Managed.Records {
		fileID, fileErr := coveragedetail.StableFileID(record.ProjectID, record.SourceRelativePath)
		if !managedtest.ValidRecord(record) || record.Status != managedtest.StatusCurrent ||
			record.ToolchainID != candidate.Managed.ToolchainID || record.ValidationReceiptDigest != candidate.Managed.ValidationReceiptDigest ||
			fileErr != nil || fileID != record.SourceFileID || records[record.CaseID].CaseID != "" {
			return Plan{}, ErrInvalidPlan
		}
		records[record.CaseID] = record
		if previous, found := sources[record.SourceRelativePath]; found {
			if previous.digest != record.SourceDigest {
				return Plan{}, ErrInvalidPlan
			}
		} else {
			if !validRelative(record.SourceRelativePath) {
				return Plan{}, ErrInvalidPlan
			}
			data, _, exists, identity, err := p.readTarget(record.SourceRelativePath)
			if err != nil || !exists || digest(data) != record.SourceDigest {
				return Plan{}, ErrConflict
			}
			sources[record.SourceRelativePath] = preparedSource{record.SourceRelativePath, record.SourceDigest, identity}
		}
	}
	resolved := make([]testgenrender.StagedFile, 0, len(candidate.Files))
	reviews := make([]managedtest.Review, 0, len(candidate.Managed.Inputs))
	publication := &managedPublication{}
	seenPaths, seenCases, required := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, input := range candidate.Managed.Inputs {
		if err := ctx.Err(); err != nil {
			return Plan{}, err
		}
		generated := input.Generated
		if !generatedTestPath(generated.Path) || seenPaths[strings.ToLower(generated.Path)] ||
			!bytes.Equal(staged[strings.ToLower(generated.Path)].Content, generated.Content) ||
			staged[strings.ToLower(generated.Path)].AfterDigest != digest(generated.Content) {
			return Plan{}, ErrInvalidPlan
		}
		seenPaths[strings.ToLower(generated.Path)] = true
		current, mode, exists, currentIdentity, err := p.readTarget(generated.Path)
		if err != nil {
			return Plan{}, ErrConflict
		}
		if exists && mode&0222 == 0 {
			return Plan{}, ErrConflict
		}
		currentDoc, err := managedtest.ParseDocument(current, maxEditBytes, 4096)
		if err != nil {
			return Plan{}, ErrConflict
		}
		for _, accepted := range input.Accepted {
			if !managedtest.ValidRecord(accepted) || accepted.TestRelativePath != generated.Path || seenCases[accepted.CaseID] {
				return Plan{}, ErrInvalidPlan
			}
			seenCases[accepted.CaseID] = true
			stored, err := p.ManagedRegistry.Get(ctx, accepted.CaseID)
			if err != nil || stored != accepted {
				return Plan{}, ErrConflict
			}
		}
		input.Current = currentDoc
		review, err := managedtest.Reconcile(input)
		if err != nil {
			return Plan{}, ErrConflict
		}
		reviews = append(reviews, review)
		for _, op := range review.Operations {
			if op.Conflict {
				required[op.CaseID] = true
			}
		}
		if review.Scaffold != nil && review.Scaffold.Conflict {
			required["scaffold:"+review.Path] = true
		}
		merged, err := mergeManagedReview(currentDoc, generated.Content, review, decision.Resolutions)
		if err != nil {
			return Plan{}, err
		}
		output, err := managedtest.ParseDocument(merged, maxEditBytes, 4096)
		if err != nil {
			return Plan{}, ErrConflict
		}
		for _, block := range output.Blocks {
			if seenCases[block.CaseID] && !recordAccepted(input.Accepted, block.CaseID) {
				return Plan{}, ErrInvalidPlan
			}
			seenCases[block.CaseID] = true
			record, ok := records[block.CaseID]
			if !ok || record.TestRelativePath != generated.Path || record.FunctionID != block.FunctionID || record.AcceptedBlockDigest != block.Digest {
				return Plan{}, ErrConflict
			}
			old, err := p.ManagedRegistry.Get(ctx, block.CaseID)
			if err == nil {
				if !recordAccepted(input.Accepted, block.CaseID) || old.ProjectID != record.ProjectID || old.SourceFileID != record.SourceFileID || old.FunctionID != record.FunctionID || old.TestRelativePath != record.TestRelativePath || !record.LastVerifiedAt.After(old.LastVerifiedAt) {
					return Plan{}, ErrConflict
				}
			} else if !errors.Is(err, task.ErrNotFound) {
				return Plan{}, ErrConflict
			}
			publication.acceptances = append(publication.acceptances, managedtest.Acceptance{ReviewDigest: decision.ReviewDigest, PreimageDigest: digest(current), PublishedFileDigest: digest(merged), Record: record, At: record.LastVerifiedAt})
		}
		for _, accepted := range input.Accepted {
			if !containsBlock(output, accepted.CaseID) {
				publication.retirements = append(publication.retirements, managedtest.Retirement{CaseID: accepted.CaseID, ReviewDigest: decision.ReviewDigest, PublishedFileDigest: digest(merged), TestRelativePath: generated.Path, At: time.Now().UTC()})
			}
		}
		if bytes.Equal(current, merged) {
			if !exists {
				return Plan{}, ErrInvalidPlan
			}
			publication.readonly = append(publication.readonly, PlannedEdit{Path: generated.Path, BeforeDigest: digest(current), AfterDigest: digest(current)})
			publication.sources = append(publication.sources, preparedSource{generated.Path, digest(current), currentIdentity})
			continue
		}
		beforeDigest := ""
		if exists {
			beforeDigest = digest(current)
		}
		resolved = append(resolved, testgenrender.StagedFile{Path: generated.Path, Content: merged, BeforeDigest: beforeDigest, AfterDigest: digest(merged)})
	}
	if len(seenCases) != len(records) || len(candidate.CaseIDs) != len(seenCases) {
		return Plan{}, ErrInvalidPlan
	}
	caseIDs := map[string]bool{}
	for _, id := range candidate.CaseIDs {
		if !validHex(id, 32) || caseIDs[id] || !seenCases["utc_"+id] {
			return Plan{}, ErrInvalidPlan
		}
		caseIDs[id] = true
	}
	for key, choice := range decision.Resolutions {
		if !required[key] || !managedtest.ValidConflictChoice(choice) {
			return Plan{}, ErrInvalidPlan
		}
	}
	if len(required) != len(decision.Resolutions) {
		return Plan{}, ErrInvalidPlan
	}
	if len(reviews) == 1 {
		if reviews[0].Digest() != decision.ReviewDigest {
			return Plan{}, ErrConflict
		}
	} else {
		sort.Slice(reviews, func(i, j int) bool { return reviews[i].Path < reviews[j].Path })
		encoded, _ := json.Marshal(reviews)
		if digest(append([]byte("managed-review-set-v1\x00"), encoded...)) != decision.ReviewDigest {
			return Plan{}, ErrConflict
		}
	}
	cmakeEdited := false
	for _, file := range candidate.Files {
		if cmakePath(file.Path) {
			cmakeEdited = true
			resolved = append(resolved, file)
		}
	}
	if !cmakePath(candidate.Managed.CMakePath) {
		return Plan{}, ErrInvalidPlan
	}
	if !cmakeEdited {
		cmake, _, exists, identity, err := p.readTarget(candidate.Managed.CMakePath)
		if err != nil || !exists {
			return Plan{}, ErrConflict
		}
		for _, input := range candidate.Managed.Inputs {
			ref, ok := cmakeSourceRef(candidate.Managed.CMakePath, input.Generated.Path)
			if !ok || !managedCMakeLinked(string(cmake), candidate, ref) {
				return Plan{}, ErrConflict
			}
		}
		publication.sources = append(publication.sources, preparedSource{candidate.Managed.CMakePath, digest(cmake), identity})
	}
	candidate.Files = resolved
	candidate.Diff = ""
	var base PublishPlan
	originalConfirmation := ""
	if len(resolved) > 0 {
		var err error
		base, err = p.plan(ctx, candidate, true)
		if err != nil {
			return Plan{}, err
		}
		originalConfirmation = base.ConfirmationDigest
	} else {
		if !validHex(candidate.RunID, 32) || !validHex(candidate.SnapshotDigest, 64) || len(publication.readonly) == 0 || len(candidate.CaseIDs) == 0 {
			return Plan{}, ErrInvalidPlan
		}
		base = PublishPlan{RunID: candidate.RunID, SnapshotDigest: candidate.SnapshotDigest, DiffDigest: digest(nil), CandidateSetDigest: digest([]byte("managed-readonly-v1\x00" + candidate.RunID + candidate.SnapshotDigest + candidate.TestTarget + candidate.ProductionTarget + candidate.FrameworkTarget))}
	}
	base.ManagedReadOnly = append([]PlannedEdit(nil), publication.readonly...)
	decisionDigest := digestManagedDecision(decision)
	evidenceDigest := digestManagedEvidence(candidate.Managed)
	base.ManagedReviewID, base.ManagedReviewDigest, base.ManagedDecisionDigest, base.ManagedEvidenceDigest = decision.ReviewID, decision.ReviewDigest, decisionDigest, evidenceDigest
	readonlyEncoded, _ := json.Marshal(base.ManagedReadOnly)
	base.CandidateSetDigest = digest([]byte("managed-candidate-v1\x00" + base.CandidateSetDigest + decision.ReviewDigest + decisionDigest + evidenceDigest + digest(readonlyEncoded)))
	base.ConfirmationDigest = digest([]byte("managed-confirmation-v1\x00" + base.CandidateSetDigest + base.DiffDigest))
	for i := range publication.acceptances {
		publication.acceptances[i].AcceptanceID = digest([]byte(base.ConfirmationDigest + publication.acceptances[i].Record.CaseID))[:32]
	}
	for i := range publication.retirements {
		publication.retirements[i].ConfirmationDigest = base.ConfirmationDigest
	}
	for _, source := range sources {
		publication.sources = append(publication.sources, source)
	}
	p.mu.Lock()
	prepared := preparedPlan{}
	if originalConfirmation != "" {
		var ok bool
		prepared, ok = p.plans[originalConfirmation]
		if !ok {
			p.mu.Unlock()
			return Plan{}, ErrConflict
		}
		delete(p.plans, originalConfirmation)
	}
	prepared.public = base
	prepared.managed = publication
	p.plans[base.ConfirmationDigest] = prepared
	p.mu.Unlock()
	return base, nil
}

func managedCMakeLinked(cmake string, set CandidateSet, ref string) bool {
	if set.FrameworkTarget == "CppUTest" {
		return strings.Contains(cmake, fmt.Sprintf("target_sources(%s PRIVATE \"%s\")", set.TestTarget, ref))
	}
	if (set.FrameworkTarget == "Unity" || set.FrameworkTarget == "unity") && validHex(set.SymbolID, 64) {
		generated := set.TestTarget + "_generated_" + set.SymbolID[:12]
		return strings.Contains(cmake, fmt.Sprintf("add_executable(%s \"%s\")", generated, ref)) && strings.Contains(cmake, fmt.Sprintf("target_link_libraries(%s PRIVATE %s %s)", generated, set.ProductionTarget, set.FrameworkTarget))
	}
	return false
}

func recordAccepted(records []managedtest.Record, id string) bool {
	for _, r := range records {
		if r.CaseID == id {
			return true
		}
	}
	return false
}
func containsBlock(doc managedtest.Document, id string) bool {
	for _, b := range doc.Blocks {
		if b.CaseID == id {
			return true
		}
	}
	return false
}

func digestManagedDecision(d ManagedDecision) string {
	type choice struct {
		ID     string
		Choice managedtest.ConflictChoice
	}
	choices := make([]choice, 0, len(d.Resolutions))
	for id, value := range d.Resolutions {
		choices = append(choices, choice{id, value})
	}
	sort.Slice(choices, func(i, j int) bool { return choices[i].ID < choices[j].ID })
	encoded, _ := json.Marshal(struct {
		ReviewID, ReviewDigest string
		Choices                []choice
	}{d.ReviewID, d.ReviewDigest, choices})
	return digest(append([]byte("managed-decision-v1\x00"), encoded...))
}

func digestManagedEvidence(value *ManagedCandidateSet) string {
	records := append([]managedtest.Record(nil), value.Records...)
	sort.Slice(records, func(i, j int) bool { return records[i].CaseID < records[j].CaseID })
	accepted := make([]managedtest.Record, 0)
	for _, input := range value.Inputs {
		accepted = append(accepted, input.Accepted...)
	}
	sort.Slice(accepted, func(i, j int) bool { return accepted[i].CaseID < accepted[j].CaseID })
	encoded, _ := json.Marshal(struct {
		CMakePath, ToolchainID, ValidationReceiptDigest string
		Records, Accepted                               []managedtest.Record
	}{value.CMakePath, value.ToolchainID, value.ValidationReceiptDigest, records, accepted})
	return digest(append([]byte("managed-evidence-v1\x00"), encoded...))
}

func mergeManagedReview(current managedtest.Document, generated []byte, review managedtest.Review, choices map[string]managedtest.ConflictChoice) ([]byte, error) {
	generatedDoc, err := managedtest.ParseDocument(generated, maxEditBytes, 4096)
	if err != nil {
		return nil, ErrConflict
	}
	if len(current.Bytes) == 0 {
		return bytes.Clone(generated), nil
	}
	gen := map[string]managedtest.Block{}
	for _, block := range generatedDoc.Blocks {
		gen[block.CaseID] = block
	}
	ops := map[string]managedtest.Operation{}
	for _, op := range review.Operations {
		ops[op.CaseID] = op
	}
	useGeneratedScaffold := review.Scaffold != nil && review.Scaffold.Conflict && choices["scaffold:"+review.Path] == managedtest.UseGenerated
	if review.Scaffold != nil && review.Scaffold.Conflict && !useGeneratedScaffold && choices["scaffold:"+review.Path] != managedtest.KeepCurrent {
		return nil, ErrInvalidPlan
	}
	base := current
	if useGeneratedScaffold {
		base = generatedDoc
	}
	var out bytes.Buffer
	cursor := 0
	for _, block := range base.Blocks {
		out.Write(base.Bytes[cursor:block.StartByte])
		op := ops[block.CaseID]
		choice := choices[block.CaseID]
		old, hasCurrent := blockByID(current, block.CaseID)
		next, hasGenerated := gen[block.CaseID]
		switch {
		case op.Conflict && choice == managedtest.ConvertToManual:
			if hasCurrent {
				out.Write(old.Body)
			}
		case op.Conflict && choice == managedtest.KeepCurrent:
			if hasCurrent {
				out.Write(current.Bytes[old.StartByte:old.EndByte])
			}
		case op.Conflict && choice == managedtest.UseGenerated:
			if hasGenerated {
				out.Write(generatedDoc.Bytes[next.StartByte:next.EndByte])
			}
		case !op.Conflict && (op.Kind == managedtest.OperationUpdate || op.Kind == managedtest.OperationAdd):
			if !hasGenerated {
				return nil, ErrConflict
			}
			out.Write(generatedDoc.Bytes[next.StartByte:next.EndByte])
		case op.Kind == managedtest.OperationUnchanged:
			out.Write(base.Bytes[block.StartByte:block.EndByte])
		default:
			return nil, ErrInvalidPlan
		}
		cursor = block.EndByte
	}
	other := generatedDoc
	if useGeneratedScaffold {
		other = current
	}
	for _, block := range other.Blocks {
		if containsBlock(base, block.CaseID) {
			continue
		}
		op := ops[block.CaseID]
		if op.Conflict {
			choice := choices[block.CaseID]
			if choice == managedtest.ConvertToManual {
				out.Write(block.Body)
				continue
			}
			if useGeneratedScaffold && choice != managedtest.KeepCurrent || !useGeneratedScaffold && choice != managedtest.UseGenerated {
				continue
			}
		}
		if out.Len() > 0 && !bytes.HasSuffix(out.Bytes(), []byte("\n")) {
			return nil, ErrConflict
		}
		out.Write(other.Bytes[block.StartByte:block.EndByte])
	}
	out.Write(base.Bytes[cursor:])
	return out.Bytes(), nil
}

func blockByID(doc managedtest.Document, id string) (managedtest.Block, bool) {
	for _, b := range doc.Blocks {
		if b.CaseID == id {
			return b, true
		}
	}
	return managedtest.Block{}, false
}

func (p *Publisher) PublishManaged(ctx context.Context, plan Plan) (Receipt, error) {
	if p == nil || ctx == nil || !validHex(plan.ManagedReviewID, 32) || !validHex(plan.ManagedReviewDigest, 64) || !validHex(plan.ManagedDecisionDigest, 64) || !validHex(plan.ManagedEvidenceDigest, 64) || p.ManagedRegistry == nil {
		return Receipt{}, ErrInvalidPlan
	}
	if digest([]byte(plan.Diff)) != plan.DiffDigest {
		return Receipt{}, ErrConflict
	}
	p.mu.Lock()
	prepared, planned := p.plans[plan.ConfirmationDigest]
	if planned && (!reflect.DeepEqual(prepared.public, plan) || prepared.managed == nil) {
		p.mu.Unlock()
		return Receipt{}, ErrConflict
	}
	if !planned {
		receipt, exists, err := p.readReceipt(plan.ConfirmationDigest)
		if err != nil || !exists || !matchesReceipt(receipt, plan) {
			p.mu.Unlock()
			return Receipt{}, ErrConflict
		}
	}
	p.mu.Unlock()
	return p.accept(ctx, AcceptRequest{RunID: plan.RunID, CandidateSetDigest: plan.CandidateSetDigest, SnapshotDigest: plan.SnapshotDigest, DiffDigest: plan.DiffDigest, ConfirmationDigest: plan.ConfirmationDigest, CharacterizationDigest: plan.CharacterizationDigest}, true)
}

func (p *Publisher) verifyManagedSources(sources []preparedSource) error {
	for _, source := range sources {
		data, _, exists, identity, err := p.readTarget(source.path)
		if err != nil || !exists || digest(data) != source.digest || !os.SameFile(identity, source.identity) {
			return ErrConflict
		}
	}
	return nil
}

func (p *Publisher) finalizeManaged(ctx context.Context, journal journalRecord) error {
	if p.ManagedRegistry == nil {
		return ErrConflict
	}
	pending, err := p.ManagedRegistry.ListPendingManagedAcceptances(ctx)
	if err != nil {
		return err
	}
	byID := make(map[string]managedtest.Acceptance, len(pending))
	for _, item := range pending {
		byID[item.Acceptance.AcceptanceID] = item.Acceptance
	}
	for _, acceptance := range journal.ManagedAcceptances {
		if current, exists := byID[acceptance.AcceptanceID]; exists {
			if current != acceptance {
				return ErrConflict
			}
			if err := p.ManagedRegistry.MarkManagedFileWritten(ctx, acceptance.AcceptanceID, acceptance.PublishedFileDigest); err != nil {
				return err
			}
			if err := p.ManagedRegistry.CommitAccepted(ctx, acceptance); err != nil {
				return err
			}
		}
	}
	// Get is intentionally unavailable while *any* acceptance is pending.
	// Replay all pending rows first, then verify already-committed rows.
	for _, acceptance := range journal.ManagedAcceptances {
		if _, exists := byID[acceptance.AcceptanceID]; exists {
			continue
		}
		record, err := p.ManagedRegistry.Get(ctx, acceptance.Record.CaseID)
		if err != nil || record != acceptance.Record {
			return ErrConflict
		}
	}
	for _, retirement := range journal.ManagedRetirements {
		if err := p.ManagedRegistry.RetireAccepted(ctx, retirement); err != nil {
			return err
		}
	}
	return nil
}

func (p *Publisher) resolveManagedRollback(ctx context.Context, journal journalRecord) error {
	if p.ManagedRegistry == nil {
		return ErrConflict
	}
	pending, err := p.ManagedRegistry.ListPendingManagedAcceptances(ctx)
	if err != nil {
		return err
	}
	byID := make(map[string]managedtest.Acceptance, len(pending))
	for _, item := range pending {
		byID[item.Acceptance.AcceptanceID] = item.Acceptance
	}
	for _, acceptance := range journal.ManagedAcceptances {
		current, exists := byID[acceptance.AcceptanceID]
		if !exists {
			continue
		}
		if current != acceptance {
			return ErrConflict
		}
		if err := p.ManagedRegistry.ResolvePendingManagedAcceptance(ctx, acceptance.AcceptanceID, acceptance.PreimageDigest); err != nil {
			return err
		}
	}
	return nil
}
