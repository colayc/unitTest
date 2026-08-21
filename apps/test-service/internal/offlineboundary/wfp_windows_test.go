//go:build windows

package offlineboundary

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsBoundaryStartUsesInjectedEngineLeaseAndIdempotentClose(t *testing.T) {
	ctx := context.Background()
	ready := make(chan struct{})
	close(ready)
	engine := &fakeWfpEngine{}
	boundary := New(Config{
		engineFactory: func() (wfpEngine, error) { return engine, nil },
		leaseIDSource: func() []byte { return []byte("0123456789abcdef") },
	})

	lease, err := boundary.Start(ctx, OwnerIdentity{PID: 42, CreationTime: 99})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	select {
	case <-lease.Ready():
	default:
		t.Fatal("Ready channel is not closed")
	}
	if got, want := engine.addLeaseIDs, [][]byte{[]byte("0123456789abcdef")}; !equalByteSlices(got, want) {
		t.Fatalf("add lease IDs = %q, want %q", got, want)
	}
	if got, want := engine.auditLeaseIDs, [][]byte{[]byte("0123456789abcdef")}; !equalByteSlices(got, want) {
		t.Fatalf("audit lease IDs = %q, want %q", got, want)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if err := lease.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if engine.closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", engine.closeCalls)
	}

	_ = ready
}

func TestOpenWFPEngineUsesDynamicSessionAndExpectedBlockFilters(t *testing.T) {
	abi := &recordingWfpAPI{}
	engine, err := openWFPEngineWithAPI(abi, func() (windows.GUID, error) {
		return windows.GUID{Data1: 0x11223344, Data2: 0x5566, Data3: 0x7788, Data4: [8]byte{0x90, 0xab, 0xcd, 0xef, 0x10, 0x20, 0x30, 0x40}}, nil
	})
	if err != nil {
		t.Fatalf("openWFPEngineWithAPI() error = %v", err)
	}

	leaseID := []byte("0123456789abcdef")
	if err := engine.AddOutboundBlockFilters(context.Background(), leaseID); err != nil {
		t.Fatalf("AddOutboundBlockFilters() error = %v", err)
	}

	if abi.openSession == nil {
		t.Fatal("session was not opened")
	}
	if abi.openSession.Flags != fwpmSessionFlagDynamic {
		t.Fatalf("session flags = %#x, want %#x", abi.openSession.Flags, fwpmSessionFlagDynamic)
	}
	if len(abi.addedFilters) != 2 {
		t.Fatalf("added filters = %d, want 2", len(abi.addedFilters))
	}
	wantV4, wantV6 := filterKeysForLease(leaseID)
	gotV4, gotV6 := abi.addedFilters[0], abi.addedFilters[1]
	if gotV4.FilterKey != wantV4 {
		t.Fatalf("v4 filter key = %#v, want %#v", gotV4.FilterKey, wantV4)
	}
	if gotV6.FilterKey != wantV6 {
		t.Fatalf("v6 filter key = %#v, want %#v", gotV6.FilterKey, wantV6)
	}
	if gotV4.LayerKey != fwpmLayerALEAuthConnectV4 || gotV6.LayerKey != fwpmLayerALEAuthConnectV6 {
		t.Fatalf("layers = %#v / %#v", gotV4.LayerKey, gotV6.LayerKey)
	}
	if gotV4.Action.Type != fwpActionBlock || gotV6.Action.Type != fwpActionBlock {
		t.Fatalf("actions = %#x / %#x", gotV4.Action.Type, gotV6.Action.Type)
	}
	if gotV4.Flags&fwpmFilterFlagPersistent != 0 || gotV6.Flags&fwpmFilterFlagPersistent != 0 {
		t.Fatalf("persistent flags = %#x / %#x", gotV4.Flags, gotV6.Flags)
	}
}

