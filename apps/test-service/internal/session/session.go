package session

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"sync"
	"time"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/artifactstore"
	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/diagnostic"
	"unit-test-ide.local/test-service/internal/discovery"
	"unit-test-ide.local/test-service/internal/eventbroker"
	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/protocolmodel"
	artifactv12 "unit-test-ide.local/test-service/internal/protocolmodel/v1_2/artifact"
	capabilitiesv12 "unit-test-ide.local/test-service/internal/protocolmodel/v1_2/capabilities"
	targetlistv12 "unit-test-ide.local/test-service/internal/protocolmodel/v1_2/targetlist"
	taskv12 "unit-test-ide.local/test-service/internal/protocolmodel/v1_2/task"
	workspacev12 "unit-test-ide.local/test-service/internal/protocolmodel/v1_2/workspace"
	capabilitiesv13 "unit-test-ide.local/test-service/internal/protocolmodel/v1_3/capabilities"
	taskv13 "unit-test-ide.local/test-service/internal/protocolmodel/v1_3/task"
	testv13 "unit-test-ide.local/test-service/internal/protocolmodel/v1_3/test"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/toolchain"
)

const (
	defaultPageLimit = 100
	maxPageLimit     = 200
	maxTestPageLimit = 1000
	maxTimeoutMS     = int64((24 * time.Hour) / time.Millisecond)
)

type ArtifactChunk struct {
	Data       []byte
	NextOffset int64
	EOF        bool
	Metadata   task.Artifact
}

type Backend interface {
	StartSimulation(context.Context, task.SimulationStart) (task.Task, error)
	InspectWorkspace(context.Context) (discovery.Snapshot, error)
	ListTargets(context.Context, build.TargetsRequest) ([]cmake.Target, error)
	StartBuild(context.Context, build.StartRequest) (task.Task, error)
	Get(context.Context, string) (task.Task, error)
	List(context.Context, string, int, []task.Kind) (task.Page[task.Task], error)
	Cancel(context.Context, string) (task.Task, error)
	Subscribe(context.Context, int64) (*eventbroker.Subscription, error)
	ListArtifacts(context.Context, string, string, int) (task.Page[task.Artifact], error)
	ReadArtifact(context.Context, string, int64, int) (ArtifactChunk, error)
}

type TestBackend interface {
	StartTestDiscovery(
		context.Context,
		TestDiscoveryStart,
	) (task.Task, error)
	StartTestRun(
		context.Context,
		TestRunStart,
	) (task.Task, testdomain.TestRun, error)
	GetTestCatalog(
		context.Context,
		testdomain.CatalogPageRequest,
	) (testdomain.CatalogPage, error)
	GetTestRun(
		context.Context,
		string,
	) (testdomain.TestRun, error)
	GetTestRunForTask(
		context.Context,
		string,
	) (testdomain.TestRun, error)
	ListTestRuns(
		context.Context,
		testdomain.RunPageRequest,
	) (testdomain.RunPage, error)
}

type TestDiscoveryStart struct {
	IdempotencyKey string
	ProjectID      string
	ProfileID      string
}

type TestRunStart struct {
	IdempotencyKey  string
	ProjectID       string
	ProfileID       string
	CatalogRevision string
	Selection       testdomain.Selection
	RepeatCount     int64
}

type HandleResult struct {
	protocol.Response
	Subscription *eventbroker.Subscription
}

type Session struct {
	token, platform, transport string
	mu                         sync.Mutex
	authenticated              bool
	negotiatedVersion          string
	backend                    Backend
	shutdown                   chan struct{}
	shutdownOnce               sync.Once
}

type handshake struct {
	Token                     string   `json:"token"`
	ClientName                string   `json:"clientName"`
	ClientVersion             string   `json:"clientVersion"`
	SupportedProtocolVersions []string `json:"supportedProtocolVersions,omitempty"`
}

type startPayload struct {
	IdempotencyKey string        `json:"idempotencyKey"`
	Scenario       task.Scenario `json:"scenario"`
	TimeoutMS      int64         `json:"timeoutMs"`
}

type startKindPayload struct {
	Kind string `json:"kind"`
}

type simulationStartPayloadV12 struct {
	IdempotencyKey string        `json:"idempotencyKey"`
	Kind           string        `json:"kind"`
	Scenario       task.Scenario `json:"scenario"`
	TimeoutMS      int64         `json:"timeoutMs"`
}

type buildStartPayloadV12 struct {
	IdempotencyKey      string   `json:"idempotencyKey"`
	Kind                string   `json:"kind"`
	WorkspaceGeneration string   `json:"workspaceGeneration"`
	ProjectID           string   `json:"projectId"`
	BuildProfileID      string   `json:"buildProfileId"`
	TargetIDs           []string `json:"targetIds"`
	Jobs                int      `json:"jobs"`
	TimeoutMS           int64    `json:"timeoutMs"`
}

type targetsPayloadV12 struct {
	WorkspaceGeneration string `json:"workspaceGeneration"`
	ProjectID           string `json:"projectId"`
	BuildProfileID      string `json:"buildProfileId"`
}

type taskIDPayload struct {
	TaskID string `json:"taskId"`
}

type listPayload struct {
	Cursor *string `json:"cursor"`
	Limit  *int    `json:"limit"`
}

type subscribePayload struct {
	AfterSequence *int64 `json:"afterSequence"`
}

type artifactListPayload struct {
	TaskID string  `json:"taskId"`
	Cursor *string `json:"cursor"`
	Limit  *int    `json:"limit"`
}

type artifactReadPayload struct {
	ArtifactID string `json:"artifactId"`
	Offset     *int64 `json:"offset"`
	Length     int    `json:"length"`
}

type testDiscoveryStartPayloadV13 struct {
	IdempotencyKey string `json:"idempotencyKey"`
	Kind           string `json:"kind"`
	ProjectID      string `json:"projectId"`
	ProfileID      string `json:"profileId"`
}

type testRunStartPayloadV13 struct {
	IdempotencyKey  string          `json:"idempotencyKey"`
	Kind            string          `json:"kind"`
	ProjectID       string          `json:"projectId"`
	ProfileID       string          `json:"profileId"`
	CatalogRevision string          `json:"catalogRevision"`
	Selection       json.RawMessage `json:"selection"`
	RepeatCount     int64           `json:"repeatCount"`
}

type testCatalogPayloadV13 struct {
	ProjectID string  `json:"projectId"`
	ProfileID string  `json:"profileId"`
	Cursor    *string `json:"cursor"`
	Limit     *int    `json:"limit"`
}

type testRunIDPayloadV13 struct {
	RunID string `json:"runId"`
}

type testRunsListPayloadV13 struct {
	ProjectID *string `json:"projectId"`
	ProfileID *string `json:"profileId"`
	Cursor    *string `json:"cursor"`
	Limit     *int    `json:"limit"`
}

type taskPagePayload struct {
	Items      []protocolmodel.TaskSnapshot `json:"items"`
	NextCursor string                       `json:"nextCursor,omitempty"`
}

type taskPagePayloadV12 struct {
	Items      []taskv12.TaskSnapshotV12 `json:"items"`
	NextCursor string                    `json:"nextCursor,omitempty"`
}

type artifactPagePayload struct {
	Items      []protocolmodel.ArtifactMetadata `json:"items"`
	NextCursor string                           `json:"nextCursor,omitempty"`
}

type artifactPagePayloadV12 struct {
	Items      []artifactv12.ArtifactMetadataV12 `json:"items"`
	NextCursor string                            `json:"nextCursor,omitempty"`
}

type artifactChunkPayload struct {
	Data       string `json:"data"`
	NextOffset int64  `json:"nextOffset"`
	EOF        bool   `json:"eof"`
	SizeBytes  int64  `json:"sizeBytes"`
	SHA256     string `json:"sha256"`
}

func New(token, platform, transport string, backend Backend) *Session {
	return &Session{token: token, platform: platform, transport: transport, backend: backend, shutdown: make(chan struct{})}
}

func (s *Session) ShutdownRequested() <-chan struct{} { return s.shutdown }

func (s *Session) Authenticated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authenticated
}

func (s *Session) NegotiatedVersion() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.negotiatedVersion
}

