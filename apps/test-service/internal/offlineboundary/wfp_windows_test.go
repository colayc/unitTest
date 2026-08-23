//go:build windows

package offlineboundary

import (
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"testing"

	"golang.org/x/sys/windows"
)

func TestOpenWFPEngineUsesDynamicSessionAndExpectedBlockFilters(t *testing.T) {
	abi := &recordingWfpAPI{}
	engine, err := openWFPEngineWithAPI(abi, func() (windows.GUID, error) {
		return windows.GUID{Data1: 0x11223344, Data2: 0x5566, Data3: 0x7788, Data4: [8]byte{0x90, 0xab, 0xcd, 0xef, 0x10, 0x20, 0x30, 0x40}}, nil
	})
	if err != nil {
		t.Fatalf("openWFPEngineWithAPI() error = %v", err)
	}

	leaseID := []byte("0123456789abcdef")
	applicationPath := `C:\fixture\guarded.exe`
	if err := engine.AddOutboundBlockFilters(context.Background(), leaseID, []string{applicationPath}); err != nil {
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
	applicationID, _ := abi.ApplicationID(applicationPath)
	wantV4, wantV6 := filterKeysForApplication(leaseID, applicationID)
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
	for _, filter := range abi.addedFilters {
		if filter.NumFilterConditions != 2 || filter.FilterCondition == nil {
			t.Fatalf("filter %#v has %d conditions at %p; want APP_ID plus loopback exclusion", filter.FilterKey, filter.NumFilterConditions, filter.FilterCondition)
		}
	}
	for _, filter := range abi.enumFilters {
		if !auditConditionsMatch(filter.Conditions, applicationID) {
			t.Fatalf("filter %#v conditions = %#v, want exact APP_ID and loopback exclusion", filter.FilterKey, filter.Conditions)
		}
	}
	if err := engine.AuditOutboundBlockFilters(context.Background(), leaseID); err != nil {
		t.Fatalf("AuditOutboundBlockFilters() error = %v", err)
	}
}

func TestRegisterExecutableAddsAuditedV4V6ChildFiltersWithoutChangingExistingApp(t *testing.T) {
	abi := &recordingWfpAPI{}
	engine, err := openWFPEngineWithAPI(abi, func() (windows.GUID, error) { return windows.GUID{Data1: 1}, nil })
	if err != nil {
		t.Fatalf("openWFPEngineWithAPI() error = %v", err)
	}
	leaseID := []byte("registered-child")
	parent := `C:\fixture\cmake.exe`
	child := `C:\fixture\clang-cl.exe`
	if err := engine.AddOutboundBlockFilters(context.Background(), leaseID, []string{parent}); err != nil {
		t.Fatalf("AddOutboundBlockFilters() error = %v", err)
	}
	if err := engine.RegisterExecutable(context.Background(), leaseID, child); err != nil {
		t.Fatalf("RegisterExecutable() error = %v", err)
	}
	if len(abi.addedFilters) != 4 {
		t.Fatalf("filters after child registration = %d, want parent+child V4/V6", len(abi.addedFilters))
	}
	childID, _ := abi.ApplicationID(child)
	wantV4, wantV6 := filterKeysForApplication(leaseID, childID)
	if abi.addedFilters[2].FilterKey != wantV4 || abi.addedFilters[3].FilterKey != wantV6 {
		t.Fatalf("child filter keys = %#v / %#v, want V4/V6 child identities", abi.addedFilters[2].FilterKey, abi.addedFilters[3].FilterKey)
	}
	if err := engine.AuditOutboundBlockFilters(context.Background(), leaseID); err != nil {
		t.Fatalf("AuditOutboundBlockFilters() after child = %v", err)
	}
}

func TestAuditEnumeratesEveryFilterForNineRegisteredApplications(t *testing.T) {
	abi := &recordingWfpAPI{enumPageSize: 16}
	engine, err := openWFPEngineWithAPI(abi, func() (windows.GUID, error) { return windows.GUID{Data1: 9}, nil })
	if err != nil {
		t.Fatalf("openWFPEngineWithAPI() error = %v", err)
	}
	leaseID := []byte("pagination-nine-apps")
	applications := []string{
		`C:\fixture\app-1.exe`, `C:\fixture\app-2.exe`, `C:\fixture\app-3.exe`,
		`C:\fixture\app-4.exe`, `C:\fixture\app-5.exe`, `C:\fixture\app-6.exe`,
		`C:\fixture\app-7.exe`, `C:\fixture\app-8.exe`, `C:\fixture\app-9.exe`,
	}
	if err := engine.AddOutboundBlockFilters(context.Background(), leaseID, applications); err != nil {
		t.Fatalf("AddOutboundBlockFilters() error = %v", err)
	}
	if len(abi.enumFilters) != 18 {
		t.Fatalf("registered filters = %d, want 18", len(abi.enumFilters))
	}
	if err := engine.AuditOutboundBlockFilters(context.Background(), leaseID); err != nil {
		t.Fatalf("AuditOutboundBlockFilters() error = %v, want all pages audited", err)
	}
	if want := []uint64{0, 16, 18}; !reflect.DeepEqual(abi.enumCursors, want) {
		t.Fatalf("enumeration cursors = %#v, want %#v", abi.enumCursors, want)
	}
	if abi.enumCloseCalls != 1 {
		t.Fatalf("enumerator close calls = %d, want 1", abi.enumCloseCalls)
	}
}

func TestAuditFallsBackToFilterKeyLookupWhenProviderEnumerationNeverMatches(t *testing.T) {
	abi := &recordingWfpAPI{enumErr: windows.Errno(windows.FWP_E_NEVER_MATCH)}
	engine, err := openWFPEngineWithAPI(abi, func() (windows.GUID, error) { return windows.GUID{Data1: 11}, nil })
	if err != nil {
		t.Fatalf("openWFPEngineWithAPI() error = %v", err)
	}
	leaseID := []byte("never-match-provider-enumeration")
	if err := engine.AddOutboundBlockFilters(context.Background(), leaseID, []string{`C:\fixture\guarded.exe`}); err != nil {
		t.Fatalf("AddOutboundBlockFilters() error = %v", err)
	}
	if err := engine.AuditOutboundBlockFilters(context.Background(), leaseID); err != nil {
		t.Fatalf("AuditOutboundBlockFilters() error = %v, want filter-key fallback", err)
	}
}

func TestAuditPaginationRejectsRepeatedCursorAndOverflow(t *testing.T) {
	tests := []struct {
		name       string
		nextCursor uint64
	}{
		{name: "repeated cursor", nextCursor: 0},
		{name: "cursor overflow", nextCursor: maxAuditFilterEntries + 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			abi := &recordingWfpAPI{}
			engine, err := openWFPEngineWithAPI(abi, func() (windows.GUID, error) { return windows.GUID{Data1: 10}, nil })
			if err != nil {
				t.Fatalf("openWFPEngineWithAPI() error = %v", err)
			}
			leaseID := []byte("pagination-invalid")
			if err := engine.AddOutboundBlockFilters(context.Background(), leaseID, []string{`C:\fixture\guarded.exe`}); err != nil {
				t.Fatalf("AddOutboundBlockFilters() error = %v", err)
			}
			abi.enumPages = []auditFilterPage{{
				Filters:    append([]auditFilterRecord(nil), abi.enumFilters[:1]...),
				NextCursor: test.nextCursor,
			}}
			if err := engine.AuditOutboundBlockFilters(context.Background(), leaseID); !errors.Is(err, FilterAuditFailed) {
				t.Fatalf("AuditOutboundBlockFilters() error = %v, want FilterAuditFailed", err)
			}
			if abi.enumCloseCalls != 1 {
				t.Fatalf("enumerator close calls = %d, want 1", abi.enumCloseCalls)
			}
			if len(abi.enumCursors) != 1 {
				t.Fatalf("enumeration calls = %d, want immediate cursor rejection", len(abi.enumCursors))
			}
		})
	}
}