func TestOpenWFPEngineAuditRejectsMissingExtraAndMismatchedFilters(t *testing.T) {
	leaseID := []byte("fedcba9876543210")
	wantV4, wantV6 := filterKeysForLease(leaseID)

	tests := []struct {
		name    string
		filters map[windows.GUID]fwpmFilter0
	}{
		{
			name: "missing v6",
			filters: map[windows.GUID]fwpmFilter0{
				wantV4: {FilterKey: wantV4, LayerKey: fwpmLayerALEAuthConnectV4, Action: fwpmAction0{Type: fwpActionBlock}},
			},
		},
		{
			name: "duplicate actual key acts like extra",
			filters: map[windows.GUID]fwpmFilter0{
				wantV4: {FilterKey: wantV4, LayerKey: fwpmLayerALEAuthConnectV4, Action: fwpmAction0{Type: fwpActionBlock}},
				wantV6: {FilterKey: wantV4, LayerKey: fwpmLayerALEAuthConnectV6, Action: fwpmAction0{Type: fwpActionBlock}},
			},
		},
		{
			name: "wrong action",
			filters: map[windows.GUID]fwpmFilter0{
				wantV4: {FilterKey: wantV4, LayerKey: fwpmLayerALEAuthConnectV4, Action: fwpmAction0{Type: 0}},
				wantV6: {FilterKey: wantV6, LayerKey: fwpmLayerALEAuthConnectV6, Action: fwpmAction0{Type: fwpActionBlock}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			abi := &recordingWfpAPI{
				filters: test.filters,
			}
			engine, err := openWFPEngineWithAPI(abi, func() (windows.GUID, error) { return windows.GUID{}, nil })
			if err != nil {
				t.Fatalf("openWFPEngineWithAPI() error = %v", err)
			}
			engine.filterKeys = []windows.GUID{wantV4, wantV6}
			if err := engine.AuditOutboundBlockFilters(context.Background(), leaseID); !errors.Is(err, FilterAuditFailed) {
				t.Fatalf("AuditOutboundBlockFilters() error = %v, want FilterAuditFailed", err)
			}
		})
	}
}

func TestOpenWFPEngineMapsAccessDeniedAndDeletesFiltersOnClose(t *testing.T) {
	deny := &recordingWfpAPI{openErr: windows.ERROR_ACCESS_DENIED}
	if _, err := openWFPEngineWithAPI(deny, func() (windows.GUID, error) { return windows.GUID{}, nil }); !errors.Is(err, WFPAccessDenied) {
		t.Fatalf("openWFPEngineWithAPI() error = %v, want WFPAccessDenied", err)
	}

	leaseID := []byte("0123456789abcdef")
	abi := &recordingWfpAPI{}
	engine, err := openWFPEngineWithAPI(abi, func() (windows.GUID, error) { return windows.GUID{}, nil })
	if err != nil {
		t.Fatalf("openWFPEngineWithAPI() error = %v", err)
	}
	if err := engine.AddOutboundBlockFilters(context.Background(), leaseID); err != nil {
		t.Fatalf("AddOutboundBlockFilters() error = %v", err)
	}
	wantV4, wantV6 := filterKeysForLease(leaseID)
	if err := engine.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if len(abi.deletedKeys) != 2 {
		t.Fatalf("deleted keys = %d, want 2", len(abi.deletedKeys))
	}
	if abi.deletedKeys[0] != wantV4 || abi.deletedKeys[1] != wantV6 {
		t.Fatalf("deleted keys = %#v, want %#v %#v", abi.deletedKeys, wantV4, wantV6)
	}
	if abi.closeCalls != 1 {
		t.Fatalf("engine close calls = %d, want 1", abi.closeCalls)
	}
}

type fakeWfpEngine struct {
	addLeaseIDs   [][]byte
	auditLeaseIDs [][]byte
	closeCalls    int
}

func (engine *fakeWfpEngine) AddOutboundBlockFilters(_ context.Context, leaseID []byte) error {
	engine.addLeaseIDs = append(engine.addLeaseIDs, append([]byte(nil), leaseID...))
	return nil
}

func (engine *fakeWfpEngine) AuditOutboundBlockFilters(_ context.Context, leaseID []byte) error {
	engine.auditLeaseIDs = append(engine.auditLeaseIDs, append([]byte(nil), leaseID...))
	return nil
}

func (engine *fakeWfpEngine) Close() error {
	engine.closeCalls++
	return nil
}

type recordingWfpAPI struct {
	openSession  *fwpmSession0
	openErr      error
	addedFilters []fwpmFilter0
	filters      map[windows.GUID]fwpmFilter0
	deletedKeys  []windows.GUID
	closeCalls   int
}

func (api *recordingWfpAPI) OpenSession(session *fwpmSession0) (windows.Handle, error) {
	copy := *session
	api.openSession = &copy
	if api.openErr != nil {
		return 0, api.openErr
	}
	return windows.Handle(1234), nil
}

func (api *recordingWfpAPI) AddFilter(_ windows.Handle, filter *fwpmFilter0) error {
	api.addedFilters = append(api.addedFilters, *filter)
	if api.filters == nil {
		api.filters = map[windows.GUID]fwpmFilter0{}
	}
	api.filters[filter.FilterKey] = *filter
	return nil
}

func (api *recordingWfpAPI) GetFilterByKey(_ windows.Handle, key *windows.GUID) (*fwpmFilter0, error) {
	filter, ok := api.filters[*key]
	if !ok {
		return nil, windows.ERROR_NOT_FOUND
	}
	copy := filter
	return &copy, nil
}

func (api *recordingWfpAPI) DeleteFilterByKey(_ windows.Handle, key *windows.GUID) error {
	api.deletedKeys = append(api.deletedKeys, *key)
	return nil
}

func (api *recordingWfpAPI) CloseEngine(windows.Handle) error {
	api.closeCalls++
	return nil
}

func equalByteSlices(left, right [][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if string(left[index]) != string(right[index]) {
			return false
		}
	}
	return true
}
