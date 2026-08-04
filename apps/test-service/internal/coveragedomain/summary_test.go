package coveragedomain

import (
	"errors"
	"reflect"
	"testing"
)

func TestSummaryValidation(t *testing.T) {
	valid := []Summary{
		{},
		{
			Lines:     Metric{Covered: MaxSafeInteger, Total: MaxSafeInteger},
			Branches:  Metric{Covered: MaxSafeInteger, Total: MaxSafeInteger},
			Functions: Metric{Covered: MaxSafeInteger, Total: MaxSafeInteger},
		},
	}
	for _, value := range valid {
		if got, err := NewSummary(value); err != nil || !reflect.DeepEqual(got, value) {
			t.Fatalf("NewSummary(%#v) = %#v, %v", value, got, err)
		}
	}

	for name, mutate := range map[string]func(*Summary){
		"lines covered negative":      func(v *Summary) { v.Lines.Covered = -1 },
		"lines total negative":        func(v *Summary) { v.Lines.Total = -1 },
		"lines covered exceeds total": func(v *Summary) { v.Lines = Metric{Covered: 2, Total: 1} },
		"lines total unsafe":          func(v *Summary) { v.Lines.Total = MaxSafeInteger + 1 },
		"branches invalid":            func(v *Summary) { v.Branches = Metric{Covered: 2, Total: 1} },
		"functions invalid":           func(v *Summary) { v.Functions = Metric{Covered: 2, Total: 1} },
	} {
		t.Run(name, func(t *testing.T) {
			value := Summary{}
			mutate(&value)
			if got, err := NewSummary(value); got != (Summary{}) || !errors.Is(err, ErrInvalidSummary) {
				t.Fatalf("NewSummary() = %#v, %v; want zero, ErrInvalidSummary", got, err)
			}
		})
	}
}

func TestSummaryAdditionIsSafeAndDoesNotMutateInputs(t *testing.T) {
	left := Summary{
		Lines: Metric{Covered: 1, Total: 2}, Branches: Metric{Covered: 3, Total: 4}, Functions: Metric{Covered: 5, Total: 6},
	}
	right := Summary{
		Lines: Metric{Covered: 7, Total: 8}, Branches: Metric{Covered: 9, Total: 10}, Functions: Metric{Covered: 11, Total: 12},
	}
	wantLeft, wantRight := left, right
	want := Summary{
		Lines: Metric{Covered: 8, Total: 10}, Branches: Metric{Covered: 12, Total: 14}, Functions: Metric{Covered: 16, Total: 18},
	}
	got, err := AddSummary(left, right)
	if err != nil || got != want {
		t.Fatalf("AddSummary() = %#v, %v; want %#v", got, err, want)
	}
	if left != wantLeft || right != wantRight {
		t.Fatal("AddSummary mutated an input")
	}
}

func TestSummaryAdditionRejectsInvalidInputAndOverflowAtomically(t *testing.T) {
	for name, values := range map[string][2]Summary{
		"invalid left": {
			{Lines: Metric{Covered: 2, Total: 1}}, {},
		},
		"invalid right": {
			{}, {Branches: Metric{Covered: -1}},
		},
		"covered overflow": {
			{Lines: Metric{Covered: MaxSafeInteger, Total: MaxSafeInteger}},
			{Lines: Metric{Covered: 1, Total: 1}},
		},
		"total overflow after earlier component": {
			{Lines: Metric{Covered: 1, Total: 1}, Functions: Metric{Total: MaxSafeInteger}},
			{Lines: Metric{Covered: 1, Total: 1}, Functions: Metric{Total: 1}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			left, right := values[0], values[1]
			wantLeft, wantRight := left, right
			got, err := AddSummary(left, right)
			if got != (Summary{}) || !errors.Is(err, ErrInvalidSummary) {
				t.Fatalf("AddSummary() = %#v, %v; want zero, ErrInvalidSummary", got, err)
			}
			if left != wantLeft || right != wantRight {
				t.Fatal("AddSummary mutated an input on failure")
			}
		})
	}
}
