package managedtest

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"time"
)

func reviewFixture(t *testing.T) (Record, []byte, ManagedFile) {
	t.Helper()
	fid := strings.Repeat("a", 32)
	id, err := StableCaseID("project", "src/a.cpp", fid, "zero")
	if err != nil {
		t.Fatal(err)
	}
	old, err := RenderMarkers(id, fid, "choose", []byte("TEST(Group, Zero) { CHECK_TRUE(1); }\n"), "\n")
	if err != nil {
		t.Fatal(err)
	}
	newBlock, err := RenderMarkers(id, fid, "choose", []byte("TEST(Group, Zero) { CHECK_TRUE(2); }\n"), "\n")
	if err != nil {
		t.Fatal(err)
	}
	oldDoc, err := ParseDocument(old, 4096, 4)
	if err != nil {
		t.Fatal(err)
	}
	rec := Record{CaseID: id, ProjectID: "project", SourceFileID: strings.Repeat("b", 32), FunctionID: fid, SourceRelativePath: "src/a.cpp", ScenarioID: "zero", TestRelativePath: "tests/generated/src/a_test.cpp", AcceptedBlockDigest: oldDoc.Blocks[0].Digest, GeneratorVersion: "1", Framework: "cpputest", ToolchainID: "toolchain", SourceDigest: strings.Repeat("c", 64), ValidationReceiptDigest: strings.Repeat("d", 64), Status: StatusCurrent, LastVerifiedAt: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)}
	return rec, old, ManagedFile{Path: rec.TestRelativePath, Content: newBlock}
}

func document(t *testing.T, b []byte) Document {
	t.Helper()
	d, err := ParseDocument(b, 4096, 8)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestReconcileNormalUpdateAndUnmanagedPreservation(t *testing.T) {
	rec, old, next := reviewFixture(t)
	current := append(append([]byte("// handwritten header\n"), old...), []byte("// handwritten tail\n")...)
	copyBefore := bytes.Clone(current)
	r, err := Reconcile(ReconcileInput{Accepted: []Record{rec}, Current: document(t, current), Generated: next})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(current, copyBefore) || len(r.Operations) != 1 || r.Operations[0].Kind != OperationUpdate || r.Operations[0].Conflict {
		t.Fatalf("review=%+v", r)
	}
	if r.Digest() == "" || len(r.Preview.Unified) == 0 {
		t.Fatal("missing bound review/preview")
	}
	if !strings.Contains(r.Preview.Unified, "@@ -2,3 +1,3 @@ case=") {
		t.Fatalf("not an exact unified hunk: %s", r.Preview.Unified)
	}
	if r.Operations[0].CurrentDigest != rec.AcceptedBlockDigest {
		t.Fatal("preimage not bound")
	}
}

func TestReconcileNewGeneratedBlockIsAddOnly(t *testing.T) {
	_, _, next := reviewFixture(t)
	r, err := Reconcile(ReconcileInput{Current: document(t, []byte("// handwritten\n")), Generated: next})
	if err != nil || len(r.Operations) != 1 || r.Operations[0].Kind != OperationAdd || r.Operations[0].Conflict {
		t.Fatalf("new case review=%+v err=%v", r, err)
	}
}

func TestReconcileSurfacesUnmanagedIncludeChangeWithUnchangedBlock(t *testing.T) {
	rec, old, _ := reviewFixture(t)
	generated := ManagedFile{Path: rec.TestRelativePath, Content: append([]byte("#include \"new-header.h\"\n"), old...)}
	r, err := Reconcile(ReconcileInput{Accepted: []Record{rec}, Current: document(t, old), Generated: generated})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Operations) != 1 || r.Operations[0].Kind != OperationUnchanged || r.Scaffold == nil || !r.Scaffold.Conflict || r.Scaffold.GeneratedDigest == r.Scaffold.CurrentDigest || !strings.Contains(r.Preview.Unified, "new-header.h") {
		t.Fatalf("unmanaged change hidden: %+v", r)
	}
}

func TestReconcileTreatsHandwrittenBytesAsScaffoldConflict(t *testing.T) {
	rec, old, next := reviewFixture(t)
	current := append([]byte("// handwritten helper\n"), old...)
	r, err := Reconcile(ReconcileInput{Accepted: []Record{rec}, Current: document(t, current), Generated: next})
	if err != nil || r.Scaffold == nil || !r.Scaffold.Conflict || !strings.Contains(r.Scaffold.Preview.Current, "handwritten helper") {
		t.Fatalf("handwritten scaffold was not preserved for review: %+v %v", r, err)
	}
}

func TestReconcileRawBlockChangeCannotBeCalledUnchanged(t *testing.T) {
	rec, old, _ := reviewFixture(t)
	crlf := bytes.ReplaceAll(old, []byte("\n"), []byte("\r\n"))
	r, err := Reconcile(ReconcileInput{Accepted: []Record{rec}, Current: document(t, crlf), Generated: ManagedFile{Path: rec.TestRelativePath, Content: old}})
	if err != nil || len(r.Operations) != 1 || r.Operations[0].Kind == OperationUnchanged {
		t.Fatalf("raw byte drift hidden: %+v %v", r, err)
	}
}

