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
	if abi.addedProvider == nil {
		t.Fatal("provider was not added")
	}
	if abi.addedSubLayer == nil {
		t.Fatal("sublayer was not added")
	}
	if abi.addedSubLayer.ProviderKey == nil || *abi.addedSubLayer.ProviderKey != abi.addedProvider.ProviderKey {
		t.Fatalf("sublayer provider key = %#v, want %#v", abi.addedSubLayer.ProviderKey, abi.addedProvider.ProviderKey)
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
	if gotV4.ProviderKey == nil || *gotV4.ProviderKey != abi.addedProvider.ProviderKey {
		t.Fatalf("v4 provider key = %#v, want %#v", gotV4.ProviderKey, abi.addedProvider.ProviderKey)
	}
	if gotV6.ProviderKey == nil || *gotV6.ProviderKey != abi.addedProvider.ProviderKey {
		t.Fatalf("v6 provider key = %#v, want %#v", gotV6.ProviderKey, abi.addedProvider.ProviderKey)
	}
	if gotV4.SubLayerKey != abi.addedSubLayer.SubLayerKey || gotV6.SubLayerKey != abi.addedSubLayer.SubLayerKey {
		t.Fatalf("sublayers = %#v / %#v, want %#v", gotV4.SubLayerKey, gotV6.SubLayerKey, abi.addedSubLayer.SubLayerKey)
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
	providerKey := providerKeyForLease(leaseID)
	subLayerKey := subLayerKeyForLease(leaseID)

	tests := []struct {
		name    string
		filters []fwpmFilter0
	}{
		{
			name:    "missing v6",
			filters: []fwpmFilter0{{FilterKey: wantV4, ProviderKey: &providerKey, SubLayerKey: subLayerKey, LayerKey: fwpmLayerALEAuthConnectV4, Action: fwpmAction0{Type: fwpActionBlock}}},
		},
		{
			name: "extra third filter",
			filters: []fwpmFilter0{
				{FilterKey: wantV4, ProviderKey: &providerKey, SubLayerKey: subLayerKey, LayerKey: fwpmLayerALEAuthConnectV4, Action: fwpmAction0{Type: fwpActionBlock}},
				{FilterKey: wantV6, ProviderKey: &providerKey, SubLayerKey: subLayerKey, LayerKey: fwpmLayerALEAuthConnectV6, Action: fwpmAction0{Type: fwpActionBlock}},
				{FilterKey: windows.GUID{Data1: 0xdeadbeef, Data2: 0xcafe, Data3: 0xbeef, Data4: [8]byte{1, 2, 3, 4, 5, 6, 7, 8}}, ProviderKey: &providerKey, SubLayerKey: subLayerKey, LayerKey: fwpmLayerALEAuthConnectV4, Action: fwpmAction0{Type: fwpActionBlock}},
			},
		},
		{
			name: "wrong action",
			filters: []fwpmFilter0{
				{FilterKey: wantV4, ProviderKey: &providerKey, SubLayerKey: subLayerKey, LayerKey: fwpmLayerALEAuthConnectV4, Action: fwpmAction0{Type: 0}},
				{FilterKey: wantV6, ProviderKey: &providerKey, SubLayerKey: subLayerKey, LayerKey: fwpmLayerALEAuthConnectV6, Action: fwpmAction0{Type: fwpActionBlock}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			abi := &recordingWfpAPI{
				enumFilters: test.filters,
			}
			engine, err := openWFPEngineWithAPI(abi, func() (windows.GUID, error) { return windows.GUID{}, nil })
			if err != nil {
				t.Fatalf("openWFPEngineWithAPI() error = %v", err)
			}
			engine.providerKey = providerKey
			engine.subLayerKey = subLayerKey
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
	if len(abi.deletedSubLayerKeys) != 1 || abi.deletedSubLayerKeys[0] != abi.addedSubLayer.SubLayerKey {
		t.Fatalf("deleted sublayer keys = %#v, want %#v", abi.deletedSubLayerKeys, abi.addedSubLayer.SubLayerKey)
	}
	if len(abi.deletedProviderKeys) != 1 || abi.deletedProviderKeys[0] != abi.addedProvider.ProviderKey {
		t.Fatalf("deleted provider keys = %#v, want %#v", abi.deletedProviderKeys, abi.addedProvider.ProviderKey)
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
	openSession         *fwpmSession0
	openErr             error
	addedProvider       *fwpmProvider0
	addedSubLayer       *fwpmSubLayer0
	addedFilters        []fwpmFilter0
	enumFilters         []fwpmFilter0
	deletedKeys         []windows.GUID
	deletedSubLayerKeys []windows.GUID
	deletedProviderKeys []windows.GUID
	closeCalls          int
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
	api.enumFilters = append(api.enumFilters, *filter)
	return nil
}

func (api *recordingWfpAPI) GetFilterByKey(_ windows.Handle, key *windows.GUID) (*fwpmFilter0, error) {
	for _, filter := range api.enumFilters {
		if filter.FilterKey == *key {
			copy := filter
			return &copy, nil
		}
	}
	return nil, windows.ERROR_NOT_FOUND
}

func (api *recordingWfpAPI) AddProvider(_ windows.Handle, provider *fwpmProvider0) error {
	copy := *provider
	api.addedProvider = &copy
	return nil
}

func (api *recordingWfpAPI) DeleteProviderByKey(_ windows.Handle, key *windows.GUID) error {
	api.deletedProviderKeys = append(api.deletedProviderKeys, *key)
	return nil
}

func (api *recordingWfpAPI) AddSubLayer(_ windows.Handle, subLayer *fwpmSubLayer0) error {
	copy := *subLayer
	api.addedSubLayer = &copy
	return nil
}

func (api *recordingWfpAPI) DeleteSubLayerByKey(_ windows.Handle, key *windows.GUID) error {
	api.deletedSubLayerKeys = append(api.deletedSubLayerKeys, *key)
	return nil
}

func (api *recordingWfpAPI) EnumFilters(_ windows.Handle, template *fwpmFilterEnumTemplate0) ([]fwpmFilter0, error) {
	var filtered []fwpmFilter0
	for _, filter := range api.enumFilters {
		if template.ProviderKey != nil {
			if filter.ProviderKey == nil || *filter.ProviderKey != *template.ProviderKey {
				continue
			}
		}
		if filter.LayerKey != template.LayerKey && template.LayerKey != (windows.GUID{}) {
			continue
		}
		filtered = append(filtered, filter)
	}
	return filtered, nil
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
