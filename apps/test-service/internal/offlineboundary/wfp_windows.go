//go:build windows

package offlineboundary

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	fwpmSessionFlagDynamic   = 0x00000001
	fwpmFilterFlagPersistent = 0x00000001
	fwpActionBlock           = 0x00000001 | 0x00001000
	fwpUint32                = 3
	fwpByteBlobType          = 12
	fwpMatchEqual            = 0
	fwpMatchFlagsNoneSet     = 8
	fwpConditionFlagLoopback = 0x00000001
	rpcCAuthnWinnt           = 10
)

var (
	fwpmLayerALEAuthConnectV4 = windows.GUID{Data1: 0xc38d57d1, Data2: 0x05a7, Data3: 0x4c33, Data4: [8]byte{0x90, 0x4f, 0x7f, 0xbc, 0xee, 0xe6, 0x0e, 0x82}}
	fwpmLayerALEAuthConnectV6 = windows.GUID{Data1: 0x4a72393b, Data2: 0x319f, Data3: 0x44bc, Data4: [8]byte{0x84, 0xc3, 0xba, 0x54, 0xdc, 0xb3, 0xb6, 0xb4}}
	fwpmConditionFlags        = windows.GUID{Data1: 0x632ce23b, Data2: 0x5167, Data3: 0x435c, Data4: [8]byte{0x86, 0xd7, 0xe9, 0x03, 0x68, 0x4a, 0xa8, 0x0c}}
	fwpmConditionALEAppID     = windows.GUID{Data1: 0xd78e1e87, Data2: 0x8644, Data3: 0x4ea5, Data4: [8]byte{0x94, 0x37, 0xd8, 0x09, 0xec, 0xef, 0xc9, 0x71}}
)

type fwpmDisplayData0 struct {
	Name        *uint16
	Description *uint16
}

type fwpmSession0 struct {
	SessionKey           windows.GUID
	DisplayData          fwpmDisplayData0
	Flags                uint32
	TxnWaitTimeoutInMSec uint32
	ProcessID            uint32
	SID                  *windows.SID
	Username             *uint16
	KernelMode           int32
}

type fwpmProvider0 struct {
	ProviderKey  windows.GUID
	DisplayData  fwpmDisplayData0
	Flags        uint32
	ProviderData fwpByteBlob
	ServiceName  *uint16
}

type fwpByteBlob struct {
	Size uint32
	Data *byte
}

type fwpValue0 struct {
	Type    uint32
	Padding uint32
	Value   uint64
}

type fwpmAction0 struct {
	Type       uint32
	FilterType windows.GUID
}

type fwpmFilterCondition0 struct {
	FieldKey       windows.GUID
	MatchType      uint32
	ConditionValue fwpValue0
}

type fwpmFilter0 struct {
	FilterKey           windows.GUID
	DisplayData         fwpmDisplayData0
	Flags               uint32
	ProviderKey         *windows.GUID
	ProviderData        fwpByteBlob
	LayerKey            windows.GUID
	SubLayerKey         windows.GUID
	Weight              fwpValue0
	NumFilterConditions uint32
	FilterCondition     *fwpmFilterCondition0
	Action              fwpmAction0
	ProviderContextKey  windows.GUID
	Reserved            *windows.GUID
	FilterID            uint64
	EffectiveWeight     fwpValue0
}

type fwpmSubLayer0 struct {
	SubLayerKey  windows.GUID
	DisplayData  fwpmDisplayData0
	Flags        uint32
	ProviderKey  *windows.GUID
	ProviderData fwpByteBlob
	Weight       uint16
}

type fwpmFilterEnumTemplate0 struct {
	ProviderKey             *windows.GUID
	LayerKey                windows.GUID
	EnumType                uint32
	Flags                   uint32
	ProviderContextTemplate uintptr
	NumFilterConditions     uint32
	FilterCondition         *fwpmFilterCondition0
	ActionMask              uint32
	CalloutKey              *windows.GUID
}

type auditFilterRecord struct {
	FilterKey      windows.GUID
	HasProviderKey bool
	ProviderKey    windows.GUID
	SubLayerKey    windows.GUID
	LayerKey       windows.GUID
	ActionType     uint32
	Flags          uint32
	Conditions     []auditConditionRecord
}

