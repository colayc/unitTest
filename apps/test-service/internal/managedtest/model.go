package managedtest

import "errors"

var ErrInvalidManagedTest = errors.New("invalid managed generated test")

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
