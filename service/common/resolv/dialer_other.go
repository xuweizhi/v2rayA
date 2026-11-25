//go:build !linux
// +build !linux

package resolv

import "syscall"

func fallbackSocketControl(_, _ string, _ syscall.RawConn) error {
	return nil
}
