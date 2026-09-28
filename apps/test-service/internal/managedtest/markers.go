package managedtest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
)

const maxDocumentBytes = 8 * 1024 * 1024
const maxDocumentBlocks = 4096

const markerToken = "unit-test-ide"
const beginPrefix = "// unit-test-ide:managed-begin case="
const endPrefix = "// unit-test-ide:managed-end case="
const functionPrefix = " function="
const symbolPrefix = " symbol="

// ParseDocument recognizes only complete column-zero marker lines. It never
// interprets C/C++ syntax; marker-looking text in another context is invalid.
func ParseDocument(input []byte, maxBytes int64, maxBlocks int) (Document, error) {
	if maxBytes < 0 || maxBlocks < 1 || int64(len(input)) > maxBytes || len(input) > maxDocumentBytes {
		return Document{}, ErrInvalidManagedTest
	}
	if maxBlocks > maxDocumentBlocks {
		maxBlocks = maxDocumentBlocks
	}
	if bytes.IndexByte(input, 0) >= 0 {
		return Document{}, ErrInvalidManagedTest
	}
	owned := bytes.Clone(input)
	doc := Document{Bytes: owned}
	seen := make(map[string]struct{})
	var active *Block
	activeBodyStart := 0
	newlineKind := byte(0)
	var context lexicalContext
	for offset := 0; offset < len(owned); {
		next := bytes.IndexByte(owned[offset:], '\n')
		lineEnd := len(owned)
		lineAfter := len(owned)
		if next >= 0 {
			lineEnd = offset + next
			lineAfter = lineEnd + 1
		}
		kind := byte(0)
		if lineEnd > offset && owned[lineEnd-1] == '\r' {
			if next < 0 {
				return Document{}, ErrInvalidManagedTest
			}
			lineEnd--
			kind = 2
		} else if next >= 0 {
			kind = 1
		}
		line := owned[offset:lineEnd]
		if bytes.IndexByte(line, '\r') >= 0 || kind != 0 && newlineKind != 0 && kind != newlineKind {
			return Document{}, ErrInvalidManagedTest
		}
		if kind != 0 {
			newlineKind = kind
		}
		if containsMarkerNamespace(line) {
			if context.mode != lexicalNormal {
				return Document{}, ErrInvalidManagedTest
			}
			if kind == 0 {
				return Document{}, ErrInvalidManagedTest
			}
			if caseID, functionID, symbol, ok := parseBegin(line); ok {
				if active != nil || len(doc.Blocks) >= maxBlocks {
					return Document{}, ErrInvalidManagedTest
				}
				if _, duplicate := seen[caseID]; duplicate {
					return Document{}, ErrInvalidManagedTest
				}
				seen[caseID] = struct{}{}
				active = &Block{CaseID: caseID, FunctionID: functionID, Symbol: symbol, StartByte: offset}
				activeBodyStart = lineAfter
			} else if caseID, ok := parseEnd(line); ok {
				if active == nil || active.CaseID != caseID {
					return Document{}, ErrInvalidManagedTest
				}
				active.Body = owned[activeBodyStart:offset]
				active.EndByte = lineAfter
				active.Digest = managedDigest(owned[active.StartByte:active.EndByte])
				doc.Blocks = append(doc.Blocks, *active)
				active = nil
			} else {
				return Document{}, ErrInvalidManagedTest
			}
		}
		if err := context.scanLine(line, kind != 0); err != nil {
			return Document{}, err
		}
		offset = lineAfter
	}
	if active != nil || context.mode != lexicalNormal {
		return Document{}, ErrInvalidManagedTest
	}
	return doc, nil
}

func containsMarkerNamespace(line []byte) bool {
	for i := 0; i+len(markerToken) <= len(line); i++ {
		if line[i] != 'u' && line[i] != 'U' {
			continue
		}
		if bytes.EqualFold(line[i:i+len(markerToken)], []byte(markerToken)) {
			return true
		}
	}
	return false
}

const (
	lexicalNormal byte = iota
	lexicalBlockComment
	lexicalDoubleQuote
	lexicalSingleQuote
	lexicalRawString
	lexicalContinuedLineComment
)

// lexicalContext tracks only the spans that could make a column-zero marker
// line ambiguous. It is deliberately not a C/C++ parser or an AST lexer.
type lexicalContext struct {
	mode   byte
	rawEnd []byte
}