func TestOpenWFPEngineAuditRejectsMissingExtraAndMismatchedFilters(t *testing.T) {
	leaseID := []byte("fedcba9876543210")
	applicationID := []byte("audit-app-id")
	wantV4, wantV6 := filterKeysForApplication(leaseID, applicationID)
	providerKey := providerKeyForLease(leaseID)
	subLayerKey := subLayerKeyForLease(leaseID)

	tests := []struct {
		name    string
		filters []auditFilterRecord
	}{
		{
			name:    "missing v6",
			filters: []auditFilterRecord{{FilterKey: wantV4, HasProviderKey: true, ProviderKey: providerKey, SubLayerKey: subLayerKey, LayerKey: fwpmLayerALEAuthConnectV4, ActionType: fwpActionBlock}},
		},
		{
			name: "extra third filter",
			filters: []auditFilterRecord{
				{FilterKey: wantV4, HasProviderKey: true, ProviderKey: providerKey, SubLayerKey: subLayerKey, LayerKey: fwpmLayerALEAuthConnectV4, ActionType: fwpActionBlock},
				{FilterKey: wantV6, HasProviderKey: true, ProviderKey: providerKey, SubLayerKey: subLayerKey, LayerKey: fwpmLayerALEAuthConnectV6, ActionType: fwpActionBlock},
				{FilterKey: windows.GUID{Data1: 0xdeadbeef, Data2: 0xcafe, Data3: 0xbeef, Data4: [8]byte{1, 2, 3, 4, 5, 6, 7, 8}}, HasProviderKey: true, ProviderKey: providerKey, SubLayerKey: subLayerKey, LayerKey: fwpmLayerALEAuthConnectV4, ActionType: fwpActionBlock},
			},
		},
		{
			name: "wrong action",
			filters: []auditFilterRecord{
				{FilterKey: wantV4, HasProviderKey: true, ProviderKey: providerKey, SubLayerKey: subLayerKey, LayerKey: fwpmLayerALEAuthConnectV4, ActionType: 0},
				{FilterKey: wantV6, HasProviderKey: true, ProviderKey: providerKey, SubLayerKey: subLayerKey, LayerKey: fwpmLayerALEAuthConnectV6, ActionType: fwpActionBlock},
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
			engine.applications = [][]byte{applicationID}
			if err := engine.AuditOutboundBlockFilters(context.Background(), leaseID); !errors.Is(err, FilterAuditFailed) {
				t.Fatalf("AuditOutboundBlockFilters() error = %v, want FilterAuditFailed", err)
			}
		})
	}
}

