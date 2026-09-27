package testgendomain

import (
	"strings"
	"testing"
)

func validRequest() Request {
	return Request{
		IdempotencyKey: strings.Repeat("1", 32), WorkspaceGeneration: strings.Repeat("a", 64),
		ProjectID: "project", Scope: ScopeSymbol, SymbolID: "fn:classify", Framework: FrameworkAuto,
		Goals:                 Goals{FunctionPercent: 90, LinePercent: 80, BranchPercent: 70},
		Budgets:               Budgets{WallTimeMS: 1000, CandidateCount: 4, MemoryMiB: 256, Concurrency: 1},
		CompileSnapshotDigest: strings.Repeat("b", 64), CoverageSnapshotDigest: strings.Repeat("c", 64),
		SourceDigest: strings.Repeat("1", 64), CMakeTargetDigest: strings.Repeat("2", 64), FrameworkBundleDigest: strings.Repeat("3", 64), AnalyzerBundleDigest: strings.Repeat("4", 64), BaselineReportDigest: strings.Repeat("5", 64), ProcessOwnerDigest: strings.Repeat("d", 64),
	}
}

func TestValidateRequestRejectsInvalidClosedValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Request)
	}{
		{"scope", func(r *Request) { r.Scope = "all" }},
		{"framework", func(r *Request) { r.Framework = "gtest" }},
		{"missing symbol", func(r *Request) { r.SymbolID = "" }},
		{"extra target", func(r *Request) { r.TargetID = strings.Repeat("d", 64) }},
		{"absolute file", func(r *Request) { r.Scope = ScopeFile; r.SymbolID = ""; r.File = "/tmp/a.c" }},
		{"zero wall time", func(r *Request) { r.Budgets.WallTimeMS = 0 }},
		{"overflow candidates", func(r *Request) { r.Budgets.CandidateCount = 1001 }},
		{"zero concurrency", func(r *Request) { r.Budgets.Concurrency = 0 }},
		{"invalid memory", func(r *Request) { r.Budgets.MemoryMiB = 63 }},
		{"invalid goal", func(r *Request) { r.Goals.BranchPercent = 101 }},
		{"invalid workspace identity", func(r *Request) { r.WorkspaceGeneration = "untrusted" }},
		{"invalid compile snapshot", func(r *Request) { r.CompileSnapshotDigest = "stale" }},
		{"invalid coverage snapshot", func(r *Request) { r.CoverageSnapshotDigest = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := validRequest()
			tc.change(&r)
			if ValidateRequest(r) == nil {
				t.Fatal("accepted invalid request")
			}
		})
	}
	if err := ValidateRequest(validRequest()); err != nil {
		t.Fatalf("valid request: %v", err)
	}
}

func TestRequestSnapshotIdentityMustMatch(t *testing.T) {
	r := validRequest()
	if err := r.SnapshotMatches(r.SnapshotIdentity()); err != nil {
		t.Fatal(err)
	}
	changed := r.SnapshotIdentity()
	changed.CompileSnapshotDigest = strings.Repeat("f", 64)
	if err := r.SnapshotMatches(changed); err == nil {
		t.Fatal("accepted stale compile snapshot")
	}
}

func TestRequestRejectsMalformedPersistedSessionOwner(t *testing.T) {
	r := validRequest()
	r.SessionOwnerDigest = "foreign-session"
	if ValidateRequest(r) == nil {
		t.Fatal("accepted malformed session owner digest")
	}
	r.SessionOwnerDigest = strings.Repeat("e", 64)
	if err := ValidateRequest(r); err != nil {
		t.Fatalf("valid session owner rejected: %v", err)
	}
}
