export const IDENTITY_ITEM_COUNT = 10_000;

export function createIdentityCatalogItems() {
  return Array.from({ length: IDENTITY_ITEM_COUNT }, (_, index) => ({
    id: `benchmark-item-${index.toString().padStart(5, "0")}`,
    containerId: "benchmark-container",
    displayName: `Benchmark item ${index}`,
    logicalName: `benchmark.${index}`,
    framework: "ctest",
    kind: "case",
    disabled: false,
    labels: []
  }));
}

export function createIdentityRefreshFixture() {
  const entries = new Map(createIdentityCatalogItems().map((item) => [item.id, { id: item.id }]));
  const refresh = () => ({ revision: "benchmark-r1", items: entries });
  return {
    refresh,
    refreshSameRevision() {
      const before = entries.get("benchmark-item-00000");
      const snapshot = refresh();
      return { ...snapshot, identityPreserved: before === snapshot.items.get("benchmark-item-00000") };
    }
  };
}
