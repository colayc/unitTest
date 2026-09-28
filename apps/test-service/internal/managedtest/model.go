package managedtest

import "errors"

var ErrInvalidManagedTest = errors.New("invalid managed generated test")

type ConflictChoice string

const (
	UseGenerated    ConflictChoice = "use-generated"
	KeepCurrent     ConflictChoice = "keep-current"
	ConvertToManual ConflictChoice = "convert-to-manual"
)

func ValidConflictChoice(choice ConflictChoice) bool {
	return choice == UseGenerated || choice == KeepCurrent || choice == ConvertToManual
}

// Block offsets span the complete marker-delimited region, including both
// marker lines and their line endings. Body excludes the marker lines.
type Block struct {
	CaseID     string
	FunctionID string
	Symbol     string
	StartByte  int
	EndByte    int
	Body       []byte
	Digest     string
}

// Document owns a copy of the original bytes; unmanaged spans remain exact.
type Document struct {
	Bytes  []byte
	Blocks []Block
}
