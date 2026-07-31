package testdomain

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

func EncodeCatalog(value Catalog) ([]byte, error) {
	validated, err := NewCatalog(value)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(validated)
	if err != nil {
		return nil, invalid(ErrInvalidCatalog, "encoding", "JSON encoding failed")
	}
	return append(encoded, '\n'), nil
}

func DecodeCatalog(encoded []byte) (Catalog, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var value Catalog
	if err := decoder.Decode(&value); err != nil {
		return Catalog{}, invalid(ErrInvalidCatalog, "encoding", "invalid Catalog JSON")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Catalog{}, invalid(ErrInvalidCatalog, "encoding", "multiple JSON values")
	}
	return NewCatalog(value)
}
