//go:build !linux
// +build !linux

package httpClient

import "syscall"

func directSocketControl(_, _ string, _ syscall.RawConn) error {
	return nil
}
