package processcontrol

import "testing"

func TestBatchHostStatusValidationBindsStartedAndExitToSpec(
	t *testing.T,
) {
	batch := []BatchItem{
		{ID: "first"},
		{ID: "second"},
	}
	if err := validateBatchStartedStatus(
		HostStatus{
			Kind: "started", PID: 40,
			TargetProcessGroups: []int{41, 42},
		},
		batch,
	); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []HostStatus{
		{
			Kind: "started", PID: 40,
			TargetProcessGroups: []int{41},
		},
		{
			Kind: "started", PID: 40,
			TargetProcessGroups: []int{41, 41},
		},
		{
			Kind: "started", PID: 40,
			TargetProcessGroups: []int{41, 0},
		},
	} {
		if err := validateBatchStartedStatus(
			invalid,
			batch,
		); err == nil {
			t.Fatalf("accepted status %#v", invalid)
		}
	}
	children, valid := hostChildResults(
		[]HostChildResult{
			{ID: "second", TimedOut: true},
			{ID: "first", ExitCode: 17},
		},
		batch,
	)
	if !valid || len(children) != 2 {
		t.Fatalf("children = %#v, valid=%t", children, valid)
	}
	for _, invalid := range [][]HostChildResult{
		{{ID: "first"}},
		{{ID: "first"}, {ID: "unknown"}},
		{
			{ID: "first"},
			{ID: "second", ErrorCode: "UNKNOWN"},
		},
	} {
		if _, valid := hostChildResults(
			invalid,
			batch,
		); valid {
			t.Fatalf("accepted children %#v", invalid)
		}
	}
}
