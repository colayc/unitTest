package coveragedomain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/testdomain"
)

const (
	taskID       = "44444444444444444444444444444444"
	testRunID    = "55555555555555555555555555555555"
	reportID     = "66666666666666666666666666666666"
	coverageJSON = "77777777777777777777777777777777"
	junitXML     = "88888888888888888888888888888888"
	coverageHTML = "99999999999999999999999999999999"
)

func validToolchain() ToolchainSnapshot {
	return ToolchainSnapshot{
		Platform: PlatformWindows, Architecture: ArchitectureX64,
		Compiler:          CompilerSnapshot{Family: CompilerFamilyClangCL, Version: "18.1.8"},
		Driver:            DriverSnapshot{Name: DriverLLVMCov, Version: "18.1.8"},
		Collector:         CollectorSnapshot{Name: CollectorLLVMCov, Version: "18.1.8"},
		NormalizerVersion: "1.0.0", InstrumentationFingerprint: strings.Repeat("a", 64),
	}
}

func validSnapshot() testdomain.SelectionSnapshot {
	return testdomain.SelectionSnapshot{Mode: testdomain.SelectionItems, ItemIDs: []testdomain.ID{firstStableID, secondStableID}}
}

func validRun() Run {
	request := validRequest()
	id, err := CoverageRunID(request)
	if err != nil {
		panic(err)
	}
	return Run{
		ID: id, TaskID: taskID, TestRunID: testRunID, Status: StatusQueued,
		Request: request, SelectionSnapshot: validSnapshot(), Toolchain: validToolchain(),
		CreatedAt: time.Date(2026, 8, 4, 1, 2, 3, 4, time.FixedZone("test", 8*60*60)), LastSequence: 0,
	}
}

func finishedRun(outcome Outcome) Run {
	value := validRun()
	started := value.CreatedAt.Add(time.Second)
	finished := started.Add(time.Second)
	value.Status, value.Outcome = StatusFinished, outcome
	value.StartedAt, value.FinishedAt = &started, &finished
	switch outcome {
	case OutcomeAvailable, OutcomePartial:
		summary := Summary{Lines: Metric{Covered: 1, Total: 2}}
		value.Summary, value.ReportID = &summary, reportID
		value.Artifacts = ArtifactRefs{CoverageJSONID: coverageJSON, JUnitXMLID: junitXML, CoverageHTMLID: coverageHTML}
	case OutcomeUnavailable:
		value.Reason = ReasonBuildFailed
	case OutcomeCancelled:
		value.Reason = ReasonUserCancelled
	}
	return value
}