func (s *Session) Handle(ctx context.Context, request protocol.Request) HandleResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !protocol.SupportedVersion(request.ProtocolVersion) {
		return handled(protocol.Failure(request.ProtocolVersion, request, "UNSUPPORTED_PROTOCOL", "protocol version is not supported", false))
	}
	responseVersion := request.ProtocolVersion
	if s.authenticated {
		responseVersion = s.negotiatedVersion
		if request.ProtocolVersion != s.negotiatedVersion {
			return handled(protocol.Failure(responseVersion, request, "UNSUPPORTED_PROTOCOL", "protocol version does not match the negotiated version", false))
		}
	}
	if !s.authenticated && request.Method != "handshake" {
		return handled(protocol.Failure(responseVersion, request, "AUTH_REQUIRED", "handshake must be completed first", false))
	}

	switch request.Method {
	case "handshake":
		payload, err := decodeHandshake(request.Payload, request.ProtocolVersion)
		if err != nil {
			return handled(protocol.Failure(responseVersion, request, "INVALID_MESSAGE", "invalid handshake payload", false))
		}
		negotiatedVersion, ok := negotiate(request.ProtocolVersion, payload.SupportedProtocolVersions)
		if !ok {
			return handled(protocol.Failure(responseVersion, request, "UNSUPPORTED_PROTOCOL", "protocol version is not supported", false))
		}
		if subtle.ConstantTimeCompare([]byte(payload.Token), []byte(s.token)) != 1 {
			return handled(protocol.Failure(negotiatedVersion, request, "AUTH_FAILED", "authentication failed", false))
		}
		s.authenticated = true
		s.negotiatedVersion = negotiatedVersion
		return handled(protocol.Success(negotiatedVersion, request, map[string]string{"negotiatedProtocolVersion": negotiatedVersion, "serviceVersion": "0.1.0"}))
	case "capabilities/get":
		if err := decodeEmpty(request.Payload); err != nil {
			return handled(protocol.Failure(responseVersion, request, "INVALID_MESSAGE", "payload must be an empty object", false))
		}
		if s.negotiatedVersion == protocol.Version13 {
			return handled(protocol.Success(
				responseVersion,
				request,
				capabilitiesV13(),
			))
		}
		if s.negotiatedVersion == protocol.Version12 {
			return handled(protocol.Success(responseVersion, request, capabilitiesv12.CapabilitiesV12{
				WorkspaceInspect: true,
				TargetList:       true,
				CmakeBuild:       true,
			}))
		}
		if s.negotiatedVersion == protocol.Version11 {
			return handled(protocol.Success(responseVersion, request, protocolmodel.CapabilitiesV11{
				Platform:           protocolmodel.Platform(s.platform),
				Transports:         []protocolmodel.Transport{protocolmodel.Transport(s.transport)},
				Toolchains:         []string{},
				Frameworks:         []string{},
				CoverageTools:      []string{},
				TaskExecution:      true,
				EventReplay:        true,
				SqliteHistory:      true,
				ArtifactRead:       true,
				ProcessTreeControl: map[string]protocolmodel.ProcessTreeControl{"windows": protocolmodel.JobObject, "linux": protocolmodel.ProcessGroup}[s.platform],
			}))
		}
		return handled(protocol.Success(responseVersion, request, protocolmodel.Capabilities{Platform: s.platform, Transports: []string{s.transport}, Toolchains: []string{}, Frameworks: []string{}, CoverageTools: []string{}}))
	case "shutdown":
		if err := decodeEmpty(request.Payload); err != nil {
			return handled(protocol.Failure(responseVersion, request, "INVALID_MESSAGE", "payload must be an empty object", false))
		}
		s.shutdownOnce.Do(func() { close(s.shutdown) })
		return handled(protocol.Success(responseVersion, request, map[string]bool{"accepted": true}))
	}

	if phase3Method(request.Method) {
		if s.negotiatedVersion != protocol.Version12 &&
			s.negotiatedVersion != protocol.Version13 {
			return handled(protocol.Failure(responseVersion, request, "PROTOCOL_FEATURE_UNAVAILABLE", "method requires protocol 1.2", false))
		}
		if s.backend == nil {
			return handled(protocol.Failure(responseVersion, request, "SERVICE_UNHEALTHY", "workspace service is unavailable", true))
		}
		return s.handlePhase3(ctx, responseVersion, request)
	}
	if phase4Method(request.Method) {
		if s.negotiatedVersion != protocol.Version13 {
			return handled(protocol.Failure(
				responseVersion,
				request,
				"PROTOCOL_FEATURE_UNAVAILABLE",
				"method requires protocol 1.3",
				false,
			))
		}
		if s.backend == nil {
			return handled(protocol.Failure(
				responseVersion,
				request,
				"SERVICE_UNHEALTHY",
				"test service is unavailable",
				true,
			))
		}
		backend, ok := s.backend.(TestBackend)
		if !ok {
			return handled(protocol.Failure(
				responseVersion,
				request,
				"SERVICE_UNHEALTHY",
				"test service is unavailable",
				true,
			))
		}
		return s.handlePhase4(
			ctx,
			responseVersion,
			request,
			backend,
		)
	}
	if phase2Method(request.Method) {
		if s.negotiatedVersion == protocol.Version10 {
			return handled(protocol.Failure(responseVersion, request, "PROTOCOL_FEATURE_UNAVAILABLE", "method requires protocol 1.1", false))
		}
		if s.backend == nil {
			return handled(protocol.Failure(responseVersion, request, "SERVICE_UNHEALTHY", "task service is unavailable", true))
		}
		return s.handlePhase2(ctx, responseVersion, request)
	}
	return handled(protocol.Failure(responseVersion, request, "METHOD_NOT_FOUND", "method is not supported", false))
}

func (s *Session) handlePhase2(ctx context.Context, version string, request protocol.Request) HandleResult {
	switch request.Method {
	case "tasks/start":
		if version == protocol.Version13 {
			backend, ok := s.backend.(TestBackend)
			if !ok {
				return handled(protocol.Failure(
					version,
					request,
					"SERVICE_UNHEALTHY",
					"test service is unavailable",
					true,
				))
			}
			return s.handleV13TaskStart(
				ctx,
				version,
				request,
				backend,
			)
		}
		if version == protocol.Version12 {
			return s.handleV12TaskStart(ctx, version, request)
		}
		payload, err := decodeStrict[startPayload](request.Payload)
		if err != nil || !validID(payload.IdempotencyKey) || !task.ValidScenario(payload.Scenario) || payload.TimeoutMS < 1 || payload.TimeoutMS > maxTimeoutMS {
			return invalidPayload(version, request)
		}
		value, err := s.backend.StartSimulation(
			ctx,
			task.SimulationStart{
				IdempotencyKey: payload.IdempotencyKey,
				Scenario:       payload.Scenario,
				Timeout:        time.Duration(payload.TimeoutMS) * time.Millisecond,
			},
		)
		if err != nil {
			return backendFailure(version, request, err)
		}
		return handled(protocol.Success(version, request, toProtocolTask(value)))
	case "tasks/get", "tasks/cancel":
		payload, err := decodeStrict[taskIDPayload](request.Payload)
		if err != nil || !validID(payload.TaskID) {
			return invalidPayload(version, request)
		}
		value, err := s.backend.Get(ctx, payload.TaskID)
		if err != nil {
			return backendFailure(version, request, err)
		}
		if legacyTaskHidden(version, value.Kind) {
			return taskNotFound(version, request)
		}
		if request.Method == "tasks/cancel" {
			value, err = s.backend.Cancel(ctx, payload.TaskID)
		}
		if err != nil {
			return backendFailure(version, request, err)
		}
		if version == protocol.Version13 {
			var run *testdomain.TestRun
			if value.Kind == task.KindTestRun {
				testBackend, ok := s.backend.(TestBackend)
				if !ok {
					return backendFailure(
						version,
						request,
						task.ErrStorageUnavailable,
					)
				}
				persisted, runErr :=
					testBackend.GetTestRunForTask(
						ctx,
						value.ID,
					)
				if runErr != nil {
					return backendFailure(
						version,
						request,
						runErr,
					)
				}
				run = &persisted
			}
			projected, projectErr :=
				toProtocolTaskV13(value, run)
			if projectErr != nil {
				return backendFailure(
					version,
					request,
					projectErr,
				)
			}
			return handled(protocol.Success(
				version,
				request,
				projected,
			))
		}
		if version == protocol.Version12 {
			projected, projectErr := toProtocolTaskV12(value)
			if projectErr != nil {
				return backendFailure(version, request, projectErr)
			}
			return handled(protocol.Success(version, request, projected))
		}
		return handled(protocol.Success(version, request, toProtocolTask(value)))
	case "tasks/list":
		payload, cursor, limit, err := decodeList(request.Payload)
		_ = payload
		if err != nil {
			return invalidPayload(version, request)
		}
		var kinds []task.Kind
		if version == protocol.Version11 {
			kinds = []task.Kind{task.KindSimulation}
		} else if version == protocol.Version12 {
			kinds = []task.Kind{
				task.KindSimulation,
				task.KindCMakeBuild,
			}
		}
		page, err := s.backend.List(ctx, cursor, limit, kinds)
		if err != nil {
			return backendFailure(version, request, err)
		}
		if version == protocol.Version13 {
			testBackend, ok := s.backend.(TestBackend)
			if !ok {
				return backendFailure(
					version,
					request,
					task.ErrStorageUnavailable,
				)
			}
			items := make(
				[]taskv13.TaskSnapshotV13,
				len(page.Items),
			)
			for index := range page.Items {
				var run *testdomain.TestRun
				if page.Items[index].Kind ==
					task.KindTestRun {
					persisted, runErr :=
						testBackend.GetTestRunForTask(
							ctx,
							page.Items[index].ID,
						)
					if runErr != nil {
						return backendFailure(
							version,
							request,
							runErr,
						)
					}
					run = &persisted
				}
				items[index], err = toProtocolTaskV13(
					page.Items[index],
					run,
				)
				if err != nil {
					return backendFailure(
						version,
						request,
						err,
					)
				}
			}
			return handled(protocol.Success(
				version,
				request,
				struct {
					Items      []taskv13.TaskSnapshotV13 `json:"items"`
					NextCursor string                    `json:"nextCursor,omitempty"`
				}{
					Items:      items,
					NextCursor: page.NextCursor,
				},
			))
		}
		if version == protocol.Version12 {
			items := make([]taskv12.TaskSnapshotV12, len(page.Items))
			for index := range page.Items {
				items[index], err = toProtocolTaskV12(page.Items[index])
				if err != nil {
					return backendFailure(version, request, err)
				}
			}
			return handled(protocol.Success(version, request, taskPagePayloadV12{Items: items, NextCursor: page.NextCursor}))
		}
		items := make([]protocolmodel.TaskSnapshot, len(page.Items))
		for index := range page.Items {
			if page.Items[index].Kind != task.KindSimulation {
				return backendFailure(version, request, errors.New("legacy task list contains unsupported kind"))
			}
			items[index] = toProtocolTask(page.Items[index])
		}
		return handled(protocol.Success(version, request, taskPagePayload{Items: items, NextCursor: page.NextCursor}))
	case "events/subscribe":
		payload, err := decodeStrict[subscribePayload](request.Payload)
		if err != nil || payload.AfterSequence == nil || *payload.AfterSequence < 0 {
			return invalidPayload(version, request)
		}
		subscription, err := s.backend.Subscribe(ctx, *payload.AfterSequence)
		if err != nil {
			return backendFailure(version, request, err)
		}
		return HandleResult{Response: protocol.Success(version, request, map[string]int64{"afterSequence": *payload.AfterSequence}), Subscription: subscription}
	case "artifacts/list":
		payload, err := decodeStrict[artifactListPayload](request.Payload)
		if err != nil || !validID(payload.TaskID) {
			return invalidPayload(version, request)
		}
		cursor, limit, err := normalizedPage(payload.Cursor, payload.Limit)
		if err != nil {
			return invalidPayload(version, request)
		}
		if version == protocol.Version11 ||
			version == protocol.Version12 {
			parent, getErr := s.backend.Get(ctx, payload.TaskID)
			if getErr != nil {
				return backendFailure(version, request, getErr)
			}
			if legacyTaskHidden(version, parent.Kind) {
				return taskNotFound(version, request)
			}
		}
		page, err := s.backend.ListArtifacts(ctx, payload.TaskID, cursor, limit)
		if err != nil {
			return backendFailure(version, request, err)
		}
		if version == protocol.Version12 ||
			version == protocol.Version13 {
			items := make([]artifactv12.ArtifactMetadataV12, len(page.Items))
			for index := range page.Items {
				items[index] = toProtocolArtifactV12(page.Items[index])
			}
			return handled(protocol.Success(version, request, artifactPagePayloadV12{Items: items, NextCursor: page.NextCursor}))
		}
		items := make([]protocolmodel.ArtifactMetadata, len(page.Items))
		for index := range page.Items {
			items[index] = toProtocolArtifact(page.Items[index])
		}
		return handled(protocol.Success(version, request, artifactPagePayload{Items: items, NextCursor: page.NextCursor}))
	case "artifacts/read":
		payload, err := decodeStrict[artifactReadPayload](request.Payload)
		if err != nil || !validID(payload.ArtifactID) || payload.Offset == nil || *payload.Offset < 0 || payload.Length < 1 || payload.Length > artifactstore.MaxReadChunk {
			return invalidPayload(version, request)
		}
		chunk, err := s.backend.ReadArtifact(ctx, payload.ArtifactID, *payload.Offset, payload.Length)
		if err != nil {
			return backendFailure(version, request, err)
		}
		if version == protocol.Version11 ||
			version == protocol.Version12 {
			parent, getErr := s.backend.Get(ctx, chunk.Metadata.TaskID)
			if getErr != nil {
				return backendFailure(version, request, getErr)
			}
			if legacyTaskHidden(version, parent.Kind) {
				return taskNotFound(version, request)
			}
		}
		return handled(protocol.Success(version, request, artifactChunkPayload{
			Data: base64.RawURLEncoding.EncodeToString(chunk.Data), NextOffset: chunk.NextOffset, EOF: chunk.EOF,
			SizeBytes: chunk.Metadata.Size, SHA256: chunk.Metadata.SHA256,
		}))
	default:
		return handled(protocol.Failure(version, request, "METHOD_NOT_FOUND", "method is not supported", false))
	}
}