type auditConditionRecord struct {
	FieldKey  windows.GUID
	MatchType uint32
	ValueType uint32
	Uint32    uint32
	Blob      []byte
}

type wfpAPI interface {
	ApplicationID(string) ([]byte, error)
	OpenSession(*fwpmSession0) (windows.Handle, error)
	AddProvider(windows.Handle, *fwpmProvider0) error
	DeleteProviderByKey(windows.Handle, *windows.GUID) error
	AddSubLayer(windows.Handle, *fwpmSubLayer0) error
	DeleteSubLayerByKey(windows.Handle, *windows.GUID) error
	AddFilter(windows.Handle, *fwpmFilter0) error
	GetFilterByKey(windows.Handle, *windows.GUID) (*fwpmFilter0, error)
	EnumFilters(windows.Handle, *fwpmFilterEnumTemplate0) ([]auditFilterRecord, error)
	DeleteFilterByKey(windows.Handle, *windows.GUID) error
	CloseEngine(windows.Handle) error
}

type windowsWfpEngine struct {
	handle       windows.Handle
	api          wfpAPI
	providerKey  windows.GUID
	subLayerKey  windows.GUID
	filterKeys   []windows.GUID
	applications [][]byte
	closeOnce    sync.Once
	closeErr     error
}

type procWfpAPI struct{}

var fwpuclnt = windows.NewLazySystemDLL("fwpuclnt.dll")
var (
	procFwpmEngineOpen0              = fwpuclnt.NewProc("FwpmEngineOpen0")
	procFwpmProviderAdd0             = fwpuclnt.NewProc("FwpmProviderAdd0")
	procFwpmProviderDeleteByKey0     = fwpuclnt.NewProc("FwpmProviderDeleteByKey0")
	procFwpmSubLayerAdd0             = fwpuclnt.NewProc("FwpmSubLayerAdd0")
	procFwpmSubLayerDeleteByKey0     = fwpuclnt.NewProc("FwpmSubLayerDeleteByKey0")
	procFwpmFilterAdd0               = fwpuclnt.NewProc("FwpmFilterAdd0")
	procFwpmFilterGetByKey0          = fwpuclnt.NewProc("FwpmFilterGetByKey0")
	procFwpmFilterCreateEnumHandle0  = fwpuclnt.NewProc("FwpmFilterCreateEnumHandle0")
	procFwpmFilterEnum0              = fwpuclnt.NewProc("FwpmFilterEnum0")
	procFwpmFilterDestroyEnumHandle0 = fwpuclnt.NewProc("FwpmFilterDestroyEnumHandle0")
	procFwpmFilterDeleteByKey0       = fwpuclnt.NewProc("FwpmFilterDeleteByKey0")
	procFwpmEngineClose0             = fwpuclnt.NewProc("FwpmEngineClose0")
	procFwpmFreeMemory0              = fwpuclnt.NewProc("FwpmFreeMemory0")
	procFwpmGetAppIdFromFileName0    = fwpuclnt.NewProc("FwpmGetAppIdFromFileName0")
)

func defaultWFPEngineFactory() (wfpEngine, error) {
	return openWFPEngineWithAPI(procWfpAPI{}, windows.GenerateGUID)
}

func openWFPEngineWithAPI(api wfpAPI, newGUID func() (windows.GUID, error)) (*windowsWfpEngine, error) {
	sessionKey, err := newGUID()
	if err != nil {
		return nil, errors.Join(GuardianStartFailed, err)
	}
	name, err := windows.UTF16PtrFromString("offlineboundary")
	if err != nil {
		return nil, errors.Join(GuardianStartFailed, err)
	}
	session := &fwpmSession0{
		SessionKey: sessionKey,
		DisplayData: fwpmDisplayData0{
			Name: name,
		},
		Flags: fwpmSessionFlagDynamic,
	}
	handle, err := api.OpenSession(session)
	if err != nil {
		return nil, classifyStartError(err)
	}
	return &windowsWfpEngine{handle: handle, api: api}, nil
}

