package cpputest

import (
	"bytes"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseListAcceptsGoldenGrammar(t *testing.T) {
	tests := map[string]struct {
		fixture string
		want    []CaseIdentity
	}{
		"LF and Unicode": {
			fixture: "list.valid.txt",
			want: []CaseIdentity{
				{Group: "Core", Name: "passes"},
				{Group: "Core", Name: "fails"},
				{Group: "Networking", Name: "connects"},
				{Group: "Unicode_组", Name: "案例_一"},
			},
		},
		"CRLF and tabs": {
			fixture: "list-crlf.valid.txt",
			want: []CaseIdentity{
				{Group: "Tabbed", Name: "first"},
				{Group: "Tabbed", Name: "second"},
				{Group: "Another", Name: "third"},
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile("testdata/" + test.fixture)
			if err != nil {
				t.Fatal(err)
			}
			if test.fixture == "list-crlf.valid.txt" &&
				!bytes.Contains(data, []byte("\r\n")) {
				t.Fatal("CRLF fixture does not contain CRLF line endings")
			}
			got, err := ParseList(bytes.NewReader(data), DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ParseList() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestParseListHandlesANSIAndEveryChunkBoundary(t *testing.T) {
	input := "\x1b[32mUnicode_组.案例_一\x1b[0m\tCore.passes"
	want := []CaseIdentity{
		{Group: "Unicode_组", Name: "案例_一"},
		{Group: "Core", Name: "passes"},
	}
	for width := 1; width <= len(input); width++ {
		got, err := ParseList(
			&fixedChunkReader{data: []byte(input), width: width},
			DefaultLimits(),
		)
		if err != nil {
			t.Fatalf("chunk width %d: %v", width, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("chunk width %d: got %#v, want %#v", width, got, want)
		}
	}
}

func TestParseListAcceptsEmptyAndFinalUnterminatedToken(t *testing.T) {
	for name, input := range map[string]string{
		"empty":            "",
		"whitespace":       " \t\r\n",
		"ANSI only":        "\x1b[33m\x1b[0m",
		"unterminated EOF": "Core.passes",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseList(strings.NewReader(input), DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			if input == "Core.passes" {
				want := []CaseIdentity{{Group: "Core", Name: "passes"}}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("ParseList() = %#v, want %#v", got, want)
				}
				return
			}
			if len(got) != 0 {
				t.Fatalf("ParseList() = %#v, want empty", got)
			}
		})
	}
}

func TestParseListRejectsMalformedOrDuplicateIdentity(t *testing.T) {
	fixtures := []string{
		"list-duplicate.invalid.txt",
		"list-malformed.invalid.txt",
	}
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			file, err := os.Open("testdata/" + fixture)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			cases, err := ParseList(file, DefaultLimits())
			if !errors.Is(err, ErrInvalidList) {
				t.Fatalf("ParseList() error = %v, want ErrInvalidList", err)
			}
			if cases != nil {
				t.Fatalf("ParseList() cases = %#v, want nil", cases)
			}
		})
	}

	tests := map[string][]byte{
		"missing dot":       []byte("Core"),
		"empty group":       []byte(".passes"),
		"empty name":        []byte("Core."),
		"ambiguous dots":    []byte("Core.group.passes"),
		"NUL":               []byte("Core.\x00passes"),
		"invalid UTF-8":     {'C', 'o', 'r', 'e', '.', 0xff},
		"incomplete UTF-8":  {'C', 'o', 'r', 'e', '.', 0xe4, 0xb8},
		"incomplete ANSI":   []byte("Core.passes\x1b[31"),
		"non-CSI escape":    []byte("Core.passes\x1b]title"),
		"control character": []byte("Core.pass\x07es"),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			cases, err := ParseList(bytes.NewReader(input), DefaultLimits())
			if !errors.Is(err, ErrInvalidList) {
				t.Fatalf("ParseList() error = %v, want ErrInvalidList", err)
			}
			if cases != nil {
				t.Fatalf("ParseList() cases = %#v, want nil", cases)
			}
		})
	}

	if cases, err := ParseList(nil, DefaultLimits()); !errors.Is(err, ErrInvalidList) ||
		cases != nil {
		t.Fatalf("ParseList(nil) = %#v, %v", cases, err)
	}
}

