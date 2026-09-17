package testdomain

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

var catalog10000Result Catalog

func TestCatalog10000AllocationBudget(t *testing.T) {
	const maxAllocationsPerOperation = 300_000
	input := catalog10000Input(t)
	var err error
	allocations := testing.AllocsPerRun(3, func() {
		catalog10000Result, err = NewCatalog(input)
	})
	if err != nil {
		t.Fatal(err)
	}
	if allocations > maxAllocationsPerOperation {
		t.Fatalf("NewCatalog(10000 items) allocations/op = %.0f, want <= %d", allocations, maxAllocationsPerOperation)
	}
}

func BenchmarkCatalog10000(b *testing.B) {
	input := catalog10000Input(b)
	artifact, err := json.Marshal(input)
	if err != nil {
		b.Fatal(err)
	}
	ids := make([]string, len(input.Items))
	for index, item := range input.Items {
		ids[index] = item.ID.String()
	}
	sort.Strings(ids)
	// Identity diagnostics happen outside the measured operation; the fixed
	// producer validates all three lines against committed input anchors.
	fmt.Printf("UTIDE_CATALOG10000 %s %x %x\n", input.Revision, sha256.Sum256(artifact), sha256.Sum256([]byte(strings.Join(ids, "\n"))))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		catalog, err := NewCatalog(input)
		if err != nil {
			b.Fatal(err)
		}
		catalog10000Result = catalog
	}
}

func catalog10000Input(tb testing.TB) Catalog {
	tb.Helper()
	containerID, err := ContainerID("benchmark", "catalog.tests")
	if err != nil {
		tb.Fatal(err)
	}
	items := make([]Item, 10_000)
	for index := range items {
		name := fmt.Sprintf("case-%05d", index)
		itemID, itemErr := CaseID(CaseIdentity{
			ProjectID: "benchmark",
			CTestName: "catalog.tests",
			Framework: FrameworkCppUTest,
			Name:      name,
		})
		if itemErr != nil {
			tb.Fatal(itemErr)
		}
		items[index] = Item{
			ID:          itemID,
			ContainerID: containerID,
			Kind:        ItemCase,
			Framework:   FrameworkCppUTest,
			LogicalName: name,
			DisplayName: name,
		}
	}
	return Catalog{
		ProjectID:   "benchmark",
		ProfileID:   "1f6196ffa30f4d5f229b2ab4cf079e95768147be66bd08ec86ee2b63dfcac602",
		Revision:    "719c74062e19f57e40df62f5183f591bba6dde4941406dac2452f469e89da9d4",
		GeneratedAt: time.Unix(1_700_000_000, 0).UTC(),
		Containers: []Container{{
			ID:               containerID,
			ProjectID:        "benchmark",
			CTestLogicalName: "catalog.tests",
			DisplayName:      "Catalog benchmark",
			Framework:        FrameworkCppUTest,
		}},
		Items: items,
	}
}
