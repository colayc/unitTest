package testgenanalysis

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// These synthetic facts exercise the closed decoder, not a real tool handoff.
func integerABITestBinding(platform string) integerABIBinding {
	return integerABIBinding{strings.Repeat("b", 64), strings.Repeat("c", 64), "-std=c11", platform}
}

// Accepting malformed, partial, or ambiguous consumed facts breaks these tests.
func TestIntegerABIFactsRejectMalformed(t *testing.T) {
	base := integerABITestMacros("windows-x64", false)
	bad := bytes.Replace(base, []byte("__INT_WIDTH__ 32"), []byte("__INT_WIDTH__ (16+16)"), 1)
	if _, err := decodeIntegerABIFacts(bad, integerABITestBinding("windows-x64")); err == nil {
		t.Fatal("macro expression accepted")
	}
	cases := map[string][]byte{"empty": nil, "oversize": bytes.Repeat([]byte("x"), maxIntegerABIBytes+1), "nul": append(append([]byte{}, base...), 0, '\n'), "unterminated": base[:len(base)-1]}
	for _, value := range []string{"(16+16)", "", "0x20", "032", "-32", "32Z", "32ULUL", strings.Repeat("9", 33), "0", "128", "18446744073709551616", "16"} {
		cases["width/"+value] = bytes.Replace(base, []byte("__INT_WIDTH__ 32"), []byte("__INT_WIDTH__ "+value), 1)
	}
	for _, pair := range [][2]string{{"__INT_MAX__ 2147483647", "__INT_MAX__ 127"}, {"__SIZEOF_INT__ 4", "__SIZEOF_INT__ 2"}, {"__SHRT_WIDTH__ 16", "__SHRT_WIDTH__ 64"}, {"__CHAR_UNSIGNED__ 1", "__CHAR_UNSIGNED__ 0"}, {"__INT_WIDTH__ 32", "__INT_WIDTH__(x) 32"}} {
		source := base
		if strings.HasPrefix(pair[0], "__CHAR_UNSIGNED__") {
			source = integerABITestMacros("windows-x64", true)
		}
		cases[pair[1]] = bytes.Replace(source, []byte(pair[0]), []byte(pair[1]), 1)
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			facts, err := decodeIntegerABIFacts(input, integerABITestBinding("windows-x64"))
			if err != errIntegerABI || facts.verified {
				t.Fatal("malformed fact accepted")
			}
		})
	}
	// Unknown, even function-like macros do not expand the consumed set.
	if _, err := decodeIntegerABIFacts(append(base, []byte("#define PROJECT_FEATURE(x) (x+1)\n")...), integerABITestBinding("windows-x64")); err != nil {
		t.Fatal("unknown macro rejected")
	}
}

func TestIntegerABIFactsRejectMissingOrDuplicate(t *testing.T) {
	base := integerABITestMacros("windows-x64", false)
	for _, line := range bytes.Split(base, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		name := strings.Fields(string(line))[1]
		t.Run("missing/"+name, func(t *testing.T) {
			input := bytes.Replace(base, append(append([]byte{}, line...), '\n'), nil, 1)
			if _, err := decodeIntegerABIFacts(input, integerABITestBinding("windows-x64")); err != errIntegerABI {
				t.Fatal("missing fact accepted")
			}
		})
		for _, value := range []string{"32", "33"} {
			t.Run("duplicate/"+name+"/"+value, func(t *testing.T) {
				if _, err := decodeIntegerABIFacts(append(append([]byte{}, base...), []byte("#define "+name+" "+value+"\n")...), integerABITestBinding("windows-x64")); err != errIntegerABI {
					t.Fatal("duplicate fact accepted")
				}
			})
		}
	}
}

