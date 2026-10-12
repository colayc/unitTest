package artifactstore

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCommitGenerationSourceUsesOwnedArtifactAndVerifiesBytes(t *testing.T) {
	root := t.TempDir()
	s, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	body := []byte("TEST(Classify, Positive) { CHECK_TRUE(1); }\n")
	a, err := s.CommitGenerationSource(context.Background(), id(1), id(2), time.Now().UTC(), body)
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != "test-generation-source" || a.TaskID != id(1) || a.RelativePath != "tasks/"+id(1)+"/"+id(2)+".source" || a.Size != int64(len(body)) {
		t.Fatalf("artifact: %+v", a)
	}
	if err := s.VerifyGenerationSource(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath(root, a), []byte(strings.Repeat("x", len(body))), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyGenerationSource(context.Background(), a); !errors.Is(err, ErrArtifactChanged) {
		t.Fatalf("changed artifact: %v", err)
	}
}

func TestCommitGenerationSourceRejectsUnboundedOrEmptyBytes(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, data := range [][]byte{nil, make([]byte, 4*1024*1024+1)} {
		if _, err := s.CommitGenerationSource(context.Background(), id(1), id(2), time.Now().UTC(), data); !errors.Is(err, ErrInvalidArtifact) {
			t.Fatalf("unbounded source: %v", err)
		}
	}
}

func TestCommitGenerationEvidenceRoundTripsCanonicalBytes(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	body := []byte(`{"version":1,"receipts":["configure","compile","discover","candidate","suite","coverage"]}`)
	artifact, err := s.CommitGenerationEvidence(context.Background(), id(1), id(3), time.Now().UTC(), body)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Kind != "test-generation-evidence" || artifact.RelativePath != "tasks/"+id(1)+"/"+id(3)+".evidence" {
		t.Fatalf("artifact=%+v", artifact)
	}
	read, err := s.ReadGenerationEvidence(context.Background(), artifact)
	if err != nil || string(read) != string(body) {
		t.Fatalf("read=%q err=%v", read, err)
	}
	read[0] ^= 0xff
	again, err := s.ReadGenerationEvidence(context.Background(), artifact)
	if err != nil || string(again) != string(body) {
		t.Fatalf("read alias changed stored bytes: %q %v", again, err)
	}
}
