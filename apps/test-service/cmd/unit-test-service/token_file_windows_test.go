//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func prepareTokenFileForTest(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	return setTokenFileOwnerAndACL(path, user.User.Sid, []windows.EXPLICIT_ACCESS{allowSID(user.User.Sid, windows.GENERIC_ALL)})
}

func TestPreparedTokenFileIsOwnedByCurrentUser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token-owner")
	if err := os.WriteFile(path, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareTokenFileForTest(path); err != nil {
		t.Fatal(err)
	}

	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if owner == nil || !owner.Equals(user.User.Sid) {
		t.Fatalf("token owner = %v, want current user %v", owner, user.User.Sid)
	}
}

func TestValidateOwnerOnlyDACLAcceptsWellKnownAliasForOwner(t *testing.T) {
	owner := ownerSIDFromSDDL(t, "LA")
	if err := validateOwnerOnlyDACL("D:P(A;;GA;;;LA)", owner); err != nil {
		t.Fatal(err)
	}
}

func TestValidateOwnerOnlyDACLRejectsEveryoneAlias(t *testing.T) {
	owner := ownerSIDFromSDDL(t, "LA")
	if err := validateOwnerOnlyDACL("D:P(A;;GA;;;WD)", owner); err == nil {
		t.Fatal("expected DACL granting Everyone access to be rejected")
	}
}

func ownerSIDFromSDDL(t *testing.T, trustee string) *windows.SID {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString("O:" + trustee)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		t.Fatal(err)
	}
	if owner == nil {
		t.Fatal("security descriptor owner is missing")
	}
	return owner
}

func TestConsumeTokenFileRejectsACLThatGrantsAnotherPrincipal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	if err := setTokenFileACL(path, []windows.EXPLICIT_ACCESS{
		allowSID(user.User.Sid, windows.GENERIC_ALL),
		allowSID(everyone, windows.GENERIC_READ),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := consumeTokenFile(path); err == nil {
		t.Fatal("expected token ACL granting another principal to be rejected")
	}
	assertRemoved(t, path)
}

func allowSID(sid *windows.SID, permissions windows.ACCESS_MASK) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: permissions,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}

func setTokenFileACL(path string, entries []windows.EXPLICIT_ACCESS) error {
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	)
}

func setTokenFileOwnerAndACL(path string, owner *windows.SID, entries []windows.EXPLICIT_ACCESS) error {
	ownerErr := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION,
		owner,
		nil,
		nil,
		nil,
	)
	if aclErr := setTokenFileACL(path, entries); aclErr != nil {
		return errors.Join(ownerErr, aclErr)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return errors.Join(ownerErr, err)
	}
	actual, _, err := sd.Owner()
	if err != nil {
		return errors.Join(ownerErr, err)
	}
	if actual == nil || !actual.Equals(owner) {
		return errors.Join(ownerErr, fmt.Errorf("token owner = %v, want current user %v", actual, owner))
	}
	return nil
}