func (s *Session) handlePhase4(
	ctx context.Context,
	version string,
	request protocol.Request,
	backend TestBackend,
) HandleResult {
	switch request.Method {
	case "tests/catalog/get":
		payload, err :=
			decodeStrict[testCatalogPayloadV13](request.Payload)
		cursor, limit, pageErr := normalizedTestPage(
			payload.Cursor,
			payload.Limit,
			testdomain.DefaultCatalogPageSize,
		)
		if err != nil || pageErr != nil ||
			!validProjectID(payload.ProjectID) ||
			!validHash(payload.ProfileID) {
			return invalidPayload(version, request)
		}
		page, err := backend.GetTestCatalog(
			ctx,
			testdomain.CatalogPageRequest{
				ProjectID: payload.ProjectID,
				ProfileID: payload.ProfileID,
				Cursor:    cursor,
				Limit:     limit,
			},
		)
		if err != nil {
			return backendFailure(version, request, err)
		}
		projected, err := toProtocolTestCatalog(page)
		if err != nil {
			return backendFailure(version, request, err)
		}
		return handled(protocol.Success(
			version,
			request,
			projected,
		))
	case "tests/runs/get":
		payload, err :=
			decodeStrict[testRunIDPayloadV13](request.Payload)
		if err != nil || !validID(payload.RunID) {
			return invalidPayload(version, request)
		}
		run, err := backend.GetTestRun(ctx, payload.RunID)
		if err != nil {
			return backendFailure(version, request, err)
		}
		projected, err := toProtocolTestRun(run)
		if err != nil {
			return backendFailure(version, request, err)
		}
		return handled(protocol.Success(
			version,
			request,
			projected,
		))
	case "tests/runs/list":
		payload, err :=
			decodeStrict[testRunsListPayloadV13](request.Payload)
		cursor, limit, pageErr := normalizedTestPage(
			payload.Cursor,
			payload.Limit,
			testdomain.DefaultRunPageSize,
		)
		if err != nil || pageErr != nil ||
			payload.ProjectID != nil &&
				!validProjectID(*payload.ProjectID) ||
			payload.ProfileID != nil &&
				!validHash(*payload.ProfileID) {
			return invalidPayload(version, request)
		}
		pageRequest := testdomain.RunPageRequest{
			Cursor: cursor,
			Limit:  limit,
		}
		if payload.ProjectID != nil {
			pageRequest.ProjectID = *payload.ProjectID
		}
		if payload.ProfileID != nil {
			pageRequest.ProfileID = *payload.ProfileID
		}
		page, err := backend.ListTestRuns(ctx, pageRequest)
		if err != nil {
			return backendFailure(version, request, err)
		}
		projected, err := toProtocolTestRunPage(page)
		if err != nil {
			return backendFailure(version, request, err)
		}
		return handled(protocol.Success(
			version,
			request,
			projected,
		))
	default:
		return handled(protocol.Failure(
			version,
			request,
			"METHOD_NOT_FOUND",
			"method is not supported",
			false,
		))
	}
}

func (s *Session) handlePhase3(ctx context.Context, version string, request protocol.Request) HandleResult {
	switch request.Method {
	case "workspace/inspect":
		if err := decodeEmpty(request.Payload); err != nil {
			return invalidPayload(version, request)
		}
		snapshot, err := s.backend.InspectWorkspace(ctx)
		if err != nil {
			return backendFailure(version, request, err)
		}
		projected, err := toProtocolWorkspace(snapshot)
		if err != nil {
			return backendFailure(version, request, err)
		}
		return handled(protocol.Success(version, request, projected))
	case "cmake/targets/list":
		payload, err := decodeStrict[targetsPayloadV12](request.Payload)
		if err != nil || !validHash(payload.WorkspaceGeneration) || !validProjectID(payload.ProjectID) || !validHash(payload.BuildProfileID) {
			return invalidPayload(version, request)
		}
		targets, err := s.backend.ListTargets(ctx, build.TargetsRequest{
			WorkspaceGeneration: payload.WorkspaceGeneration,
			ProjectID:           payload.ProjectID,
			BuildProfileID:      payload.BuildProfileID,
		})
		if err != nil {
			return backendFailure(version, request, err)
		}
		items := make([]targetlistv12.TargetListSchema, len(targets))
		for index, target := range targets {
			if !validHash(target.ID) || target.Name == "" {
				return backendFailure(version, request, errors.New("invalid target projection"))
			}
			items[index] = targetlistv12.TargetListSchema{TargetID: target.ID, Name: target.Name}
		}
		return handled(protocol.Success(version, request, targetlistv12.TargetList{
			WorkspaceGeneration: payload.WorkspaceGeneration,
			ProjectID:           payload.ProjectID,
			BuildProfileID:      payload.BuildProfileID,
			Targets:             items,
		}))
	default:
		return handled(protocol.Failure(version, request, "METHOD_NOT_FOUND", "method is not supported", false))
	}
}