func (engine *windowsWfpEngine) AddOutboundBlockFilters(ctx context.Context, leaseID []byte, applicationPaths []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(applicationPaths) == 0 {
		return GuardianStartFailed
	}
	applicationIDs := make([][]byte, 0, len(applicationPaths))
	seen := make(map[string]struct{}, len(applicationPaths))
	for _, path := range applicationPaths {
		if path == "" {
			return GuardianStartFailed
		}
		applicationID, err := engine.api.ApplicationID(path)
		if err != nil || len(applicationID) == 0 {
			return classifyStartError(err)
		}
		key := string(applicationID)
		if _, duplicate := seen[key]; duplicate {
			return GuardianStartFailed
		}
		seen[key] = struct{}{}
		applicationIDs = append(applicationIDs, append([]byte(nil), applicationID...))
	}
	engine.providerKey = providerKeyForLease(leaseID)
	engine.subLayerKey = subLayerKeyForLease(leaseID)
	provider := newProvider(engine.providerKey)
	if err := engine.api.AddProvider(engine.handle, &provider); err != nil {
		return classifyStartError(err)
	}
	subLayer := newSubLayer(engine.subLayerKey, engine.providerKey)
	if err := engine.api.AddSubLayer(engine.handle, &subLayer); err != nil {
		return classifyStartError(err)
	}
	for _, applicationID := range applicationIDs {
		if err := engine.registerApplicationID(leaseID, applicationID); err != nil {
			return err
		}
	}
	return nil
}

func (engine *windowsWfpEngine) RegisterExecutable(ctx context.Context, leaseID []byte, applicationPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if applicationPath == "" || engine.providerKey == (windows.GUID{}) || engine.subLayerKey == (windows.GUID{}) {
		return GuardianStartFailed
	}
	applicationID, err := engine.api.ApplicationID(applicationPath)
	if err != nil || len(applicationID) == 0 {
		return classifyStartError(err)
	}
	for _, registered := range engine.applications {
		if bytes.Equal(registered, applicationID) {
			return engine.AuditOutboundBlockFilters(ctx, leaseID)
		}
	}
	if err := engine.registerApplicationID(leaseID, applicationID); err != nil {
		return err
	}
	return engine.AuditOutboundBlockFilters(ctx, leaseID)
}

func (engine *windowsWfpEngine) registerApplicationID(leaseID, applicationID []byte) error {
	v4Key, v6Key := filterKeysForApplication(leaseID, applicationID)
	for _, entry := range []struct {
		key   windows.GUID
		layer windows.GUID
	}{{v4Key, fwpmLayerALEAuthConnectV4}, {v6Key, fwpmLayerALEAuthConnectV6}} {
		filter, conditions, blob := newOutboundBlockFilter(
			entry.key, entry.layer, engine.providerKey, engine.subLayerKey, applicationID,
		)
		if err := engine.api.AddFilter(engine.handle, &filter); err != nil {
			return classifyStartError(err)
		}
		runtime.KeepAlive(conditions)
		runtime.KeepAlive(blob)
		engine.filterKeys = append(engine.filterKeys, filter.FilterKey)
	}
	engine.applications = append(engine.applications, append([]byte(nil), applicationID...))
	return nil
}

func (engine *windowsWfpEngine) AuditOutboundBlockFilters(ctx context.Context, leaseID []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	providerKey := providerKeyForLease(leaseID)
	subLayerKey := subLayerKeyForLease(leaseID)
	expected := make(map[windows.GUID]struct {
		key   windows.GUID
		layer windows.GUID
		appID []byte
	}, len(engine.applications)*2)
	for _, applicationID := range engine.applications {
		v4Key, v6Key := filterKeysForApplication(leaseID, applicationID)
		expected[v4Key] = struct {
			key   windows.GUID
			layer windows.GUID
			appID []byte
		}{v4Key, fwpmLayerALEAuthConnectV4, applicationID}
		expected[v6Key] = struct {
			key   windows.GUID
			layer windows.GUID
			appID []byte
		}{v6Key, fwpmLayerALEAuthConnectV6, applicationID}
	}
	filters, err := engine.api.EnumFilters(engine.handle, &fwpmFilterEnumTemplate0{
		ProviderKey: &providerKey,
	})
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return WFPAccessDenied
		}
		return errors.Join(FilterAuditFailed, err)
	}
	if len(filters) != len(expected) {
		return FilterAuditFailed
	}
	remaining := expected
	for _, filter := range filters {
		if !filter.HasProviderKey || filter.ProviderKey != providerKey {
			return FilterAuditFailed
		}
		if filter.SubLayerKey != subLayerKey {
			return FilterAuditFailed
		}
		want, ok := remaining[filter.FilterKey]
		if !ok || filter.LayerKey != want.layer || filter.ActionType != fwpActionBlock || filter.Flags&fwpmFilterFlagPersistent != 0 ||
			!auditConditionsMatch(filter.Conditions, want.appID) {
			return FilterAuditFailed
		}
		delete(remaining, filter.FilterKey)
	}
	return mapEmpty(remaining)
}

