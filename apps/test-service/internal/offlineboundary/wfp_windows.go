//go:build windows

package offlineboundary

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	fwpmSessionFlagDynamic   = 0x00000001
	fwpmFilterFlagPersistent = 0x00000001
	fwpActionBlock           = 0x00000001 | 0x00001000
)

var (
	fwpmLayerALEAuthConnectV4 = windows.GUID{Data1: 0xc38d57d1, Data2: 0x05a7, Data3: 0x4c33, Data4: [8]byte{0x90, 0x4f, 0x7f, 0xbc, 0xee, 0xe6, 0x0e, 0x82}}
	fwpmLayerALEAuthConnectV6 = windows.GUID{Data1: 0x4a72393b, Data2: 0x319f, Data3: 0x44bc, Data4: [8]byte{0x84, 0xc3, 0xba, 0x54, 0xdc, 0xb3, 0xb6, 0xb4}}
	fwpmSubLayerUniversal     = windows.GUID{Data1: 0xeebecc03, Data2: 0xced4, Data3: 0x4380, Data4: [8]byte{0x81, 0x9a, 0x27, 0x34, 0x39, 0x7b, 0x2b, 0x74}}
)

type boundaryLease struct {
	ready     chan struct{}
	done      chan struct{}
	engine    wfpEngine
	closeOnce sync.Once
	closeErr  error
}

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

type fwpByteBlob struct {
	Size uint32
	Data *byte
}

type fwpValue0 struct {
	Type  uint32
	Value uintptr
}

type fwpmAction0 struct {
	Type       uint32
	FilterType windows.GUID
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
	FilterCondition     uintptr
	Action              fwpmAction0
	ProviderContextKey  windows.GUID
	Reserved            *windows.GUID
	FilterID            uint64
	EffectiveWeight     fwpValue0
}

type wfpAPI interface {
	OpenSession(*fwpmSession0) (windows.Handle, error)
	AddFilter(windows.Handle, *fwpmFilter0) error
	GetFilterByKey(windows.Handle, *windows.GUID) (*fwpmFilter0, error)
	DeleteFilterByKey(windows.Handle, *windows.GUID) error
	CloseEngine(windows.Handle) error
}

type windowsWfpEngine struct {
	handle     windows.Handle
	api        wfpAPI
	filterKeys []windows.GUID
	closeOnce  sync.Once
	closeErr   error
}

type procWfpAPI struct{}

var fwpuclnt = windows.NewLazySystemDLL("fwpuclnt.dll")
var (
	procFwpmEngineOpen0        = fwpuclnt.NewProc("FwpmEngineOpen0")
	procFwpmFilterAdd0         = fwpuclnt.NewProc("FwpmFilterAdd0")
	procFwpmFilterGetByKey0    = fwpuclnt.NewProc("FwpmFilterGetByKey0")
	procFwpmFilterDeleteByKey0 = fwpuclnt.NewProc("FwpmFilterDeleteByKey0")
	procFwpmEngineClose0       = fwpuclnt.NewProc("FwpmEngineClose0")
	procFwpmFreeMemory0        = fwpuclnt.NewProc("FwpmFreeMemory0")
)

func (boundary *boundary) Start(ctx context.Context, owner OwnerIdentity) (Lease, error) {
	if err := validateOwnerIdentity(owner); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	engineFactory := boundary.engineFactory
	if engineFactory == nil {
		engineFactory = defaultWFPEngineFactory
	}
	engine, err := engineFactory()
	if err != nil {
		return nil, err
	}

	leaseIDSource := boundary.leaseIDSource
	if leaseIDSource == nil {
		leaseIDSource = newLeaseID
	}
	leaseID := append([]byte(nil), leaseIDSource()...)
	if len(leaseID) == 0 {
		leaseID = newLeaseID()
	}

	if err := engine.AddOutboundBlockFilters(ctx, leaseID); err != nil {
		_ = engine.Close()
		return nil, err
	}
	if err := engine.AuditOutboundBlockFilters(ctx, leaseID); err != nil {
		_ = engine.Close()
		return nil, err
	}

	ready := make(chan struct{})
	close(ready)
	return &boundaryLease{
		ready:  ready,
		done:   make(chan struct{}),
		engine: engine,
	}, nil
}