func (s *Session) handleV12TaskStart(ctx context.Context, version string, request protocol.Request) HandleResult {
	var kind startKindPayload
	if err := json.Unmarshal(request.Payload, &kind); err != nil || kind.Kind == "" {
		return invalidPayload(version, request)
	}
	switch kind.Kind {
	case "simulation":
		payload, err := decodeStrict[simulationStartPayloadV12](request.Payload)
		if err != nil || !validID(payload.IdempotencyKey) || !task.ValidScenario(payload.Scenario) ||
			payload.TimeoutMS < 1 || payload.TimeoutMS > maxTimeoutMS {
			return invalidPayload(version, request)
		}
		value, err := s.backend.StartSimulation(ctx, task.SimulationStart{
			IdempotencyKey: payload.IdempotencyKey,
			Scenario:       payload.Scenario,
			Timeout:        time.Duration(payload.TimeoutMS) * time.Millisecond,
		})
		if err != nil {
			return backendFailure(version, request, err)
		}
		projected, err := toProtocolTaskV12(value)
		if err != nil {
			return backendFailure(version, request, err)
		}
		return handled(protocol.Success(version, request, projected))
	case "cmakeBuild":
		payload, err := decodeStrict[buildStartPayloadV12](request.Payload)
		if err != nil || !validBuildStart(payload) {
			return invalidPayload(version, request)
		}
		value, err := s.backend.StartBuild(ctx, build.StartRequest{
			IdempotencyKey:      payload.IdempotencyKey,
			WorkspaceGeneration: payload.WorkspaceGeneration,
			ProjectID:           payload.ProjectID,
			BuildProfileID:      payload.BuildProfileID,
			TargetIDs:           append([]string(nil), payload.TargetIDs...),
			Jobs:                payload.Jobs,
			Timeout:             time.Duration(payload.TimeoutMS) * time.Millisecond,
		})
		if err != nil {
			return backendFailure(version, request, err)
		}
		projected, err := toProtocolTaskV12(value)
		if err != nil {
			return backendFailure(version, request, err)
		}
		return handled(protocol.Success(version, request, projected))
	default:
		return invalidPayload(version, request)
	}
}

func (s *Session) handleV13TaskStart(
	ctx context.Context,
	version string,
	request protocol.Request,
	backend TestBackend,
) HandleResult {
	var kind startKindPayload
	if err := json.Unmarshal(request.Payload, &kind); err != nil ||
		kind.Kind == "" {
		return invalidPayload(version, request)
	}
	var (
		started task.Task
		run     *testdomain.TestRun
		err     error
	)
	switch kind.Kind {
	case "simulation":
		payload, decodeErr :=
			decodeStrict[simulationStartPayloadV12](request.Payload)
		if decodeErr != nil ||
			!validID(payload.IdempotencyKey) ||
			!task.ValidScenario(payload.Scenario) ||
			payload.TimeoutMS < 1 ||
			payload.TimeoutMS > maxTimeoutMS {
			return invalidPayload(version, request)
		}
		started, err = s.backend.StartSimulation(
			ctx,
			task.SimulationStart{
				IdempotencyKey: payload.IdempotencyKey,
				Scenario:       payload.Scenario,
				Timeout: time.Duration(
					payload.TimeoutMS,
				) * time.Millisecond,
			},
		)
	case "cmakeBuild":
		payload, decodeErr :=
			decodeStrict[buildStartPayloadV12](request.Payload)
		if decodeErr != nil || !validBuildStart(payload) {
			return invalidPayload(version, request)
		}
		started, err = s.backend.StartBuild(
			ctx,
			build.StartRequest{
				IdempotencyKey: payload.IdempotencyKey,
				WorkspaceGeneration: payload.
					WorkspaceGeneration,
				ProjectID:      payload.ProjectID,
				BuildProfileID: payload.BuildProfileID,
				TargetIDs: append(
					[]string(nil),
					payload.TargetIDs...,
				),
				Jobs: payload.Jobs,
				Timeout: time.Duration(
					payload.TimeoutMS,
				) * time.Millisecond,
			},
		)
	case "testDiscovery":
		payload, decodeErr :=
			decodeStrict[testDiscoveryStartPayloadV13](
				request.Payload,
			)
		if decodeErr != nil ||
			!validID(payload.IdempotencyKey) ||
			!validProjectID(payload.ProjectID) ||
			!validHash(payload.ProfileID) {
			return invalidPayload(version, request)
		}
		started, err = backend.StartTestDiscovery(
			ctx,
			TestDiscoveryStart{
				IdempotencyKey: payload.IdempotencyKey,
				ProjectID:      payload.ProjectID,
				ProfileID:      payload.ProfileID,
			},
		)
	case "testRun":
		payload, decodeErr :=
			decodeStrict[testRunStartPayloadV13](
				request.Payload,
			)
		if decodeErr != nil ||
			!validID(payload.IdempotencyKey) ||
			!validProjectID(payload.ProjectID) ||
			!validHash(payload.ProfileID) ||
			!validHash(payload.CatalogRevision) ||
			payload.RepeatCount < 1 ||
			payload.RepeatCount > 100 {
			return invalidPayload(version, request)
		}
		selection, decodeErr := decodeTestSelection(
			payload.Selection,
		)
		if decodeErr != nil {
			return invalidPayload(version, request)
		}
		var value testdomain.TestRun
		started, value, err = backend.StartTestRun(
			ctx,
			TestRunStart{
				IdempotencyKey:  payload.IdempotencyKey,
				ProjectID:       payload.ProjectID,
				ProfileID:       payload.ProfileID,
				CatalogRevision: payload.CatalogRevision,
				Selection:       selection,
				RepeatCount:     payload.RepeatCount,
			},
		)
		run = &value
	default:
		return invalidPayload(version, request)
	}
	if err != nil {
		return backendFailure(version, request, err)
	}
	projected, err := toProtocolTaskV13(started, run)
	if err != nil {
		return backendFailure(version, request, err)
	}
	return handled(protocol.Success(version, request, projected))
}

func handled(response protocol.Response) HandleResult { return HandleResult{Response: response} }

func invalidPayload(version string, request protocol.Request) HandleResult {
	return handled(protocol.Failure(version, request, "INVALID_MESSAGE", "invalid method payload", false))
}

func backendFailure(version string, request protocol.Request, err error) HandleResult {
	code, message, retryable := "SERVICE_UNHEALTHY", "task service is unavailable", true
	if request.Method == "events/subscribe" {
		code, message = "STORAGE_UNAVAILABLE", "event subscription storage is unavailable"
	}
	switch {
	case errors.Is(err, build.ErrWorkspaceTrustRequired):
		code, message, retryable = "WORKSPACE_TRUST_REQUIRED", "workspace trust is required", false
	case errors.Is(err, build.ErrWorkspaceChanged):
		code, message, retryable = "WORKSPACE_CHANGED", "workspace generation is stale", false
	case errors.Is(err, build.ErrProjectNotFound):
		code, message, retryable = "PROJECT_NOT_FOUND", "project was not found", false
	case errors.Is(err, build.ErrBuildProfileNotFound):
		code, message, retryable = "BUILD_PROFILE_NOT_FOUND", "build profile was not found", false
	case errors.Is(err, build.ErrTargetNotFound):
		code, message, retryable = "TARGET_NOT_FOUND", "target was not found", false
	case errors.Is(err, build.ErrConfigureRequired):
		code, message, retryable = "CONFIGURE_REQUIRED", "CMake configure is required", false
	case errors.Is(err, testdomain.ErrCatalogStale):
		code, message, retryable = "CATALOG_STALE", "test Catalog is stale", false
	case errors.Is(err, testdomain.ErrEmptySelection),
		errors.Is(err, testdomain.ErrSelectionTooLarge),
		errors.Is(err, testdomain.ErrUnknownSelectionID),
		errors.Is(err, testdomain.ErrInvalidSelection),
		errors.Is(err, testdomain.ErrFailedRunResolverRequired):
		code, message, retryable = "INVALID_TASK_SPEC", "test selection is invalid", false
	case errors.Is(err, task.ErrIdempotencyConflict):
		code, message, retryable = "IDEMPOTENCY_CONFLICT", "idempotency key conflicts with an existing task", false
	case errors.Is(err, eventbroker.ErrInvalidCursor):
		code, message, retryable = "EVENT_CURSOR_INVALID", "event cursor is invalid", false
	case errors.Is(err, eventbroker.ErrSubscriberTooSlow):
		code, message, retryable = "SUBSCRIBER_TOO_SLOW", "event subscriber is too slow", true
	case errors.Is(err, task.ErrNotFound):
		if request.Method == "artifacts/list" || request.Method == "artifacts/read" {
			code, message = "ARTIFACT_NOT_FOUND", "artifact was not found"
		} else {
			code, message = "TASK_NOT_FOUND", "task was not found"
		}
		retryable = false
	case errors.Is(err, task.ErrInvalidArgument):
		if request.Method == "tasks/start" {
			code, message = "INVALID_TASK_SPEC", "task specification is invalid"
		} else {
			code, message = "INVALID_MESSAGE", "request argument is invalid"
		}
		retryable = false
	case errors.Is(err, artifactstore.ErrInvalidRange):
		code, message, retryable = "INVALID_MESSAGE", "artifact range is invalid", false
	case errors.Is(err, artifactstore.ErrArtifactChanged):
		code, message, retryable = "ARTIFACT_NOT_READY", "artifact is not ready", true
	case errors.Is(err, artifactstore.ErrInvalidArtifact), errors.Is(err, artifactstore.ErrUnsafePath):
		code, message, retryable = "ARTIFACT_NOT_FOUND", "artifact was not found", false
	case errors.Is(err, task.ErrStorageUnavailable), errors.Is(err, artifactstore.ErrStoreUnavailable):
		code, message, retryable = "STORAGE_UNAVAILABLE", "storage is unavailable", true
	}
	return handled(protocol.Failure(version, request, code, message, retryable))
}

