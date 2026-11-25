//go:build darwin || dragonfly || freebsd || netbsd || openbsd
// +build darwin dragonfly freebsd netbsd openbsd

package ipforward

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

func IsIpForwardOn() bool {
	val, err := syscall.SysctlUint32("net.inet.ip.forwarding")
	return err == nil && val == 1
}

func WriteIpForward(on bool) (err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("WriteIpForward: %w", err)
		}
	}()
	val := "0"
	if on {
		val = "1"
	}
	out, err := exec.Command("sysctl", "-w", "net.inet.ip.forwarding="+val).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
