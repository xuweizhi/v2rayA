//go:build windows
// +build windows

package privilege

import (
	"golang.org/x/sys/windows"
)

// IsRootOrAdmin reports whether the current process runs with elevated
// privileges (Administrators or LocalSystem).
func IsRootOrAdmin() bool {
	token := windows.GetCurrentProcessToken()
	if token.IsElevated() {
		return true
	}

	if systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid); err == nil {
		if tokenUser, err := token.GetTokenUser(); err == nil && tokenUser != nil && tokenUser.User.Sid != nil && tokenUser.User.Sid.Equals(systemSID) {
			return true
		}
	}

	return false
}