func taskNotFound(version string, request protocol.Request) HandleResult {
	return handled(protocol.Failure(version, request, "TASK_NOT_FOUND", "task was not found", false))
}

func decodeList(raw json.RawMessage) (listPayload, string, int, error) {
	payload, err := decodeStrict[listPayload](raw)
	if err != nil {
		return payload, "", 0, err
	}
	cursor, limit, err := normalizedPage(payload.Cursor, payload.Limit)
	return payload, cursor, limit, err
}

func normalizedTestPage(
	cursorValue *string,
	limitValue *int,
	defaultLimit int,
) (string, int, error) {
	cursor := ""
	if cursorValue != nil {
		if *cursorValue == "" || len(*cursorValue) > 4096 {
			return "", 0, errors.New("invalid cursor")
		}
		cursor = *cursorValue
	}
	limit := defaultLimit
	if limitValue != nil {
		if *limitValue < 1 || *limitValue > maxTestPageLimit {
			return "", 0, errors.New("invalid limit")
		}
		limit = *limitValue
	}
	return cursor, limit, nil
}

func normalizedPage(cursorValue *string, limitValue *int) (string, int, error) {
	cursor := ""
	if cursorValue != nil {
		if *cursorValue == "" {
			return "", 0, errors.New("empty cursor")
		}
		cursor = *cursorValue
	}
	limit := defaultPageLimit
	if limitValue != nil {
		if *limitValue < 1 || *limitValue > maxPageLimit {
			return "", 0, errors.New("invalid limit")
		}
		limit = *limitValue
	}
	return cursor, limit, nil
}

func decodeTestSelection(
	raw json.RawMessage,
) (testdomain.Selection, error) {
	var discriminator struct {
		Mode testdomain.SelectionMode `json:"mode"`
	}
	if err := json.Unmarshal(raw, &discriminator); err != nil ||
		!discriminator.Mode.Valid() {
		return testdomain.Selection{}, errors.New(
			"invalid test selection discriminator",
		)
	}
	var selection testdomain.Selection
	switch discriminator.Mode {
	case testdomain.SelectionAll:
		value, err := decodeStrict[struct {
			Mode testdomain.SelectionMode `json:"mode"`
		}](raw)
		if err != nil {
			return testdomain.Selection{}, err
		}
		selection.Mode = value.Mode
	case testdomain.SelectionContainers:
		value, err := decodeStrict[struct {
			Mode         testdomain.SelectionMode `json:"mode"`
			ContainerIDs []testdomain.ID          `json:"containerIds"`
		}](raw)
		if err != nil {
			return testdomain.Selection{}, err
		}
		selection.Mode = value.Mode
		selection.ContainerIDs = value.ContainerIDs
	case testdomain.SelectionItems:
		value, err := decodeStrict[struct {
			Mode    testdomain.SelectionMode `json:"mode"`
			ItemIDs []testdomain.ID          `json:"itemIds"`
		}](raw)
		if err != nil {
			return testdomain.Selection{}, err
		}
		selection.Mode = value.Mode
		selection.ItemIDs = value.ItemIDs
	case testdomain.SelectionFilter:
		type filterPayload struct {
			Group          *string         `json:"group"`
			Suite          *string         `json:"suite"`
			Label          *string         `json:"label"`
			NameContains   *string         `json:"nameContains"`
			IncludeItemIDs []testdomain.ID `json:"includeItemIds"`
			ExcludeItemIDs []testdomain.ID `json:"excludeItemIds"`
		}
		value, err := decodeStrict[struct {
			Mode   testdomain.SelectionMode `json:"mode"`
			Filter filterPayload            `json:"filter"`
		}](raw)
		if err != nil {
			return testdomain.Selection{}, err
		}
		selection.Mode = value.Mode
		if value.Filter.Group != nil {
			selection.Filter.Group = *value.Filter.Group
		}
		if value.Filter.Suite != nil {
			selection.Filter.Suite = *value.Filter.Suite
		}
		if value.Filter.Label != nil {
			selection.Filter.Label = *value.Filter.Label
		}
		if value.Filter.NameContains != nil {
			selection.Filter.NameContains =
				*value.Filter.NameContains
		}
		selection.Filter.IncludeItemIDs =
			value.Filter.IncludeItemIDs
		selection.Filter.ExcludeItemIDs =
			value.Filter.ExcludeItemIDs
	case testdomain.SelectionFailedFromRun:
		value, err := decodeStrict[struct {
			Mode  testdomain.SelectionMode `json:"mode"`
			RunID string                   `json:"runId"`
		}](raw)
		if err != nil || !validID(value.RunID) {
			return testdomain.Selection{}, errors.New(
				"invalid failed run selection",
			)
		}
		selection.Mode = value.Mode
		selection.RunID = value.RunID
	}
	return testdomain.NewSelection(selection)
}

func decodeStrict[T any](raw json.RawMessage) (T, error) {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return value, errors.New("multiple JSON values")
	}
	return value, nil
}

func toProtocolWorkspace(value discovery.Snapshot) (workspacev12.WorkspaceSnapshot, error) {
	if value.WorkspaceURI == "" || !validHash(value.Generation) {
		return workspacev12.WorkspaceSnapshot{}, errors.New("invalid workspace snapshot")
	}
	result := workspacev12.WorkspaceSnapshot{
		WorkspaceURI:        value.WorkspaceURI,
		WorkspaceGeneration: value.Generation,
		Capabilities: workspacev12.WorkspaceSnapshotCapabilities{
			WorkspaceInspect: true,
			TargetList:       true,
			CmakeBuild:       true,
		},
		Toolchains:  []workspacev12.ToolchainElement{},
		Diagnostics: []workspacev12.DiagnosticElement{},
		Projects:    make([]workspacev12.ProjectElement, len(value.Projects)),
	}
	toolchainIDs := make(map[string]struct{}, len(value.Toolchains))
	for _, instance := range value.Toolchains {
		projected, err := toProtocolToolchain(instance)
		if err != nil {
			return workspacev12.WorkspaceSnapshot{}, err
		}
		if _, duplicate := toolchainIDs[instance.ID]; duplicate {
			return workspacev12.WorkspaceSnapshot{}, errors.New("duplicate workspace toolchain")
		}
		toolchainIDs[instance.ID] = struct{}{}
		result.Toolchains = append(result.Toolchains, projected)
	}
	if len(value.Diagnostics) > 4096 {
		return workspacev12.WorkspaceSnapshot{}, errors.New("workspace diagnostic limit exceeded")
	}
	for _, value := range value.Diagnostics {
		projected, err := toProtocolWorkspaceDiagnostic(value)
		if err != nil {
			return workspacev12.WorkspaceSnapshot{}, err
		}
		result.Diagnostics = append(result.Diagnostics, projected)
	}
	for index, project := range value.Projects {
		if !validProjectID(project.ID) || project.SourceDir == "" {
			return workspacev12.WorkspaceSnapshot{}, errors.New("invalid workspace project")
		}
		sourceURI, err := url.JoinPath(value.WorkspaceURI, project.SourceDir)
		if err != nil {
			return workspacev12.WorkspaceSnapshot{}, errors.New("invalid workspace project URI")
		}
		projected := workspacev12.ProjectElement{
			ProjectID:     project.ID,
			SourceURI:     sourceURI,
			BuildProfiles: []workspacev12.BuildProfileElement{},
		}
		for _, profile := range value.Profiles {
			if profile.ProjectID != project.ID {
				continue
			}
			if !validHash(profile.ID) {
				return workspacev12.WorkspaceSnapshot{}, errors.New("invalid build profile")
			}
			profileProjection, err := toProtocolBuildProfile(profile, toolchainIDs)
			if err != nil {
				return workspacev12.WorkspaceSnapshot{}, err
			}
			projected.BuildProfiles = append(projected.BuildProfiles, profileProjection)
		}
		result.Projects[index] = projected
	}
	return result, nil
}

func toProtocolWorkspaceDiagnostic(
	value diagnostic.Diagnostic,
) (workspacev12.DiagnosticElement, error) {
	if (value.Severity != "error" && value.Severity != "warning" && value.Severity != "info") ||
		!boundedProtocolString(value.Code, 256) ||
		!boundedProtocolString(value.Message, 1024*1024) {
		return workspacev12.DiagnosticElement{}, errors.New("invalid workspace diagnostic")
	}
	result := workspacev12.DiagnosticElement{
		Severity: workspacev12.Severity(value.Severity),
		Code:     value.Code,
		Message:  value.Message,
	}
	if value.FileURI != "" {
		parsed, err := url.Parse(value.FileURI)
		if err != nil || parsed.Scheme == "" {
			return workspacev12.DiagnosticElement{}, errors.New("invalid workspace diagnostic URI")
		}
		result.SourceURI = &value.FileURI
	}
	if value.Range != nil {
		line := int64(value.Range.Start.Line + 1)
		column := int64(value.Range.Start.Character + 1)
		if line < 1 || column < 1 {
			return workspacev12.DiagnosticElement{}, errors.New("invalid workspace diagnostic range")
		}
		result.Line = &line
		result.Column = &column
	}
	return result, nil
}

