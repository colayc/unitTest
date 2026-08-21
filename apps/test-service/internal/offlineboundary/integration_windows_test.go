//go:build windows

package offlineboundary

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wfpIntegrationRequiredEnvironment = "UNIT_TEST_IDE_WFP_INTEGRATION_REQUIRED"
	wfpIntegrationOwnerHelper         = "UNIT_TEST_IDE_WFP_OWNER_HELPER"
	wfpProviderFlagPersistent         = 0x00000001
	wfpSubLayerFlagPersistent         = 0x00000001
)

var (
	procFwpmProviderCreateEnumHandle0  = fwpuclnt.NewProc("FwpmProviderCreateEnumHandle0")
	procFwpmProviderEnum0              = fwpuclnt.NewProc("FwpmProviderEnum0")
	procFwpmProviderDestroyEnumHandle0 = fwpuclnt.NewProc("FwpmProviderDestroyEnumHandle0")
	procFwpmProviderGetByKey0          = fwpuclnt.NewProc("FwpmProviderGetByKey0")
	procFwpmSubLayerGetByKey0          = fwpuclnt.NewProc("FwpmSubLayerGetByKey0")
)

func TestGuardianAccessDeniedFramePreservesCanonicalReason(t *testing.T) {
	session := newScriptedGuardianSession()
	boundary := New(Config{
		ownerVerifier:          funcVerifierForOwner(42, 99),
		guardianFactory:        func(context.Context, OwnerIdentity) (guardianSession, error) { return session, nil },
		guardianReadyTimeout:   time.Second,
		guardianReleaseTimeout: time.Second,
	})

	lease, err := boundary.Start(context.Background(), OwnerIdentity{PID: 42, CreationTime: 99})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	session.pushInbound(guardianFrame{Kind: guardianFrameHello})
	session.pushInbound(guardianFrame{Kind: guardianFrameError, Code: guardianErrorWFPAccessDenied})

	if err := lease.Wait(); !errors.Is(err, WFPAccessDenied) {
		t.Fatalf("Wait() error = %v, want WFPAccessDenied", err)
	}
	session.finish(errors.New("guardian exited after access denial"))
}

func TestGuardianRunReportsWFPAccessDeniedWithoutReady(t *testing.T) {
	session := newScriptedGuardianSession()
	ownerDone := make(chan struct{})
	err := runGuardianLoop(context.Background(), guardianRuntime{
		session: session,
		engineFactory: func() (wfpEngine, error) {
			return nil, WFPAccessDenied
		},
		owner: fakeGuardianOwnerWatcher{done: ownerDone},
	}, OwnerIdentity{PID: 42, CreationTime: 99})
	if !errors.Is(err, WFPAccessDenied) {
		t.Fatalf("runGuardianLoop() error = %v, want WFPAccessDenied", err)
	}
	if frame := session.nextOutbound(t); frame.Kind != guardianFrameHello {
		t.Fatalf("first frame = %#v, want Hello", frame)
	}
	if frame := session.nextOutbound(t); frame.Kind != guardianFrameError || frame.Code != guardianErrorWFPAccessDenied {
		t.Fatalf("second frame = %#v, want WFPAccessDenied error", frame)
	}
}

