package managedtest

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const maxReviewBytes = 512 * 1024
const maxPreviewBytes = 256 * 1024

type ReconcileInput struct {
	Accepted []Record
	// AcceptedBlocks supplies ancestor bytes for an edited managed block.
	// The registry stores only their digest, so absent or unauthenticated bytes
	// cannot be used to manufacture a three-sided preview.
	AcceptedBlocks map[string][]byte
	Current        Document
	Generated      ManagedFile
}

func Reconcile(in ReconcileInput) (Review, error) {
	if !validTestPath(in.Generated.Path) || len(in.Current.Bytes) > maxReviewBytes || len(in.Generated.Content) > maxReviewBytes || len(in.Accepted) > maxDocumentBlocks || len(in.AcceptedBlocks) > maxDocumentBlocks {
		return Review{}, ErrInvalidManagedTest
	}
	current, err := ParseDocument(in.Current.Bytes, maxReviewBytes, maxDocumentBlocks)
	if err != nil {
		return Review{}, err
	}
	generated, err := ParseDocument(in.Generated.Content, maxReviewBytes, maxDocumentBlocks)
	if err != nil {
		return Review{}, err
	}
	if !utf8.Valid(current.Bytes) || !utf8.Valid(generated.Bytes) {
		return Review{}, ErrInvalidManagedTest
	}
	accepted := make(map[string]Record, len(in.Accepted))
	for _, record := range in.Accepted {
		if !ValidRecord(record) || record.TestRelativePath != in.Generated.Path || record.Status == StatusInvalid {
			return Review{}, ErrInvalidManagedTest
		}
		if _, exists := accepted[record.CaseID]; exists {
			return Review{}, ErrInvalidManagedTest
		}
		accepted[record.CaseID] = record
	}
	cur := make(map[string]Block, len(current.Blocks))
	gen := make(map[string]Block, len(generated.Blocks))
	for _, block := range current.Blocks {
		cur[block.CaseID] = block
	}
	for _, block := range generated.Blocks {
		gen[block.CaseID] = block
	}
	for id := range cur {
		if _, ok := accepted[id]; !ok {
			return Review{}, ErrInvalidManagedTest
		}
	}
	for id, raw := range in.AcceptedBlocks {
		record, ok := accepted[id]
		if !ok || len(raw) > maxPreviewBytes || !utf8.Valid(raw) {
			return Review{}, ErrInvalidManagedTest
		}
		doc, err := ParseDocument(raw, int64(len(raw)), 1)
		if err != nil || len(doc.Blocks) != 1 || doc.Blocks[0].StartByte != 0 || doc.Blocks[0].EndByte != len(raw) || doc.Blocks[0].CaseID != id || doc.Blocks[0].FunctionID != record.FunctionID || doc.Blocks[0].Digest != record.AcceptedBlockDigest {
			return Review{}, ErrInvalidManagedTest
		}
	}
	ids := make([]string, 0, len(accepted)+len(gen))
	for id := range accepted {
		ids = append(ids, id)
	}
	for id := range gen {
		if _, exists := accepted[id]; !exists {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	review := Review{Path: in.Generated.Path, PreimageDigest: byteDigest(current.Bytes), GeneratedDigest: byteDigest(generated.Bytes), Operations: make([]Operation, 0, len(ids))}
	previewBytes := 0
	for _, id := range ids {
		record, wasAccepted := accepted[id]
		old, hasCurrent := cur[id]
		next, hasGenerated := gen[id]
		if hasGenerated && wasAccepted && next.FunctionID != record.FunctionID || hasCurrent && wasAccepted && old.FunctionID != record.FunctionID {
			return Review{}, ErrInvalidManagedTest
		}
		op := Operation{CaseID: id}
		if wasAccepted {
			op.AcceptedDigest = record.AcceptedBlockDigest
		}
		if hasCurrent {
			op.CurrentDigest = old.Digest
		}
		if hasGenerated {
			op.GeneratedDigest = next.Digest
		}
		switch {
		case !wasAccepted && hasGenerated && !hasCurrent:
			op.Kind = OperationAdd
		case wasAccepted && !hasCurrent:
			op.Kind, op.Conflict = OperationOrphan, true
		case wasAccepted && old.Digest != record.AcceptedBlockDigest:
			ancestor, ok := in.AcceptedBlocks[id]
			if !ok {
				return Review{}, ErrInvalidManagedTest
			}
			op.Kind, op.Conflict = OperationConflict, true
			op.Preview.Accepted = string(ancestor)
		case wasAccepted && !hasGenerated:
			op.Kind, op.Conflict = OperationOrphan, true
		case wasAccepted && old.Digest == next.Digest && bytes.Equal(current.Bytes[old.StartByte:old.EndByte], generated.Bytes[next.StartByte:next.EndByte]):
			op.Kind = OperationUnchanged
		case wasAccepted:
			op.Kind = OperationUpdate
		default:
			return Review{}, ErrInvalidManagedTest
		}
		if op.Kind != OperationUnchanged {
			if hasCurrent {
				op.Preview.Current = string(current.Bytes[old.StartByte:old.EndByte])
			}
			if hasGenerated {
				op.Preview.Generated = string(generated.Bytes[next.StartByte:next.EndByte])
			}
			previewBytes += len(op.Preview.Accepted) + len(op.Preview.Current) + len(op.Preview.Generated)
			if previewBytes > maxPreviewBytes {
				return Review{}, ErrInvalidManagedTest
			}
		}
		review.Operations = append(review.Operations, op)
		if op.Kind != OperationUnchanged {
			oldStart, oldCount, newStart, newCount := 0, 0, 0, 0
			if hasCurrent {
				oldStart = 1 + strings.Count(string(current.Bytes[:old.StartByte]), "\n")
				oldCount = strings.Count(op.Preview.Current, "\n")
			}
			if hasGenerated {
				newStart = 1 + strings.Count(string(generated.Bytes[:next.StartByte]), "\n")
				newCount = strings.Count(op.Preview.Generated, "\n")
			}
			fragment := fmt.Sprintf("--- current/%s\n+++ generated/%s\n@@ -%d,%d +%d,%d @@ case=%s\n", review.Path, review.Path, oldStart, oldCount, newStart, newCount, id)
			if hasCurrent {
				fragment += prefixedLines('-', op.Preview.Current)
			}
			if hasGenerated {
				fragment += prefixedLines('+', op.Preview.Generated)
			}
			if previewBytes+len(review.Preview.Unified)+len(fragment) > maxPreviewBytes {
				return Review{}, ErrInvalidManagedTest
			}
			review.Preview.Unified += fragment
		}
	}
	common := map[string]bool{}
	for id := range cur {
		_, exists := gen[id]
		if exists {
			common[id] = true
		}
	}
	currentLayout, currentPreview := scaffoldFrame(current, common)
	generatedLayout, generatedPreview := scaffoldFrame(generated, common)
	if !bytes.Equal(currentLayout, generatedLayout) {
		scaffold := &ScaffoldOperation{Kind: OperationConflict, Conflict: true, CurrentDigest: byteDigest(currentLayout), GeneratedDigest: byteDigest(generatedLayout), Preview: CasePreview{Current: currentPreview, Generated: generatedPreview}}
		if len(current.Bytes) == 0 {
			scaffold.Kind, scaffold.Conflict = OperationAdd, false
		}
		previewBytes += len(currentPreview) + len(generatedPreview)
		fragment := fmt.Sprintf("--- current/%s (scaffold)\n+++ generated/%s (scaffold)\n@@ scaffold @@\n", review.Path, review.Path) + prefixedLines('-', currentPreview) + prefixedLines('+', generatedPreview)
		if previewBytes+len(review.Preview.Unified)+len(fragment) > maxPreviewBytes {
			return Review{}, ErrInvalidManagedTest
		}
		review.Scaffold = scaffold
		review.Preview.Unified += fragment
	}
	if len(review.Operations) == 0 && review.Scaffold == nil {
		return Review{}, ErrInvalidManagedTest
	}
	if review.Scaffold == nil && len(review.Operations) > 0 && bytes.Equal(current.Bytes, generated.Bytes) == false {
		allUnchanged := true
		for _, op := range review.Operations {
			if op.Kind != OperationUnchanged {
				allUnchanged = false
				break
			}
		}
		if allUnchanged {
			return Review{}, ErrInvalidManagedTest
		}
	}
	encoded, err := json.Marshal(review)
	if err != nil || len(encoded) > maxReviewBytes {
		return Review{}, ErrInvalidManagedTest
	}
	return review, nil
}

func byteDigest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func prefixedLines(prefix byte, raw string) string {
	if raw == "" {
		return ""
	}
	var out strings.Builder
	for _, line := range strings.SplitAfter(raw, "\n") {
		if line == "" {
			continue
		}
		out.WriteByte(prefix)
		out.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// scaffoldFrame removes non-common managed blocks, then length-prefixes each
// unmanaged span and common case ID. This detects text moved across block
// boundaries or reordered blocks without treating a new case as hand editing.
func scaffoldFrame(doc Document, common map[string]bool) ([]byte, string) {
	var frame bytes.Buffer
	var preview strings.Builder
	pending := make([]byte, 0)
	cursor := 0
	writeSpan := func(span []byte) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(span)))
		frame.Write(length[:])
		frame.Write(span)
		preview.Write(span)
	}
	for _, block := range doc.Blocks {
		pending = append(pending, doc.Bytes[cursor:block.StartByte]...)
		if common[block.CaseID] {
			writeSpan(pending)
			pending = pending[:0]
			frame.WriteString(block.CaseID)
			fmt.Fprintf(&preview, "\n<managed-case:%s>\n", block.CaseID)
		}
		cursor = block.EndByte
	}
	pending = append(pending, doc.Bytes[cursor:]...)
	writeSpan(pending)
	return frame.Bytes(), preview.String()
}