func (lease *boundaryLease) Ready() <-chan struct{} { return lease.ready }

func (lease *boundaryLease) Close() error {
	lease.closeOnce.Do(func() {
		defer close(lease.done)
		if lease.engine != nil {
			lease.closeErr = lease.engine.Close()
		}
	})
	return lease.closeErr
}

func (lease *boundaryLease) Wait() error {
	<-lease.done
	return lease.closeErr
}

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

func (engine *windowsWfpEngine) AddOutboundBlockFilters(ctx context.Context, leaseID []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	v4Key, v6Key := filterKeysForLease(leaseID)
	filters := []fwpmFilter0{
		newOutboundBlockFilter(v4Key, fwpmLayerALEAuthConnectV4),
		newOutboundBlockFilter(v6Key, fwpmLayerALEAuthConnectV6),
	}
	for _, filter := range filters {
		filterCopy := filter
		if err := engine.api.AddFilter(engine.handle, &filterCopy); err != nil {
			return classifyStartError(err)
		}
		engine.filterKeys = append(engine.filterKeys, filterCopy.FilterKey)
	}
	return nil
}

func (engine *windowsWfpEngine) AuditOutboundBlockFilters(ctx context.Context, leaseID []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	v4Key, v6Key := filterKeysForLease(leaseID)
	expected := []struct {
		key   windows.GUID
		layer windows.GUID
	}{
		{key: v4Key, layer: fwpmLayerALEAuthConnectV4},
		{key: v6Key, layer: fwpmLayerALEAuthConnectV6},
	}
	seen := map[windows.GUID]struct{}{}
	for _, want := range expected {
		filter, err := engine.api.GetFilterByKey(engine.handle, &want.key)
		if err != nil {
			if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				return WFPAccessDenied
			}
			return errors.Join(FilterAuditFailed, err)
		}
		if filter == nil || filter.FilterKey != want.key || filter.LayerKey != want.layer || filter.Action.Type != fwpActionBlock || filter.Flags&fwpmFilterFlagPersistent != 0 {
			return FilterAuditFailed
		}
		if _, duplicate := seen[filter.FilterKey]; duplicate {
			return FilterAuditFailed
		}
		seen[filter.FilterKey] = struct{}{}
	}
	return nil
}

func (engine *windowsWfpEngine) Close() error {
	engine.closeOnce.Do(func() {
		var closeErr error
		for _, key := range engine.filterKeys {
			if err := engine.api.DeleteFilterByKey(engine.handle, &key); err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) {
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

func newOutboundBlockFilter(filterKey, layerKey windows.GUID) fwpmFilter0 {
	name, _ := windows.UTF16PtrFromString("offlineboundary outbound block")
	return fwpmFilter0{
		FilterKey:   filterKey,
		DisplayData: fwpmDisplayData0{Name: name},
		LayerKey:    layerKey,
		SubLayerKey: fwpmSubLayerUniversal,
		Action:      fwpmAction0{Type: fwpActionBlock},
	}
}

func filterKeysForLease(leaseID []byte) (windows.GUID, windows.GUID) {
	return keyedLeaseGUID(0x41, leaseID), keyedLeaseGUID(0x61, leaseID)
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

func classifyStartError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return WFPAccessDenied
	}
	return errors.Join(GuardianStartFailed, err)
}

func (procWfpAPI) OpenSession(session *fwpmSession0) (windows.Handle, error) {
	var handle windows.Handle
	status, _, _ := procFwpmEngineOpen0.Call(
		0,
		0,
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
