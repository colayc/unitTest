// Package testgenpublish is the sole workspace-write boundary for generated tests.
package testgenpublish

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unit-test-ide.local/test-service/internal/managedtest"
	"unit-test-ide.local/test-service/internal/testgenrender"
)

type SnapshotVerifier func(context.Context, string) error

// ManagedSelectionValidator validates the exact post-resolution test and CMake
// bytes. Runtime integrations must compile/run the selected set and return a
// bounded validation receipt; absence of this callback fails managed planning.
type ManagedSelectionValidator func(context.Context, ManagedSelection) ([]byte, error)
type ManagedSelection struct {
	RunID, SnapshotDigest, ToolchainID, SelectedOutputDigest string
	Files                                                    []testgenrender.StagedFile
}
type AcceptRequest struct{ RunID, CandidateSetDigest, SnapshotDigest, DiffDigest, ConfirmationDigest, CharacterizationDigest string }
type publisherHooks struct {
	fail               func(string) error
	beforeRename       func(string)
	beforeRollbackMove func(string)
	afterRestoreCreate func(string, string)
	afterReplayRemove  func(string)
	afterBackup        func(string)
	cleanupRemove      func(string) error
}
type Publisher struct {
	root, journal   *os.Root
	verify          SnapshotVerifier
	mu              sync.Mutex
	plans           map[string]preparedPlan
	hooks           publisherHooks
	ManagedRegistry interface {
		managedtest.Registry
		managedtest.AcceptanceJournal
		managedtest.RetirementRegistry
	}
	ManagedSelectionValidator ManagedSelectionValidator
}

type journalFile struct {
	Path, BeforeDigest, AfterDigest, StageName, BackupName, HoldName string
	Before                                                           []byte
	Mode                                                             uint32
	Existed                                                          bool
	Restored                                                         *restoredState `json:",omitempty"`
	publishedIdentity                                                os.FileInfo    `json:"-"`
}
type restoredState struct {
	Kind, Digest, Target string
	Mode                 uint32
}
type journalRecord struct {
	Version            int
	ConfirmationDigest string
	Receipt            Receipt
	Files              []journalFile
	CreatedDirs        []string
	ManagedAcceptances []managedtest.Acceptance `json:",omitempty"`
	ManagedRetirements []managedtest.Retirement `json:",omitempty"`
}

var errMissingParent = errors.New("missing generated-test directory")

func New(source, journal string, verify SnapshotVerifier) (*Publisher, error) {
	if verify == nil || !absoluteSafeDirectory(source) || !absoluteSafeDirectory(journal) || overlaps(source, journal) {
		return nil, ErrInvalidPlan
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		return nil, ErrInvalidPlan
	}
	storage, err := os.OpenRoot(journal)
	if err != nil {
		_ = root.Close()
		return nil, ErrInvalidPlan
	}
	return &Publisher{root: root, journal: storage, verify: verify, plans: map[string]preparedPlan{}}, nil
}
func (p *Publisher) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return errors.Join(p.root.Close(), p.journal.Close())
}
func overlaps(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	rel, err := filepath.Rel(a, b)
	if err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return true
	}
	rel, err = filepath.Rel(b, a)
	return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
func absoluteSafeDirectory(value string) bool {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return false
	}
	for current := value; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || linked(info) {
			return false
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return true
}

func (p *Publisher) fail(stage string) error {
	if p.hooks.fail != nil {
		return p.hooks.fail(stage)
	}
	return nil
}

func (p *Publisher) parent(relative string, create bool) (*os.Root, error) {
	if !validRelative(relative) {
		return nil, ErrInvalidPlan
	}
	dir := path.Dir(relative)
	if dir == "." {
		return nil, ErrInvalidPlan
	}
	current := ""
	for _, segment := range strings.Split(dir, "/") {
		if current == "" {
			current = segment
		} else {
			current += "/" + segment
		}
		info, err := p.root.Lstat(filepath.FromSlash(current))
		if errors.Is(err, os.ErrNotExist) && !create {
			return nil, errMissingParent
		}
		if errors.Is(err, os.ErrNotExist) && create {
			if err := p.root.Mkdir(filepath.FromSlash(current), 0700); err != nil {
				return nil, ErrConflict
			}
			info, err = p.root.Lstat(filepath.FromSlash(current))
		}
		if err != nil || !info.IsDir() || linked(info) {
			return nil, ErrConflict
		}
		if !exactEntry(p.root, path.Dir(current), segment) {
			return nil, ErrConflict
		}
	}
	parent, err := p.root.OpenRoot(filepath.FromSlash(dir))
	if err != nil {
		return nil, ErrConflict
	}
	info, err := p.root.Lstat(filepath.FromSlash(dir))
	opened, openErr := parent.Stat(".")
	if err != nil || openErr != nil || linked(info) || !os.SameFile(info, opened) {
		_ = parent.Close()
		return nil, ErrConflict
	}
	return parent, nil
}
func exactEntry(root *os.Root, dir, base string) bool {
	name := "."
	if dir != "." {
		name = filepath.FromSlash(dir)
	}
	f, err := root.Open(name)
	if err != nil {
		return false
	}
	entries, err := f.ReadDir(-1)
	_ = f.Close()
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), base) {
			return entry.Name() == base
		}
	}
	return false
}
func (p *Publisher) readTarget(relative string) ([]byte, os.FileMode, bool, os.FileInfo, error) {
	parent, err := p.parent(relative, false)
	if errors.Is(err, errMissingParent) {
		return nil, 0, false, nil, nil
	}
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return nil, 0, false, nil, ErrConflict
		}
		return nil, 0, false, nil, err
	}
	defer parent.Close()
	name := path.Base(relative)
	info, err := parent.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		if !exactEntryAlias(parent, name) {
			return nil, 0, false, nil, nil
		}
		return nil, 0, false, nil, ErrConflict
	}
	if err != nil || linked(info) || !info.Mode().IsRegular() || !exactEntry(parent, ".", name) {
		return nil, 0, false, nil, ErrConflict
	}
	f, err := parent.Open(name)
	if err != nil {
		return nil, 0, false, nil, ErrConflict
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Size() < 0 || opened.Size() > maxEditBytes {
		return nil, 0, false, nil, ErrConflict
	}
	data, err := io.ReadAll(io.LimitReader(f, maxEditBytes+1))
	if err != nil || len(data) > maxEditBytes {
		return nil, 0, false, nil, ErrConflict
	}
	last, err := parent.Lstat(name)
	if err != nil || !os.SameFile(last, opened) || linked(last) || last.Size() != opened.Size() {
		return nil, 0, false, nil, ErrConflict
	}
	return data, info.Mode().Perm(), true, opened, nil
}
func exactEntryAlias(parent *os.Root, name string) bool {
	f, err := parent.Open(".")
	if err != nil {
		return true
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return true
	}
	for _, e := range entries {
		if strings.EqualFold(e.Name(), name) {
			return true
		}
	}
	return false
}