func TestCoverageWireVocabularyLiterals(t *testing.T) {
	values := []struct {
		name string
		got  string
		want string
	}{
		{name: "StatusQueued", got: string(StatusQueued), want: "queued"},
		{name: "StatusRunning", got: string(StatusRunning), want: "running"},
		{name: "StatusFinished", got: string(StatusFinished), want: "finished"},
		{name: "OutcomeAvailable", got: string(OutcomeAvailable), want: "available"},
		{name: "OutcomePartial", got: string(OutcomePartial), want: "partial"},
		{name: "OutcomeUnavailable", got: string(OutcomeUnavailable), want: "unavailable"},
		{name: "OutcomeCancelled", got: string(OutcomeCancelled), want: "cancelled"},
		{name: "ReasonUserCancelled", got: string(ReasonUserCancelled), want: "user_cancelled"},
		{name: "ReasonTaskTimedOut", got: string(ReasonTaskTimedOut), want: "task_timed_out"},
		{name: "ReasonInstrumentationFailed", got: string(ReasonInstrumentationFailed), want: "instrumentation_failed"},
		{name: "ReasonBuildFailed", got: string(ReasonBuildFailed), want: "build_failed"},
		{name: "ReasonProfileCollectionFailed", got: string(ReasonProfileCollectionFailed), want: "profile_collection_failed"},
		{name: "ReasonMergeFailed", got: string(ReasonMergeFailed), want: "merge_failed"},
		{name: "ReasonNormalizationFailed", got: string(ReasonNormalizationFailed), want: "normalization_failed"},
		{name: "ReasonReportGenerationFailed", got: string(ReasonReportGenerationFailed), want: "report_generation_failed"},
		{name: "ReasonPersistenceFailed", got: string(ReasonPersistenceFailed), want: "persistence_failed"},
		{name: "ReasonServiceRestarted", got: string(ReasonServiceRestarted), want: "service_restarted"},
		{name: "CompletenessReasonTestCrashed", got: string(CompletenessReasonTestCrashed), want: "test_crashed"},
		{name: "CompletenessReasonTestTimedOut", got: string(CompletenessReasonTestTimedOut), want: "test_timed_out"},
		{name: "CompletenessReasonProfileMissingForFailedInvocation", got: string(CompletenessReasonProfileMissingForFailedInvocation), want: "profile_missing_for_failed_invocation"},
		{name: "PlatformWindows", got: string(PlatformWindows), want: "windows"},
		{name: "PlatformLinux", got: string(PlatformLinux), want: "linux"},
		{name: "ArchitectureX86", got: string(ArchitectureX86), want: "x86"},
		{name: "ArchitectureX64", got: string(ArchitectureX64), want: "x64"},
		{name: "ArchitectureARM64", got: string(ArchitectureARM64), want: "arm64"},
		{name: "CompilerFamilyGCC", got: string(CompilerFamilyGCC), want: "gcc"},
		{name: "CompilerFamilyClang", got: string(CompilerFamilyClang), want: "clang"},
		{name: "CompilerFamilyClangCL", got: string(CompilerFamilyClangCL), want: "clang-cl"},
		{name: "DriverGCov", got: string(DriverGCov), want: "gcov"},
		{name: "DriverLLVMCov", got: string(DriverLLVMCov), want: "llvm-cov"},
		{name: "CollectorGCovr", got: string(CollectorGCovr), want: "gcovr"},
		{name: "CollectorLLVMCov", got: string(CollectorLLVMCov), want: "llvm-cov"},
		{name: "SchemaVersion10", got: SchemaVersion10, want: "1.0"},
	}
	for _, value := range values {
		t.Run(value.name, func(t *testing.T) {
			if value.got != value.want {
				t.Fatalf("%s = %q, want Protocol literal %q", value.name, value.got, value.want)
			}
		})
	}
}

func TestRunLifecycleAndClosedEnums(t *testing.T) {
	queued := validRun()
	if _, err := NewRun(queued); err != nil {
		t.Fatalf("queued NewRun() error = %v", err)
	}
	running := validRun()
	started := running.CreatedAt.Add(time.Second)
	running.Status, running.StartedAt = StatusRunning, &started
	if _, err := NewRun(running); err != nil {
		t.Fatalf("running NewRun() error = %v", err)
	}
	for _, outcome := range []Outcome{OutcomeAvailable, OutcomePartial, OutcomeUnavailable, OutcomeCancelled} {
		if _, err := NewRun(finishedRun(outcome)); err != nil {
			t.Fatalf("finished %q NewRun() error = %v", outcome, err)
		}
	}

	for name, mutate := range map[string]func(*Run){
		"unknown status":   func(v *Run) { v.Status = "unknown" },
		"queued started":   func(v *Run) { now := v.CreatedAt; v.StartedAt = &now },
		"queued outcome":   func(v *Run) { v.Outcome = OutcomeAvailable },
		"queued reason":    func(v *Run) { v.Reason = ReasonBuildFailed },
		"queued finished":  func(v *Run) { now := v.CreatedAt; v.FinishedAt = &now },
		"queued report":    func(v *Run) { v.ReportID = reportID },
		"queued summary":   func(v *Run) { v.Summary = &Summary{} },
		"queued artifacts": func(v *Run) { v.Artifacts.CoverageJSONID = coverageJSON },
		"running no start": func(v *Run) { v.Status = StatusRunning },
		"running outcome": func(v *Run) {
			v.Status = StatusRunning
			now := v.CreatedAt
			v.StartedAt = &now
			v.Outcome = OutcomeAvailable
		},
		"finished no outcome": func(v *Run) { f := finishedRun(OutcomeAvailable); *v = f; v.Outcome = "" },
		"finished bad outcome": func(v *Run) {
			f := finishedRun(OutcomeAvailable)
			*v = f
			v.Outcome = "unknown"
		},
		"finished no time":     func(v *Run) { f := finishedRun(OutcomeAvailable); *v = f; v.FinishedAt = nil },
		"unknown compiler":     func(v *Run) { v.Toolchain.Compiler.Family = "unknown" },
		"unknown architecture": func(v *Run) { v.Toolchain.Architecture = "unknown" },
	} {
		t.Run(name, func(t *testing.T) {
			value := queued
			mutate(&value)
			if _, err := NewRun(value); err == nil {
				t.Fatal("NewRun() error = nil, want rejection")
			}
		})
	}
}