func TestReconcileExistingFileCanAddOnlyManagedBlock(t *testing.T) {
	rec, old, _ := reviewFixture(t)
	other := rec
	other.ScenarioID = "more"
	var err error
	other.CaseID, err = StableCaseID(other.ProjectID, other.SourceRelativePath, other.FunctionID, other.ScenarioID)
	if err != nil {
		t.Fatal(err)
	}
	added, err := RenderMarkers(other.CaseID, other.FunctionID, "choose", []byte("TEST(Group, More) {}\n"), "\n")
	if err != nil {
		t.Fatal(err)
	}
	generated := ManagedFile{Path: rec.TestRelativePath, Content: append(bytes.Clone(old), added...)}
	r, err := Reconcile(ReconcileInput{Accepted: []Record{rec}, Current: document(t, old), Generated: generated})
	if err != nil || r.Scaffold != nil || len(r.Operations) != 2 {
		t.Fatalf("new block incorrectly changed scaffold: %+v %v", r, err)
	}
}

func TestReconcileReorderedUnchangedBlocksRequiresLayoutReview(t *testing.T) {
	rec, old, _ := reviewFixture(t)
	other := rec
	other.ScenarioID = "more"
	var err error
	other.CaseID, err = StableCaseID(other.ProjectID, other.SourceRelativePath, other.FunctionID, other.ScenarioID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RenderMarkers(other.CaseID, other.FunctionID, "choose", []byte("TEST(Group, More) {}\n"), "\n")
	if err != nil {
		t.Fatal(err)
	}
	other.AcceptedBlockDigest = document(t, second).Blocks[0].Digest
	r, err := Reconcile(ReconcileInput{Accepted: []Record{rec, other}, Current: document(t, append(bytes.Clone(old), second...)), Generated: ManagedFile{Path: rec.TestRelativePath, Content: append(bytes.Clone(second), old...)}})
	if err != nil || r.Scaffold == nil || !r.Scaffold.Conflict {
		t.Fatalf("block reordering was hidden: %+v %v", r, err)
	}
	for _, op := range r.Operations {
		if op.Kind != OperationUnchanged {
			t.Fatalf("block content changed unexpectedly: %+v", op)
		}
	}
}

func TestReconcileEditedManagedBlockRequiresAuthenticatedAncestor(t *testing.T) {
	rec, old, next := reviewFixture(t)
	current := bytes.Replace(old, []byte("CHECK_TRUE(1)"), []byte("CHECK_TRUE(3)"), 1)
	if _, err := Reconcile(ReconcileInput{Accepted: []Record{rec}, Current: document(t, current), Generated: next}); err == nil {
		t.Fatal("ancestor-free three-way accepted")
	}
	r, err := Reconcile(ReconcileInput{Accepted: []Record{rec}, AcceptedBlocks: map[string][]byte{rec.CaseID: old}, Current: document(t, current), Generated: next})
	if err != nil || len(r.Operations) != 1 || !r.Operations[0].Conflict || r.Operations[0].Kind != OperationConflict {
		t.Fatalf("review=%+v err=%v", r, err)
	}
	if r.Operations[0].Preview.Accepted == "" || r.Operations[0].Preview.Current == "" || r.Operations[0].Preview.Generated == "" {
		t.Fatal("incomplete three-sided preview")
	}
	bad := bytes.Replace(old, []byte("CHECK_TRUE(1)"), []byte("CHECK_TRUE(4)"), 1)
	if _, err := Reconcile(ReconcileInput{Accepted: []Record{rec}, AcceptedBlocks: map[string][]byte{rec.CaseID: bad}, Current: document(t, current), Generated: next}); err == nil {
		t.Fatal("wrong ancestor accepted")
	}
}

func TestReconcileRemovalOrphanAndCorruptionFailClosed(t *testing.T) {
	rec, old, next := reviewFixture(t)
	next.Content = []byte("// no generated cases\n")
	r, err := Reconcile(ReconcileInput{Accepted: []Record{rec}, Current: document(t, old), Generated: next})
	if err != nil || len(r.Operations) != 1 || r.Operations[0].Kind != OperationOrphan || !r.Operations[0].Conflict {
		t.Fatalf("removal=%+v err=%v", r, err)
	}
	broken := bytes.Replace(old, []byte("managed-end"), []byte("managed-fin"), 1)
	if _, err := Reconcile(ReconcileInput{Accepted: []Record{rec}, Current: Document{Bytes: broken}, Generated: next}); err == nil {
		t.Fatal("corrupt marker accepted")
	}
}

func TestReconcileEditedManagedBlockIsConflictEvenWhenGeneratorRemovesIt(t *testing.T) {
	rec, old, next := reviewFixture(t)
	current := bytes.Replace(old, []byte("CHECK_TRUE(1)"), []byte("CHECK_TRUE(3)"), 1)
	next.Content = []byte("// generated removal\n")
	if _, err := Reconcile(ReconcileInput{Accepted: []Record{rec}, Current: document(t, current), Generated: next}); err == nil {
		t.Fatal("edited removal bypassed ancestor requirement")
	}
	r, err := Reconcile(ReconcileInput{Accepted: []Record{rec}, AcceptedBlocks: map[string][]byte{rec.CaseID: old}, Current: document(t, current), Generated: next})
	if err != nil || len(r.Operations) != 1 || r.Operations[0].Kind != OperationConflict || !r.Operations[0].Conflict {
		t.Fatalf("edited removal review=%+v err=%v", r, err)
	}
}

func TestReconcileDigestBindsEveryOperationAndPreviewBounded(t *testing.T) {
	rec, old, next := reviewFixture(t)
	r, err := Reconcile(ReconcileInput{Accepted: []Record{rec}, Current: document(t, old), Generated: next})
	if err != nil {
		t.Fatal(err)
	}
	tampered := r
	tampered.Operations = append([]Operation(nil), r.Operations...)
	tampered.Operations[0].GeneratedDigest = strings.Repeat("0", 64)
	if tampered.Digest() == r.Digest() {
		t.Fatal("operation digest not bound")
	}
	huge := bytes.Repeat([]byte("// user\n"), 100000)
	largeDoc, err := ParseDocument(append(huge, old...), int64(len(huge)+len(old)), 8)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Reconcile(ReconcileInput{Accepted: []Record{rec}, Current: largeDoc, Generated: next})
	if err == nil {
		t.Fatal("oversized review accepted")
	}
}

func TestReconcileTwoIndependentConflictsAreOrdered(t *testing.T) {
	recA, oldA, nextA := reviewFixture(t)
	recB := recA
	recB.ScenarioID = "negative"
	var err error
	recB.CaseID, err = StableCaseID(recB.ProjectID, recB.SourceRelativePath, recB.FunctionID, recB.ScenarioID)
	if err != nil {
		t.Fatal(err)
	}
	oldB, err := RenderMarkers(recB.CaseID, recB.FunctionID, "choose", []byte("TEST(Group, Negative) { CHECK_TRUE(1); }\n"), "\n")
	if err != nil {
		t.Fatal(err)
	}
	newB, err := RenderMarkers(recB.CaseID, recB.FunctionID, "choose", []byte("TEST(Group, Negative) { CHECK_TRUE(2); }\n"), "\n")
	if err != nil {
		t.Fatal(err)
	}
	recB.AcceptedBlockDigest = document(t, oldB).Blocks[0].Digest
	current := append(bytes.Replace(oldA, []byte("CHECK_TRUE(1)"), []byte("CHECK_TRUE(3)"), 1), bytes.Replace(oldB, []byte("CHECK_TRUE(1)"), []byte("CHECK_TRUE(3)"), 1)...)
	generated := ManagedFile{Path: nextA.Path, Content: append(bytes.Clone(nextA.Content), newB...)}
	r, err := Reconcile(ReconcileInput{Accepted: []Record{recB, recA}, AcceptedBlocks: map[string][]byte{recA.CaseID: oldA, recB.CaseID: oldB}, Current: document(t, current), Generated: generated})
	if err != nil || len(r.Operations) != 2 || !r.Operations[0].Conflict || !r.Operations[1].Conflict || r.Operations[0].CaseID > r.Operations[1].CaseID {
		t.Fatalf("review=%+v err=%v", r, err)
	}
}

func TestReconcileBoundsTotalCasePreviewEvenWhenUnchanged(t *testing.T) {
	record, _, candidate := reviewFixture(t)
	var all []Record
	var documentBytes []byte
	for i := 0; i < 24; i++ {
		item := record
		item.ScenarioID = "scenario-" + strconv.Itoa(i)
		var err error
		item.CaseID, err = StableCaseID(item.ProjectID, item.SourceRelativePath, item.FunctionID, item.ScenarioID)
		if err != nil {
			t.Fatal(err)
		}
		body := []byte("// " + strings.Repeat("x", 7000) + "\nTEST(Group, Scenario) {}\n")
		block, err := RenderMarkers(item.CaseID, item.FunctionID, "choose", body, "\n")
		if err != nil {
			t.Fatal(err)
		}
		blockDoc, err := ParseDocument(block, int64(len(block)), 1)
		if err != nil {
			t.Fatal(err)
		}
		item.AcceptedBlockDigest = blockDoc.Blocks[0].Digest
		all = append(all, item)
		documentBytes = append(documentBytes, block...)
	}
	doc, err := ParseDocument(documentBytes, int64(len(documentBytes)), 24)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Content = documentBytes
	review, err := Reconcile(ReconcileInput{Accepted: all, Current: doc, Generated: candidate})
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range review.Operations {
		if op.Kind != OperationUnchanged || op.Preview.Current != "" || op.Preview.Generated != "" {
			t.Fatal("unchanged case needlessly carries a large preview")
		}
	}
}
