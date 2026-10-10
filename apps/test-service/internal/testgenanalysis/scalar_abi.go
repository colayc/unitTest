package testgenanalysis

import (
	"bufio"
	"bytes"
	"errors"
	"math/big"
	"regexp"
	"strings"
)

type integerABIBinding struct{ manifest, compile, standard, platform string }

func (b integerABIBinding) valid() bool {
	return validSHA(b.manifest) && validSHA(b.compile) && languageModes[b.standard] && (b.platform == "windows-x64" || b.platform == "linux-x64")
}

type integerABIWidths struct{ char, short, int_, long, longLong int }
type integerABIFacts struct {
	binding    integerABIBinding
	widths     integerABIWidths
	charSigned bool
	verified   bool
}

const maxIntegerABIBytes = 64 << 10

var errIntegerABI = errors.New("unproven integer ABI")

const integerABIMacroNames = " __clang__ __clang_major__ __clang_minor__ __clang_patchlevel__ __x86_64__ __CHAR_BIT__ __SCHAR_MAX__ __SHRT_WIDTH__ __SIZEOF_SHORT__ __SHRT_MAX__ __INT_WIDTH__ __SIZEOF_INT__ __INT_MAX__ __LONG_WIDTH__ __SIZEOF_LONG__ __LONG_MAX__ __LLONG_WIDTH__ __SIZEOF_LONG_LONG__ __LONG_LONG_MAX__ __SIZEOF_POINTER__ __POINTER_WIDTH__ _WIN32 _WIN64 __linux__ __CHAR_UNSIGNED__ "

func reservedIntegerABIMacro(name string) bool {
	return name != "" && !strings.ContainsAny(name, " \t\r\n") && strings.Contains(integerABIMacroNames, " "+name+" ")
}

var integerABILiteral = regexp.MustCompile(`(?i)^(0|[1-9][0-9]*)(U|L|LL|UL|LU|ULL|LLU)?$`)

func integerABINumber(raw string) (*big.Int, error) {
	if len(raw) > 32 {
		return nil, errIntegerABI
	}
	match := integerABILiteral.FindStringSubmatch(raw)
	if match == nil {
		return nil, errIntegerABI
	}
	n, ok := new(big.Int).SetString(match[1], 10)
	if !ok {
		return nil, errIntegerABI
	}
	return n, nil
}

