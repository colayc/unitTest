package testgendomain

import "strings"

type FailureDiagnostic struct{ Stage, Operation, Reason string }

func (d FailureDiagnostic) Valid() bool {
	switch d.Stage {
	case "baseline", "analyzing", "solving", "rendering", "validating", "minimizing", "awaiting_confirmation", "unknown":
	default:
		return false
	}
	switch d.Operation {
	case "ledger", "reserve_budget", "stage_run", "stage_context", "transition", "preview_set", "authoritative_set", "publish_plan", "preview_diff", "commit_budget", "finalize_review", "resume", "resolve_target", "authorize_process", "pipeline_generate", "validate_candidates", "minimize_candidates", "unknown":
	default:
		return false
	}
	switch d.Reason {
	case "timed_out", "cancelled", "budget_exceeded", "budget_invalid", "stale_snapshot", "invalid_argument", "storage_unavailable", "unavailable", "conflict", "invalid_transition", "invalid_preview", "unknown":
		return true
	default:
		return false
	}
}

func (d FailureDiagnostic) Encode() (string, error) {
	if !d.Valid() {
		return "", ErrInvalid
	}
	raw := "TG1:" + d.Stage + ":" + d.Operation + ":" + d.Reason
	if len(raw) > 128 {
		return "", ErrInvalid
	}
	return raw, nil
}

func ParseFailureDiagnostic(raw string) (FailureDiagnostic, bool) {
	if len(raw) > 128 {
		return FailureDiagnostic{}, false
	}
	parts := strings.Split(raw, ":")
	if len(parts) != 4 || parts[0] != "TG1" {
		return FailureDiagnostic{}, false
	}
	d := FailureDiagnostic{parts[1], parts[2], parts[3]}
	if !d.Valid() {
		return FailureDiagnostic{}, false
	}
	return d, true
}
