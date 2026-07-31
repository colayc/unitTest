package cpputest

import (
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	readBufferBytes   = 4 * 1024
	maxEmptyReadCount = 100
)

type ansiState uint8

const (
	ansiText ansiState = iota
	ansiEscape
	ansiCSI
)

type listParser struct {
	limits        Limits
	documentBytes int
	ansi          ansiState
	runeBytes     []byte
	token         []byte
	cases         []CaseIdentity
	seen          map[string]struct{}
}

func ParseList(reader io.Reader, limits Limits) ([]CaseIdentity, error) {
	if !limits.Valid() {
		return nil, ErrInvalidLimits
	}
	if reader == nil {
		return nil, ErrInvalidList
	}
	parser := listParser{
		limits: limits,
		cases:  make([]CaseIdentity, 0, min(limits.MaxCases, 128)),
		seen:   make(map[string]struct{}),
	}
	buffer := make([]byte, readBufferBytes)
	emptyReads := 0
	for {
		read, err := reader.Read(buffer)
		if read < 0 || read > len(buffer) {
			return nil, fmt.Errorf("%w: invalid Reader byte count", ErrInvalidList)
		}
		if read > 0 {
			emptyReads = 0
			if read > limits.MaxDocumentBytes-parser.documentBytes {
				return nil, ErrLimitExceeded
			}
			parser.documentBytes += read
			for _, value := range buffer[:read] {
				if err := parser.writeByte(value); err != nil {
					return nil, err
				}
			}
		} else if err == nil {
			emptyReads++
			if emptyReads >= maxEmptyReadCount {
				return nil, fmt.Errorf("read CppUTest discovery list: %w", io.ErrNoProgress)
			}
		}
		if err != nil {
			if err != io.EOF {
				return nil, fmt.Errorf("read CppUTest discovery list: %w", err)
			}
			break
		}
	}
	if parser.ansi != ansiText || len(parser.runeBytes) != 0 {
		return nil, fmt.Errorf("%w: incomplete ANSI or UTF-8 sequence", ErrInvalidList)
	}
	if err := parser.finishToken(); err != nil {
		return nil, err
	}
	return parser.cases, nil
}

func (parser *listParser) writeByte(value byte) error {
	switch parser.ansi {
	case ansiEscape:
		if value != '[' {
			return fmt.Errorf("%w: unsupported ANSI escape", ErrInvalidList)
		}
		parser.ansi = ansiCSI
		return nil
	case ansiCSI:
		switch {
		case value >= 0x40 && value <= 0x7e:
			parser.ansi = ansiText
			return nil
		case value >= 0x20 && value <= 0x3f:
			return nil
		default:
			return fmt.Errorf("%w: malformed ANSI CSI sequence", ErrInvalidList)
		}
	}

	if value == 0x1b {
		if len(parser.runeBytes) != 0 {
			return fmt.Errorf("%w: ANSI escape splits UTF-8", ErrInvalidList)
		}
		parser.ansi = ansiEscape
		return nil
	}
	return parser.writeUTF8Byte(value)
}

func (parser *listParser) writeUTF8Byte(value byte) error {
	parser.runeBytes = append(parser.runeBytes, value)
	if !utf8.FullRune(parser.runeBytes) {
		if len(parser.runeBytes) >= utf8.UTFMax {
			return fmt.Errorf("%w: malformed UTF-8", ErrInvalidList)
		}
		return nil
	}
	character, size := utf8.DecodeRune(parser.runeBytes)
	if character == utf8.RuneError && size == 1 {
		return fmt.Errorf("%w: malformed UTF-8", ErrInvalidList)
	}
	encoded := parser.runeBytes[:size]
	if unicode.IsSpace(character) {
		if err := parser.finishToken(); err != nil {
			return err
		}
	} else {
		if unicode.IsControl(character) {
			return fmt.Errorf("%w: control character in identity", ErrInvalidList)
		}
		if len(encoded) > parser.limits.MaxTokenBytes-len(parser.token) {
			return ErrLimitExceeded
		}
		parser.token = append(parser.token, encoded...)
	}
	parser.runeBytes = parser.runeBytes[size:]
	return nil
}

func (parser *listParser) finishToken() error {
	if len(parser.token) == 0 {
		return nil
	}
	token := string(parser.token)
	parser.token = parser.token[:0]
	if strings.Count(token, ".") != 1 {
		return fmt.Errorf("%w: identity %q must contain one dot", ErrInvalidList, token)
	}
	group, name, _ := strings.Cut(token, ".")
	if group == "" || name == "" {
		return fmt.Errorf("%w: empty group or case name", ErrInvalidList)
	}
	key := group + "\x00" + name
	if _, duplicate := parser.seen[key]; duplicate {
		return fmt.Errorf("%w: duplicate identity %q", ErrInvalidList, token)
	}
	if len(parser.cases) >= parser.limits.MaxCases {
		return ErrLimitExceeded
	}
	parser.seen[key] = struct{}{}
	parser.cases = append(parser.cases, CaseIdentity{Group: group, Name: name})
	return nil
}
