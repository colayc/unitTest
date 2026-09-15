package testdomain

import (
	"fmt"
	"testing"
	"time"
)

var catalog10000Result Catalog

func BenchmarkCatalog10000(b *testing.B) {
	input := catalog10000Input(b)
	b.ReportAllocs()
	b.SetBytes(10_000)
	b.ResetTimer()
	for range b.N {
		catalog, err := NewCatalog(input)
		if err != nil {
			b.Fatal(err)
		}
		catalog10000Result = catalog
	}
}

func catalog10000Input(b *testing.B) Catalog {
	b.Helper()
	containerID, err := ContainerID("benchmark", "catalog.tests")
	if err != nil {
		b.Fatal(err)
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
			b.Fatal(itemErr)
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