func TestIntegerABIFactsRejectIdentity(t *testing.T) {
	base := integerABITestMacros("windows-x64", false)
	cases := map[string][]byte{}
	for _, p := range [][2]string{{"__clang__ 1", "__clang__ 0"}, {"__clang_major__ 22", "__clang_major__ 21"}, {"__clang_minor__ 1", "__clang_minor__ 0"}, {"__clang_patchlevel__ 8", "__clang_patchlevel__ 7"}, {"__x86_64__ 1", "__x86_64__ 0"}, {"__CHAR_BIT__ 8", "__CHAR_BIT__ 16"}, {"__SIZEOF_POINTER__ 8", "__SIZEOF_POINTER__ 4"}, {"__POINTER_WIDTH__ 64", "__POINTER_WIDTH__ 32"}, {"_WIN32 1", "_WIN32 0"}, {"_WIN64 1", "_WIN64 0"}} {
		cases[p[1]] = bytes.Replace(base, []byte(p[0]), []byte(p[1]), 1)
	}
	cases["mixed"] = append(append([]byte{}, base...), []byte("#define __linux__ 1\n")...)
	cases["no-platform"] = bytes.Replace(base, []byte("#define _WIN32 1\n#define _WIN64 1\n"), nil, 1)
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeIntegerABIFacts(input, integerABITestBinding("windows-x64")); err != errIntegerABI {
				t.Fatal("untrusted identity accepted")
			}
		})
	}
	linux := integerABITestMacros("linux-x64", false)
	for _, suffix := range []string{"#define _WIN32 1\n", "#define _WIN64 1\n"} {
		if _, err := decodeIntegerABIFacts(append(append([]byte{}, linux...), []byte(suffix)...), integerABITestBinding("linux-x64")); err != errIntegerABI {
			t.Fatal("mixed Linux identity accepted")
		}
	}
	if _, err := decodeIntegerABIFacts(bytes.Replace(linux, []byte("__linux__ 1"), []byte("__linux__ 0"), 1), integerABITestBinding("linux-x64")); err != errIntegerABI {
		t.Fatal("invalid Linux identity accepted")
	}
}

// Cross-request facts and invalid bindings must fail before any mutation.
func TestIntegerABIFactsBindingIsolation(t *testing.T) {
	binding := integerABITestBinding("windows-x64")
	facts, err := decodeIntegerABIFacts(integerABITestMacros(binding.platform, false), binding)
	if err != nil {
		t.Fatal("test facts invalid")
	}
	other := binding
	other.compile = strings.Repeat("d", 64)
	invalid := []integerABIBinding{other, {binding.manifest, binding.compile, "-std=bad", binding.platform}, {"bad", binding.compile, binding.standard, binding.platform}, {binding.manifest, "bad", binding.standard, binding.platform}, {binding.manifest, binding.compile, binding.standard, "arm64"}}
	for _, b := range invalid {
		p := Program{Version: IRVersion, Digest: strings.Repeat("e", 64), Functions: []Function{{ReturnType: parseType("int", nil)}}}
		before := p.Functions[0]
		if applyIntegerABIFacts(&p, facts, b) != errIntegerABI || p.Digest != strings.Repeat("e", 64) || !reflect.DeepEqual(p.Functions[0], before) {
			t.Fatal("cross-request facts applied")
		}
		if b != other {
			if _, err := decodeIntegerABIFacts(integerABITestMacros(binding.platform, false), b); err != errIntegerABI {
				t.Fatal("invalid binding decoded")
			}
		}
	}
	if applyIntegerABIFacts(nil, facts, binding) != errIntegerABI {
		t.Fatal("nil program accepted")
	}
	facts.verified = false
	p := Program{Digest: "preserved"}
	if applyIntegerABIFacts(&p, facts, binding) != errIntegerABI || p.Digest != "preserved" {
		t.Fatal("unverified facts applied")
	}
}

