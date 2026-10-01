package testgenrender

import (
	"fmt"
	"sort"
	"strings"

	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
	assert "unit-test-ide.local/test-service/internal/testgenassert"
)

func renderUnity(r RenderRequest, fn analysis.Function, cases []derivedCase) (string, error) {
	sort.Slice(cases, func(i, j int) bool { return cases[i].vector.ID < cases[j].vector.ID })
	var b strings.Builder
	fmt.Fprintf(&b, "#include \"unity.h\"\n#include \"%s\"\n#include <stdint.h>\n#include <stdbool.h>\n\nvoid setUp(void) {}\nvoid tearDown(void) {}\n", r.HeaderPath)
	short := map[string]bool{}
	for _, c := range cases {
		name := "test_case_" + c.vector.ID[:16]
		if short[name] {
			return "", ErrInvalidRender
		}
		short[name] = true
		if c.kind == assert.KindCharacterization {
			b.WriteString("\n// characterization: requires separate confirmation\n")
		}
		fmt.Fprintf(&b, "\nvoid %s(void) {\n", name)
		if err := renderBody(&b, fn, c, LanguageC); err != nil {
			return "", err
		}
		b.WriteString("}\n")
	}
	b.WriteString("\nint main(void) {\n  UNITY_BEGIN();\n")
	for _, c := range cases {
		fmt.Fprintf(&b, "  RUN_TEST(test_case_%s);\n", c.vector.ID[:16])
	}
	b.WriteString("  return UNITY_END();\n}\n")
	return b.String(), nil
}
