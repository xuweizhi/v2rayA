//go:build linux
// +build linux

package httpClient

import (
	"syscall"

	"golang.org/x/sys/unix"
)

const directBypassMark = 0x80

func directSocketControl(_, _ string, connection syscall.RawConn) error {
	var socketErr error
	if err := connection.Control(func(fd uintptr) {
		socketErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, directBypassMark)
	}); err != nil {
		return err
	}
	return socketErr
}
