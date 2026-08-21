package offlineboundary

import "errors"

var (
	ErrUnsupported           = errors.New("offline boundary is unsupported on this platform")
	ToolchainUnavailable     = errors.New("verified toolchain is unavailable")
	WFPAccessDenied          = errors.New("windows filtering platform access denied")
	GuardianStartFailed      = errors.New("offline boundary start failed")
	FilterAuditFailed        = errors.New("offline boundary filter audit failed")
	OwnerIdentityMismatch    = errors.New("offline boundary owner identity mismatch")
	ErrOwnerIdentityMismatch = OwnerIdentityMismatch
	GuardianTimeout          = errors.New("offline boundary guardian timeout")
	SessionCloseFailed       = errors.New("offline boundary session close failed")
)