func TestRunFinishedOutcomeOwnership(t *testing.T) {
	for name, mutate := range map[string]func(*Run){
		"available missing summary": func(v *Run) { v.Summary = nil },
		"partial invalid summary": func(v *Run) {
			v.Summary = &Summary{Lines: Metric{Covered: 2, Total: 1}}
		},
		"available invalid report": func(v *Run) { v.ReportID = "bad" },
		"available reason":         func(v *Run) { v.Reason = ReasonBuildFailed },
		"available missing artifact": func(v *Run) {
			v.Artifacts.CoverageHTMLID = ""
		},
		"available duplicate artifact": func(v *Run) {
			v.Artifacts.CoverageHTMLID = v.Artifacts.CoverageJSONID
		},
		"unavailable wrong reason":   func(v *Run) { v.Reason = ReasonUserCancelled },
		"unavailable unknown reason": func(v *Run) { v.Reason = "unknown" },
		"unavailable no reason":      func(v *Run) { v.Reason = "" },
		"unavailable summary":        func(v *Run) { v.Summary = &Summary{} },
		"unavailable report":         func(v *Run) { v.ReportID = reportID },
		"unavailable artifacts":      func(v *Run) { v.Artifacts.CoverageJSONID = coverageJSON },
		"cancelled wrong reason":     func(v *Run) { v.Reason = ReasonBuildFailed },
		"cancelled no reason":        func(v *Run) { v.Reason = "" },
		"cancelled summary":          func(v *Run) { v.Summary = &Summary{} },
		"cancelled report":           func(v *Run) { v.ReportID = reportID },
		"cancelled artifacts":        func(v *Run) { v.Artifacts.CoverageHTMLID = coverageHTML },
	} {
		t.Run(name, func(t *testing.T) {
			var value Run
			switch {
			case strings.HasPrefix(name, "unavailable"):
				value = finishedRun(OutcomeUnavailable)
			case strings.HasPrefix(name, "cancelled"):
				value = finishedRun(OutcomeCancelled)
			case strings.HasPrefix(name, "partial"):
				value = finishedRun(OutcomePartial)
			default:
				value = finishedRun(OutcomeAvailable)
			}
			mutate(&value)
			if _, err := NewRun(value); err == nil {
				t.Fatal("NewRun() error = nil, want rejection")
			}
		})
	}
	for _, reason := range []Reason{
		ReasonInstrumentationFailed, ReasonBuildFailed, ReasonProfileCollectionFailed, ReasonMergeFailed,
		ReasonNormalizationFailed, ReasonReportGenerationFailed, ReasonPersistenceFailed, ReasonServiceRestarted,
	} {
		value := finishedRun(OutcomeUnavailable)
		value.Reason = reason
		if _, err := NewRun(value); err != nil {
			t.Fatalf("unavailable reason %q rejected: %v", reason, err)
		}
	}
	for _, reason := range []Reason{ReasonUserCancelled, ReasonTaskTimedOut} {
		value := finishedRun(OutcomeCancelled)
		value.Reason = reason
		if _, err := NewRun(value); err != nil {
			t.Fatalf("cancelled reason %q rejected: %v", reason, err)
		}
	}
}

