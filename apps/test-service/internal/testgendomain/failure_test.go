package testgendomain

import (
	"strings"
	"testing"
)

func TestFailureDiagnosticCodecClosedValues(t *testing.T) {
	stages := strings.Fields("baseline analyzing solving rendering validating minimizing awaiting_confirmation unknown")
	operations := strings.Fields("ledger reserve_budget stage_run stage_context transition preview_set authoritative_set publish_plan preview_diff commit_budget finalize_review resume resolve_target authorize_process pipeline_generate validate_candidates minimize_candidates unknown")
	reasons := strings.Fields("timed_out cancelled budget_exceeded budget_invalid stale_snapshot invalid_argument storage_unavailable unavailable conflict invalid_transition invalid_preview unknown")
	for _, stage := range stages {
		for _, operation := range operations {
			for _, reason := range reasons {
				d := FailureDiagnostic{stage, operation, reason}
				raw, err := d.Encode()
				if err != nil || raw != "TG1:"+stage+":"+operation+":"+reason || len(raw) > 128 {
					t.Fatal("canonical closed value rejected")
				}
				got, ok := ParseFailureDiagnostic(raw)
				if !ok || got != d {
					t.Fatal("round trip changed value")
				}
			}
		}
	}
}

func TestFailureDiagnosticRejectsFreeTextAndLegacy(t *testing.T) {
	for _, d := range []FailureDiagnostic{
		{"", "stage_run", "unknown"}, {"failed", "stage_run", "unknown"},
		{"baseline", "FAKE_SECRET_PATH_MARKER", "unknown"},
		{"baseline", "stage_run", strings.Repeat("x", 129)},
		{"baseline", "stage_run", "UNKNOWN"}, {"baseline", "stage_run", "原因"},
	} {
		if d.Valid() {
			t.Fatal("invalid value considered valid")
		}
		if _, err := d.Encode(); err == nil {
			t.Fatal("free text encoded")
		}
	}
	for _, raw := range []string{
		"", "stale-snapshot", "TG2:baseline:stage_run:unknown", "TG1::stage_run:unknown",
		"TG1:baseline:stage_run:unknown:extra", "TG1:baseline:stage_run:FAKE_SECRET_PATH_MARKER",
		"TG1:failed:stage_run:unknown", "tg1:baseline:stage_run:unknown", strings.Repeat("x", 129),
	} {
		got, ok := ParseFailureDiagnostic(raw)
		if ok || got != (FailureDiagnostic{}) {
			t.Fatal("invalid/legacy value parsed or echoed")
		}
	}
}
