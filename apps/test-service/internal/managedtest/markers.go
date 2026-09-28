package managedtest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
)

const maxDocumentBytes = 8 * 1024 * 1024
const maxDocumentBlocks = 4096

const markerToken = "unit-test-ide:managed"
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
		if bytes.Contains(line, []byte(markerToken)) {
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
		offset = lineAfter
	}
	if active != nil {
		return Document{}, ErrInvalidManagedTest
	}
	return doc, nil
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
