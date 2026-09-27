package testgenrender

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
	assert "unit-test-ide.local/test-service/internal/testgenassert"
	solver "unit-test-ide.local/test-service/internal/testgensolver"
)

func renderCppUTest(r RenderRequest, fn analysis.Function, cases []derivedCase) (string, error) {
	sort.Slice(cases, func(i, j int) bool { return cases[i].vector.ID < cases[j].vector.ID })
	var b strings.Builder
	fmt.Fprintf(&b, "#include \"CppUTest/TestHarness.h\"\n#include \"%s\"\n#include <stdint.h>\n\nTEST_GROUP(Generated_%s) {};\n", r.HeaderPath, fn.Name)
	short := map[string]bool{}
	for _, c := range cases {
		name := "case_" + c.vector.ID[:16]
		if short[name] {
			return "", ErrInvalidRender
		}
		short[name] = true
		if c.kind == assert.KindCharacterization {
			b.WriteString("\n// characterization: requires separate confirmation\n")
		}
		fmt.Fprintf(&b, "\nTEST(Generated_%s, %s) {\n", fn.Name, name)
		if err := renderBody(&b, fn, c, LanguageCPP); err != nil {
			return "", err
		}
		b.WriteString("}\n")
	}
	return b.String(), nil
}

func renderBody(b *strings.Builder, fn analysis.Function, c derivedCase, lang Language) error {
	args := make([]string, len(c.vector.Inputs))
	locals := []string{}
	for i, input := range c.vector.Inputs {
		t := fn.Parameters[i].Type
		value, err := literal(input.Value, t, lang)
		if err != nil {
			return err
		}
		if t.Kind == analysis.TypeArray || t.Kind == analysis.TypeRecord {
			decl, err := declaration(input.Name, t, value, lang)
			if err != nil {
				return err
			}
			locals = append(locals, decl)
			args[i] = input.Name
		} else {
			args[i] = value
		}
	}
	for _, line := range locals {
		fmt.Fprintf(b, "  %s\n", line)
	}
	call := fmt.Sprintf("%s(%s)", fn.Name, strings.Join(args, ", "))
	needsReturn := false
	for _, a := range c.assertions {
		if a.Target == assert.TargetReturn {
			needsReturn = true
		}
	}
	if needsReturn {
		if fn.ReturnType.Kind == analysis.TypeVoid {
			return ErrInvalidRender
		}
		if lang == LanguageCPP {
			fmt.Fprintf(b, "  const auto actual = %s;\n", call)
		} else {
			typ, err := typeName(fn.ReturnType, lang)
			if err != nil {
				return err
			}
			fmt.Fprintf(b, "  const %s actual = %s;\n", typ, call)
		}
	} else {
		fmt.Fprintf(b, "  %s;\n", call)
	}
	for _, a := range c.assertions {
		var typ analysis.Type
		expr := "actual"
		if a.Target == assert.TargetReturn {
			typ = fn.ReturnType
		} else if a.Target == assert.TargetOutput {
			found := false
			for _, p := range fn.Parameters {
				if p.Name == a.TargetName {
					typ = p.Type
					found = true
					break
				}
			}
			if !found {
				return ErrInvalidRender
			}
			expr = a.TargetName
		} else {
			return ErrInvalidRender
		}
		if err := renderComparison(b, expr, a.Expected, typ, a.Rule, a.Tolerance, lang, 0); err != nil {
			return err
		}
	}
	return nil
}