func TestPrivilegedWindowsWFPDynamicLifecycle(t *testing.T) {
	required := requireWFPIntegrationMode(t)
	probeRealWFPManagement(t, required)

	t.Run("dynamic V4 and V6 filters leave loopback local resources reachable and normal release removes all objects", func(t *testing.T) {
		v4 := listenIntegrationLoopback(t, "tcp4", "127.0.0.1:0", required)
		defer v4.Close() //nolint:errcheck
		v6 := listenIntegrationLoopback(t, "tcp6", "[::1]:0", required)
		defer v6.Close() //nolint:errcheck
		assertIntegrationConnectAllowed(t, "tcp4", v4.Addr().String())
		assertIntegrationConnectAllowed(t, "tcp6", v6.Addr().String())

		leaseID := []byte("task5-real-wfp-1")
		engine, err := defaultWFPEngineFactory()
		if err != nil {
			t.Fatalf("defaultWFPEngineFactory() error after privilege probe = %v", err)
		}
		closed := false
		t.Cleanup(func() {
			if !closed {
				_ = engine.Close()
			}
		})
		application, pathErr := os.Executable()
		if pathErr != nil {
			t.Fatalf("os.Executable() error = %v", pathErr)
		}
		if err := engine.AddOutboundBlockFilters(context.Background(), leaseID, []string{application}); err != nil {
			t.Fatalf("AddOutboundBlockFilters() error after privilege probe = %v", err)
		}
		if err := engine.AuditOutboundBlockFilters(context.Background(), leaseID); err != nil {
			t.Fatalf("AuditOutboundBlockFilters() error = %v", err)
		}
		assertIntegrationConnectAllowed(t, "tcp4", v4.Addr().String())
		assertIntegrationConnectAllowed(t, "tcp6", v6.Addr().String())

		providerKey := providerKeyForLease(leaseID)
		subLayerKey := subLayerKeyForLease(leaseID)
		applicationID, appErr := (procWfpAPI{}).ApplicationID(application)
		if appErr != nil {
			t.Fatalf("ApplicationID() error = %v", appErr)
		}
		v4Key, v6Key := filterKeysForApplication(leaseID, applicationID)
		assertIntegrationObjectsLiveAndDynamic(t, providerKey, subLayerKey, []windows.GUID{v4Key, v6Key})
		if err := engine.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		closed = true

		assertIntegrationObjectsAbsent(t, providerKey, subLayerKey, []windows.GUID{v4Key, v6Key})
		assertIntegrationConnectAllowed(t, "tcp4", v4.Addr().String())
		assertIntegrationConnectAllowed(t, "tcp6", v6.Addr().String())
	})

	guardianPath := buildIntegrationGuardian(t)

	t.Run("guardian crash closes the dynamic session and removes the run keys", func(t *testing.T) {
		assertNoIntegrationProviders(t)
		lease := startIntegrationGuardian(t, guardianPath, OwnerIdentityForIntegration(t, uint32(os.Getpid())))
		providerKey, subLayerKey, filterKeys := waitForIntegrationProvider(t)

		concrete, ok := lease.(*guardianLease)
		if !ok {
			t.Fatalf("lease type = %T, want *guardianLease", lease)
		}
		if err := concrete.session.Kill(); err != nil {
			t.Fatalf("guardian Kill() error = %v", err)
		}
		if err := lease.Wait(); !errors.Is(err, SessionCloseFailed) {
			t.Fatalf("Wait() after guardian crash = %v, want SessionCloseFailed", err)
		}
		waitForIntegrationObjectsAbsent(t, providerKey, subLayerKey, filterKeys)
	})

	t.Run("owner termination closes the guardian session and removes the run keys", func(t *testing.T) {
		assertNoIntegrationProviders(t)
		owner := startIntegrationOwner(t)
		identity := OwnerIdentityForIntegration(t, uint32(owner.Process.Pid))
		lease := startIntegrationGuardian(t, guardianPath, identity)
		providerKey, subLayerKey, filterKeys := waitForIntegrationProvider(t)

		if err := owner.Process.Kill(); err != nil {
			t.Fatalf("owner Kill() error = %v", err)
		}
		if err := owner.Wait(); err == nil {
			t.Fatal("killed owner exited without a process error")
		}
		if err := waitLeaseWithTimeout(lease, 10*time.Second); err != nil {
			t.Fatalf("guardian Wait() after owner termination = %v", err)
		}
		waitForIntegrationObjectsAbsent(t, providerKey, subLayerKey, filterKeys)
	})

	t.Run("creation-time mismatch rejects PID reuse before native side effects", func(t *testing.T) {
		assertNoIntegrationProviders(t)
		identity := OwnerIdentityForIntegration(t, uint32(os.Getpid()))
		identity.CreationTime++
		lease, err := New(Config{GuardianExecutablePath: guardianPath}).Start(context.Background(), identity)
		if lease != nil {
			t.Fatalf("Start() lease = %T, want nil", lease)
		}
		if !errors.Is(err, ErrOwnerIdentityMismatch) {
			t.Fatalf("Start() error = %v, want ErrOwnerIdentityMismatch", err)
		}
		assertNoIntegrationProviders(t)
	})
}

func TestWFPIntegrationOwnerHelper(t *testing.T) {
	if os.Getenv(wfpIntegrationOwnerHelper) != "1" {
		t.Skip("integration owner helper")
	}
	fmt.Fprintln(os.Stdout, "READY")
	select {}
}

func requireWFPIntegrationMode(t *testing.T) bool {
	t.Helper()
	switch value := os.Getenv(wfpIntegrationRequiredEnvironment); value {
	case "", "0":
		return false
	case "1":
		return true
	default:
		t.Fatalf("%s must be empty, 0, or 1; got %q", wfpIntegrationRequiredEnvironment, value)
		return false
	}
}

