package ctest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// SemanticHash hashes the validated CTest model rather than the original JSON
// bytes. Test and property collection order is canonicalized; command and
// property value order remains intact where it can affect execution semantics.
func SemanticHash(snapshot Snapshot) string {
	canonical := snapshot
	canonical.Tests = append([]RawTest(nil), snapshot.Tests...)
	for index := range canonical.Tests {
		canonical.Tests[index].Command = append(
			[]string(nil),
			canonical.Tests[index].Command...,
		)
		canonical.Tests[index].Properties = append(
			[]Property(nil),
			canonical.Tests[index].Properties...,
		)
		sort.Slice(canonical.Tests[index].Properties, func(first, second int) bool {
			return canonical.Tests[index].Properties[first].Name <
				canonical.Tests[index].Properties[second].Name
		})
		for propertyIndex := range canonical.Tests[index].Properties {
			value := &canonical.Tests[index].Properties[propertyIndex].Value
			value.Strings = append([]string(nil), value.Strings...)
		}
	}
	sort.Slice(canonical.Tests, func(first, second int) bool {
		return canonical.Tests[first].Name < canonical.Tests[second].Name
	})
	canonical.BacktraceGraph.Commands = append(
		[]string(nil),
		snapshot.BacktraceGraph.Commands...,
	)
	canonical.BacktraceGraph.Files = append(
		[]string(nil),
		snapshot.BacktraceGraph.Files...,
	)
	canonical.BacktraceGraph.Nodes = append(
		[]BacktraceNode(nil),
		snapshot.BacktraceGraph.Nodes...,
	)
	encoded, _ := json.Marshal(canonical)
	sum := sha256.Sum256(append([]byte("ctest-semantic-v1\x00"), encoded...))
	return hex.EncodeToString(sum[:])
}
