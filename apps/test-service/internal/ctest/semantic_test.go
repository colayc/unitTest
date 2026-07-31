package ctest

import "testing"

func TestSemanticHashCanonicalizesCollectionOrder(t *testing.T) {
	first := Snapshot{
		Kind:    "ctestInfo",
		Version: Version{Major: 1},
		Tests: []RawTest{
			{
				Name: "z", Command: []string{"/z", "--fixed"},
				Properties: []Property{
					{Name: "TIMEOUT", Value: PropertyValue{Kind: PropertyNumber, Number: "10"}},
					{Name: "LABELS", Value: PropertyValue{Kind: PropertyStrings, Strings: []string{"unit"}}},
				},
			},
			{Name: "a", Command: []string{"/a"}},
		},
	}
	second := first
	second.Tests = []RawTest{first.Tests[1], first.Tests[0]}
	second.Tests[1].Properties = []Property{
		first.Tests[0].Properties[1],
		first.Tests[0].Properties[0],
	}
	if SemanticHash(first) != SemanticHash(second) {
		t.Fatal("non-semantic collection order changed CTest semantic hash")
	}

	changed := second
	changed.Tests = append([]RawTest(nil), second.Tests...)
	changed.Tests[1] = second.Tests[1]
	changed.Tests[1].Command = []string{"/z", "--changed"}
	if SemanticHash(first) == SemanticHash(changed) {
		t.Fatal("command semantic change kept CTest semantic hash")
	}
}
