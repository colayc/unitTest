package testgendomain

import "testing"

func TestGenerationRecordRejectsUnknownVersion(t *testing.T) {
	r := validRequest()
	record := NewGenerationRecord(r)
	if !record.ValidFor(r, 0) {
		t.Fatal("current record invalid")
	}
	record.Version = 2
	if record.ValidFor(r, 0) {
		t.Fatal("accepted unknown version")
	}
}

func TestMinimizedSetCannotChangeAfterCheckpoint(t *testing.T) {
	r := validRequest()
	a := NewGenerationRecord(r)
	a.MinimizedCaseIDs = []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	b := a
	b.MinimizedCaseIDs = []string{"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	if b.MonotonicAfter(a) {
		t.Fatal("minimized set changed after durable checkpoint")
	}
}