func toProtocolBuildProfile(
	profile cmake.BuildProfile,
	toolchainIDs map[string]struct{},
) (workspacev12.BuildProfileElement, error) {
	name := profileDisplayName(profile)
	if profile.Origin != "preset" && profile.Origin != "generated" ||
		!boundedProtocolString(name, 256) || !validProtocolGenerator(profile.Generator) {
		return workspacev12.BuildProfileElement{}, errors.New("invalid build profile metadata")
	}
	result := workspacev12.BuildProfileElement{
		BuildProfileID: profile.ID,
		Name:           name,
		Origin:         workspacev12.Origin(profile.Origin),
		Generator:      workspacev12.Generator(profile.Generator),
	}
	if profile.Configuration != "" {
		if !boundedProtocolString(profile.Configuration, 256) {
			return workspacev12.BuildProfileElement{}, errors.New("invalid build profile configuration")
		}
		result.Configuration = &profile.Configuration
	}
	if profile.ToolchainID != "" {
		if !validProtocolToolchainID(profile.ToolchainID) {
			return workspacev12.BuildProfileElement{}, errors.New("invalid build profile toolchain")
		}
		if _, exists := toolchainIDs[profile.ToolchainID]; !exists {
			return workspacev12.BuildProfileElement{}, errors.New("build profile toolchain is absent")
		}
		result.ToolchainID = &profile.ToolchainID
	} else if profile.Origin == "generated" {
		return workspacev12.BuildProfileElement{}, errors.New("generated build profile has no toolchain")
	}
	return result, nil
}

func toProtocolToolchain(instance toolchain.Instance) (workspacev12.ToolchainElement, error) {
	if !validProtocolToolchainID(instance.ID) ||
		!validProtocolToolchainFamily(instance.Family) ||
		!boundedProtocolString(instance.Version, 128) ||
		!boundedProtocolString(instance.TargetTriple, 256) ||
		!validProtocolArchitecture(instance.HostArchitecture) ||
		!validProtocolArchitecture(instance.TargetArchitecture) ||
		len(instance.Generators) > 4 {
		return workspacev12.ToolchainElement{}, errors.New("invalid workspace toolchain")
	}
	generators := make([]workspacev12.Generator, len(instance.Generators))
	seenGenerators := make(map[string]struct{}, len(instance.Generators))
	for index, generator := range instance.Generators {
		if !validProtocolGenerator(generator) {
			return workspacev12.ToolchainElement{}, errors.New("invalid workspace toolchain generator")
		}
		if _, duplicate := seenGenerators[generator]; duplicate {
			return workspacev12.ToolchainElement{}, errors.New("duplicate workspace toolchain generator")
		}
		seenGenerators[generator] = struct{}{}
		generators[index] = workspacev12.Generator(generator)
	}
	coverage := []workspacev12.CoverageDriver{}
	if instance.Coverage.GCov != "" {
		coverage = append(coverage, workspacev12.Gcov)
	}
	if instance.Coverage.LLVMProfdata != "" && instance.Coverage.LLVMCov != "" {
		coverage = append(coverage, workspacev12.LlvmCov)
	}
	return workspacev12.ToolchainElement{
		ToolchainID:        instance.ID,
		Family:             workspacev12.Family(instance.Family),
		Version:            instance.Version,
		TargetTriple:       instance.TargetTriple,
		HostArchitecture:   workspacev12.TArchitecture(instance.HostArchitecture),
		TargetArchitecture: workspacev12.TArchitecture(instance.TargetArchitecture),
		Generators:         generators,
		Capabilities:       workspacev12.ToolchainCapabilities{CoverageDrivers: coverage},
	}, nil
}

func boundedProtocolString(value string, maximumRunes int) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= maximumRunes
}

func validProtocolToolchainID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validProtocolToolchainFamily(value toolchain.Family) bool {
	return value == toolchain.FamilyGCC || value == toolchain.FamilyClang ||
		value == toolchain.FamilyMSVC || value == toolchain.FamilyClangCL
}

func validProtocolArchitecture(value string) bool {
	return value == "x86" || value == "x64" || value == "arm64"
}

func validProtocolGenerator(value string) bool {
	return value == "Ninja" || value == "Unix Makefiles" ||
		value == "Visual Studio 17 2022" || value == "Visual Studio 18 2026" ||
		value == "NMake Makefiles"
}

func profileDisplayName(profile cmake.BuildProfile) string {
	for _, candidate := range []string{profile.BuildPreset, profile.ConfigurePreset, profile.Configuration, profile.ID} {
		if candidate != "" {
			return candidate
		}
	}
	return "CMake"
}

type storedBuildRequest struct {
	ProjectID      string   `json:"projectId"`
	BuildProfileID string   `json:"buildProfileId"`
	TargetIDs      []string `json:"targetIds"`
	Jobs           int64    `json:"jobs"`
	TimeoutMS      int64    `json:"timeoutMs"`
}

type storedTestDiscoveryRequest struct {
	ProjectID      string   `json:"projectId"`
	BuildProfileID string   `json:"buildProfileId"`
	TargetIDs      []string `json:"targetIds"`
	Jobs           int64    `json:"jobs"`
	TimeoutMS      int64    `json:"timeoutMs"`
}

func toProtocolTaskV13(
	value task.Task,
	run *testdomain.TestRun,
) (taskv13.TaskSnapshotV13, error) {
	switch value.Kind {
	case task.KindSimulation:
		result := taskv13.SimulationTaskSnapshotV13{
			TaskID:       value.ID,
			Kind:         taskv13.Simulation,
			Scenario:     taskv13.SimulationScenarioV13(value.Scenario),
			Status:       taskv13.TaskStatusV13(value.Status),
			CreatedAt:    value.CreatedAt,
			LastSequence: value.LastSequence,
		}
		if value.Timeout > 0 {
			timeout := value.Timeout.Milliseconds()
			result.TimeoutMS = &timeout
		}
		projectV13TaskCompletion(
			value,
			&result.Outcome,
			&result.StartedAt,
			&result.FinishedAt,
			&result.ErrorCode,
			&result.ErrorMessage,
		)
		return result, nil
	case task.KindCMakeBuild:
		request, err :=
			decodeStrict[storedBuildRequest](value.Request)
		if err != nil ||
			!validProjectID(request.ProjectID) ||
			!validHash(request.BuildProfileID) ||
			request.TargetIDs == nil ||
			!validTargetIDs(request.TargetIDs) ||
			request.Jobs < 1 || request.Jobs > 256 ||
			!validHash(value.WorkspaceGeneration) ||
			value.Timeout < time.Millisecond ||
			value.Timeout > 24*time.Hour ||
			request.TimeoutMS != value.Timeout.Milliseconds() {
			return nil, errors.New(
				"invalid persisted CMake task",
			)
		}
		result := taskv13.CmakeBuildTaskSnapshotV13{
			TaskID:              value.ID,
			Kind:                taskv13.CmakeBuild,
			WorkspaceGeneration: value.WorkspaceGeneration,
			ProjectID:           request.ProjectID,
			BuildProfileID:      request.BuildProfileID,
			TargetIDs: append(
				[]string{},
				request.TargetIDs...,
			),
			Jobs:         request.Jobs,
			TimeoutMS:    value.Timeout.Milliseconds(),
			Status:       taskv13.TaskStatusV13(value.Status),
			CreatedAt:    value.CreatedAt,
			LastSequence: value.LastSequence,
		}
		projectV13TaskCompletion(
			value,
			&result.Outcome,
			&result.StartedAt,
			&result.FinishedAt,
			&result.ErrorCode,
			&result.ErrorMessage,
		)
		return result, nil
	case task.KindTestDiscovery:
		request, err :=
			decodeStrict[storedTestDiscoveryRequest](
				value.Request,
			)
		if err != nil ||
			!validProjectID(request.ProjectID) ||
			!validHash(request.BuildProfileID) ||
			request.TargetIDs == nil ||
			!validTargetIDs(request.TargetIDs) ||
			request.Jobs < 1 || request.Jobs > 256 ||
			!validHash(value.WorkspaceGeneration) ||
			value.Timeout < time.Millisecond ||
			value.Timeout > 24*time.Hour ||
			request.TimeoutMS !=
				value.Timeout.Milliseconds() {
			return nil, errors.New(
				"invalid persisted test discovery task",
			)
		}
		result := taskv13.TestDiscoveryTaskSnapshotV13{
			TaskID:       value.ID,
			Kind:         taskv13.TestDiscovery,
			ProjectID:    request.ProjectID,
			ProfileID:    request.BuildProfileID,
			Status:       taskv13.TaskStatusV13(value.Status),
			CreatedAt:    value.CreatedAt,
			LastSequence: value.LastSequence,
		}
		projectV13TaskCompletion(
			value,
			&result.Outcome,
			&result.StartedAt,
			&result.FinishedAt,
			&result.ErrorCode,
			&result.ErrorMessage,
		)
		return result, nil
	case task.KindTestRun:
		if run == nil || run.TaskID != value.ID ||
			run.Summary.Iterations < 1 ||
			!validProjectID(run.ProjectID) ||
			!validHash(run.ProfileID) ||
			!validHash(run.CatalogRevision) ||
			!validID(run.RunID) {
			return nil, errors.New(
				"invalid persisted test run task",
			)
		}
		result := taskv13.TestRunTaskSnapshotV13{
			TaskID:          value.ID,
			Kind:            taskv13.TestRun,
			ProjectID:       run.ProjectID,
			ProfileID:       run.ProfileID,
			CatalogRevision: run.CatalogRevision,
			RunID:           run.RunID,
			RepeatCount:     run.Summary.Iterations,
			Status:          taskv13.TaskStatusV13(value.Status),
			CreatedAt:       value.CreatedAt,
			LastSequence:    value.LastSequence,
		}
		projectV13TaskCompletion(
			value,
			&result.Outcome,
			&result.StartedAt,
			&result.FinishedAt,
			&result.ErrorCode,
			&result.ErrorMessage,
		)
		return result, nil
	default:
		return nil, errors.New("unsupported task kind")
	}
}

