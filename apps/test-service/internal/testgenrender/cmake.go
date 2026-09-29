package testgenrender

import (
	"fmt"
	"path"
	"strings"
	"unicode"
)

type cmakeCall struct {
	name       string
	start, end int
	args       []string
}

// parseCMakeCalls is a bounded lexical parser for top-level CMake calls. It
// respects quotes, comments and balanced parentheses; unknown commands are
// preserved byte-for-byte. An unbalanced/ambiguous file is rejected.
func parseCMakeCalls(src string) ([]cmakeCall, error) {
	if len(src) > 1<<20 || strings.ContainsRune(src, 0) {
		return nil, ErrInvalidRender
	}
	calls := []cmakeCall{}
	i := 0
	for i < len(src) {
		if unicode.IsSpace(rune(src[i])) {
			i++
			continue
		}
		if src[i] == '#' {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}
		start := i
		for i < len(src) && (src[i] >= 'A' && src[i] <= 'Z' || src[i] >= 'a' && src[i] <= 'z' || src[i] == '_') {
			i++
		}
		if start == i {
			return nil, ErrInvalidRender
		}
		name := strings.ToLower(src[start:i])
		for i < len(src) && unicode.IsSpace(rune(src[i])) {
			i++
		}
		if i == len(src) || src[i] != '(' {
			return nil, ErrInvalidRender
		}
		i++
		bodyStart := i
		depth := 1
		quoted := false
		comment := false
		escaped := false
		for i < len(src) && depth > 0 {
			ch := src[i]
			if comment {
				if ch == '\n' {
					comment = false
				}
				i++
				continue
			}
			if escaped {
				escaped = false
				i++
				continue
			}
			if ch == '\\' && quoted {
				escaped = true
				i++
				continue
			}
			if ch == '"' {
				quoted = !quoted
				i++
				continue
			}
			if !quoted {
				if ch == '#' {
					comment = true
					i++
					continue
				}
				if ch == '(' {
					depth++
				}
				if ch == ')' {
					depth--
				}
			}
			i++
		}
		if depth != 0 || quoted {
			return nil, ErrInvalidRender
		}
		body := src[bodyStart : i-1]
		args, err := cmakeArgs(body)
		if err != nil {
			return nil, err
		}
		calls = append(calls, cmakeCall{name: name, start: start, end: i, args: args})
	}
	return calls, nil
}
func cmakeArgs(body string) ([]string, error) {
	var args []string
	var token strings.Builder
	quoted := false
	escaped := false
	comment := false
	flush := func() {
		if token.Len() > 0 {
			args = append(args, token.String())
			token.Reset()
		}
	}
	for _, ch := range body {
		if comment {
			if ch == '\n' {
				comment = false
			}
			continue
		}
		if escaped {
			token.WriteRune(ch)
			escaped = false
			continue
		}
		if quoted && ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			quoted = !quoted
			continue
		}
		if !quoted && ch == '#' {
			flush()
			comment = true
			continue
		}
		if !quoted && unicode.IsSpace(ch) {
			flush()
			continue
		}
		if ch == '$' || ch == ';' || ch == '[' || ch == ']' {
			return nil, ErrInvalidRender
		}
		token.WriteRune(ch)
	}
	if quoted || escaped {
		return nil, ErrInvalidRender
	}
	flush()
	return args, nil
}
func patchCMake(t TargetMetadata, lang Language, symbolID string) (string, error) {
	base := path.Dir(t.CMakePath)
	if base == "." || !strings.HasPrefix(t.TestPath, base+"/") {
		return "", ErrInvalidRender
	}
	sourceRef := strings.TrimPrefix(t.TestPath, base+"/")
	if !validPath(sourceRef) {
		return "", ErrInvalidRender
	}
	calls, err := parseCMakeCalls(t.ExistingCMake)
	if err != nil {
		return "", err
	}
	var target *cmakeCall
	linked := false
	frameworkLinked := false
	already := false
	for i := range calls {
		c := &calls[i]
		if len(c.args) == 0 {
			continue
		}
		if c.name == "add_executable" && c.args[0] == t.TestTarget {
			if target != nil {
				return "", ErrInvalidRender
			}
			target = c
		}
		if c.name == "target_link_libraries" && c.args[0] == t.TestTarget {
			for _, a := range c.args[1:] {
				if a == t.ProductionTarget {
					linked = true
				}
				if a == t.FrameworkTarget {
					frameworkLinked = true
				}
			}
		}
		if c.name == "target_sources" && c.args[0] == t.TestTarget {
			for _, a := range c.args[1:] {
				if a == sourceRef {
					already = true
				}
			}
		}
	}
	if target == nil || !linked || !frameworkLinked {
		return "", ErrInvalidRender
	}
	lineEnding := "\n"
	if strings.Contains(t.ExistingCMake, "\r\n") {
		lineEnding = "\r\n"
	}
	if lang == LanguageC {
		generated := t.TestTarget + "_generated_" + symbolID[:12]
		foundExe := false
		foundLink := false
		foundTest := false
		for _, c := range calls {
			if len(c.args) == 0 {
				continue
			}
			if c.name == "add_test" && len(c.args) == 4 && c.args[0] == "NAME" && c.args[1] == generated && c.args[2] == "COMMAND" && c.args[3] == generated {
				foundTest = true
				continue
			}
			if c.args[0] != generated {
				continue
			}
			switch c.name {
			case "add_executable":
				foundExe = len(c.args) == 2 && c.args[1] == sourceRef
			case "target_link_libraries":
				foundLink = contains(c.args, t.ProductionTarget) && contains(c.args, t.FrameworkTarget)
			}
		}
		if foundExe || foundLink || foundTest {
			if foundExe && foundLink && foundTest {
				return t.ExistingCMake, nil
			}
			return "", ErrInvalidRender
		}
		insertion := fmt.Sprintf("%sadd_executable(%s \"%s\")%starget_link_libraries(%s PRIVATE %s %s)%sadd_test(NAME %s COMMAND %s)", lineEnding, generated, sourceRef, lineEnding, generated, t.ProductionTarget, t.FrameworkTarget, lineEnding, generated, generated)
		return t.ExistingCMake[:target.end] + insertion + t.ExistingCMake[target.end:], nil
	}
	if already {
		return t.ExistingCMake, nil
	}
	insertion := fmt.Sprintf("%starget_sources(%s PRIVATE \"%s\")", lineEnding, t.TestTarget, sourceRef)
	return t.ExistingCMake[:target.end] + insertion + t.ExistingCMake[target.end:], nil
}
func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
