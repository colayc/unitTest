package testrun

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/task"
)

func TestDecodePersistedRequestsRejectsUnsafeTimeouts(t *testing.T) {
	t.Parallel()
	for _, timeout := range []string{
		"0",
		"-1",
		"86400001",
		"9223372036854775807",
	} {
		timeout := timeout
		t.Run(timeout, func(t *testing.T) {
			discovery := json.RawMessage(
				`{"projectId":"core","buildProfileId":"` +
					strings.Repeat("a", 64) +
					`","targetIds":[],"jobs":1,"timeoutMs":` +
					timeout + `}`,
			)
			if _, err := decodeDiscoveryRequest(discovery); !errors.Is(
				err,
				task.ErrInvalidArgument,
			) {
				t.Fatalf("decodeDiscoveryRequest() error = %v", err)
			}
			run := json.RawMessage(
				`{"projectId":"core","buildProfileId":"` +
					strings.Repeat("a", 64) +
					`","catalogRevision":"` +
					strings.Repeat("b", 64) +
					`","targetIds":[],"jobs":1,"timeoutMs":` +
					timeout +
					`,"repeatCount":1,"maxConcurrency":1,` +
					`"selection":{"mode":"all"}}`,
			)
			if _, err := decodeRunRequest(run); !errors.Is(
				err,
				task.ErrInvalidArgument,
			) {
				t.Fatalf("decodeRunRequest() error = %v", err)
			}
		})
	}
}