func projectV13TaskCompletion(
	value task.Task,
	outcome **taskv13.TaskOutcomeV13,
	startedAt, finishedAt **time.Time,
	errorCode, errorMessage **string,
) {
	if value.Status == task.StatusFinished {
		projected := taskv13.TaskOutcomeV13(value.Outcome)
		*outcome = &projected
	}
	if value.StartedAt != nil {
		projected := *value.StartedAt
		*startedAt = &projected
	}
	if value.FinishedAt != nil {
		projected := *value.FinishedAt
		*finishedAt = &projected
	}
	if value.ErrorCode != "" {
		projected := value.ErrorCode
		*errorCode = &projected
	}
	if value.ErrorMessage != "" {
		projected := value.ErrorMessage
		*errorMessage = &projected
	}
}

func toProtocolTaskV12(value task.Task) (taskv12.TaskSnapshotV12, error) {
	switch value.Kind {
	case task.KindSimulation:
		result := taskv12.SimulationTaskSnapshotV12{
			TaskID:       value.ID,
			Kind:         taskv12.Simulation,
			Scenario:     taskv12.SimulationScenarioV12(value.Scenario),
			Status:       taskv12.TaskStatusV12(value.Status),
			CreatedAt:    value.CreatedAt,
			LastSequence: value.LastSequence,
		}
		if value.Timeout > 0 {
			timeout := value.Timeout.Milliseconds()
			result.TimeoutMS = &timeout
		}
		projectV12TaskCompletion(value, &result.Outcome, &result.StartedAt, &result.FinishedAt, &result.ErrorCode, &result.ErrorMessage)
		return result, nil
	case task.KindCMakeBuild:
		request, err := decodeStrict[storedBuildRequest](value.Request)
		if err != nil || !validProjectID(request.ProjectID) || !validHash(request.BuildProfileID) ||
			request.TargetIDs == nil || !validTargetIDs(request.TargetIDs) ||
			request.Jobs < 1 || request.Jobs > 256 ||
			!validHash(value.WorkspaceGeneration) || value.Timeout < time.Millisecond || value.Timeout > 24*time.Hour ||
			request.TimeoutMS != value.Timeout.Milliseconds() {
			return nil, errors.New("invalid persisted CMake task")
		}
		result := taskv12.CmakeBuildTaskSnapshotV12{
			TaskID:              value.ID,
			Kind:                taskv12.CmakeBuild,
			WorkspaceGeneration: value.WorkspaceGeneration,
			ProjectID:           request.ProjectID,
			BuildProfileID:      request.BuildProfileID,
			TargetIDs:           append([]string{}, request.TargetIDs...),
			Jobs:                request.Jobs,
			TimeoutMS:           value.Timeout.Milliseconds(),
			Status:              taskv12.TaskStatusV12(value.Status),
			CreatedAt:           value.CreatedAt,
			LastSequence:        value.LastSequence,
		}
		projectV12TaskCompletion(value, &result.Outcome, &result.StartedAt, &result.FinishedAt, &result.ErrorCode, &result.ErrorMessage)
		return result, nil
	default:
		return nil, errors.New("unsupported task kind")
	}
}

func projectV12TaskCompletion(
	value task.Task,
	outcome **taskv12.TaskOutcomeV12,
	startedAt, finishedAt **time.Time,
	errorCode, errorMessage **string,
) {
	if value.Status == task.StatusFinished {
		projected := taskv12.TaskOutcomeV12(value.Outcome)
		*outcome = &projected
	}
	if value.StartedAt != nil {
		projected := *value.StartedAt
		*startedAt = &projected
	}
	if value.FinishedAt != nil {
		projected := *value.FinishedAt
		*finishedAt = &projected
	}
	if value.ErrorCode != "" {
		projected := value.ErrorCode
		*errorCode = &projected
	}
	if value.ErrorMessage != "" {
		projected := value.ErrorMessage
		*errorMessage = &projected
	}
}

func toProtocolTask(value task.Task) protocolmodel.TaskSnapshot {
	result := protocolmodel.TaskSnapshot{
		TaskID: value.ID, Kind: protocolmodel.Simulation, Scenario: protocolmodel.Scenario(value.Scenario),
		Status: protocolmodel.Status(value.Status), CreatedAt: value.CreatedAt, LastSequence: value.LastSequence,
	}
	if value.Timeout > 0 {
		timeout := value.Timeout.Milliseconds()
		result.TimeoutMS = &timeout
	}
	if value.StartedAt != nil {
		started := *value.StartedAt
		result.StartedAt = &started
	}
	if value.FinishedAt != nil {
		finished := *value.FinishedAt
		result.FinishedAt = &finished
	}
	if value.Status == task.StatusFinished {
		outcome := protocolmodel.Outcome(value.Outcome)
		result.Outcome = &outcome
	}
	if value.ErrorCode != "" {
		code := value.ErrorCode
		result.ErrorCode = &code
	}
	if value.ErrorMessage != "" {
		message := value.ErrorMessage
		result.ErrorMessage = &message
	}
	return result
}

func toProtocolArtifact(value task.Artifact) protocolmodel.ArtifactMetadata {
	return protocolmodel.ArtifactMetadata{
		ArtifactID: value.ID, TaskID: value.TaskID, Kind: protocolmodel.ArtifactKind(value.Kind),
		MIMEType: protocolmodel.MIMEType(value.MIMEType), SizeBytes: value.Size, Sha256: value.SHA256, CreatedAt: value.CreatedAt,
	}
}

func toProtocolArtifactV12(value task.Artifact) artifactv12.ArtifactMetadataV12 {
	return artifactv12.ArtifactMetadataV12{
		ArtifactID: value.ID,
		TaskID:     value.TaskID,
		Kind:       artifactv12.Kind(value.Kind),
		MIMEType:   artifactv12.MIMEType(value.MIMEType),
		SizeBytes:  value.Size,
		Sha256:     value.SHA256,
		CreatedAt:  value.CreatedAt,
		URI:        (&url.URL{Scheme: "unit-test-ide", Host: "artifact", Path: "/" + value.ID}).String(),
	}
}

func toProtocolTestCatalog(
	page testdomain.CatalogPage,
) (testv13.TestCatalog, error) {
	if !validProjectID(page.ProjectID) ||
		!validHash(page.ProfileID) ||
		!validHash(page.Revision) ||
		page.GeneratedAt.IsZero() ||
		page.Partial ||
		len(page.Containers) > maxTestPageLimit ||
		len(page.Items) > maxTestPageLimit ||
		len(page.Diagnostics) > maxTestPageLimit {
		return testv13.TestCatalog{}, errors.New(
			"invalid persisted test Catalog page",
		)
	}
	result := testv13.TestCatalog{
		ProjectID:   page.ProjectID,
		ProfileID:   page.ProfileID,
		Revision:    page.Revision,
		GeneratedAt: page.GeneratedAt.UTC(),
		Containers: make(
			[]testv13.TestContainer,
			len(page.Containers),
		),
		Items: make(
			[]testv13.TestItem,
			len(page.Items),
		),
		Diagnostics: make(
			[]testv13.DiagnosticV13,
			len(page.Diagnostics),
		),
		Partial: false,
	}
	if page.NextCursor != "" {
		cursor := page.NextCursor
		result.NextCursor = &cursor
	}
	for index, container := range page.Containers {
		result.Containers[index] = testv13.TestContainer{
			ID:               container.ID.String(),
			ProjectID:        container.ProjectID,
			CtestLogicalName: container.CTestLogicalName,
			DisplayName:      container.DisplayName,
			Framework: testv13.TestFrameworkV13(
				container.Framework,
			),
			Capabilities: testv13.TestCapabilitiesV13{
				CanDiscoverCases: container.Capabilities.
					CanDiscoverCases,
				CanRunCase: container.Capabilities.
					CanRunCase,
				CanReportSkipped: container.Capabilities.
					CanReportSkipped,
				CanReportSourceLocation: container.
					Capabilities.CanReportSourceLocation,
				CanReportMockDetails: container.Capabilities.
					CanReportMockDetails,
			},
			Labels:   append([]string{}, container.Labels...),
			Disabled: container.Disabled,
		}
		if container.DegradedReason != "" {
			reason := container.DegradedReason
			result.Containers[index].DegradedReason = &reason
		}
		result.Containers[index].SourceLocation =
			toProtocolTestLocation(container.SourceLocation)
	}
	for index, item := range page.Items {
		result.Items[index] = testv13.TestItem{
			ID:          item.ID.String(),
			ContainerID: item.ContainerID.String(),
			Kind: testv13.TestItemKindV13(
				item.Kind,
			),
			Framework: testv13.TestFrameworkV13(
				item.Framework,
			),
			LogicalName: item.LogicalName,
			DisplayName: item.DisplayName,
			Labels:      append([]string{}, item.Labels...),
			Disabled:    item.Disabled,
			Parameters: make(
				[]testv13.TestParameterV13,
				len(item.Parameters),
			),
			SourceLocation: toProtocolTestLocation(
				item.SourceLocation,
			),
		}
		if item.ParentID != "" {
			parent := item.ParentID.String()
			result.Items[index].ParentID = &parent
		}
		for parameterIndex, parameter := range item.Parameters {
			value := parameter.Value
			result.Items[index].Parameters[parameterIndex] =
				testv13.TestParameterV13{
					Name: parameter.Name,
					Value: &testv13.Value{
						String: &value,
					},
				}
		}
	}
	for index, value := range page.Diagnostics {
		result.Diagnostics[index] =
			toProtocolTestDiagnostic(value)
	}
	return result, nil
}