func probeRealWFPManagement(t *testing.T, required bool) {
	t.Helper()
	engine, err := openWFPEngineWithAPI(integrationTracingWfpAPI{}, windows.GenerateGUID)
	if err == nil {
		application, pathErr := os.Executable()
		if pathErr != nil {
			err = pathErr
		} else {
			err = engine.AddOutboundBlockFilters(context.Background(), []byte("task5-wfp-probe"), []string{application})
		}
		closeErr := engine.Close()
		if err == nil {
			err = closeErr
		}
	}
	if errors.Is(err, WFPAccessDenied) {
		t.Fatalf("WFP integration FAIL after test start: WFPAccessDenied (local and required modes fail closed)")
	}
	if err != nil {
		t.Fatalf("real WFP management probe error = %v", err)
	}
}

type integrationTracingWfpAPI struct{ procWfpAPI }

func (integrationTracingWfpAPI) OpenSession(session *fwpmSession0) (windows.Handle, error) {
	handle, err := (procWfpAPI{}).OpenSession(session)
	if err != nil {
		return 0, fmt.Errorf("FwpmEngineOpen0: %w", err)
	}
	return handle, nil
}

func (integrationTracingWfpAPI) AddProvider(handle windows.Handle, provider *fwpmProvider0) error {
	if err := (procWfpAPI{}).AddProvider(handle, provider); err != nil {
		return fmt.Errorf("FwpmProviderAdd0: %w", err)
	}
	return nil
}

func (integrationTracingWfpAPI) AddSubLayer(handle windows.Handle, subLayer *fwpmSubLayer0) error {
	if err := (procWfpAPI{}).AddSubLayer(handle, subLayer); err != nil {
		return fmt.Errorf("FwpmSubLayerAdd0: %w", err)
	}
	return nil
}

func (integrationTracingWfpAPI) AddFilter(handle windows.Handle, filter *fwpmFilter0) error {
	if err := (procWfpAPI{}).AddFilter(handle, filter); err != nil {
		return fmt.Errorf("FwpmFilterAdd0 layer=%#v: %w", filter.LayerKey, err)
	}
	return nil
}

func (integrationTracingWfpAPI) EnumFilters(handle windows.Handle, template *fwpmFilterEnumTemplate0) ([]auditFilterRecord, error) {
	filters, err := (procWfpAPI{}).EnumFilters(handle, template)
	if err != nil {
		return nil, fmt.Errorf("FwpmFilterEnum0: %w", err)
	}
	return filters, nil
}

func listenIntegrationLoopback(t *testing.T, network, address string, required bool) net.Listener {
	t.Helper()
	listener, err := net.Listen(network, address)
	if err == nil {
		return listener
	}
	if required {
		t.Fatalf("required WFP integration FAIL: %s loopback unavailable: %v", network, err)
	}
	t.Skipf("SKIP: %s loopback unavailable: %v", network, err)
	return nil
}

func assertIntegrationConnectAllowed(t *testing.T, network, address string) {
	t.Helper()
	conn, err := net.DialTimeout(network, address, time.Second)
	if err != nil {
		t.Fatalf("%s connect to local listener after release failed: %v", network, err)
	}
	_ = conn.Close()
}

func assertIntegrationConnectBlocked(t *testing.T, network, address string) {
	t.Helper()
	conn, err := net.DialTimeout(network, address, 750*time.Millisecond)
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil {
		t.Fatalf("%s outbound connect was not blocked", network)
	}
}

func buildIntegrationGuardian(t *testing.T) string {
	t.Helper()
	root := integrationRepositoryRoot(t)
	output := filepath.Join(t.TempDir(), "native-offline-guardian.exe")
	command := exec.Command("go", "build", "-trimpath", "-o", output, "./apps/test-service/cmd/native-offline-guardian")
	command.Dir = root
	command.Env = append(os.Environ(), "GOENV=off", "GOTOOLCHAIN=local")
	if combined, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build native guardian: %v\n%s", err, combined)
	}
	return output
}

func integrationRepositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.work")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root was not found")
		}
		directory = parent
	}
}

func OwnerIdentityForIntegration(t *testing.T, pid uint32) OwnerIdentity {
	t.Helper()
	creationTime, err := OwnerCreationTime(pid)
	if err != nil {
		t.Fatalf("OwnerCreationTime(%d) error = %v", pid, err)
	}
	return OwnerIdentity{PID: pid, CreationTime: creationTime}
}

func startIntegrationGuardian(t *testing.T, guardianPath string, owner OwnerIdentity) Lease {
	t.Helper()
	lease, err := New(Config{GuardianExecutablePath: guardianPath}).Start(context.Background(), owner)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	select {
	case <-lease.Ready():
		return lease
	case <-time.After(10 * time.Second):
		if concrete, ok := lease.(*guardianLease); ok {
			_ = concrete.session.Kill()
		}
		t.Fatal("guardian did not report Ready")
		return nil
	}
}