func TestRunIdentitySnapshotSequenceAndTimeValidation(t *testing.T) {
	for name, mutate := range map[string]func(*Run){
		"wrong deterministic id": func(v *Run) { v.ID = strings.Repeat("f", 32) },
		"invalid task id":        func(v *Run) { v.TaskID = "bad" },
		"invalid test run id":    func(v *Run) { v.TestRunID = "bad" },
		"invalid request":        func(v *Run) { v.Request.RepeatCount = 0 },
		"invalid snapshot":       func(v *Run) { v.SelectionSnapshot.ItemIDs[0] = secondStableID },
		"negative sequence":      func(v *Run) { v.LastSequence = -1 },
		"unsafe sequence":        func(v *Run) { v.LastSequence = MaxSafeInteger + 1 },
		"zero created":           func(v *Run) { v.CreatedAt = time.Time{} },
		"start before created": func(v *Run) {
			v.Status = StatusRunning
			started := v.CreatedAt.Add(-time.Nanosecond)
			v.StartedAt = &started
		},
		"finish before created": func(v *Run) {
			f := finishedRun(OutcomeUnavailable)
			*v = f
			finished := v.CreatedAt.Add(-time.Nanosecond)
			v.FinishedAt = &finished
			v.StartedAt = nil
		},
		"finish before start": func(v *Run) {
			f := finishedRun(OutcomeUnavailable)
			*v = f
			finished := v.StartedAt.Add(-time.Nanosecond)
			v.FinishedAt = &finished
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := validRun()
			mutate(&value)
			if _, err := NewRun(value); err == nil {
				t.Fatal("NewRun() error = nil, want rejection")
			}
		})
	}

	value := validRun()
	value.LastSequence = MaxSafeInteger
	originalSnapshot := value.SelectionSnapshot.ItemIDs[0]
	got, err := NewRun(value)
	if err != nil {
		t.Fatal(err)
	}
	value.SelectionSnapshot.ItemIDs[0] = secondStableID
	if got.SelectionSnapshot.ItemIDs[0] != originalSnapshot {
		t.Fatal("NewRun retained the caller's selection slice")
	}
	if got.CreatedAt.Location() != time.UTC {
		t.Fatalf("CreatedAt location = %v, want UTC", got.CreatedAt.Location())
	}
}

func TestRunCloneOwnsNestedStateAndTimePointers(t *testing.T) {
	input := finishedRun(OutcomePartial)
	startedInput, finishedInput := input.StartedAt, input.FinishedAt
	run, err := NewRun(input)
	if err != nil {
		t.Fatal(err)
	}
	if run.StartedAt == startedInput || run.FinishedAt == finishedInput || run.StartedAt.Location() != time.UTC || run.FinishedAt.Location() != time.UTC {
		t.Fatal("NewRun did not take UTC defensive copies of lifecycle pointers")
	}
	input.Request.Selection.ItemIDs[0] = secondStableID
	input.SelectionSnapshot.ItemIDs[0] = secondStableID
	input.Summary.Lines.Covered = 2
	*input.StartedAt = time.Time{}
	clone := run.Clone()
	clone.Request.Selection.ItemIDs[0] = secondStableID
	clone.SelectionSnapshot.ItemIDs[0] = secondStableID
	clone.Summary.Lines.Covered = 2
	*clone.StartedAt = time.Time{}
	if run.Request.Selection.ItemIDs[0] != firstStableID || run.SelectionSnapshot.ItemIDs[0] != firstStableID ||
		run.Summary.Lines.Covered != 1 || run.StartedAt.IsZero() {
		t.Fatal("validated run was mutated through input or clone")
	}
}

