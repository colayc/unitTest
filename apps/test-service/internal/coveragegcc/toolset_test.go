package coveragegcc

import "testing"

func TestGCCInstrumentationFingerprintIsStableHex(t *testing.T) {
	first, second := InstrumentationFingerprint(), InstrumentationFingerprint()
	if len(first) != 64 || first != second {
		t.Fatalf("InstrumentationFingerprint() = %q, %q; want stable SHA-256", first, second)
	}
}