func (p *Publisher) Accept(ctx context.Context, req AcceptRequest) (Receipt, error) {
	return p.accept(ctx, req, false)
}

func (p *Publisher) accept(ctx context.Context, req AcceptRequest, managed bool) (Receipt, error) {
	if p == nil || ctx == nil || !validHex(req.RunID, 32) || !validHex(req.CandidateSetDigest, 64) || !validHex(req.SnapshotDigest, 64) || !validHex(req.DiffDigest, 64) || !validHex(req.ConfirmationDigest, 64) || req.CharacterizationDigest != "" && !validHex(req.CharacterizationDigest, 64) {
		return Receipt{}, ErrConflict
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	if err := p.verify(ctx, req.SnapshotDigest); err != nil {
		return Receipt{}, ErrConflict
	}
	if receipt, exists, err := p.readReceipt(req.ConfirmationDigest); err != nil {
		return Receipt{}, err
	} else if exists {
		if managed != (receipt.ManagedReviewDigest != "") {
			return Receipt{}, ErrConflict
		}
		if !matchesReceiptRequest(receipt, req) || p.verifyReceiptCurrent(receipt) != nil {
			return Receipt{}, ErrConflict
		}
		if _, err := p.journal.Lstat(journalName(req.ConfirmationDigest)); err == nil {
			return receipt, ErrRecoveryRequired
		} else if !errors.Is(err, os.ErrNotExist) {
			return Receipt{}, ErrConflict
		}
		return receipt, nil
	}
	plan, ok := p.plans[req.ConfirmationDigest]
	if !ok || !matchesRequest(plan.public, req) || managed != (plan.managed != nil) {
		return Receipt{}, ErrConflict
	}
	if managed && p.verifyManagedSources(plan.managed.sources) != nil {
		return Receipt{}, ErrConflict
	}
	if managed && !validManagedSelectedReceipt(receiptFor(plan.public)) {
		return Receipt{}, ErrConflict
	}
	if err := p.verifyCurrent(plan.files, false); err != nil {
		return Receipt{}, err
	}
	journal := journalRecord{Version: 1, ConfirmationDigest: req.ConfirmationDigest, Receipt: receiptFor(plan.public)}
	if managed {
		journal.ManagedAcceptances = append([]managedtest.Acceptance(nil), plan.managed.acceptances...)
		journal.ManagedRetirements = append([]managedtest.Retirement(nil), plan.managed.retirements...)
	}
	created, err := p.missingDirectories(plan.files)
	if err != nil {
		return Receipt{}, err
	}
	journal.CreatedDirs = created
	for index, file := range plan.files {
		suffix := req.ConfirmationDigest[:16] + "-" + string(rune('a'+index))
		journal.Files = append(journal.Files, journalFile{Path: file.edit.Path, BeforeDigest: file.edit.BeforeDigest, AfterDigest: file.edit.AfterDigest, Before: append([]byte(nil), file.before...), Mode: uint32(file.mode), Existed: file.existed, StageName: ".testgen-" + suffix + ".stage", BackupName: ".testgen-" + suffix + ".backup", HoldName: ".testgen-" + suffix + ".hold"})
	}
	if err := p.fail("journal"); err != nil {
		return Receipt{}, err
	}
	if err := p.writeJournal(journal); err != nil {
		return Receipt{}, err
	}
	rollback := func(cause error) (Receipt, error) {
		recoverErr := p.rollback(journal)
		if recoverErr == nil && managed {
			recoverErr = p.resolveManagedRollback(context.Background(), journal)
		}
		if recoverErr == nil {
			_ = p.journal.Remove(journalName(req.ConfirmationDigest))
			return Receipt{}, cause
		}
		return Receipt{}, errors.Join(cause, recoverErr, ErrRecoveryRequired)
	}
	if managed {
		for _, acceptance := range journal.ManagedAcceptances {
			if err := p.ManagedRegistry.BeginManagedAcceptance(ctx, acceptance); err != nil {
				return rollback(err)
			}
		}
	}
	for i, file := range plan.files {
		stage := "stage-first"
		if i == len(plan.files)-1 {
			stage = "stage-last"
		}
		if err := p.fail(stage); err != nil {
			return rollback(err)
		}
		if err := ctx.Err(); err != nil {
			return rollback(err)
		}
		if err := p.stageFile(journal.Files[i], file.after); err != nil {
			return rollback(err)
		}
	}
	if err := p.verifyCurrent(plan.files, false); err != nil {
		return rollback(err)
	}
	if managed && p.verifyManagedSources(plan.managed.sources) != nil {
		return rollback(ErrConflict)
	}
	if err := p.verify(ctx, req.SnapshotDigest); err != nil {
		return rollback(ErrConflict)
	}
	for i := range journal.Files {
		stage := "commit-first"
		if i == len(journal.Files)-1 {
			stage = "commit-last"
		}
		if err := p.fail(stage); err != nil {
			return rollback(err)
		}
		if err := ctx.Err(); err != nil {
			return rollback(err)
		}
		if err := p.verify(ctx, req.SnapshotDigest); err != nil {
			return rollback(ErrConflict)
		}
		if managed && p.verifyManagedSources(plan.managed.sources) != nil {
			return rollback(ErrConflict)
		}
		if err := p.commitFile(&journal.Files[i], plan.files[i]); err != nil {
			return rollback(err)
		}
	}
	if err := p.fail("readback"); err != nil {
		return rollback(err)
	}
	if err := ctx.Err(); err != nil {
		return rollback(err)
	}
	if err := p.verifyCurrent(plan.files, true); err != nil {
		return rollback(err)
	}
	if err := p.verify(ctx, req.SnapshotDigest); err != nil {
		return rollback(ErrConflict)
	}
	if managed && p.verifyManagedSources(plan.managed.sources) != nil {
		return rollback(ErrConflict)
	}
	if err := p.fail("cleanup"); err != nil {
		return rollback(err)
	}
	if managed {
		for _, acceptance := range journal.ManagedAcceptances {
			if err := p.ManagedRegistry.MarkManagedFileWritten(ctx, acceptance.AcceptanceID, acceptance.PublishedFileDigest); err != nil {
				return rollback(err)
			}
		}
	}
	if err := p.writeReceipt(journal.Receipt); err != nil {
		return rollback(err)
	}
	if managed {
		if err := p.finalizeManaged(context.Background(), journal); err != nil {
			return journal.Receipt, errors.Join(ErrRecoveryRequired, err)
		}
	}
	// A durable receipt marks commit. Leftover backups/journal are safely cleaned on restart.
	if err := p.cleanupCommitted(journal); err != nil {
		return journal.Receipt, errors.Join(ErrRecoveryRequired, err)
	}
	return journal.Receipt, nil
}

// Receipt reports only a completed publication whose immutable receipt and
// current files match the requested preview. It never plans or writes files.
func (p *Publisher) Receipt(ctx context.Context, req AcceptRequest) (Receipt, bool, error) {
	if p == nil || ctx == nil || !validHex(req.RunID, 32) || !validHex(req.CandidateSetDigest, 64) || !validHex(req.SnapshotDigest, 64) || !validHex(req.DiffDigest, 64) || !validHex(req.ConfirmationDigest, 64) || req.CharacterizationDigest != "" && !validHex(req.CharacterizationDigest, 64) {
		return Receipt{}, false, ErrConflict
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Receipt{}, false, err
	}
	if err := p.verify(ctx, req.SnapshotDigest); err != nil {
		return Receipt{}, false, ErrConflict
	}
	receipt, exists, err := p.readReceipt(req.ConfirmationDigest)
	if err != nil || !exists {
		return receipt, exists, err
	}
	if !matchesReceiptRequest(receipt, req) || p.verifyReceiptCurrent(receipt) != nil {
		return Receipt{}, false, ErrConflict
	}
	if _, err := p.journal.Lstat(journalName(req.ConfirmationDigest)); err == nil {
		return Receipt{}, false, ErrRecoveryRequired
	} else if !errors.Is(err, os.ErrNotExist) {
		return Receipt{}, false, ErrConflict
	}
	return receipt, true, nil
}

// ManagedPublicationReady is a wiring gate, not a guarantee that a particular
// review is still current. The latter is rechecked at plan and commit time.
func (p *Publisher) ManagedPublicationReady() bool {
	return p != nil && p.root != nil && p.journal != nil && p.verify != nil &&
		p.ManagedRegistry != nil && p.ManagedSelectionValidator != nil
}

// ReadManagedPreimage uses the same rooted, no-symlink read boundary as the
// publisher. The caller compares it with the authenticated durable review.
func (p *Publisher) ReadManagedPreimage(ctx context.Context, relative string) ([]byte, error) {
	if !p.ManagedPublicationReady() || ctx == nil || !generatedTestPath(relative) {
		return nil, ErrInvalidPlan
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	data, _, exists, _, err := p.readTarget(relative)
	if err != nil {
		return nil, ErrConflict
	}
	if !exists {
		return nil, nil
	}
	return bytes.Clone(data), nil
}

// ManagedReceipt finds a previously committed review decision after a
// service restart. It never reconstructs a plan or writes to the workspace.
func (p *Publisher) ManagedReceipt(ctx context.Context, decision ManagedDecision) (Receipt, bool, error) {
	if !p.ManagedPublicationReady() || ctx == nil || !validHex(decision.ReviewID, 32) || !validHex(decision.ReviewDigest, 64) {
		return Receipt{}, false, ErrInvalidPlan
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	root, err := p.journal.Open(".")
	if err != nil {
		return Receipt{}, false, ErrConflict
	}
	defer root.Close()
	wantDecision := digestManagedDecision(decision)
	var found Receipt
	for {
		if err := ctx.Err(); err != nil {
			return Receipt{}, false, err
		}
		entries, readErr := root.ReadDir(128)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return Receipt{}, false, ErrConflict
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasPrefix(name, "receipt-") || !strings.HasSuffix(name, ".json") {
				continue
			}
			confirmation := strings.TrimSuffix(strings.TrimPrefix(name, "receipt-"), ".json")
			if !validHex(confirmation, 64) {
				return Receipt{}, false, ErrConflict
			}
			receipt, ok, err := p.readReceipt(confirmation)
			if err != nil || !ok {
				return Receipt{}, false, ErrConflict
			}
			if receipt.ManagedReviewID != decision.ReviewID || receipt.ManagedReviewDigest != decision.ReviewDigest || receipt.ManagedDecisionDigest != wantDecision {
				continue
			}
			if found.ConfirmationDigest != "" || !validManagedSelectedReceipt(receipt) || p.verifyReceiptCurrent(receipt) != nil {
				return Receipt{}, false, ErrConflict
			}
			if _, err := p.journal.Lstat(journalName(confirmation)); err == nil {
				return Receipt{}, false, ErrRecoveryRequired
			} else if !errors.Is(err, os.ErrNotExist) {
				return Receipt{}, false, ErrConflict
			}
			found = receipt
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	return found, found.ConfirmationDigest != "", nil
}
func matchesRequest(p PublishPlan, r AcceptRequest) bool {
	return p.RunID == r.RunID && p.CandidateSetDigest == r.CandidateSetDigest && p.SnapshotDigest == r.SnapshotDigest && p.DiffDigest == r.DiffDigest && p.ConfirmationDigest == r.ConfirmationDigest && p.CharacterizationDigest == r.CharacterizationDigest
}
func matchesReceiptRequest(p Receipt, r AcceptRequest) bool {
	return p.RunID == r.RunID && p.CandidateSetDigest == r.CandidateSetDigest && p.SnapshotDigest == r.SnapshotDigest && p.DiffDigest == r.DiffDigest && p.ConfirmationDigest == r.ConfirmationDigest && p.CharacterizationDigest == r.CharacterizationDigest
}
func receiptFor(p PublishPlan) Receipt {
	return Receipt{RunID: p.RunID, CandidateSetDigest: p.CandidateSetDigest, SnapshotDigest: p.SnapshotDigest, DiffDigest: p.DiffDigest, ConfirmationDigest: p.ConfirmationDigest, CharacterizationDigest: p.CharacterizationDigest, Edits: append([]PlannedEdit(nil), p.Edits...), ManagedReadOnly: append([]PlannedEdit(nil), p.ManagedReadOnly...), ManagedReviewID: p.ManagedReviewID, ManagedReviewDigest: p.ManagedReviewDigest, ManagedDecisionDigest: p.ManagedDecisionDigest, ManagedEvidenceDigest: p.ManagedEvidenceDigest, ManagedSelectedOutputDigest: p.ManagedSelectedOutputDigest, ManagedValidationReceiptDigest: p.ManagedValidationReceiptDigest, ManagedSelectedOutputs: append([]SelectedOutput(nil), p.ManagedSelectedOutputs...)}
}
func matchesReceipt(r Receipt, p PublishPlan) bool { return r.String() == receiptFor(p).String() }
func (p *Publisher) verifyReceiptCurrent(r Receipt) error {
	if len(r.Edits)+len(r.ManagedReadOnly) == 0 || len(r.Edits)+len(r.ManagedReadOnly) > maxFiles {
		return ErrConflict
	}
	seen := map[string]bool{}
	for _, edit := range r.Edits {
		if !generatedTestPath(edit.Path) && !cmakePath(edit.Path) || !validHex(edit.AfterDigest, 64) || seen[strings.ToLower(edit.Path)] {
			return ErrConflict
		}
		seen[strings.ToLower(edit.Path)] = true
		data, _, exists, _, err := p.readTarget(edit.Path)
		if err != nil || !exists || digest(data) != edit.AfterDigest {
			return ErrConflict
		}
	}
	for _, edit := range r.ManagedReadOnly {
		if !generatedTestPath(edit.Path) || !validHex(edit.BeforeDigest, 64) || edit.BeforeDigest != edit.AfterDigest || seen[strings.ToLower(edit.Path)] {
			return ErrConflict
		}
		seen[strings.ToLower(edit.Path)] = true
		data, _, exists, _, err := p.readTarget(edit.Path)
		if err != nil || !exists || digest(data) != edit.AfterDigest {
			return ErrConflict
		}
	}
	if r.ManagedReviewDigest != "" {
		if !validManagedSelectedReceipt(r) {
			return ErrConflict
		}
		for _, output := range r.ManagedSelectedOutputs {
			data, _, exists, _, err := p.readTarget(output.Path)
			if err != nil || !exists || digest(data) != output.Digest {
				return ErrConflict
			}
		}
	}
	return nil
}

func validManagedSelectedReceipt(r Receipt) bool {
	if !validHex(r.ManagedSelectedOutputDigest, 64) || !validHex(r.ManagedValidationReceiptDigest, 64) || len(r.ManagedSelectedOutputs) < 2 || len(r.ManagedSelectedOutputs) > maxFiles {
		return false
	}
	seen := map[string]bool{}
	cmake := 0
	for i, output := range r.ManagedSelectedOutputs {
		if !generatedTestPath(output.Path) && !cmakePath(output.Path) || !validHex(output.Digest, 64) || seen[strings.ToLower(output.Path)] || i > 0 && r.ManagedSelectedOutputs[i-1].Path >= output.Path {
			return false
		}
		seen[strings.ToLower(output.Path)] = true
		if cmakePath(output.Path) {
			cmake++
		}
	}
	if cmake != 1 || len(r.Edits)+len(r.ManagedReadOnly) < len(r.ManagedSelectedOutputs)-1 || len(r.Edits)+len(r.ManagedReadOnly) > len(r.ManagedSelectedOutputs) {
		return false
	}
	for _, edit := range append(append([]PlannedEdit(nil), r.Edits...), r.ManagedReadOnly...) {
		match := false
		for _, output := range r.ManagedSelectedOutputs {
			if output.Path == edit.Path && output.Digest == edit.AfterDigest {
				match = true
				break
			}
		}
		if !match {
			return false
		}
	}
	for _, output := range r.ManagedSelectedOutputs {
		if cmakePath(output.Path) {
			continue
		}
		matched := false
		for _, edit := range append(append([]PlannedEdit(nil), r.Edits...), r.ManagedReadOnly...) {
			if edit.Path == output.Path && edit.AfterDigest == output.Digest {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	encoded, _ := json.Marshal(r.ManagedSelectedOutputs)
	return digest(append([]byte("managed-selected-v1\x00"), encoded...)) == r.ManagedSelectedOutputDigest
}
func (p *Publisher) verifyCurrent(files []preparedFile, after bool) error {
	for _, file := range files {
		data, _, exists, identity, err := p.readTarget(file.edit.Path)
		if err != nil {
			return ErrConflict
		}
		if after {
			if !exists || digest(data) != file.edit.AfterDigest {
				return conflictReceipt(file.edit.Path, file.edit.AfterDigest, data, exists)
			}
		} else if exists != file.existed || exists && (!bytes.Equal(data, file.before) || digest(data) != file.edit.BeforeDigest || !os.SameFile(identity, file.identity)) {
			return conflictReceipt(file.edit.Path, file.edit.BeforeDigest, data, exists)
		}
	}
	return nil
}

func (p *Publisher) stageFile(item journalFile, data []byte) error {
	parent, err := p.parent(item.Path, true)
	if err != nil {
		return err
	}
	defer parent.Close()
	f, err := parent.OpenFile(item.StageName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrConflict
	}
	writeErr := error(nil)
	if _, err := f.Write(data); err != nil {
		writeErr = err
	}
	if writeErr == nil {
		writeErr = f.Chmod(os.FileMode(item.Mode))
	}
	if writeErr == nil {
		writeErr = f.Sync()
	}
	writeErr = errors.Join(writeErr, f.Close())
	if writeErr != nil {
		return ErrConflict
	}
	return nil
}
func (p *Publisher) missingDirectories(files []preparedFile) ([]string, error) {
	missing := map[string]bool{}
	for _, file := range files {
		current := ""
		for _, segment := range strings.Split(path.Dir(file.edit.Path), "/") {
			if current == "" {
				current = segment
			} else {
				current += "/" + segment
			}
			info, err := p.root.Lstat(filepath.FromSlash(current))
			if errors.Is(err, os.ErrNotExist) {
				missing[current] = true
				continue
			}
			if err != nil || linked(info) || !info.IsDir() {
				return nil, ErrConflict
			}
		}
	}
	result := make([]string, 0, len(missing))
	for item := range missing {
		result = append(result, item)
	}
	sort.Strings(result)
	return result, nil
}
func (p *Publisher) commitFile(item *journalFile, file preparedFile) error {
	before, _, exists, identity, err := p.readTarget(item.Path)
	if err != nil || exists != item.Existed || exists && (digest(before) != item.BeforeDigest || !os.SameFile(identity, file.identity)) {
		return ErrConflict
	}
	parent, err := p.parent(item.Path, false)
	if err != nil {
		return err
	}
	defer parent.Close()
	name := path.Base(item.Path)
	if _, err := parent.Lstat(item.BackupName); err == nil {
		return ErrConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrConflict
	}
	if p.hooks.beforeRename != nil {
		p.hooks.beforeRename(item.Path)
	}
	if item.Existed {
		if err := parent.Rename(name, item.BackupName); err != nil {
			return ErrConflict
		}
		backupInfo, err := parent.Lstat(item.BackupName)
		if err != nil || linked(backupInfo) || !os.SameFile(backupInfo, identity) {
			return ErrConflict
		}
		data, err := p.readRelative(parent, item.BackupName)
		if err != nil || digest(data) != item.BeforeDigest {
			return ErrConflict
		}
		if p.hooks.afterBackup != nil {
			p.hooks.afterBackup(item.Path)
		}
	}
	stageIdentity, err := parent.Lstat(item.StageName)
	if err != nil || linked(stageIdentity) || !stageIdentity.Mode().IsRegular() {
		return ErrConflict
	}
	stagedBytes, err := p.readRelative(parent, item.StageName)
	if err != nil || digest(stagedBytes) != item.AfterDigest {
		return ErrConflict
	}
	// Link is exclusive: a user-created destination during the rename window is
	// never replaced by the generated file.
	if err := parent.Link(item.StageName, name); err != nil {
		return ErrConflict
	}
	item.publishedIdentity = stageIdentity
	if err := parent.Remove(item.StageName); err != nil {
		return ErrConflict
	}
	return nil
}

func journalName(d string) string { return "txn-" + d + ".json" }
func receiptName(d string) string { return "receipt-" + d + ".json" }
func writeRootJSON(root *os.Root, name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return ErrInvalidPlan
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return ErrConflict
	}
	tmp := name + "." + hex.EncodeToString(random[:]) + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrConflict
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		_ = root.Remove(tmp)
		return ErrConflict
	}
	if err := root.Rename(tmp, name); err != nil {
		_ = root.Remove(tmp)
		return ErrConflict
	}
	return nil
}
func (p *Publisher) writeJournal(j journalRecord) error {
	if _, err := p.journal.Lstat(journalName(j.ConfirmationDigest)); err == nil {
		return ErrConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrConflict
	}
	return writeRootJSON(p.journal, journalName(j.ConfirmationDigest), j)
}
func (p *Publisher) writeReceipt(r Receipt) error {
	if _, err := p.journal.Lstat(receiptName(r.ConfirmationDigest)); err == nil {
		return ErrConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrConflict
	}
	return writeRootJSON(p.journal, receiptName(r.ConfirmationDigest), r)
}
func (p *Publisher) readReceipt(d string) (Receipt, bool, error) {
	f, err := p.journal.Open(receiptName(d))
	if errors.Is(err, os.ErrNotExist) {
		return Receipt{}, false, nil
	}
	if err != nil {
		return Receipt{}, false, ErrConflict
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return Receipt{}, false, ErrConflict
	}
	var r Receipt
	if json.Unmarshal(data, &r) != nil || r.ConfirmationDigest != d {
		return Receipt{}, false, ErrConflict
	}
	return r, true, nil
}

// restoreExclusive recreates the original pathname only when it is vacant.
// Link preserves an existing symlink object rather than following its target.
func (p *Publisher) restoreExclusive(parent *os.Root, from, to string, beforeRemove func() error) error {
	if err := parent.Link(from, to); err != nil {
		info, statErr := parent.Lstat(from)
		if statErr != nil || !linked(info) {
			return ErrConflict
		}
		target, linkErr := parent.Readlink(from)
		if linkErr != nil || parent.Symlink(target, to) != nil {
			return ErrConflict
		}
	}
	if p.hooks.afterRestoreCreate != nil {
		p.hooks.afterRestoreCreate(from, to)
	}
	if beforeRemove != nil {
		if err := beforeRemove(); err != nil {
			return ErrConflict
		}
	}
	if err := parent.Remove(from); err != nil {
		return ErrConflict
	}
	if p.hooks.afterReplayRemove != nil {
		p.hooks.afterReplayRemove(from)
	}
	return nil
}

// movePublished moves the current pathname into a journal-known hold before
// any deletion. A raced replacement is restored exclusively, never removed.
func (p *Publisher) movePublished(parent *os.Root, item journalFile, current os.FileInfo, beforeRestoreRemoval func() error) error {
	name := path.Base(item.Path)
	if _, err := parent.Lstat(item.HoldName); err == nil {
		return ErrConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrConflict
	}
	if p.hooks.beforeRollbackMove != nil {
		p.hooks.beforeRollbackMove(item.Path)
	}
	if err := parent.Rename(name, item.HoldName); err != nil {
		return ErrConflict
	}
	moved, err := parent.Lstat(item.HoldName)
	if err != nil {
		return ErrConflict
	}
	if !os.SameFile(moved, current) || linked(moved) || !moved.Mode().IsRegular() || !sameMode(moved.Mode().Perm(), os.FileMode(item.Mode)) {
		return errors.Join(ErrConflict, p.restoreExclusive(parent, item.HoldName, name, beforeRestoreRemoval))
	}
	data, err := p.readRelative(parent, item.HoldName)
	if err != nil || digest(data) != item.AfterDigest {
		return errors.Join(ErrConflict, p.restoreExclusive(parent, item.HoldName, name, beforeRestoreRemoval))
	}
	if err := parent.Remove(item.HoldName); err != nil {
		return ErrConflict
	}
	return nil
}

// sameRestoredEntry recognizes an interrupted exclusive restore. A regular
// file must still be the same filesystem object; a recreated symlink fallback
// is equivalent only when both link targets are byte-identical.
func (p *Publisher) sameRestoredEntry(parent *os.Root, left, right string, leftInfo, rightInfo os.FileInfo) bool {
	if leftInfo == nil || rightInfo == nil {
		return false
	}
	if linked(leftInfo) || linked(rightInfo) {
		if !linked(leftInfo) || !linked(rightInfo) {
			return false
		}
		a, aErr := parent.Readlink(left)
		b, bErr := parent.Readlink(right)
		return aErr == nil && bErr == nil && a == b
	}
	if !leftInfo.Mode().IsRegular() || !rightInfo.Mode().IsRegular() || !os.SameFile(leftInfo, rightInfo) {
		return false
	}
	a, aErr := p.readRelative(parent, left)
	b, bErr := p.readRelative(parent, right)
	return aErr == nil && bErr == nil && digest(a) == digest(b)
}

// markRestored makes removal of the final private alias replayable. The marker
// describes only a terminal public entry; replay never removes that entry.
func (p *Publisher) markRestored(j *journalRecord, index int, parent *os.Root, name, private string) error {
	current, currentErr := parent.Lstat(name)
	alias, aliasErr := parent.Lstat(private)
	if currentErr != nil || aliasErr != nil || !p.sameRestoredEntry(parent, name, private, current, alias) {
		return ErrConflict
	}
	state := &restoredState{}
	if linked(current) {
		target, err := parent.Readlink(name)
		if err != nil || len(target) > maxEditBytes {
			return ErrConflict
		}
		state.Kind, state.Target = "symlink", target
	} else if current.Mode().IsRegular() {
		data, err := p.readRelative(parent, name)
		if err != nil {
			return ErrConflict
		}
		state.Kind, state.Digest, state.Mode = "regular", digest(data), uint32(current.Mode().Perm())
	} else {
		return ErrConflict
	}
	j.Files[index].Restored = state
	if err := writeRootJSON(p.journal, journalName(j.ConfirmationDigest), *j); err != nil {
		return ErrConflict
	}
	return nil
}

func (p *Publisher) matchesRestored(parent *os.Root, name string, state *restoredState) bool {
	if state == nil {
		return false
	}
	info, err := parent.Lstat(name)
	if err != nil {
		return false
	}
	if state.Kind == "symlink" {
		if !linked(info) {
			return false
		}
		target, err := parent.Readlink(name)
		return err == nil && target == state.Target
	}
	if linked(info) || !info.Mode().IsRegular() || uint32(info.Mode().Perm()) != state.Mode {
		return false
	}
	data, err := p.readRelative(parent, name)
	return err == nil && digest(data) == state.Digest
}

func (p *Publisher) clearRestoredPair(parent *os.Root, item journalFile, private string, alsoBackup bool) error {
	if alsoBackup {
		info, err := parent.Lstat(item.BackupName)
		if err != nil || linked(info) || !info.Mode().IsRegular() {
			return ErrConflict
		}
		data, err := p.readRelative(parent, item.BackupName)
		if err != nil || digest(data) != item.BeforeDigest {
			return ErrConflict
		}
		if err := parent.Remove(item.BackupName); err != nil {
			return ErrConflict
		}
		if p.hooks.afterReplayRemove != nil {
			p.hooks.afterReplayRemove(item.BackupName)
		}
	}
	if err := parent.Remove(private); err != nil {
		return ErrConflict
	}
	if p.hooks.afterReplayRemove != nil {
		p.hooks.afterReplayRemove(private)
	}
	if err := parent.Remove(item.StageName); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrConflict
	}
	return nil
}

func (p *Publisher) rollback(j journalRecord) error {
	var result error
	for i := len(j.Files) - 1; i >= 0; i-- {
		item := j.Files[i]
		parent, err := p.parent(item.Path, false)
		if errors.Is(err, errMissingParent) && !item.Existed {
			continue
		}
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		name := path.Base(item.Path)
		current, currentErr := parent.Lstat(name)
		backup, backupErr := parent.Lstat(item.BackupName)
		currentPresent := currentErr == nil
		mark := func(private string) error { return p.markRestored(&j, i, parent, name, private) }
		if item.Restored != nil && errors.Is(backupErr, os.ErrNotExist) {
			if _, holdErr := parent.Lstat(item.HoldName); errors.Is(holdErr, os.ErrNotExist) {
				if !p.matchesRestored(parent, name, item.Restored) {
					result = errors.Join(result, ErrConflict)
					_ = parent.Close()
					continue
				}
				if err := parent.Remove(item.StageName); err != nil && !errors.Is(err, os.ErrNotExist) {
					result = errors.Join(result, ErrConflict)
				}
				result = errors.Join(result, parent.Close())
				continue
			}
		}
		if hold, holdErr := parent.Lstat(item.HoldName); holdErr == nil {
			if currentPresent {
				if p.sameRestoredEntry(parent, name, item.HoldName, current, hold) {
					if err := mark(item.HoldName); err != nil {
						result = errors.Join(result, err, parent.Close())
						continue
					}
					result = errors.Join(result, p.clearRestoredPair(parent, item, item.HoldName, backupErr == nil), parent.Close())
					continue
				}
				result = errors.Join(result, ErrConflict)
				_ = parent.Close()
				continue
			}
			if !linked(hold) && hold.Mode().IsRegular() && sameMode(hold.Mode().Perm(), os.FileMode(item.Mode)) {
				data, readErr := p.readRelative(parent, item.HoldName)
				if readErr == nil && digest(data) == item.AfterDigest && (item.publishedIdentity == nil || os.SameFile(hold, item.publishedIdentity)) {
					if err := parent.Remove(item.HoldName); err != nil {
						result = errors.Join(result, ErrConflict)
						_ = parent.Close()
						continue
					}
				} else {
					result = errors.Join(result, ErrConflict, p.restoreExclusive(parent, item.HoldName, name, func() error { return mark(item.HoldName) }))
					_ = parent.Close()
					continue
				}
			} else {
				result = errors.Join(result, ErrConflict, p.restoreExclusive(parent, item.HoldName, name, func() error { return mark(item.HoldName) }))
				_ = parent.Close()
				continue
			}
		} else if !errors.Is(holdErr, os.ErrNotExist) {
			result = errors.Join(result, ErrConflict)
			_ = parent.Close()
			continue
		}
		if currentPresent && backupErr == nil && p.sameRestoredEntry(parent, name, item.BackupName, current, backup) {
			if err := mark(item.BackupName); err != nil {
				result = errors.Join(result, err, parent.Close())
				continue
			}
			result = errors.Join(result, p.clearRestoredPair(parent, item, item.BackupName, false), parent.Close())
			continue
		}
		if currentErr == nil {
			if linked(current) || !current.Mode().IsRegular() {
				result = errors.Join(result, ErrConflict)
				_ = parent.Close()
				continue
			}
			data, readErr := p.readRelative(parent, name)
			if readErr != nil {
				result = errors.Join(result, readErr)
				_ = parent.Close()
				continue
			}
			hash := digest(data)
			if hash == item.AfterDigest && sameMode(current.Mode().Perm(), os.FileMode(item.Mode)) && (item.publishedIdentity == nil || os.SameFile(current, item.publishedIdentity)) {
				if err := p.movePublished(parent, item, current, func() error { return mark(item.HoldName) }); err != nil {
					result = errors.Join(result, err)
					_ = parent.Close()
					continue
				}
				currentPresent = false
			} else if hash != item.BeforeDigest || backupErr == nil || item.publishedIdentity != nil && os.SameFile(current, item.publishedIdentity) {
				// A later user replacement is never deleted to make room for rollback.
				result = errors.Join(result, ErrConflict)
				_ = parent.Close()
				continue
			}
		} else if !errors.Is(currentErr, os.ErrNotExist) {
			result = errors.Join(result, ErrConflict)
			_ = parent.Close()
			continue
		}
		if item.Existed {
			if backupErr == nil {
				if backup == nil {
					result = errors.Join(result, ErrConflict)
				} else if currentPresent {
					result = errors.Join(result, ErrConflict)
				} else if err := p.restoreExclusive(parent, item.BackupName, name, func() error { return mark(item.BackupName) }); err != nil {
					result = errors.Join(result, ErrConflict)
				}
			} else if errors.Is(backupErr, os.ErrNotExist) {
				if !currentPresent {
					f, err := parent.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(item.Mode))
					if err == nil {
						_, err = f.Write(item.Before)
						err = errors.Join(err, f.Chmod(os.FileMode(item.Mode)), f.Sync(), f.Close())
					}
					if err != nil {
						result = errors.Join(result, ErrConflict)
					}
				}
			} else {
				result = errors.Join(result, ErrConflict)
			}
		} else if currentPresent {
			result = errors.Join(result, ErrConflict)
		}
		if err := parent.Remove(item.StageName); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, ErrConflict)
		}
		result = errors.Join(result, parent.Close())
	}
	for i := len(j.CreatedDirs) - 1; i >= 0; i-- {
		err := p.root.Remove(filepath.FromSlash(j.CreatedDirs[i]))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, ErrConflict)
		}
	}
	return result
}
func (p *Publisher) readRelative(parent *os.Root, name string) ([]byte, error) {
	f, err := parent.Open(name)
	if err != nil {
		return nil, ErrConflict
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxEditBytes+1))
	if err != nil || len(data) > maxEditBytes {
		return nil, ErrConflict
	}
	return data, nil
}
func (p *Publisher) cleanupCommitted(j journalRecord) error {
	for _, item := range j.Files {
		parent, err := p.parent(item.Path, false)
		if err != nil {
			return err
		}
		for _, name := range []string{item.BackupName, item.StageName} {
			if p.hooks.cleanupRemove != nil {
				if err := p.hooks.cleanupRemove(name); err != nil {
					_ = parent.Close()
					return ErrConflict
				}
			}
			if err := parent.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
				_ = parent.Close()
				return ErrConflict
			}
		}
		_ = parent.Close()
	}
	if err := p.journal.Remove(journalName(j.ConfirmationDigest)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrConflict
	}
	return nil
}

