package taskstore_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/server"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/taskstore"
)

// Only readiness is substituted; all report/run/detail reads use SQLite.
type firstReportDetailReader struct{ *taskstore.Store }

func (b firstReportDetailReader) CoverageDetailsReady() bool { return b.CoverageDetailReady() }

type firstReportCoverageReader struct {
	session.CoverageBackend
	store *taskstore.Store
}

func (b firstReportCoverageReader) GetCoverageReport(ctx context.Context, id string) (coveragedomain.Report, error) {
	return b.store.GetCoverageReport(ctx, id)
}
func (b firstReportCoverageReader) GetCoverageRun(ctx context.Context, id string) (coveragedomain.Run, error) {
	return b.store.GetCoverageRun(ctx, id)
}

type firstReportReadiness struct {
	session.ManagedGenerationBackend
	session.ManagedStartResolverBackend
	session.ManagedStartBackend
	session.ManagedRunReadBackend
}

func (firstReportReadiness) TestGenerationReady() bool { return true }
func (firstReportReadiness) ManagedTestsReady() bool   { return true }

func firstReportExchange(t *testing.T, store *taskstore.Store) func(string, any) json.RawMessage {
	t.Helper()
	client, service := net.Pipe()
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := service.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	readiness := firstReportReadiness{}
	active := session.NewWithManagedDetails("0123456789abcdef", "linux", "test-pipe", nil,
		firstReportCoverageReader{store: store}, readiness, firstReportDetailReader{store}, readiness)
	done := make(chan struct{})
	go func() { defer close(done); server.ServeConnection(service, active) }()
	t.Cleanup(func() {
		_ = client.Close()
		_ = service.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("session connection did not stop")
		}
	})
	encoder, decoder := json.NewEncoder(client), json.NewDecoder(client)
	sequence := 0
	exchange := func(method string, payload any) json.RawMessage {
		t.Helper()
		sequence++
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprintf("%032x", sequence)
		request := protocol.Request{ProtocolVersion: protocol.Version16, Kind: "request", MessageID: id,
			Method: method, SentAt: "2026-10-08T00:00:00Z", Payload: data}
		if err := encoder.Encode(request); err != nil {
			t.Fatal(err)
		}
		var response struct {
			Kind            string              `json:"kind"`
			RequestID       string              `json:"requestId"`
			ProtocolVersion string              `json:"protocolVersion"`
			Payload         json.RawMessage     `json:"payload"`
			Error           *protocol.ErrorBody `json:"error"`
		}
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		if response.Error != nil || response.Kind != "response" || response.RequestID != id || response.ProtocolVersion != protocol.Version16 {
			t.Fatalf("%s: response=%+v error=%+v", method, response, response.Error)
		}
		return response.Payload
	}
	data := exchange("handshake", map[string]any{"token": "0123456789abcdef", "clientName": "first-report-test",
		"clientVersion": "0.1.0", "supportedProtocolVersions": []string{protocol.Version16}})
	var handshake struct {
		NegotiatedProtocolVersion string `json:"negotiatedProtocolVersion"`
	}
	if err := json.Unmarshal(data, &handshake); err != nil {
		t.Fatal(err)
	}
	if handshake.NegotiatedProtocolVersion != protocol.Version16 {
		t.Fatalf("handshake=%s", data)
	}
	return exchange
}

func assertFirstReportSummary(t *testing.T, data json.RawMessage, want coveragedomain.Summary) {
	t.Helper()
	var wire map[string]map[string]json.RawMessage
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	for key, metric := range map[string]coveragedomain.Metric{"functions": want.Functions, "lines": want.Lines, "branches": want.Branches} {
		var covered, total int64
		if err := json.Unmarshal(wire[key]["covered"], &covered); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(wire[key]["total"], &total); err != nil {
			t.Fatal(err)
		}
		if covered != metric.Covered || total != metric.Total || string(wire[key]["coveredDelta"]) != "null" {
			t.Fatalf("%s: wire=%s want covered=%d total=%d delta=null", key, data, metric.Covered, metric.Total)
		}
	}
}

func assertFirstReportPages(t *testing.T, exchange func(string, any) json.RawMessage, index coveragedetail.Index) {
	t.Helper()
	context := map[string]any{"coverageReportId": index.ReportID, "workspaceGeneration": index.WorkspaceGeneration, "projectId": index.ProjectID}
	var project struct {
		Status    string          `json:"status"`
		Reasons   []string        `json:"reasons"`
		Summary   json.RawMessage `json:"summary"`
		FileCount int             `json:"fileCount"`
	}
	if err := json.Unmarshal(exchange("coverage/details/project/get", context), &project); err != nil {
		t.Fatal(err)
	}
	if project.Status != "current" || len(project.Reasons) != 0 || project.FileCount != len(index.Files) {
		t.Fatalf("project=%+v", project)
	}
	assertFirstReportSummary(t, project.Summary, index.Project.Summary)
	var files struct {
		Items []struct {
			FileID        string          `json:"fileId"`
			Summary       json.RawMessage `json:"summary"`
			FunctionCount int             `json:"functionCount"`
		} `json:"items"`
	}
	if err := json.Unmarshal(exchange("coverage/details/files/list", context), &files); err != nil {
		t.Fatal(err)
	}
	if len(files.Items) != len(index.Files) {
		t.Fatalf("files=%+v", files)
	}
	for _, file := range files.Items {
		var stored *coveragedetail.File
		for i := range index.Files {
			if index.Files[i].ID == file.FileID {
				stored = &index.Files[i]
			}
		}
		if stored == nil || file.FunctionCount != len(stored.Functions) {
			t.Fatalf("file=%+v", file)
		}
		assertFirstReportSummary(t, file.Summary, stored.Summary)
		var functions struct {
			Items []struct {
				FunctionID string          `json:"functionId"`
				Summary    json.RawMessage `json:"summary"`
			} `json:"items"`
		}
		if err := json.Unmarshal(exchange("coverage/details/functions/list", map[string]any{"coverageReportId": index.ReportID,
			"workspaceGeneration": index.WorkspaceGeneration, "fileId": file.FileID}), &functions); err != nil {
			t.Fatal(err)
		}
		if len(functions.Items) != len(stored.Functions) {
			t.Fatalf("functions=%+v", functions)
		}
		for _, fn := range functions.Items {
			var match *coveragedetail.Function
			for i := range stored.Functions {
				if stored.Functions[i].ID == fn.FunctionID {
					match = &stored.Functions[i]
				}
			}
			if match == nil {
				t.Fatalf("unknown function=%+v", fn)
			}
			assertFirstReportSummary(t, fn.Summary, match.Summary)
		}
	}
}

func TestCoverageDetailFirstReportThroughSession(t *testing.T) {
	store := taskstore.OpenCoverageDetailTestStore(t)
	index := taskstore.AttestedCoverageDetailTestFixture(t, store, 7401)
	assertFirstReportPages(t, firstReportExchange(t, store), index)
}

func TestCoverageDetailSecondReportHasNoInferredBaseline(t *testing.T) {
	store := taskstore.OpenCoverageDetailTestStore(t)
	first := taskstore.AttestedCoverageDetailTestFixture(t, store, 7401)
	exchange := firstReportExchange(t, store)
	assertFirstReportPages(t, exchange, first)
	second := taskstore.AttestedCoverageDetailTestFixture(t, store, 7402)
	assertFirstReportPages(t, exchange, second)
}