func (engine *windowsWfpEngine) Close() error {
	engine.closeOnce.Do(func() {
		var closeErr error
		for _, key := range engine.filterKeys {
			if err := engine.api.DeleteFilterByKey(engine.handle, &key); err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) {
				closeErr = errors.Join(closeErr, err)
			}
		}
		if engine.subLayerKey != (windows.GUID{}) {
			if err := engine.api.DeleteSubLayerByKey(engine.handle, &engine.subLayerKey); err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) {
				closeErr = errors.Join(closeErr, err)
			}
		}
		if engine.providerKey != (windows.GUID{}) {
			if err := engine.api.DeleteProviderByKey(engine.handle, &engine.providerKey); err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) {
				closeErr = errors.Join(closeErr, err)
			}
		}
		if err := engine.api.CloseEngine(engine.handle); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
		if closeErr != nil {
			engine.closeErr = errors.Join(SessionCloseFailed, closeErr)
		}
	})
	return engine.closeErr
}

func newProvider(providerKey windows.GUID) fwpmProvider0 {
	name, _ := windows.UTF16PtrFromString("offlineboundary provider")
	return fwpmProvider0{
		ProviderKey: providerKey,
		DisplayData: fwpmDisplayData0{Name: name},
	}
}

func newSubLayer(subLayerKey, providerKey windows.GUID) fwpmSubLayer0 {
	name, _ := windows.UTF16PtrFromString("offlineboundary sublayer")
	return fwpmSubLayer0{
		SubLayerKey: subLayerKey,
		DisplayData: fwpmDisplayData0{Name: name},
		ProviderKey: &providerKey,
		Weight:      0xffff,
	}
}

func newOutboundBlockFilter(
	filterKey, layerKey, providerKey, subLayerKey windows.GUID,
	applicationID []byte,
) (fwpmFilter0, []fwpmFilterCondition0, fwpByteBlob) {
	name, _ := windows.UTF16PtrFromString("offlineboundary outbound block")
	applicationBlob := fwpByteBlob{Size: uint32(len(applicationID)), Data: &applicationID[0]}
	appIDValue := fwpValue0{Type: fwpByteBlobType}
	*(*unsafe.Pointer)(unsafe.Pointer(&appIDValue.Value)) = unsafe.Pointer(&applicationBlob)
	conditions := []fwpmFilterCondition0{
		{
			FieldKey:       fwpmConditionALEAppID,
			MatchType:      fwpMatchEqual,
			ConditionValue: appIDValue,
		},
		{
			FieldKey:  fwpmConditionFlags,
			MatchType: fwpMatchFlagsNoneSet,
			ConditionValue: fwpValue0{
				Type:  fwpUint32,
				Value: uint64(fwpConditionFlagLoopback),
			},
		},
	}
	return fwpmFilter0{
		FilterKey:           filterKey,
		DisplayData:         fwpmDisplayData0{Name: name},
		ProviderKey:         &providerKey,
		LayerKey:            layerKey,
		SubLayerKey:         subLayerKey,
		NumFilterConditions: uint32(len(conditions)),
		FilterCondition:     &conditions[0],
		Action:              fwpmAction0{Type: fwpActionBlock},
	}, conditions, applicationBlob
}

func filterKeysForApplication(leaseID, applicationID []byte) (windows.GUID, windows.GUID) {
	identity := make([]byte, 0, len(leaseID)+len(applicationID)+1)
	identity = append(identity, leaseID...)
	identity = append(identity, 0)
	identity = append(identity, applicationID...)
	return keyedLeaseGUID(0x41, identity), keyedLeaseGUID(0x61, identity)
}

func providerKeyForLease(leaseID []byte) windows.GUID {
	return keyedLeaseGUID(0x21, leaseID)
}

