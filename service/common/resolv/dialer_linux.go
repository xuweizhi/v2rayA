//go:build linux
// +build linux

package resolv

import (
	"syscall"

	"github.com/v2rayA/v2rayA/conf"
	"golang.org/x/sys/unix"
)

const fallbackBypassMark = 0x80

func fallbackSocketControl(_, _ string, rawConn syscall.RawConn) error {
	// Lite mode never installs kernel transparent proxy rules.
	if conf.GetEnvironmentConfig().Lite {
		return nil
	}
	_ = rawConn.Control(func(fd uintptr) {
		// Best-effort SO_MARK so fallback DNS bypasses transparent proxy rules
		// when CAP_NET_ADMIN is available, without failing unprivileged callers.
		_ = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, fallbackBypassMark)
	})
	return nil
}