func TestToolchainSupportedMatrixAndValidation(t *testing.T) {
	valid := []ToolchainSnapshot{
		validToolchain(),
		{Platform: PlatformLinux, Architecture: ArchitectureX86, Compiler: CompilerSnapshot{Family: CompilerFamilyGCC, Version: "15"}, Driver: DriverSnapshot{Name: DriverGCov, Version: "15"}, Collector: CollectorSnapshot{Name: CollectorGCovr, Version: "8"}, NormalizerVersion: "1", InstrumentationFingerprint: strings.Repeat("b", 64)},
		{Platform: PlatformLinux, Architecture: ArchitectureARM64, Compiler: CompilerSnapshot{Family: CompilerFamilyClang, Version: "20"}, Driver: DriverSnapshot{Name: DriverLLVMCov, Version: "20"}, Collector: CollectorSnapshot{Name: CollectorLLVMCov, Version: "20"}, NormalizerVersion: "1", InstrumentationFingerprint: strings.Repeat("c", 64)},
	}
	for _, toolchain := range valid {
		value := validRun()
		value.Toolchain = toolchain
		if _, err := NewRun(value); err != nil {
			t.Fatalf("supported toolchain rejected: %#v: %v", toolchain, err)
		}
	}

	families := []CompilerFamily{CompilerFamilyGCC, CompilerFamilyClang, CompilerFamilyClangCL}
	drivers := []DriverName{DriverGCov, DriverLLVMCov}
	collectors := []CollectorName{CollectorGCovr, CollectorLLVMCov}
	platforms := []Platform{PlatformWindows, PlatformLinux}
	for _, platform := range platforms {
		for _, family := range families {
			for _, driver := range drivers {
				for _, collector := range collectors {
					approved := platform == PlatformWindows && family == CompilerFamilyClangCL && driver == DriverLLVMCov && collector == CollectorLLVMCov ||
						platform == PlatformLinux && family == CompilerFamilyGCC && driver == DriverGCov && collector == CollectorGCovr ||
						platform == PlatformLinux && family == CompilerFamilyClang && driver == DriverLLVMCov && collector == CollectorLLVMCov
					if approved {
						continue
					}
					value := validRun()
					value.Toolchain.Platform, value.Toolchain.Compiler.Family = platform, family
					value.Toolchain.Driver.Name, value.Toolchain.Collector.Name = driver, collector
					if _, err := NewRun(value); err == nil {
						t.Fatalf("cross-combination accepted: %s/%s/%s/%s", platform, family, driver, collector)
					}
				}
			}
		}
	}

	for name, mutate := range map[string]func(*ToolchainSnapshot){
		"unknown platform":     func(v *ToolchainSnapshot) { v.Platform = "plan9" },
		"unknown architecture": func(v *ToolchainSnapshot) { v.Architecture = "mips" },
		"unknown driver":       func(v *ToolchainSnapshot) { v.Driver.Name = "unknown" },
		"empty version":        func(v *ToolchainSnapshot) { v.Compiler.Version = "" },
		"long version":         func(v *ToolchainSnapshot) { v.Driver.Version = strings.Repeat("x", 129) },
		"invalid utf8":         func(v *ToolchainSnapshot) { v.Collector.Version = string([]byte{0xff}) },
		"nul version":          func(v *ToolchainSnapshot) { v.NormalizerVersion = "1\x00bad" },
		"short fingerprint":    func(v *ToolchainSnapshot) { v.InstrumentationFingerprint = strings.Repeat("a", 63) },
		"uppercase fingerprint": func(v *ToolchainSnapshot) {
			v.InstrumentationFingerprint = strings.Repeat("A", 64)
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := validRun()
			mutate(&value.Toolchain)
			if _, err := NewRun(value); err == nil {
				t.Fatal("NewRun() error = nil, want toolchain rejection")
			}
		})
	}
}

func validReport() Report {
	return Report{
		ID: reportID, RunID: validRun().ID, TestRunID: testRunID, SchemaVersion: SchemaVersion10,
		CreatedAt:    time.Date(2026, 8, 4, 2, 3, 4, 5, time.FixedZone("test", 8*60*60)),
		Completeness: Completeness{Outcome: OutcomePartial, Reasons: []CompletenessReason{CompletenessReasonTestTimedOut, CompletenessReasonTestCrashed}},
		Summary:      Summary{Lines: Metric{Covered: 1, Total: 2}}, Toolchain: validToolchain(), ArtifactID: coverageJSON,
	}
}