func subLayerKeyForLease(leaseID []byte) windows.GUID {
	return keyedLeaseGUID(0x31, leaseID)
}

func keyedLeaseGUID(tag byte, leaseID []byte) windows.GUID {
	sum := sha256.Sum256(append([]byte{tag}, leaseID...))
	raw := append([]byte(nil), sum[:16]...)
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return windows.GUID{
		Data1: binary.LittleEndian.Uint32(raw[:4]),
		Data2: binary.LittleEndian.Uint16(raw[4:6]),
		Data3: binary.LittleEndian.Uint16(raw[6:8]),
		Data4: [8]byte{raw[8], raw[9], raw[10], raw[11], raw[12], raw[13], raw[14], raw[15]},
	}
}

func mapEmpty[Value any](value map[windows.GUID]Value) error {
	if len(value) != 0 {
		return FilterAuditFailed
	}
	return nil
}

func newAuditFilterRecord(filter *fwpmFilter0) auditFilterRecord {
	record := auditFilterRecord{
		FilterKey:   filter.FilterKey,
		SubLayerKey: filter.SubLayerKey,
		LayerKey:    filter.LayerKey,
		ActionType:  filter.Action.Type,
		Flags:       filter.Flags,
	}
	if filter.ProviderKey != nil {
		record.HasProviderKey = true
		record.ProviderKey = *filter.ProviderKey
	}
	if filter.NumFilterConditions > 0 && filter.FilterCondition != nil {
		conditions := unsafe.Slice(filter.FilterCondition, filter.NumFilterConditions)
		record.Conditions = make([]auditConditionRecord, 0, len(conditions))
		for _, condition := range conditions {
			copy := auditConditionRecord{
				FieldKey: condition.FieldKey, MatchType: condition.MatchType,
				ValueType: condition.ConditionValue.Type,
			}
			switch condition.ConditionValue.Type {
			case fwpUint32:
				copy.Uint32 = uint32(condition.ConditionValue.Value)
			case fwpByteBlobType:
				blobPointer := *(*unsafe.Pointer)(unsafe.Pointer(&condition.ConditionValue.Value))
				blob := (*fwpByteBlob)(blobPointer)
				if blob != nil && blob.Size > 0 && blob.Data != nil {
					copy.Blob = append([]byte(nil), unsafe.Slice(blob.Data, blob.Size)...)
				}
			}
			record.Conditions = append(record.Conditions, copy)
		}
	}
	return record
}

func auditConditionsMatch(conditions []auditConditionRecord, applicationID []byte) bool {
	if len(conditions) != 2 {
		return false
	}
	appIDSeen := false
	loopbackSeen := false
	for _, condition := range conditions {
		switch {
		case condition.FieldKey == fwpmConditionALEAppID:
			if appIDSeen || condition.MatchType != fwpMatchEqual || condition.ValueType != fwpByteBlobType ||
				!bytes.Equal(condition.Blob, applicationID) {
				return false
			}
			appIDSeen = true
		case condition.FieldKey == fwpmConditionFlags:
			if loopbackSeen || condition.MatchType != fwpMatchFlagsNoneSet || condition.ValueType != fwpUint32 ||
				condition.Uint32 != fwpConditionFlagLoopback {
				return false
			}
			loopbackSeen = true
		default:
			return false
		}
	}
	return appIDSeen && loopbackSeen
}

func classifyStartError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return WFPAccessDenied
	}
	return errors.Join(GuardianStartFailed, err)
}

func (procWfpAPI) ApplicationID(path string) ([]byte, error) {
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var blob *fwpByteBlob
	status, _, _ := procFwpmGetAppIdFromFileName0.Call(
		uintptr(unsafe.Pointer(pathUTF16)),
		uintptr(unsafe.Pointer(&blob)),
	)
	if status != 0 {
		return nil, windows.Errno(status)
	}
	if blob == nil {
		return nil, GuardianStartFailed
	}
	defer procFwpmFreeMemory0.Call(uintptr(unsafe.Pointer(&blob)))
	if blob.Size == 0 || blob.Data == nil {
		return nil, GuardianStartFailed
	}
	return append([]byte(nil), unsafe.Slice(blob.Data, blob.Size)...), nil
}

