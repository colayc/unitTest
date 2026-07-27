package task

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEquivalentIdempotencyRequestUsesSemanticRequestIdentity(t *testing.T) {
	first := Task{
		Kind:                KindCMakeBuild,
		Request:             json.RawMessage(`{"sourceRoot":"src","options":{"jobs":123456789012345678901234567890,"ratio":1.0}}`),
		WorkspaceGeneration: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PlanFingerprint:     "old-plan",
		Timeout:             30 * time.Second,
		RequestHash:         "old-hash",
	}
	reordered := first
	reordered.Request = json.RawMessage(`{"options":{"ratio":1e0,"jobs":123456789012345678901234567890},"sourceRoot":"src"}`)
	reordered.PlanFingerprint = "new-plan"
	reordered.RequestHash = "new-hash"

	if !EquivalentIdempotencyRequest(first, reordered) {
		t.Fatal("semantic JSON, RequestHash, and PlanFingerprint-only changes were not equivalent")
	}

	tests := []struct {
		name   string
		change func(*Task)
	}{
		{name: "kind", change: func(value *Task) { value.Kind = KindSimulation }},
		{name: "request", change: func(value *Task) { value.Request = json.RawMessage(`{"sourceRoot":"other"}`) }},
		{name: "workspace generation", change: func(value *Task) {
			value.WorkspaceGeneration = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		}},
		{name: "timeout", change: func(value *Task) { value.Timeout = 31 * time.Second }},
		{name: "invalid first JSON", change: func(value *Task) { value.Request = json.RawMessage(`{"sourceRoot":`) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := first
			test.change(&changed)
			if EquivalentIdempotencyRequest(first, changed) {
				t.Fatalf("%s change was incorrectly equivalent", test.name)
			}
		})
	}

	invalid := first
	invalid.Request = json.RawMessage(`{"sourceRoot":`)
	if EquivalentIdempotencyRequest(invalid, invalid) {
		t.Fatal("two invalid JSON requests were incorrectly equivalent")
	}
}

func TestHashStartRequestUsesSemanticIdentityAndExcludesPlanFingerprint(t *testing.T) {
	first := StartRequest{
		Kind:                KindCMakeBuild,
		Request:             json.RawMessage(`{"sourceRoot":"src","options":{"jobs":123456789012345678901234567890,"ratio":1.0}}`),
		WorkspaceGeneration: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Timeout:             30 * time.Second,
		Plan:                ExecutionPlan{Fingerprint: "old-plan"},
	}
	reordered := first
	reordered.Request = json.RawMessage(`{"options":{"ratio":1e0,"jobs":123456789012345678901234567890},"sourceRoot":"src"}`)
	reordered.Plan.Fingerprint = "new-plan"

	if firstHash, secondHash := hashStartRequest(first), hashStartRequest(reordered); firstHash != secondHash {
		t.Fatalf("semantic identity hashes differ: %q != %q", firstHash, secondHash)
	}
}