func (p *Publisher) Recover(ctx context.Context) error {
	if p == nil || ctx == nil {
		return ErrInvalidPlan
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	f, err := p.journal.Open(".")
	if err != nil {
		return ErrConflict
	}
	entries, err := f.ReadDir(-1)
	_ = f.Close()
	if err != nil {
		return ErrConflict
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "txn-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		data, err := p.readJournal(name)
		if err != nil {
			return err
		}
		var j journalRecord
		if json.Unmarshal(data, &j) != nil || j.Version != 1 || name != journalName(j.ConfirmationDigest) || !validJournal(j) {
			return ErrConflict
		}
		if receipt, ok, err := p.readReceipt(j.ConfirmationDigest); err != nil {
			return err
		} else if ok {
			if receipt.String() != j.Receipt.String() {
				return ErrConflict
			}
			if len(j.ManagedAcceptances)+len(j.ManagedRetirements) > 0 {
				if p.ManagedRegistry == nil || p.verifyReceiptCurrent(receipt) != nil {
					return ErrConflict
				}
				if err := p.finalizeManaged(ctx, j); err != nil {
					return err
				}
			}
			if err := p.cleanupCommitted(j); err != nil {
				return err
			}
			continue
		}
		if err := p.rollback(j); err != nil {
			return err
		}
		if len(j.ManagedAcceptances)+len(j.ManagedRetirements) > 0 {
			if p.ManagedRegistry == nil {
				return ErrConflict
			}
			if err := p.resolveManagedRollback(ctx, j); err != nil {
				return err
			}
		}
		if err := p.journal.Remove(name); err != nil {
			return ErrConflict
		}
	}
	return nil
}
func (p *Publisher) readJournal(name string) ([]byte, error) {
	f, err := p.journal.Open(name)
	if err != nil {
		return nil, ErrConflict
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 32<<20))
	if err != nil || len(data) >= 32<<20 {
		return nil, ErrConflict
	}
	return data, nil
}
func validJournal(j journalRecord) bool {
	if !validHex(j.ConfirmationDigest, 64) || j.Receipt.ConfirmationDigest != j.ConfirmationDigest || len(j.Files)+len(j.Receipt.ManagedReadOnly) == 0 || len(j.Files)+len(j.Receipt.ManagedReadOnly) > maxFiles {
		return false
	}
	managedFields := j.Receipt.ManagedReviewID != "" || j.Receipt.ManagedReviewDigest != "" || j.Receipt.ManagedDecisionDigest != "" || j.Receipt.ManagedEvidenceDigest != "" || j.Receipt.ManagedSelectedOutputDigest != "" || j.Receipt.ManagedValidationReceiptDigest != "" || len(j.Receipt.ManagedSelectedOutputs) > 0 || len(j.Receipt.ManagedReadOnly) > 0
	if managedFields != (len(j.ManagedAcceptances)+len(j.ManagedRetirements) > 0) || len(j.Receipt.Edits) != len(j.Files) {
		return false
	}
	seen := map[string]bool{}
	for index, f := range j.Files {
		if j.Receipt.Edits[index] != (PlannedEdit{Path: f.Path, BeforeDigest: f.BeforeDigest, AfterDigest: f.AfterDigest}) {
			return false
		}
		if !generatedTestPath(f.Path) && !cmakePath(f.Path) || seen[strings.ToLower(f.Path)] || !validHex(f.AfterDigest, 64) || f.Existed && (!validHex(f.BeforeDigest, 64) || digest(f.Before) != f.BeforeDigest) || !f.Existed && (f.BeforeDigest != "" || len(f.Before) > 0) || len(f.Before) > maxEditBytes || !strings.HasPrefix(f.StageName, ".testgen-") || !strings.HasSuffix(f.StageName, ".stage") || !strings.HasPrefix(f.BackupName, ".testgen-") || !strings.HasSuffix(f.BackupName, ".backup") || !strings.HasPrefix(f.HoldName, ".testgen-") || !strings.HasSuffix(f.HoldName, ".hold") {
			return false
		}
		if f.Restored != nil && (f.Restored.Kind != "regular" && f.Restored.Kind != "symlink" || f.Restored.Kind == "regular" && (!validHex(f.Restored.Digest, 64) || f.Restored.Target != "" || f.Restored.Mode > 0777) || f.Restored.Kind == "symlink" && (f.Restored.Digest != "" || f.Restored.Mode != 0 || len(f.Restored.Target) > maxEditBytes)) {
			return false
		}
		seen[strings.ToLower(f.Path)] = true
	}
	for _, f := range j.Receipt.ManagedReadOnly {
		if !generatedTestPath(f.Path) || !validHex(f.BeforeDigest, 64) || f.BeforeDigest != f.AfterDigest || seen[strings.ToLower(f.Path)] {
			return false
		}
		seen[strings.ToLower(f.Path)] = true
	}
	for _, dir := range j.CreatedDirs {
		if !validRelative(dir) || !strings.HasPrefix(dir, "tests/") {
			return false
		}
	}
	if len(j.ManagedAcceptances) > 1000 || len(j.ManagedRetirements) > 1000 {
		return false
	}
	if len(j.ManagedAcceptances)+len(j.ManagedRetirements) > 0 {
		if !validHex(j.Receipt.ManagedReviewID, 32) || !validHex(j.Receipt.ManagedReviewDigest, 64) || !validHex(j.Receipt.ManagedDecisionDigest, 64) || !validHex(j.Receipt.ManagedEvidenceDigest, 64) || !validManagedSelectedReceipt(j.Receipt) {
			return false
		}
		seenCases := map[string]bool{}
		for _, a := range j.ManagedAcceptances {
			if !managedtest.ValidAcceptance(a) || a.ReviewDigest != j.Receipt.ManagedReviewDigest || a.Record.ValidationReceiptDigest != j.Receipt.ManagedValidationReceiptDigest || seenCases[a.Record.CaseID] {
				return false
			}
			seenCases[a.Record.CaseID] = true
			matched := false
			for _, f := range j.Files {
				if f.Path == a.Record.TestRelativePath && f.AfterDigest == a.PublishedFileDigest && (f.BeforeDigest == a.PreimageDigest || !f.Existed && a.PreimageDigest == digest(nil)) {
					matched = true
				}
			}
			for _, f := range j.Receipt.ManagedReadOnly {
				if f.Path == a.Record.TestRelativePath && f.BeforeDigest == a.PreimageDigest && f.AfterDigest == a.PublishedFileDigest {
					matched = true
				}
			}
			if !matched {
				return false
			}
		}
		for _, r := range j.ManagedRetirements {
			if !managedtest.ValidRetirement(r) || r.ReviewDigest != j.Receipt.ManagedReviewDigest || r.ConfirmationDigest != j.ConfirmationDigest || seenCases[r.CaseID] {
				return false
			}
			seenCases[r.CaseID] = true
			matched := false
			for _, f := range j.Files {
				if f.Path == r.TestRelativePath && f.AfterDigest == r.PublishedFileDigest {
					matched = true
				}
			}
			for _, f := range j.Receipt.ManagedReadOnly {
				if f.Path == r.TestRelativePath && f.AfterDigest == r.PublishedFileDigest {
					matched = true
				}
			}
			if !matched {
				return false
			}
		}
	}
	return true
}