func (procWfpAPI) OpenSession(session *fwpmSession0) (windows.Handle, error) {
	var handle windows.Handle
	status, _, _ := procFwpmEngineOpen0.Call(
		0,
		rpcCAuthnWinnt,
		0,
		uintptr(unsafe.Pointer(session)),
		uintptr(unsafe.Pointer(&handle)),
	)
	if status != 0 {
		return 0, windows.Errno(status)
	}
	return handle, nil
}

func (procWfpAPI) AddFilter(handle windows.Handle, filter *fwpmFilter0) error {
	status, _, _ := procFwpmFilterAdd0.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(filter)),
		0,
		0,
	)
	if status != 0 {
		return windows.Errno(status)
	}
	return nil
}

func (procWfpAPI) AddProvider(handle windows.Handle, provider *fwpmProvider0) error {
	status, _, _ := procFwpmProviderAdd0.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(provider)),
		0,
	)
	if status != 0 {
		return windows.Errno(status)
	}
	return nil
}

func (procWfpAPI) DeleteProviderByKey(handle windows.Handle, key *windows.GUID) error {
	status, _, _ := procFwpmProviderDeleteByKey0.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(key)),
	)
	if status != 0 {
		return windows.Errno(status)
	}
	return nil
}

func (procWfpAPI) AddSubLayer(handle windows.Handle, subLayer *fwpmSubLayer0) error {
	status, _, _ := procFwpmSubLayerAdd0.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(subLayer)),
		0,
	)
	if status != 0 {
		return windows.Errno(status)
	}
	return nil
}

func (procWfpAPI) DeleteSubLayerByKey(handle windows.Handle, key *windows.GUID) error {
	status, _, _ := procFwpmSubLayerDeleteByKey0.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(key)),
	)
	if status != 0 {
		return windows.Errno(status)
	}
	return nil
}

func (procWfpAPI) GetFilterByKey(handle windows.Handle, key *windows.GUID) (*fwpmFilter0, error) {
	var filter *fwpmFilter0
	status, _, _ := procFwpmFilterGetByKey0.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(key)),
		uintptr(unsafe.Pointer(&filter)),
	)
	if status != 0 {
		return nil, windows.Errno(status)
	}
	if filter == nil {
		return nil, windows.ERROR_NOT_FOUND
	}
	copy := *filter
	procFwpmFreeMemory0.Call(uintptr(unsafe.Pointer(&filter)))
	return &copy, nil
}

func (procWfpAPI) EnumFilters(handle windows.Handle, template *fwpmFilterEnumTemplate0) ([]auditFilterRecord, error) {
	var enumHandle windows.Handle
	status, _, _ := procFwpmFilterCreateEnumHandle0.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(template)),
		uintptr(unsafe.Pointer(&enumHandle)),
	)
	if status != 0 {
		return nil, windows.Errno(status)
	}
	defer procFwpmFilterDestroyEnumHandle0.Call(uintptr(handle), uintptr(enumHandle))

	var entries **fwpmFilter0
	var count uint32
	status, _, _ = procFwpmFilterEnum0.Call(
		uintptr(handle),
		uintptr(enumHandle),
		uintptr(16),
		uintptr(unsafe.Pointer(&entries)),
		uintptr(unsafe.Pointer(&count)),
	)
	if status != 0 {
		return nil, windows.Errno(status)
	}
	if entries == nil || count == 0 {
		return nil, nil
	}
	defer procFwpmFreeMemory0.Call(uintptr(unsafe.Pointer(&entries)))
	views := unsafe.Slice(entries, count)
	result := make([]auditFilterRecord, 0, count)
	for _, item := range views {
		if item != nil {
			result = append(result, newAuditFilterRecord(item))
		}
	}
	return result, nil
}

func (procWfpAPI) DeleteFilterByKey(handle windows.Handle, key *windows.GUID) error {
	status, _, _ := procFwpmFilterDeleteByKey0.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(key)),
	)
	if status != 0 {
		return windows.Errno(status)
	}
	return nil
}

func (procWfpAPI) CloseEngine(handle windows.Handle) error {
	status, _, _ := procFwpmEngineClose0.Call(uintptr(handle))
	if status != 0 {
		return windows.Errno(status)
	}
	return nil
}