func startIntegrationOwner(t *testing.T) *exec.Cmd {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestWFPIntegrationOwnerHelper$")
	command.Env = append(os.Environ(), wfpIntegrationOwnerHelper+"=1")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("owner StdoutPipe() error = %v", err)
	}
	if err := command.Start(); err != nil {
		t.Fatalf("owner Start() error = %v", err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		ready <- strings.TrimSpace(line)
	}()
	select {
	case line := <-ready:
		if line != "READY" {
			t.Fatalf("owner readiness = %q, want READY", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owner helper did not become ready")
	}
	return command
}

func waitLeaseWithTimeout(lease Lease, timeout time.Duration) error {
	result := make(chan error, 1)
	go func() { result <- lease.Wait() }()
	select {
	case err := <-result:
		return err
	case <-time.After(timeout):
		return GuardianTimeout
	}
}

type integrationProvider struct {
	key   windows.GUID
	flags uint32
}

func waitForIntegrationProvider(t *testing.T) (windows.GUID, windows.GUID, []windows.GUID) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		providers := integrationProviders(t)
		if len(providers) == 1 {
			provider := providers[0]
			if provider.flags&wfpProviderFlagPersistent != 0 {
				t.Fatalf("provider flags = %#x, unexpectedly persistent", provider.flags)
			}
			handle := openIntegrationObserver(t, 0)
			filters, err := (procWfpAPI{}).EnumFilters(handle, &fwpmFilterEnumTemplate0{ProviderKey: &provider.key})
			_ = (procWfpAPI{}).CloseEngine(handle)
			if err != nil {
				t.Fatalf("EnumFilters() error = %v", err)
			}
			if len(filters) == 2 {
				keys := []windows.GUID{filters[0].FilterKey, filters[1].FilterKey}
				subLayerKey := filters[0].SubLayerKey
				assertIntegrationObjectsLiveAndDynamic(t, provider.key, subLayerKey, keys)
				return provider.key, subLayerKey, keys
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("offlineboundary provider/filter set did not become visible; providers=%d", len(providers))
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func assertNoIntegrationProviders(t *testing.T) {
	t.Helper()
	if providers := integrationProviders(t); len(providers) != 0 {
		t.Fatalf("found %d stale offlineboundary providers before integration run", len(providers))
	}
}

func integrationProviders(t *testing.T) []integrationProvider {
	t.Helper()
	handle := openIntegrationObserver(t, 0)
	defer func() {
		if err := (procWfpAPI{}).CloseEngine(handle); err != nil {
			t.Errorf("observer CloseEngine() error = %v", err)
		}
	}()

	var enumHandle windows.Handle
	status, _, _ := procFwpmProviderCreateEnumHandle0.Call(uintptr(handle), 0, uintptr(unsafe.Pointer(&enumHandle)))
	if status != 0 {
		t.Fatalf("FwpmProviderCreateEnumHandle0 status = %#x", status)
	}
	defer procFwpmProviderDestroyEnumHandle0.Call(uintptr(handle), uintptr(enumHandle)) //nolint:errcheck

	var providers []integrationProvider
	for {
		var entries **fwpmProvider0
		var count uint32
		status, _, _ = procFwpmProviderEnum0.Call(
			uintptr(handle),
			uintptr(enumHandle),
			uintptr(256),
			uintptr(unsafe.Pointer(&entries)),
			uintptr(unsafe.Pointer(&count)),
		)
		if status != 0 {
			t.Fatalf("FwpmProviderEnum0 status = %#x", status)
		}
		if entries == nil || count == 0 {
			break
		}
		views := unsafe.Slice(entries, count)
		for _, provider := range views {
			if provider != nil && windows.UTF16PtrToString(provider.DisplayData.Name) == "offlineboundary provider" {
				providers = append(providers, integrationProvider{key: provider.ProviderKey, flags: provider.Flags})
			}
		}
		procFwpmFreeMemory0.Call(uintptr(unsafe.Pointer(&entries))) //nolint:errcheck
	}
	return providers
}

func assertIntegrationObjectsLiveAndDynamic(t *testing.T, providerKey, subLayerKey windows.GUID, filterKeys []windows.GUID) {
	t.Helper()
	handle := openIntegrationObserver(t, 0)
	defer (procWfpAPI{}).CloseEngine(handle) //nolint:errcheck
	provider, err := integrationGetProvider(handle, providerKey)
	if err != nil {
		t.Fatalf("provider lookup error = %v", err)
	}
	if provider.Flags&wfpProviderFlagPersistent != 0 {
		t.Fatalf("provider flags = %#x, unexpectedly persistent", provider.Flags)
	}
	subLayer, err := integrationGetSubLayer(handle, subLayerKey)
	if err != nil {
		t.Fatalf("sublayer lookup error = %v", err)
	}
	if subLayer.Flags&wfpSubLayerFlagPersistent != 0 {
		t.Fatalf("sublayer flags = %#x, unexpectedly persistent", subLayer.Flags)
	}
	for _, key := range filterKeys {
		filter, err := (procWfpAPI{}).GetFilterByKey(handle, &key)
		if err != nil {
			t.Fatalf("filter lookup error = %v", err)
		}
		if filter.Flags&fwpmFilterFlagPersistent != 0 || filter.Action.Type != fwpActionBlock {
			t.Fatalf("filter %#v flags/action = %#x/%#x", key, filter.Flags, filter.Action.Type)
		}
	}
}

func waitForIntegrationObjectsAbsent(t *testing.T, providerKey, subLayerKey windows.GUID, filterKeys []windows.GUID) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if integrationObjectsAbsent(providerKey, subLayerKey, filterKeys, 0) &&
			integrationObjectsAbsent(providerKey, subLayerKey, filterKeys, fwpmSessionFlagDynamic) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("dynamic WFP objects remained visible after guardian/session termination")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func assertIntegrationObjectsAbsent(t *testing.T, providerKey, subLayerKey windows.GUID, filterKeys []windows.GUID) {
	t.Helper()
	if !integrationObjectsAbsent(providerKey, subLayerKey, filterKeys, 0) {
		t.Fatal("dynamic WFP objects remained in the active engine view")
	}
	if !integrationObjectsAbsent(providerKey, subLayerKey, filterKeys, fwpmSessionFlagDynamic) {
		t.Fatal("dynamic WFP objects remained in a fresh dynamic engine view")
	}
}

func integrationObjectsAbsent(providerKey, subLayerKey windows.GUID, filterKeys []windows.GUID, sessionFlags uint32) bool {
	handle, err := (procWfpAPI{}).OpenSession(&fwpmSession0{Flags: sessionFlags})
	if err != nil {
		return false
	}
	defer (procWfpAPI{}).CloseEngine(handle) //nolint:errcheck
	if _, err := integrationGetProvider(handle, providerKey); !integrationNotFound(err, 0x80320005) {
		return false
	}
	if _, err := integrationGetSubLayer(handle, subLayerKey); !integrationNotFound(err, 0x80320007) {
		return false
	}
	for _, key := range filterKeys {
		if _, err := (procWfpAPI{}).GetFilterByKey(handle, &key); !integrationNotFound(err, 0x80320003) {
			return false
		}
	}
	return true
}

func integrationNotFound(err error, wfpStatus uintptr) bool {
	return errors.Is(err, windows.ERROR_NOT_FOUND) || errors.Is(err, windows.Errno(wfpStatus))
}

func openIntegrationObserver(t *testing.T, flags uint32) windows.Handle {
	t.Helper()
	handle, err := (procWfpAPI{}).OpenSession(&fwpmSession0{Flags: flags})
	if err != nil {
		t.Fatalf("observer OpenSession() error = %v", err)
	}
	return handle
}

func integrationGetProvider(handle windows.Handle, key windows.GUID) (*fwpmProvider0, error) {
	var provider *fwpmProvider0
	status, _, _ := procFwpmProviderGetByKey0.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&key)),
		uintptr(unsafe.Pointer(&provider)),
	)
	if status != 0 {
		return nil, windows.Errno(status)
	}
	if provider == nil {
		return nil, windows.ERROR_NOT_FOUND
	}
	copy := *provider
	procFwpmFreeMemory0.Call(uintptr(unsafe.Pointer(&provider))) //nolint:errcheck
	return &copy, nil
}

func integrationGetSubLayer(handle windows.Handle, key windows.GUID) (*fwpmSubLayer0, error) {
	var subLayer *fwpmSubLayer0
	status, _, _ := procFwpmSubLayerGetByKey0.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&key)),
		uintptr(unsafe.Pointer(&subLayer)),
	)
	if status != 0 {
		return nil, windows.Errno(status)
	}
	if subLayer == nil {
		return nil, windows.ERROR_NOT_FOUND
	}
	copy := *subLayer
	procFwpmFreeMemory0.Call(uintptr(unsafe.Pointer(&subLayer))) //nolint:errcheck
	return &copy, nil
}