func toProtocolTestRun(
	value testdomain.TestRun,
) (testv13.TestRun, error) {
	validated, err := testdomain.NewTestRun(value)
	if err != nil {
		return testv13.TestRun{}, err
	}
	result := testv13.TestRun{
		RunID:           validated.RunID,
		TaskID:          validated.TaskID,
		ProjectID:       validated.ProjectID,
		ProfileID:       validated.ProfileID,
		ToolchainID:     validated.ToolchainID,
		CatalogRevision: validated.CatalogRevision,
		SelectionSnapshot: testv13.TestSelectionSnapshotV13{
			Mode: testv13.TestSelectionModeV13(
				validated.SelectionSnapshot.Mode,
			),
			ContainerIDS: testIDsToStrings(
				validated.SelectionSnapshot.ContainerIDs,
			),
			ItemIDS: testIDsToStrings(
				validated.SelectionSnapshot.ItemIDs,
			),
		},
		Status:         testv13.TestRunStatusV13(validated.Status),
		StartedAt:      cloneProtocolTime(validated.StartedAt),
		FinishedAt:     cloneProtocolTime(validated.FinishedAt),
		Summary:        toProtocolTestRunSummary(validated.Summary),
		ResultRevision: validated.ResultRevision,
		Incomplete:     validated.Incomplete,
	}
	if validated.Outcome != "" {
		outcome := testv13.TestRunOutcomeV13(validated.Outcome)
		result.Outcome = &outcome
	}
	return result, nil
}

func toProtocolTestRunPage(
	page testdomain.RunPage,
) (testv13.TestRunPage, error) {
	if len(page.Items) > maxTestPageLimit {
		return testv13.TestRunPage{}, errors.New(
			"test run page exceeds the protocol limit",
		)
	}
	result := testv13.TestRunPage{
		Items: make([]testv13.TestRun, len(page.Items)),
	}
	if page.NextCursor != "" {
		cursor := page.NextCursor
		result.NextCursor = &cursor
	}
	for index, run := range page.Items {
		projected, err := toProtocolTestRun(run)
		if err != nil {
			return testv13.TestRunPage{}, err
		}
		result.Items[index] = projected
	}
	return result, nil
}

func toProtocolTestRunSummary(
	value testdomain.RunSummary,
) testv13.TestRunSummaryV13 {
	return testv13.TestRunSummaryV13{
		Total:      value.Total,
		Completed:  value.Completed,
		Passed:     value.Passed,
		Failed:     value.Failed,
		Skipped:    value.Skipped,
		Errored:    value.Errored,
		Cancelled:  value.Cancelled,
		TimedOut:   value.TimedOut,
		NotRun:     value.NotRun,
		Iterations: value.Iterations,
	}
}

func toProtocolTestLocation(
	value *testdomain.SourceLocation,
) *testv13.TestSourceLocationV13 {
	if value == nil {
		return nil
	}
	result := &testv13.TestSourceLocationV13{
		URI:        value.URI,
		Navigable:  value.Navigable,
		Provenance: testv13.TestSourceProvenanceV13(value.Provenance),
	}
	if value.Line > 0 {
		line := int64(value.Line)
		result.Line = &line
	}
	if value.Column > 0 {
		column := int64(value.Column)
		result.Column = &column
	}
	return result
}

func toProtocolTestDiagnostic(
	value testdomain.Diagnostic,
) testv13.DiagnosticV13 {
	result := testv13.DiagnosticV13{
		Severity: testv13.DiagnosticSeverityV13(value.Severity),
		Category: testv13.CategoryV13(value.Category),
		Code:     value.Code,
		Message:  value.Message,
	}
	if value.SourceURI != "" {
		source := value.SourceURI
		result.SourceURI = &source
	}
	if value.Line > 0 {
		line := int64(value.Line)
		result.Line = &line
	}
	if value.Column > 0 {
		column := int64(value.Column)
		result.Column = &column
	}
	return result
}

func testIDsToStrings(values []testdomain.ID) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.String()
	}
	return result
}

func cloneProtocolTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}

func validID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validHash(value string) bool {
	return len(value) == 64 && validLowerHex(value)
}

func validLowerHex(value string) bool {
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validProjectID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if (character >= 'A' && character <= 'Z') ||
			(character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			(index > 0 && (character == '.' || character == '_' || character == '-')) {
			continue
		}
		return false
	}
	return true
}

func validTargetIDs(values []string) bool {
	if len(values) > 128 {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validHash(value) {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validBuildStart(value buildStartPayloadV12) bool {
	return validID(value.IdempotencyKey) &&
		value.Kind == "cmakeBuild" &&
		validHash(value.WorkspaceGeneration) &&
		validProjectID(value.ProjectID) &&
		validHash(value.BuildProfileID) &&
		validTargetIDs(value.TargetIDs) &&
		value.Jobs >= 1 && value.Jobs <= 256 &&
		value.TimeoutMS >= 1 && value.TimeoutMS <= maxTimeoutMS
}

func legacyTaskHidden(version string, kind task.Kind) bool {
	switch version {
	case protocol.Version11:
		return kind != task.KindSimulation
	case protocol.Version12:
		return kind != task.KindSimulation &&
			kind != task.KindCMakeBuild
	default:
		return false
	}
}

func capabilitiesV13() capabilitiesv13.CapabilitiesV13 {
	return capabilitiesv13.CapabilitiesV13{
		WorkspaceInspect:           true,
		TargetList:                 true,
		CmakeBuild:                 true,
		TestDiscovery:              true,
		TestRun:                    true,
		OpaqueCTestFallback:        true,
		CtestJSON:                  true,
		MaxRepeatCount:             100,
		MaxSelectionSize:           100_000,
		MaxCatalogPageSize:         1_000,
		UnityHelperContractVersion: "1",
		UnityRunnerContractVersion: "utide.runner.v1",
		FrameworkAdapters: []capabilitiesv13.FrameworkAdapterCapabilityV13{
			{
				ID:                      capabilitiesv13.Cpputest,
				ContractVersion:         "cpputest.v1",
				DisplayName:             "CppUTest / CppUMock",
				CanDiscoverCases:        true,
				CanRunCase:              true,
				CanReportSkipped:        true,
				CanReportSourceLocation: true,
				CanReportMockDetails:    true,
			},
			{
				ID:                      capabilitiesv13.Unity,
				ContractVersion:         "utide.runner.v1",
				DisplayName:             "Unity / CMock",
				CanDiscoverCases:        true,
				CanRunCase:              true,
				CanReportSkipped:        true,
				CanReportSourceLocation: true,
				CanReportMockDetails:    true,
			},
		},
	}
}

func negotiate(envelopeVersion string, supported []string) (string, bool) {
	if envelopeVersion == protocol.Version10 && len(supported) == 0 {
		return protocol.Version10, true
	}
	candidates := []string{
		protocol.Version13,
		protocol.Version12,
		protocol.Version11,
		protocol.Version10,
	}
	maximum := -1
	for index, candidate := range candidates {
		if candidate == envelopeVersion {
			maximum = index
			break
		}
	}
	if maximum < 0 {
		return "", false
	}
	for _, candidate := range candidates[maximum:] {
		for _, offered := range supported {
			if offered == candidate {
				return candidate, true
			}
		}
	}
	return "", false
}

func phase2Method(method string) bool {
	switch method {
	case "tasks/start", "tasks/get", "tasks/list", "tasks/cancel", "events/subscribe", "artifacts/list", "artifacts/read":
		return true
	default:
		return false
	}
}

func phase3Method(method string) bool {
	switch method {
	case "workspace/inspect", "cmake/targets/list":
		return true
	default:
		return false
	}
}

func phase4Method(method string) bool {
	switch method {
	case "tests/catalog/get",
		"tests/runs/get",
		"tests/runs/list":
		return true
	default:
		return false
	}
}

func decodeEmpty(raw json.RawMessage) error {
	var payload map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&payload); err != nil || payload == nil || len(payload) != 0 {
		return errors.New("payload is not an empty object")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
}

func decodeHandshake(raw json.RawMessage, version string) (handshake, error) {
	payload, err := decodeStrict[handshake](raw)
	if err != nil {
		return handshake{}, err
	}
	if utf8.RuneCountInString(payload.Token) < 16 || payload.ClientName == "" || payload.ClientVersion == "" {
		return handshake{}, errors.New("missing handshake fields")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return handshake{}, errors.New("handshake payload must be an object")
	}
	if _, offered := fields["supportedProtocolVersions"]; version == protocol.Version10 && offered {
		return handshake{}, errors.New("protocol 1.0 handshake contains a version offer")
	}
	if version != protocol.Version10 && len(payload.SupportedProtocolVersions) == 0 {
		return handshake{}, errors.New("protocol handshake requires a version offer")
	}
	return payload, nil
}
