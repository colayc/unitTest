package managedtest

import "testing"

func TestReviewDigestBindsPreimageOperationsAndDisplayedArtifact(t *testing.T) {
	base:=Review{Path:"tests/generated/src/a_test.cpp",PreimageDigest:"old",GeneratedDigest:"new",Operations:[]Operation{{CaseID:"utc_a",Kind:OperationUpdate,CurrentDigest:"before",GeneratedDigest:"after"}},Preview:ReviewPreview{Unified:"-before\n+after\n"}}
	want:=base.Digest()
	if want=="" || base.Digest()!=want { t.Fatal("review digest unstable") }
	changed:=base
	changed.PreimageDigest="other"
	if changed.Digest()==want { t.Fatal("preimage omitted from digest") }
	changed=base
	changed.Operations=append([]Operation(nil),base.Operations...)
	changed.Operations[0].GeneratedDigest="other"
	if changed.Digest()==want { t.Fatal("operation omitted from digest") }
	changed=base
	changed.Preview.Unified="-before\n+different\n"
	if changed.Digest()==want { t.Fatal("displayed artifact omitted from digest") }
}
