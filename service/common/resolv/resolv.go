package resolv

import (
	"os"
	"runtime"
	"strings"

	"github.com/v2rayA/v2rayA/conf"
)

const resolvConf = "/etc/resolv.conf"

func WriteResolvConf(servers []string) {
	if runtime.GOOS != "linux" {
		return
	}
	var sb strings.Builder
	for _, server := range servers {
		sb.WriteString("nameserver " + server + "\n")
	}
	os.WriteFile(resolvConf, []byte(sb.String()), 0644)
}

func CheckResolvConf() {
	if runtime.GOOS != "linux" {
		return
	}
	if conf.GetEnvironmentConfig().Lite {
		return
	}
	// A dangling resolver-manager symlink still belongs to its owner.
	// Exclusive creation also leaves a concurrently installed resolver alone.
	f, err := os.OpenFile(resolvConf, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err == nil {
		_, _ = f.WriteString("nameserver 223.6.6.6\n")
		_ = f.Close()
	}
}
