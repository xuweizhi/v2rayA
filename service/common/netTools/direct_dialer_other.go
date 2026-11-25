//go:build !linux
// +build !linux

package netTools

import "syscall"

func DirectSocketControl(_, _ string, _ syscall.RawConn) error {
	return nil
}
