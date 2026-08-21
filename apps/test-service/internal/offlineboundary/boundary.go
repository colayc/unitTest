package offlineboundary

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"path/filepath"
	"time"
)

type wfpEngine interface {
	AddOutboundBlockFilters(context.Context, []byte) error
	AuditOutboundBlockFilters(context.Context, []byte) error
	Close() error
}

type guardianSession interface {
	Receive(context.Context) (guardianFrame, error)
	Send(context.Context, guardianFrame) error
	Wait() error
	Close() error
	Kill() error
}

type guardianOwnerVerifier interface {
	Verify(OwnerIdentity) error
}

// Config configures OfflineBoundary construction.
//
// On Windows, GuardianExecutablePath controls how the native guardian binary is
// located. An empty GuardianExecutablePath uses only sibling discovery for
// `native-offline-guardian.exe` beside the current executable. A non-empty
// GuardianExecutablePath is used exactly as provided by the caller with no
// fallback search. If guardian startup still fails after path resolution,
// Start returns only the canonical GuardianStartFailed sentinel and does not
// expose the attempted path.
type Config struct {
	engineFactory func() (wfpEngine, error)
	leaseIDSource func() []byte
	ownerVerifier guardianOwnerVerifier
	// GuardianExecutablePath selects the native guardian binary on Windows.
	//
	// Empty means: use only sibling discovery for `native-offline-guardian.exe`
	// beside the current executable.
	//
	// Non-empty means: use exactly this caller-supplied path with no fallback.
	//
	// If guardian startup fails after resolution, Start returns only the
	// canonical GuardianStartFailed sentinel and does not leak the resolved path.
	GuardianExecutablePath string
	guardianFactory        func(context.Context, OwnerIdentity) (guardianSession, error)
	guardianReadyTimeout   time.Duration
	guardianReleaseTimeout time.Duration
}

type boundary struct {
	engineFactory          func() (wfpEngine, error)
	leaseIDSource          func() []byte
	ownerVerifier          guardianOwnerVerifier
	guardianExecutablePath string
	guardianFactory        func(context.Context, OwnerIdentity) (guardianSession, error)
	guardianReadyTimeout   time.Duration
	guardianReleaseTimeout time.Duration
}

func New(config Config) OfflineBoundary {
	return &boundary{
		engineFactory:          config.engineFactory,
		leaseIDSource:          config.leaseIDSource,
		ownerVerifier:          config.ownerVerifier,
		guardianExecutablePath: config.GuardianExecutablePath,
		guardianFactory:        config.guardianFactory,
		guardianReadyTimeout:   config.guardianReadyTimeout,
		guardianReleaseTimeout: config.guardianReleaseTimeout,
	}
}

// ResolveGuardianExecutablePath resolves the Windows guardian executable path
// from Config using the provided current-executable resolver seam.
//
// Empty Config.GuardianExecutablePath uses only sibling discovery for
// `native-offline-guardian.exe` beside the current executable returned by
// currentExecutable.
//
// Non-empty Config.GuardianExecutablePath is returned exactly as provided and
// currentExecutable is not called.
func ResolveGuardianExecutablePath(config Config, currentExecutable func() (string, error)) (string, error) {
	if config.GuardianExecutablePath != "" {
		return config.GuardianExecutablePath, nil
	}
	if currentExecutable == nil {
		return "", ErrUnsupported
	}
	executable, err := currentExecutable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(executable), "native-offline-guardian.exe"), nil
}

func validateOwnerIdentity(owner OwnerIdentity) error {
	if owner.PID == 0 || owner.CreationTime == 0 {
		return ErrOwnerIdentityMismatch
	}
	return nil
}

func newLeaseID() []byte {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err == nil {
		return buffer
	}
	binary.LittleEndian.PutUint64(buffer[:8], uint64(1))
	binary.LittleEndian.PutUint64(buffer[8:], uint64(2))
	return buffer
}