func decodeIntegerABIFacts(output []byte, binding integerABIBinding) (integerABIFacts, error) {
	if !binding.valid() || len(output) == 0 || len(output) > maxIntegerABIBytes || output[len(output)-1] != '\n' || bytes.IndexByte(output, 0) >= 0 {
		return integerABIFacts{}, errIntegerABI
	}
	values := map[string]*big.Int{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 4096), maxIntegerABIBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "#define") || len(line) <= 7 || (line[7] != ' ' && line[7] != '\t') {
			continue
		}
		rest := strings.TrimLeft(line[7:], " \t")
		end := 0
		for end < len(rest) {
			c := rest[end]
			if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || end > 0 && c >= '0' && c <= '9') {
				break
			}
			end++
		}
		name := rest[:end]
		if !reservedIntegerABIMacro(name) {
			continue
		}
		if _, exists := values[name]; exists || end == len(rest) || (rest[end] != ' ' && rest[end] != '\t') {
			return integerABIFacts{}, errIntegerABI
		}
		value, err := integerABINumber(strings.TrimSpace(rest[end:]))
		if err != nil {
			return integerABIFacts{}, errIntegerABI
		}
		values[name] = value
	}
	if scanner.Err() != nil {
		return integerABIFacts{}, errIntegerABI
	}
	equal := func(name string, want int64) bool { n, ok := values[name]; return ok && n.Cmp(big.NewInt(want)) == 0 }
	for name, want := range map[string]int64{"__clang__": 1, "__clang_major__": 22, "__clang_minor__": 1, "__clang_patchlevel__": 8, "__x86_64__": 1, "__CHAR_BIT__": 8, "__SIZEOF_POINTER__": 8, "__POINTER_WIDTH__": 64} {
		if !equal(name, want) {
			return integerABIFacts{}, errIntegerABI
		}
	}
	_, linux := values["__linux__"]
	_, win32 := values["_WIN32"]
	_, win64 := values["_WIN64"]
	if binding.platform == "windows-x64" {
		if !equal("_WIN32", 1) || !equal("_WIN64", 1) || linux {
			return integerABIFacts{}, errIntegerABI
		}
	} else if !equal("__linux__", 1) || win32 || win64 {
		return integerABIFacts{}, errIntegerABI
	}
	bounded := func(name string) (int, bool) {
		n, ok := values[name]
		if !ok || !n.IsInt64() || n.Sign() <= 0 || n.Cmp(big.NewInt(64)) > 0 {
			return 0, false
		}
		return int(n.Int64()), true
	}
	var widths [5]int
	fields := [][3]string{{"__CHAR_BIT__", "", "__SCHAR_MAX__"}, {"__SHRT_WIDTH__", "__SIZEOF_SHORT__", "__SHRT_MAX__"}, {"__INT_WIDTH__", "__SIZEOF_INT__", "__INT_MAX__"}, {"__LONG_WIDTH__", "__SIZEOF_LONG__", "__LONG_MAX__"}, {"__LLONG_WIDTH__", "__SIZEOF_LONG_LONG__", "__LONG_LONG_MAX__"}}
	for i, field := range fields {
		width, ok := bounded(field[0])
		if !ok || (width != 8 && width != 16 && width != 32 && width != 64) || i > 0 && width < widths[i-1] {
			return integerABIFacts{}, errIntegerABI
		}
		widths[i] = width
		if field[1] != "" {
			size, ok := bounded(field[1])
			if !ok || size*widths[0] != width {
				return integerABIFacts{}, errIntegerABI
			}
		}
		actual, ok := values[field[2]]
		maximum := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(width-1)), big.NewInt(1))
		if !ok || actual.Cmp(maximum) != 0 {
			return integerABIFacts{}, errIntegerABI
		}
	}
	_, unsignedChar := values["__CHAR_UNSIGNED__"]
	if unsignedChar && !equal("__CHAR_UNSIGNED__", 1) {
		return integerABIFacts{}, errIntegerABI
	}
	return integerABIFacts{binding: binding, widths: integerABIWidths{widths[0], widths[1], widths[2], widths[3], widths[4]}, charSigned: !unsignedChar, verified: true}, nil
}

func applyIntegerABIFacts(program *Program, facts integerABIFacts, binding integerABIBinding) error {
	if program == nil || !facts.verified || !binding.valid() || facts.binding != binding {
		return errIntegerABI
	}
	enrich := func(typ *Type) bool {
		if typ.Kind != TypeInteger {
			return false
		}
		width, signed, unsupported := 0, false, false
		switch typ.Spelling {
		case "char":
			width, signed = facts.widths.char, facts.charSigned
		case "signed char":
			width, signed = facts.widths.char, true
		case "unsigned char":
			width, signed = facts.widths.char, false
		case "short":
			width, signed = facts.widths.short, true
		case "unsigned short":
			width, signed = facts.widths.short, false
		case "int":
			width, signed = facts.widths.int_, true
		case "unsigned int":
			width, signed = facts.widths.int_, false
		case "long":
			width, signed = facts.widths.long, true
		case "unsigned long":
			width, signed = facts.widths.long, false
		case "long long":
			width, signed = facts.widths.longLong, true
		case "unsigned long long":
			width, signed = facts.widths.longLong, false
		default:
			typ.BitWidth, typ.Signed, typ.Proven = 0, false, false
			unsupported = true
		}
		if !unsupported {
			typ.BitWidth, typ.Signed, typ.Proven = width, signed, true
		}
		return unsupported
	}
	for i := range program.Functions {
		f := &program.Functions[i]
		unsupported := enrich(&f.ReturnType)
		for j := range f.Parameters {
			if enrich(&f.Parameters[j].Type) {
				unsupported = true
			}
		}
		for j := range f.LocalTypes {
			if enrich(&f.LocalTypes[j]) {
				unsupported = true
			}
		}
		if unsupported && f.Decision.Kind == DecisionSupported {
			f.Decision = Decision{DecisionUnsupported, ReasonUnsupportedType}
			program.Diagnostics = append(program.Diagnostics, Diagnostic{f.SymbolID, ReasonUnsupportedType})
		}
	}
	program.Digest = ""
	return nil
}
