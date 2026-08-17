package task_test

import (
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
)

func TestCoverageTaskOutcomeMapsClosedCoverageOutcomes(t *testing.T) {
	tests := []struct {
		name    string
		outcome coveragedomain.Outcome
		reason  coveragedomain.Reason
		want    task.Outcome
	}{
		{name: "available", outcome: coveragedomain.OutcomeAvailable, want: task.OutcomeSucceeded},
		{name: "partial", outcome: coveragedomain.OutcomePartial, want: task.OutcomeSucceeded},
		{name: "unavailable instrumentation", outcome: coveragedomain.OutcomeUnavailable, reason: coveragedomain.ReasonInstrumentationFailed, want: task.OutcomeInfrastructureFailed},
		{name: "unavailable build", outcome: coveragedomain.OutcomeUnavailable, reason: coveragedomain.ReasonBuildFailed, want: task.OutcomeCommandFailed},
		{name: "unavailable collection", outcome: coveragedomain.OutcomeUnavailable, reason: coveragedomain.ReasonProfileCollectionFailed, want: task.OutcomeInfrastructureFailed},
		{name: "unavailable merge", outcome: coveragedomain.OutcomeUnavailable, reason: coveragedomain.ReasonMergeFailed, want: task.OutcomeInfrastructureFailed},
		{name: "unavailable normalization", outcome: coveragedomain.OutcomeUnavailable, reason: coveragedomain.ReasonNormalizationFailed, want: task.OutcomeInfrastructureFailed},
		{name: "unavailable report", outcome: coveragedomain.OutcomeUnavailable, reason: coveragedomain.ReasonReportGenerationFailed, want: task.OutcomeInfrastructureFailed},
		{name: "unavailable persistence", outcome: coveragedomain.OutcomeUnavailable, reason: coveragedomain.ReasonPersistenceFailed, want: task.OutcomeInfrastructureFailed},
		{name: "unavailable restart", outcome: coveragedomain.OutcomeUnavailable, reason: coveragedomain.ReasonServiceRestarted, want: task.OutcomeInterrupted},
		{name: "cancelled user", outcome: coveragedomain.OutcomeCancelled, reason: coveragedomain.ReasonUserCancelled, want: task.OutcomeCancelled},
		{name: "cancelled timeout", outcome: coveragedomain.OutcomeCancelled, reason: coveragedomain.ReasonTaskTimedOut, want: task.OutcomeTimedOut},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := task.CoverageTaskOutcome(tt.outcome, tt.reason); got != tt.want {
				t.Fatalf("CoverageTaskOutcome(%q, %q) = %q, want %q", tt.outcome, tt.reason, got, tt.want)
			}
		})
	}
}

func TestCoverageTaskOutcomeFailsClosedForInvalidCombinations(t *testing.T) {
	validReasons := []coveragedomain.Reason{
		coveragedomain.ReasonUserCancelled,
		coveragedomain.ReasonTaskTimedOut,
		coveragedomain.ReasonInstrumentationFailed,
		coveragedomain.ReasonBuildFailed,
		coveragedomain.ReasonProfileCollectionFailed,
		coveragedomain.ReasonMergeFailed,
		coveragedomain.ReasonNormalizationFailed,
		coveragedomain.ReasonReportGenerationFailed,
		coveragedomain.ReasonPersistenceFailed,
		coveragedomain.ReasonServiceRestarted,
	}
	for _, outcome := range []coveragedomain.Outcome{coveragedomain.OutcomeAvailable, coveragedomain.OutcomePartial} {
		for _, reason := range validReasons {
			if got := task.CoverageTaskOutcome(outcome, reason); got != "" {
				t.Fatalf("CoverageTaskOutcome(%q, %q) = %q, want empty", outcome, reason, got)
			}
		}
	}
	for _, outcome := range []coveragedomain.Outcome{coveragedomain.OutcomeUnavailable, coveragedomain.OutcomeCancelled} {
		if got := task.CoverageTaskOutcome(outcome, ""); got != "" {
			t.Fatalf("CoverageTaskOutcome(%q, empty) = %q, want empty", outcome, got)
		}
	}
	for _, outcome := range []coveragedomain.Outcome{"unknown", coveragedomain.OutcomeAvailable, coveragedomain.OutcomePartial, coveragedomain.OutcomeUnavailable, coveragedomain.OutcomeCancelled} {
		if got := task.CoverageTaskOutcome(outcome, "unknown"); got != "" {
			t.Fatalf("CoverageTaskOutcome(%q, unknown) = %q, want empty", outcome, got)
		}
	}
	if got := task.CoverageTaskOutcome("unknown", ""); got != "" {
		t.Fatalf("CoverageTaskOutcome(unknown, empty) = %q, want empty", got)
	}
	for _, reason := range []coveragedomain.Reason{
		coveragedomain.ReasonUserCancelled,
		coveragedomain.ReasonTaskTimedOut,
	} {
		if got := task.CoverageTaskOutcome(coveragedomain.OutcomeUnavailable, reason); got != "" {
			t.Fatalf("CoverageTaskOutcome(unavailable, %q) = %q, want empty", reason, got)
		}
	}
	for _, reason := range []coveragedomain.Reason{
		coveragedomain.ReasonInstrumentationFailed,
		coveragedomain.ReasonBuildFailed,
		coveragedomain.ReasonProfileCollectionFailed,
		coveragedomain.ReasonMergeFailed,
		coveragedomain.ReasonNormalizationFailed,
		coveragedomain.ReasonReportGenerationFailed,
		coveragedomain.ReasonPersistenceFailed,
		coveragedomain.ReasonServiceRestarted,
	} {
		if got := task.CoverageTaskOutcome(coveragedomain.OutcomeCancelled, reason); got != "" {
			t.Fatalf("CoverageTaskOutcome(cancelled, %q) = %q, want empty", reason, got)
		}
	}
}
