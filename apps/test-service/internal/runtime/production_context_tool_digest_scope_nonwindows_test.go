//go:build !windows

package runtime

import (
	"context"
	"testing"

	"unit-test-ide.local/test-service/internal/discovery"
)

type nonWindowsProductionScopeReader struct {
	*productionRuntimeContextFixture
	expected context.Context
	seen     int
	bad      bool
}

func (p *nonWindowsProductionScopeReader) InspectWorkspace(ctx context.Context) (discovery.Snapshot, error) {
	p.seen++
	if ctx != p.expected || ctx.Value(productionToolScopeKey{}) != nil {
		p.bad = true
	}
	return p.productionRuntimeContextFixture.InspectWorkspace(ctx)
}
func TestProductionContextToolScopeNonWindows(t *testing.T) {
	input := context.Background()
	ctx, scope, owner, err := beginProductionToolScope(input)
	if err != nil || ctx != input || scope != nil || owner != nil {
		t.Fatal("non-Windows acquired protected owner")
	}
	fixture := productionRuntimeAuthorityFixture()
	reader := &nonWindowsProductionScopeReader{productionRuntimeContextFixture: fixture, expected: input}
	if _, err := (runtimeProductionResolverAuthority{reader: reader}).ResolveProductionContext(input, "core", fixture.run.Request.WorkspaceGeneration, fixture.report.ID); err != nil {
		t.Fatal(err)
	}
	if reader.bad || reader.seen != 2 {
		t.Fatal("non-Windows gained marker or lost outer inspections")
	}
}
