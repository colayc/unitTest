/** Compile-time test seams. These bytes are supplied only through Go -overlay;
 * neither the tracked runtime nor its Workspace/Protocol contract is changed. */
export function createGccFaultOverlay(source: string, fault: "missing-data" | "malformed-pinned-json"): string {
  const replaceOnce = (needle: string, replacement: string): void => {
    if (source.split(needle).length !== 2) throw new Error("test-only overlay seam changed or is ambiguous");
    source = source.replace(needle, replacement);
  };
  if (fault === "malformed-pinned-json") {
    const needle = "raw, err := input.PinnedOutput.ReadAll(); if err != nil { return coveragemodelv1.CoverageDocumentV1{}, nil, err }";
    replaceOnce(needle, `${needle}\n\traw = []byte("{") // test-only: the real bounded parser must reject this`);
  } else {
    replaceOnce("import (", 'import (\n "os"\n "strings"');
    replaceOnce("type gccPreparedCoverageAdapter struct {", "type gccPreparedCoverageAdapter struct {\n testOnlyObjectRoot string");
    replaceOnce("evidence, err := coveragegcc.PrepareEvidence(prepared.CoverageObjectDirectory().Path())", "a.testOnlyObjectRoot = prepared.CoverageObjectDirectory().Path()\n evidence, err := coveragegcc.PrepareEvidence(prepared.CoverageObjectDirectory().Path())");
    replaceOnce("manifest, err := evidence.Seal(context.Background(), outcomes)", `// test-only: remove only known gcda peers before the real sealing check.
 for _, note := range evidence.Notes {
   path := filepath.Join(a.testOnlyObjectRoot, strings.TrimSuffix(note.RelativePath, ".gcno") + ".gcda")
   if err := os.Remove(path); err != nil && !os.IsNotExist(err) { return nil, err }
 }
 manifest, err := evidence.Seal(context.Background(), outcomes)`);
  }
  return source;
}