func renderComparison(b *strings.Builder, expr string, v solver.Value, t analysis.Type, rule assert.ComparisonRule, tol string, lang Language, depth int) error {
	if depth > 4 {
		return ErrInvalidRender
	}
	if t.Kind == analysis.TypeArray {
		if t.Element == nil || len(v.Elements) != t.Bound {
			return ErrInvalidRender
		}
		for i, element := range v.Elements {
			if err := renderComparison(b, fmt.Sprintf("%s[%d]", expr, i), element, *t.Element, rule, tol, lang, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if t.Kind == analysis.TypeRecord {
		if len(v.Fields) != len(t.Fields) {
			return ErrInvalidRender
		}
		for _, field := range v.Fields {
			found := false
			for _, decl := range t.Fields {
				if field.Name == decl.Name {
					if err := renderComparison(b, expr+"."+field.Name, field.Value, decl.Type, rule, tol, lang, depth+1); err != nil {
						return err
					}
					found = true
					break
				}
			}
			if !found {
				return ErrInvalidRender
			}
		}
		return nil
	}
	value, err := literal(v, t, lang)
	if err != nil {
		return err
	}
	if lang == LanguageCPP {
		switch t.Kind {
		case analysis.TypeBoolean:
			if v.Boolean {
				fmt.Fprintf(b, "  CHECK_TRUE(%s);\n", expr)
			} else {
				fmt.Fprintf(b, "  CHECK_FALSE(%s);\n", expr)
			}
		case analysis.TypeInteger, analysis.TypeEnum:
			fmt.Fprintf(b, "  CHECK_EQUAL(%s, %s);\n", value, expr)
		case analysis.TypeFloating:
			fmt.Fprintf(b, "  DOUBLES_EQUAL(%s, %s, %s);\n", value, expr, tol)
		case analysis.TypeString:
			fmt.Fprintf(b, "  STRCMP_EQUAL(%s, %s);\n", value, expr)
		default:
			return ErrInvalidRender
		}
		return nil
	}
	switch t.Kind {
	case analysis.TypeBoolean:
		if v.Boolean {
			fmt.Fprintf(b, "  TEST_ASSERT_TRUE(%s);\n", expr)
		} else {
			fmt.Fprintf(b, "  TEST_ASSERT_FALSE(%s);\n", expr)
		}
	case analysis.TypeInteger:
		prefix := "INT"
		if !t.Signed {
			prefix = "UINT"
		}
		fmt.Fprintf(b, "  TEST_ASSERT_EQUAL_%s%d(%s, %s);\n", prefix, t.BitWidth, value, expr)
	case analysis.TypeEnum:
		fmt.Fprintf(b, "  TEST_ASSERT_EQUAL_INT(%s, %s);\n", value, expr)
	case analysis.TypeFloating:
		macro := "DOUBLE"
		if t.BitWidth == 32 {
			macro = "FLOAT"
		}
		fmt.Fprintf(b, "  TEST_ASSERT_%s_WITHIN(%s, %s, %s);\n", macro, tol, value, expr)
	case analysis.TypeString:
		fmt.Fprintf(b, "  TEST_ASSERT_EQUAL_STRING(%s, %s);\n", value, expr)
	default:
		return ErrInvalidRender
	}
	return nil
}

func literal(v solver.Value, t analysis.Type, lang Language) (string, error) {
	if v.Kind != t.Kind {
		return "", ErrInvalidRender
	}
	switch t.Kind {
	case analysis.TypeBoolean:
		if v.Boolean {
			return "true", nil
		}
		return "false", nil
	case analysis.TypeInteger:
		if t.BitWidth != 8 && t.BitWidth != 16 && t.BitWidth != 32 && t.BitWidth != 64 {
			return "", ErrInvalidRender
		}
		if _, err := strconv.ParseInt(v.Integer, 10, 64); err != nil {
			if !t.Signed {
				if _, err = strconv.ParseUint(v.Integer, 10, 64); err != nil {
					return "", ErrInvalidRender
				}
			} else {
				return "", ErrInvalidRender
			}
		}
		return v.Integer, nil
	case analysis.TypeFloating:
		n, err := strconv.ParseFloat(v.Float, t.BitWidth)
		if err != nil || !finite(n) {
			return "", ErrInvalidRender
		}
		if !strings.ContainsAny(v.Float, ".eE") {
			return v.Float + ".0", nil
		}
		return v.Float, nil
	case analysis.TypeEnum:
		if !identifierPattern.MatchString(v.Enum) {
			return "", ErrInvalidRender
		}
		ok := false
		for _, member := range t.EnumValues {
			ok = ok || member == v.Enum
		}
		if !ok {
			return "", ErrInvalidRender
		}
		return v.Enum, nil
	case analysis.TypeString:
		if t.MaxLength <= 0 || len(v.String) > t.MaxLength || strings.ContainsAny(v.String, "\x00") || strings.HasPrefix(v.String, "/") || strings.Contains(v.String, ":\\") {
			return "", ErrInvalidRender
		}
		return cStringLiteral(v.String), nil
	case analysis.TypeArray:
		if t.Element == nil || t.Bound < 1 || len(v.Elements) != t.Bound {
			return "", ErrInvalidRender
		}
		parts := make([]string, len(v.Elements))
		for i, e := range v.Elements {
			x, err := literal(e, *t.Element, lang)
			if err != nil {
				return "", err
			}
			parts[i] = x
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	case analysis.TypeRecord:
		if !t.Proven || len(v.Fields) != len(t.Fields) {
			return "", ErrInvalidRender
		}
		parts := make([]string, len(t.Fields))
		for i, f := range t.Fields {
			if f.Name != v.Fields[i].Name {
				return "", ErrInvalidRender
			}
			x, err := literal(v.Fields[i].Value, f.Type, lang)
			if err != nil {
				return "", err
			}
			parts[i] = x
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	default:
		return "", ErrInvalidRender
	}
}

// C hex escapes consume an unbounded run of following hex digits, unlike Go
// quoted strings. Fixed-width octal escapes preserve each UTF-8 byte exactly.
func cStringLiteral(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c == '"':
			b.WriteString("\\\"")
		case c == '\\':
			b.WriteString("\\\\")
		case c >= 0x20 && c <= 0x7e:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "\\%03o", c)
		}
	}
	b.WriteByte('"')
	return b.String()
}
func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func typeName(t analysis.Type, lang Language) (string, error) {
	switch t.Kind {
	case analysis.TypeBoolean:
		if lang == LanguageC {
			return "bool", nil
		}
		return "bool", nil
	case analysis.TypeInteger:
		if t.BitWidth != 8 && t.BitWidth != 16 && t.BitWidth != 32 && t.BitWidth != 64 {
			return "", ErrInvalidRender
		}
		prefix := "int"
		if !t.Signed {
			prefix = "uint"
		}
		return fmt.Sprintf("%s%d_t", prefix, t.BitWidth), nil
	case analysis.TypeFloating:
		if t.BitWidth == 32 {
			return "float", nil
		}
		if t.BitWidth == 64 {
			return "double", nil
		}
	case analysis.TypeEnum, analysis.TypeRecord:
		if identifierPattern.MatchString(t.Spelling) {
			return t.Spelling, nil
		}
	case analysis.TypeString:
		return "const char *", nil
	}
	return "", ErrInvalidRender
}
func declaration(name string, t analysis.Type, value string, lang Language) (string, error) {
	if !identifierPattern.MatchString(name) {
		return "", ErrInvalidRender
	}
	if t.Kind == analysis.TypeArray {
		if t.Element == nil {
			return "", ErrInvalidRender
		}
		element, err := typeName(*t.Element, lang)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s[%d] = %s;", element, name, t.Bound, value), nil
	}
	typ, err := typeName(t, lang)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s %s = %s;", typ, name, value), nil
}