func (c *lexicalContext) scanLine(line []byte, hasNewline bool) error {
	if c.mode == lexicalContinuedLineComment {
		if len(line) > 0 && line[len(line)-1] == '\\' && hasNewline {
			return nil
		}
		c.mode = lexicalNormal
		return nil
	}
	continuedQuote := false
	for i := 0; i < len(line); {
		switch c.mode {
		case lexicalNormal:
			if i+1 < len(line) && line[i] == '/' && line[i+1] == '/' {
				if line[len(line)-1] == '\\' && hasNewline {
					c.mode = lexicalContinuedLineComment
				}
				return nil
			}
			if i+1 < len(line) && line[i] == '/' && line[i+1] == '*' {
				c.mode = lexicalBlockComment
				i += 2
				continue
			}
			if i+1 < len(line) && line[i] == 'R' && line[i+1] == '"' {
				end, ok := rawStringTerminator(line[i+2:])
				if !ok {
					return ErrInvalidManagedTest
				}
				c.mode, c.rawEnd = lexicalRawString, end
				i += 2 + len(end) - 1 // opening R"<delimiter>(
				continue
			}
			if line[i] == '"' {
				c.mode = lexicalDoubleQuote
			} else if line[i] == '\'' && !digitSeparator(line, i) {
				c.mode = lexicalSingleQuote
			}
			i++
		case lexicalBlockComment:
			close := bytes.Index(line[i:], []byte("*/"))
			if close < 0 {
				return nil
			}
			c.mode = lexicalNormal
			i += close + 2
		case lexicalRawString:
			close := bytes.Index(line[i:], c.rawEnd)
			if close < 0 {
				return nil
			}
			endLength := len(c.rawEnd)
			c.mode, c.rawEnd = lexicalNormal, nil
			i += close + endLength
		case lexicalDoubleQuote, lexicalSingleQuote:
			if line[i] == '\\' {
				if i+1 == len(line) {
					continuedQuote = hasNewline
					i++
					continue
				}
				i += 2
				continue
			}
			if c.mode == lexicalDoubleQuote && line[i] == '"' || c.mode == lexicalSingleQuote && line[i] == '\'' {
				c.mode = lexicalNormal
			}
			i++
		}
	}
	if c.mode == lexicalDoubleQuote || c.mode == lexicalSingleQuote {
		if !continuedQuote {
			return ErrInvalidManagedTest
		}
	}
	return nil
}

func digitSeparator(line []byte, index int) bool {
	if index == 0 || index+1 >= len(line) {
		return false
	}
	return hexDigit(line[index-1]) && hexDigit(line[index+1])
}

func hexDigit(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

func rawStringTerminator(afterQuote []byte) ([]byte, bool) {
	for i, b := range afterQuote {
		if b == '(' {
			end := make([]byte, 0, i+2)
			end = append(end, ')')
			end = append(end, afterQuote[:i]...)
			end = append(end, '"')
			return end, true
		}
		if i >= 16 || b < '!' || b > '~' || b == ')' || b == '\\' || b == '"' {
			return nil, false
		}
	}
	return nil, false
}

func parseBegin(line []byte) (string, string, string, bool) {
	if !bytes.HasPrefix(line, []byte(beginPrefix)) {
		return "", "", "", false
	}
	rest := string(line[len(beginPrefix):])
	if len(rest) < 36+len(functionPrefix)+32+len(symbolPrefix)+1 {
		return "", "", "", false
	}
	caseID := rest[:36]
	rest = rest[36:]
	if !validCaseID(caseID) || !bytes.HasPrefix([]byte(rest), []byte(functionPrefix)) {
		return "", "", "", false
	}
	rest = rest[len(functionPrefix):]
	functionID := rest[:32]
	rest = rest[32:]
	if !validLowerHex(functionID, 32) || !bytes.HasPrefix([]byte(rest), []byte(symbolPrefix)) {
		return "", "", "", false
	}
	symbol := rest[len(symbolPrefix):]
	if !validSymbol(symbol) {
		return "", "", "", false
	}
	return caseID, functionID, symbol, true
}

func parseEnd(line []byte) (string, bool) {
	if !bytes.HasPrefix(line, []byte(endPrefix)) {
		return "", false
	}
	caseID := string(line[len(endPrefix):])
	return caseID, validCaseID(caseID)
}

func validCaseID(value string) bool {
	return len(value) == 36 && value[:4] == "utc_" && validLowerHex(value[4:], 32)
}

// The marker stores only a bounded identifier-like display symbol. This
// alphabet needs no escaping and cannot express paths or source fragments.
func validSymbol(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '_' || i > 0 && b >= '0' && b <= '9' {
			continue
		}
		return false
	}
	return true
}

func managedDigest(block []byte) string {
	canonical := bytes.ReplaceAll(block, []byte("\r\n"), []byte("\n"))
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// RenderMarkers wraps a body without changing its bytes. Body lines must use
// the chosen newline and end at a line boundary (or be empty).
func RenderMarkers(caseID, functionID, symbol string, body []byte, newline string) ([]byte, error) {
	if !validCaseID(caseID) || !validLowerHex(functionID, 32) || !validSymbol(symbol) || newline != "\n" && newline != "\r\n" || len(body) > maxDocumentBytes {
		return nil, ErrInvalidManagedTest
	}
	if len(body) != 0 && !bytes.HasSuffix(body, []byte(newline)) {
		return nil, ErrInvalidManagedTest
	}
	begin := beginPrefix + caseID + functionPrefix + functionID + symbolPrefix + symbol + newline
	end := endPrefix + caseID + newline
	if len(begin)+len(body)+len(end) > maxDocumentBytes {
		return nil, ErrInvalidManagedTest
	}
	output := make([]byte, 0, len(begin)+len(body)+len(end))
	output = append(output, begin...)
	output = append(output, body...)
	output = append(output, end...)
	if _, err := ParseDocument(output, int64(len(output)), 1); err != nil {
		return nil, err
	}
	return output, nil
}