func TestCompletenessValidationCanonicalizationAndOwnership(t *testing.T) {
	available := validReport()
	available.Completeness = Completeness{Outcome: OutcomeAvailable, Reasons: []CompletenessReason{}}
	if _, err := NewReport(available); err != nil {
		t.Fatalf("available report rejected: %v", err)
	}
	partial := validReport()
	reasons := partial.Completeness.Reasons
	report, err := NewReport(partial)
	if err != nil {
		t.Fatal(err)
	}
	want := []CompletenessReason{CompletenessReasonTestCrashed, CompletenessReasonTestTimedOut}
	if !reflect.DeepEqual(report.Completeness.Reasons, want) {
		t.Fatalf("reasons = %#v, want %#v", report.Completeness.Reasons, want)
	}
	reasons[0] = CompletenessReasonProfileMissingForFailedInvocation
	clone := report.Clone()
	clone.Completeness.Reasons[0] = CompletenessReasonTestTimedOut
	if !reflect.DeepEqual(report.Completeness.Reasons, want) {
		t.Fatal("report reasons mutated through input or clone")
	}

	for name, completeness := range map[string]Completeness{
		"unknown outcome":       {Outcome: "unknown"},
		"available with reason": {Outcome: OutcomeAvailable, Reasons: []CompletenessReason{CompletenessReasonTestCrashed}},
		"partial empty":         {Outcome: OutcomePartial},
		"partial unknown":       {Outcome: OutcomePartial, Reasons: []CompletenessReason{"unknown"}},
		"partial duplicate":     {Outcome: OutcomePartial, Reasons: []CompletenessReason{CompletenessReasonTestCrashed, CompletenessReasonTestCrashed}},
	} {
		t.Run(name, func(t *testing.T) {
			value := validReport()
			value.Completeness = completeness
			if _, err := NewReport(value); err == nil {
				t.Fatal("NewReport() error = nil, want completeness rejection")
			}
		})
	}
	many := make([]CompletenessReason, 65)
	for index := range many {
		many[index] = CompletenessReasonTestCrashed
	}
	value := validReport()
	value.Completeness.Reasons = many
	if _, err := NewReport(value); err == nil {
		t.Fatal("NewReport accepted more than 64 reasons")
	}
}

func TestReportValidationAndClone(t *testing.T) {
	input := validReport()
	report, err := NewReport(input)
	if err != nil {
		t.Fatal(err)
	}
	if report.CreatedAt.Location() != time.UTC {
		t.Fatalf("CreatedAt location = %v, want UTC", report.CreatedAt.Location())
	}
	input.Completeness.Reasons[0] = CompletenessReasonProfileMissingForFailedInvocation
	clone := report.Clone()
	clone.Completeness.Reasons[0] = CompletenessReasonProfileMissingForFailedInvocation
	if report.Completeness.Reasons[0] != CompletenessReasonTestCrashed {
		t.Fatal("report mutated through input or clone")
	}

	for name, mutate := range map[string]func(*Report){
		"bad id":              func(v *Report) { v.ID = "bad" },
		"bad run id":          func(v *Report) { v.RunID = "bad" },
		"bad test run id":     func(v *Report) { v.TestRunID = "bad" },
		"bad schema":          func(v *Report) { v.SchemaVersion = "1" },
		"zero created":        func(v *Report) { v.CreatedAt = time.Time{} },
		"bad completeness":    func(v *Report) { v.Completeness.Outcome = "unknown" },
		"bad summary":         func(v *Report) { v.Summary.Lines = Metric{Covered: 2, Total: 1} },
		"bad toolchain":       func(v *Report) { v.Toolchain.Driver.Name = DriverGCov },
		"bad artifact":        func(v *Report) { v.ArtifactID = "bad" },
		"unknown enum member": func(v *Report) { v.Toolchain.Collector.Name = "unknown" },
	} {
		t.Run(name, func(t *testing.T) {
			value := validReport()
			mutate(&value)
			if _, err := NewReport(value); err == nil {
				t.Fatal("NewReport() error = nil, want rejection")
			}
		})
	}
}

func TestRunAndReportJSONExposeNoExecutionOrNativeArtifactFields(t *testing.T) {
	for name, value := range map[string]any{"run": finishedRun(OutcomeAvailable), "report": validReport()} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var decoded any
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			assertNoForbiddenDomainFields(t, decoded)
		})
	}
}

func assertNoForbiddenDomainFields(t *testing.T, value any) {
	t.Helper()
	forbidden := map[string]bool{
		"path": true, "nativepath": true, "command": true, "args": true, "argv": true,
		"env": true, "environment": true, "rawprofile": true, "indexedprofile": true,
		"gcda": true, "thirdpartyjson": true, "files": true,
	}
	switch item := value.(type) {
	case map[string]any:
		for key, nested := range item {
			if forbidden[strings.ToLower(key)] {
				t.Fatalf("forbidden JSON field %q", key)
			}
			assertNoForbiddenDomainFields(t, nested)
		}
	case []any:
		for _, nested := range item {
			assertNoForbiddenDomainFields(t, nested)
		}
	}
}