// Omitting a scalar pair, retaining stale widths, or omitting digest changes breaks this test.
func TestIntegerABIFactsApplyAndSeal(t *testing.T) {
	binding := integerABITestBinding("linux-x64")
	facts, err := decodeIntegerABIFacts(integerABITestMacros(binding.platform, true), binding)
	if err != nil {
		t.Fatal("test facts invalid")
	}
	cases := []struct {
		name   string
		width  int
		signed bool
	}{{"char", 8, false}, {"signed char", 8, true}, {"unsigned char", 8, false}, {"short", 16, true}, {"unsigned short", 16, false}, {"int", 32, true}, {"unsigned int", 32, false}, {"long", 64, true}, {"unsigned long", 64, false}, {"long long", 64, true}, {"unsigned long long", 64, false}}
	p := Program{Version: IRVersion, Functions: []Function{{SymbolID: strings.Repeat("a", 64), Decision: Decision{DecisionSupported, ReasonNone}}}}
	for _, c := range cases {
		typ := parseType(c.name, nil)
		typ.BitWidth = 16
		typ.Signed = !c.signed
		typ.Proven = true
		p.Functions[0].LocalTypes = append(p.Functions[0].LocalTypes, typ)
	}
	if sealProgram(&p) != nil {
		t.Fatal("initial seal failed")
	}
	old := p.Digest
	if applyIntegerABIFacts(&p, facts, binding) != nil || p.Digest != "" {
		t.Fatal("facts did not invalidate digest")
	}
	for i, c := range cases {
		typ := p.Functions[0].LocalTypes[i]
		if typ.BitWidth != c.width || typ.Signed != c.signed || !typ.Proven {
			t.Fatal("scalar pair not proven")
		}
	}
	if sealProgram(&p) != nil || p.Digest == old {
		t.Fatal("ABI omitted from digest")
	}
	digest := p.Digest
	if sealProgram(&p) != nil || p.Digest != digest {
		t.Fatal("seal unstable")
	}
	// Widths come from validated facts, never from the platform name.
	windows := integerABITestBinding("windows-x64")
	input := bytes.Replace(integerABITestMacros("linux-x64", false), []byte("#define __linux__ 1\n"), []byte("#define _WIN32 1\n#define _WIN64 1\n"), 1)
	facts, err = decodeIntegerABIFacts(input, windows)
	if err != nil {
		t.Fatal("coherent widths rejected")
	}
	p.Functions[0].ReturnType = parseType("long", nil)
	if applyIntegerABIFacts(&p, facts, windows) != nil || p.Functions[0].ReturnType.BitWidth != 64 {
		t.Fatal("platform guessed width")
	}
}

func TestIntegerABIFactsUnprovenTypes(t *testing.T) {
	binding := integerABITestBinding("windows-x64")
	facts, err := decodeIntegerABIFacts(integerABITestMacros(binding.platform, false), binding)
	if err != nil {
		t.Fatal("test facts invalid")
	}
	unchanged := []Type{parseType("bool", nil), parseType("void", nil), parseType("double", nil), parseType("struct R", map[string]TypeKind{"R": TypeRecord}), parseType("int *", nil), parseType("int[2]", nil)}
	p := Program{Functions: []Function{{SymbolID: "first", ReturnType: parseType("size_t", nil), LocalTypes: append(append([]Type{}, unchanged...), parseType("int", nil)), Decision: Decision{DecisionSupported, ReasonNone}}, {SymbolID: "second", ReturnType: parseType("size_t", nil), Decision: Decision{DecisionUnsupported, ReasonExternalCall}}}}
	p.Functions[0].ReturnType.BitWidth = 64
	p.Functions[0].ReturnType.Proven = true
	if applyIntegerABIFacts(&p, facts, binding) != nil {
		t.Fatal("facts not applied")
	}
	f := p.Functions[0]
	if f.ReturnType.Proven || f.ReturnType.BitWidth != 0 || f.ReturnType.Signed || f.Decision != (Decision{DecisionUnsupported, ReasonUnsupportedType}) || !reflect.DeepEqual(p.Diagnostics, []Diagnostic{{"first", ReasonUnsupportedType}}) {
		t.Fatal("alias gained proof or missing unsupported diagnostic")
	}
	if !reflect.DeepEqual(f.LocalTypes[:len(unchanged)], unchanged) || !f.LocalTypes[len(unchanged)].Proven {
		t.Fatal("noninteger changed or unsupported state leaked")
	}
	if p.Functions[1].Decision.Reason != ReasonExternalCall {
		t.Fatal("original unsupported reason changed")
	}
}