func TestAuditFilterRecordCopiesProviderGUIDValue(t *testing.T) {
	sourceProvider := windows.GUID{Data1: 0x11111111, Data2: 0x2222, Data3: 0x3333, Data4: [8]byte{4, 5, 6, 7, 8, 9, 10, 11}}
	filter := fwpmFilter0{
		FilterKey:   windows.GUID{Data1: 0xaaaaaaaa, Data2: 0xbbbb, Data3: 0xcccc, Data4: [8]byte{1, 1, 1, 1, 1, 1, 1, 1}},
		ProviderKey: &sourceProvider,
		SubLayerKey: windows.GUID{Data1: 0xdddddddd, Data2: 0xeeee, Data3: 0xffff, Data4: [8]byte{2, 2, 2, 2, 2, 2, 2, 2}},
		LayerKey:    fwpmLayerALEAuthConnectV4,
		Action:      fwpmAction0{Type: fwpActionBlock},
	}

	record := newAuditFilterRecord(&filter)
	sourceProvider = windows.GUID{}

	if !record.HasProviderKey {
		t.Fatal("record did not preserve provider key presence")
	}
	if record.ProviderKey == (windows.GUID{}) {
		t.Fatal("record provider key was zeroed after source mutation")
	}
	if record.ProviderKey.Data1 != 0x11111111 {
		t.Fatalf("record provider key = %#v", record.ProviderKey)
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
	applicationPath := `C:\fixture\guarded.exe`
	if err := engine.AddOutboundBlockFilters(context.Background(), leaseID, []string{applicationPath}); err != nil {
		t.Fatalf("AddOutboundBlockFilters() error = %v", err)
	}
	applicationID, _ := abi.ApplicationID(applicationPath)
	wantV4, wantV6 := filterKeysForApplication(leaseID, applicationID)
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

func (engine *fakeWfpEngine) AddOutboundBlockFilters(_ context.Context, leaseID []byte, _ []string) error {
	engine.addLeaseIDs = append(engine.addLeaseIDs, append([]byte(nil), leaseID...))
	return nil
}

func (engine *fakeWfpEngine) RegisterExecutable(context.Context, []byte, string) error { return nil }

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
	enumFilters         []auditFilterRecord
	enumPageSize        int
	enumCursors         []uint64
	enumPages           []auditFilterPage
	enumErr             error
	enumCloseCalls      int
	deletedKeys         []windows.GUID
	deletedSubLayerKeys []windows.GUID
	deletedProviderKeys []windows.GUID
	closeCalls          int
}

func (api *recordingWfpAPI) ApplicationID(path string) ([]byte, error) {
	digest := sha256.Sum256([]byte(path))
	return append([]byte(nil), digest[:]...), nil
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
	api.enumFilters = append(api.enumFilters, newAuditFilterRecord(filter))
	return nil
}

func (api *recordingWfpAPI) GetFilterByKey(_ windows.Handle, key *windows.GUID) (*fwpmFilter0, error) {
	for _, filter := range api.addedFilters {
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

func (api *recordingWfpAPI) EnumFilters(_ windows.Handle, template *fwpmFilterEnumTemplate0) (wfpFilterEnumerator, error) {
	if api.enumErr != nil {
		return nil, api.enumErr
	}
	var filtered []auditFilterRecord
	for _, filter := range api.enumFilters {
		if template.ProviderKey != nil {
			if !filter.HasProviderKey || filter.ProviderKey != *template.ProviderKey {
				continue
			}
		}
		if filter.LayerKey != template.LayerKey && template.LayerKey != (windows.GUID{}) {
			continue
		}
		filtered = append(filtered, filter)
	}
	return &recordingFilterEnumerator{api: api, filters: filtered}, nil
}

type recordingFilterEnumerator struct {
	api     *recordingWfpAPI
	filters []auditFilterRecord
	cursor  uint64
}

func (enumerator *recordingFilterEnumerator) Next(cursor uint64, maxEntries uint32) (auditFilterPage, error) {
	enumerator.api.enumCursors = append(enumerator.api.enumCursors, cursor)
	if len(enumerator.api.enumPages) > 0 {
		index := len(enumerator.api.enumCursors) - 1
		if index >= len(enumerator.api.enumPages) {
			return auditFilterPage{}, FilterAuditFailed
		}
		return enumerator.api.enumPages[index], nil
	}
	if cursor != enumerator.cursor || maxEntries == 0 {
		return auditFilterPage{}, FilterAuditFailed
	}
	if cursor >= uint64(len(enumerator.filters)) {
		return auditFilterPage{NextCursor: cursor, Done: true}, nil
	}
	pageSize := int(maxEntries)
	if enumerator.api.enumPageSize > 0 && pageSize > enumerator.api.enumPageSize {
		pageSize = enumerator.api.enumPageSize
	}
	end := int(cursor) + pageSize
	if end > len(enumerator.filters) {
		end = len(enumerator.filters)
	}
	filters := append([]auditFilterRecord(nil), enumerator.filters[int(cursor):end]...)
	enumerator.cursor = uint64(end)
	return auditFilterPage{Filters: filters, NextCursor: enumerator.cursor}, nil
}

func (enumerator *recordingFilterEnumerator) Close() error {
	enumerator.api.enumCloseCalls++
	return nil
}

func (api *recordingWfpAPI) DeleteFilterByKey(_ windows.Handle, key *windows.GUID) error {
	api.deletedKeys = append(api.deletedKeys, *key)
	return nil
}

func (api *recordingWfpAPI) CloseEngine(windows.Handle) error {
	api.closeCalls++
	return nil
}