func TestParseListEnforcesDocumentTokenAndCaseLimits(t *testing.T) {
	input := "Core.passes Other.works"
	tests := map[string]Limits{
		"document": {
			MaxDocumentBytes: len(input) - 1,
			MaxTokenBytes:    len(input),
			MaxCases:         2,
		},
		"token": {
			MaxDocumentBytes: len(input),
			MaxTokenBytes:    len("Core.passes") - 1,
			MaxCases:         2,
		},
		"cases": {
			MaxDocumentBytes: len(input),
			MaxTokenBytes:    len(input),
			MaxCases:         1,
		},
	}
	for name, limits := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseList(strings.NewReader(input), limits); !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("ParseList() error = %v, want ErrLimitExceeded", err)
			}
		})
	}

	invalid := DefaultLimits()
	invalid.MaxTokenBytes = -1
	if _, err := ParseList(strings.NewReader(""), invalid); !errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("ParseList() error = %v, want ErrInvalidLimits", err)
	}
}

func TestParseListPreservesReaderFailureWithoutPartialCases(t *testing.T) {
	readFailure := errors.New("fixture read failed")
	reader := &errorReader{
		data: []byte("Core.passes Other.works"),
		err:  readFailure,
	}
	cases, err := ParseList(reader, DefaultLimits())
	if !errors.Is(err, readFailure) {
		t.Fatalf("ParseList() error = %v, want reader error", err)
	}
	if cases != nil {
		t.Fatalf("ParseList() cases = %#v, want nil", cases)
	}
}

func FuzzParseList(f *testing.F) {
	for _, seed := range [][]byte{
		{},
		[]byte("Core.passes"),
		[]byte("\x1b[32mUnicode_组.案例_一\x1b[0m\r\n"),
		[]byte("Core.passes Core.passes"),
		[]byte("missing-dot"),
		{'G', '.', 0xe4, 0xb8},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		limits := DefaultLimits()
		limits.MaxDocumentBytes = 4 * 1024
		limits.MaxTokenBytes = 512
		limits.MaxCases = 64
		if len(input) > limits.MaxDocumentBytes {
			input = input[:limits.MaxDocumentBytes+1]
		}
		cases, err := ParseList(&fixedChunkReader{
			data:  append([]byte(nil), input...),
			width: 1,
		}, limits)
		if err != nil {
			if cases != nil {
				t.Fatalf("failed parse returned partial cases: %#v", cases)
			}
			return
		}
		seen := make(map[CaseIdentity]struct{}, len(cases))
		for _, identity := range cases {
			if identity.Group == "" || identity.Name == "" {
				t.Fatalf("empty successful identity: %#v", identity)
			}
			if _, duplicate := seen[identity]; duplicate {
				t.Fatalf("duplicate successful identity: %#v", identity)
			}
			seen[identity] = struct{}{}
		}
	})
}

type fixedChunkReader struct {
	data  []byte
	width int
}

func (reader *fixedChunkReader) Read(destination []byte) (int, error) {
	if len(reader.data) == 0 {
		return 0, io.EOF
	}
	width := min(reader.width, len(reader.data), len(destination))
	copy(destination, reader.data[:width])
	reader.data = reader.data[width:]
	return width, nil
}

type errorReader struct {
	data []byte
	err  error
}

func (reader *errorReader) Read(destination []byte) (int, error) {
	if len(reader.data) == 0 {
		return 0, reader.err
	}
	copy(destination, reader.data)
	read := len(reader.data)
	reader.data = nil
	return read, nil
}
