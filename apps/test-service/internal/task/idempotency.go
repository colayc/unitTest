package task

import (
	"bytes"
	"encoding/json"
	"math/big"
	"sort"
)

// EquivalentIdempotencyRequest compares the persisted identity of two task
// requests. Runtime execution state and derived hashes are intentionally not
// part of that identity.
func EquivalentIdempotencyRequest(first, second Task) bool {
	if first.Kind != second.Kind ||
		first.WorkspaceGeneration != second.WorkspaceGeneration ||
		first.Timeout != second.Timeout {
		return false
	}
	firstRequest, firstOK := canonicalRequestJSON(first.Request)
	secondRequest, secondOK := canonicalRequestJSON(second.Request)
	return firstOK && secondOK && bytes.Equal(firstRequest, secondRequest)
}

func canonicalRequestJSON(raw json.RawMessage) ([]byte, bool) {
	if !json.Valid(raw) {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	var result bytes.Buffer
	if !writeCanonicalJSON(&result, value) {
		return nil, false
	}
	return result.Bytes(), true
}

func writeCanonicalJSON(destination *bytes.Buffer, value any) bool {
	switch typed := value.(type) {
	case nil:
		destination.WriteByte('n')
	case bool:
		if typed {
			destination.WriteByte('t')
		} else {
			destination.WriteByte('f')
		}
	case string:
		destination.WriteByte('s')
		encoded, _ := json.Marshal(typed)
		destination.Write(encoded)
	case json.Number:
		number, ok := new(big.Rat).SetString(typed.String())
		if !ok {
			return false
		}
		destination.WriteByte('d')
		destination.WriteString(number.RatString())
	case []any:
		destination.WriteByte('[')
		for _, element := range typed {
			if !writeCanonicalJSON(destination, element) {
				return false
			}
			destination.WriteByte(',')
		}
		destination.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		destination.WriteByte('{')
		for _, key := range keys {
			encoded, _ := json.Marshal(key)
			destination.Write(encoded)
			destination.WriteByte(':')
			if !writeCanonicalJSON(destination, typed[key]) {
				return false
			}
			destination.WriteByte(',')
		}
		destination.WriteByte('}')
	default:
		return false
	}
	return true
}