func TestReservedIntegerABIMacroSet(t *testing.T) {
	names := []string{"__clang__", "__clang_major__", "__clang_minor__", "__clang_patchlevel__", "__x86_64__", "__CHAR_BIT__", "__SCHAR_MAX__", "__SHRT_WIDTH__", "__SIZEOF_SHORT__", "__SHRT_MAX__", "__INT_WIDTH__", "__SIZEOF_INT__", "__INT_MAX__", "__LONG_WIDTH__", "__SIZEOF_LONG__", "__LONG_MAX__", "__LLONG_WIDTH__", "__SIZEOF_LONG_LONG__", "__LONG_LONG_MAX__", "__SIZEOF_POINTER__", "__POINTER_WIDTH__", "_WIN32", "_WIN64", "__linux__", "__CHAR_UNSIGNED__"}
	for _, name := range names {
		if !reservedIntegerABIMacro(name) {
			t.Fatal("consumed macro not reserved")
		}
	}
	for _, name := range []string{"PROJECT_FEATURE", "_DEBUG", "", " __INT_WIDTH__", "__INT_WIDTH__\t", "__INT_WIDTH__\r", "__INT_WIDTH__\n", "__INT_WIDTH__x", "__clang_version__"} {
		if reservedIntegerABIMacro(name) {
			t.Fatal("unconsumed macro reserved")
		}
	}
}
func integerABITestMacros(platform string, unsignedChar bool) []byte {
	width, size, maximum := 32, 4, "2147483647L"
	identity := "#define _WIN32 1\n#define _WIN64 1\n"
	if platform == "linux-x64" {
		width, size, maximum = 64, 8, "9223372036854775807L"
		identity = "#define __linux__ 1\n"
	}
	output := fmt.Sprintf(`#define __clang__ 1
#define __clang_major__ 22
#define __clang_minor__ 1
#define __clang_patchlevel__ 8
#define __x86_64__ 1
#define __CHAR_BIT__ 8
#define __SCHAR_MAX__ 127
#define __SHRT_WIDTH__ 16
#define __SIZEOF_SHORT__ 2
#define __SHRT_MAX__ 32767
#define __INT_WIDTH__ 32
#define __SIZEOF_INT__ 4
#define __INT_MAX__ 2147483647
#define __LONG_WIDTH__ %d
#define __SIZEOF_LONG__ %d
#define __LONG_MAX__ %s
#define __LLONG_WIDTH__ 64
#define __SIZEOF_LONG_LONG__ 8
#define __LONG_LONG_MAX__ 9223372036854775807LL
#define __SIZEOF_POINTER__ 8
#define __POINTER_WIDTH__ 64
`, width, size, maximum) + identity
	if unsignedChar {
		output += "#define __CHAR_UNSIGNED__ 1\n"
	}
	return []byte(output)
}

// Rejecting valid facts or failing to enrich parsed source integers breaks this test.
func TestIntegerABIFactsWidthsAndSignedness(t *testing.T) {
	for _, platform := range []string{"windows-x64", "linux-x64"} {
		for _, unsignedChar := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", platform, unsignedChar), func(t *testing.T) {
				binding := integerABITestBinding(platform)
				facts, err := decodeIntegerABIFacts(integerABITestMacros(platform, unsignedChar), binding)
				if err != nil {
					t.Fatal("valid compiler facts rejected")
				}
				p := Program{Version: IRVersion, Functions: []Function{{SymbolID: strings.Repeat("a", 64), ReturnType: parseType("int", nil), Parameters: []Parameter{{Name: "x", Type: parseType("char", nil)}}, LocalTypes: []Type{parseType("long", nil), parseType("unsigned int", nil)}, Decision: Decision{Kind: DecisionSupported, Reason: ReasonNone}}}}
				if applyIntegerABIFacts(&p, facts, binding) != nil {
					t.Fatal("proven facts not applied")
				}
				f := p.Functions[0]
				wantLong := 32
				if platform == "linux-x64" {
					wantLong = 64
				}
				if f.ReturnType.BitWidth != 32 || !f.ReturnType.Signed || !f.ReturnType.Proven || f.Parameters[0].Type.BitWidth != 8 || f.Parameters[0].Type.Signed == unsignedChar || f.LocalTypes[0].BitWidth != wantLong || f.LocalTypes[1].Signed {
					t.Fatal("source-derived integer ABI missing or inconsistent")
				}
			})
		}
	}
}
