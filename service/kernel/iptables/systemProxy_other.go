//go:build !windows && !darwin
// +build !windows,!darwin

package iptables

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/pkg/util/log"
)

type systemProxy struct{}

var SystemProxy systemProxy

// linuxGsettingsState holds saved GNOME/gsettings proxy values
type linuxGsettingsState struct {
	mode     string
	httpHost string
	httpPort string
	httpsHost string
	httpsPort string
	socksHost string
	socksPort string
}

// linuxKDEState holds saved KDE proxy values
type linuxKDEState struct {
	proxyType  string
	httpProxy  string
	httpsProxy string
	socksProxy string
}

// linuxProxySavedState stores original proxy state before v2rayA modifies it
type linuxProxySavedState struct {
	mu        sync.Mutex
	saved     bool
	gsettings linuxGsettingsState
	kde       linuxKDEState
}

var savedLinuxProxy linuxProxySavedState

func (p *systemProxy) AddIPWhitelist(cidr string) {}

func (p *systemProxy) RemoveIPWhitelist(cidr string) {}

// checkCommand checks if a command exists
var checkCommand = func(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func appendKDENotifyCmd(commands *strings.Builder) {
	if checkCommand("dbus-send") {
		commands.WriteString("dbus-send --type=signal /KIO/Scheduler org.kde.KIO.Scheduler.reparseSlaveConfiguration string:'' 2>/dev/null || true\n")
	}
}

// readGsettingsValue runs "gsettings get <schema> <key>" and returns the trimmed output
func readGsettingsValue(schema, key string) string {
	out, err := exec.Command("gsettings", "get", schema, key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// readKDEConfigValue reads a KDE config value using kreadconfig5/6 or grep fallback
func readKDEConfigValue(configFile, group, key string) string {
	// Try kreadconfig6 first, then kreadconfig5
	for _, cmd := range []string{"kreadconfig6", "kreadconfig5"} {
		if checkCommand(cmd) {
			out, err := exec.Command(cmd, "--file", configFile, "--group", group, "--key", key).Output()
			if err == nil {
				return strings.TrimSpace(string(out))
			}
		}
	}
	return ""
}

// saveGsettingsState saves current GNOME proxy settings
func saveGsettingsState(state *linuxGsettingsState) {
	if !checkCommand("gsettings") {
		return
	}
	state.mode = readGsettingsValue("org.gnome.system.proxy", "mode")
	state.httpHost = readGsettingsValue("org.gnome.system.proxy.http", "host")
	state.httpPort = readGsettingsValue("org.gnome.system.proxy.http", "port")
	state.httpsHost = readGsettingsValue("org.gnome.system.proxy.https", "host")
	state.httpsPort = readGsettingsValue("org.gnome.system.proxy.https", "port")
	state.socksHost = readGsettingsValue("org.gnome.system.proxy.socks", "host")
	state.socksPort = readGsettingsValue("org.gnome.system.proxy.socks", "port")
}

// saveKDEState saves current KDE proxy settings
func saveKDEState(state *linuxKDEState) {
	configFile := "kioslaverc"
	group := "Proxy Settings"
	state.proxyType = readKDEConfigValue(configFile, group, "ProxyType")
	state.httpProxy = readKDEConfigValue(configFile, group, "httpProxy")
	state.httpsProxy = readKDEConfigValue(configFile, group, "httpsProxy")
	state.socksProxy = readKDEConfigValue(configFile, group, "socksProxy")
}

func (p *systemProxy) GetSetupCommands() Setter {
	// Only support Linux in lite mode
	if runtime.GOOS != "linux" {
		return NewErrorSetter(fmt.Errorf("does not support to configure system proxy on your OS"))
	}

	if !conf.GetEnvironmentConfig().Lite {
		return NewErrorSetter(fmt.Errorf("system proxy is only supported in lite mode on Linux"))
	}

	var commands strings.Builder
	hasGsettings := false
	hasKDE := false

	// Try gsettings for GNOME-based desktops
	if checkCommand("gsettings") {
		hasGsettings = true
		commands.WriteString("gsettings set org.gnome.system.proxy mode 'manual'\n")
		commands.WriteString("gsettings set org.gnome.system.proxy.http host '127.0.0.1'\n")
		commands.WriteString("gsettings set org.gnome.system.proxy.http port 52345\n")
		commands.WriteString("gsettings set org.gnome.system.proxy.https host '127.0.0.1'\n")
		commands.WriteString("gsettings set org.gnome.system.proxy.https port 52345\n")
		commands.WriteString("gsettings set org.gnome.system.proxy.socks host '127.0.0.1'\n")
		commands.WriteString("gsettings set org.gnome.system.proxy.socks port 52306\n")
		log.Info("Using gsettings to configure system proxy (HTTP: 52345, SOCKS: 52306)")
	} else {
		log.Warn("gsettings command not found. GNOME-based applications may not use the system proxy. Please install gsettings if you are using GNOME desktop.")
	}

	// Try kwriteconfig6 for KDE Plasma 6
	if checkCommand("kwriteconfig6") {
		hasKDE = true
		commands.WriteString("kwriteconfig6 --file kioslaverc --group 'Proxy Settings' --key ProxyType 1\n")
		commands.WriteString("kwriteconfig6 --file kioslaverc --group 'Proxy Settings' --key httpProxy 'http://127.0.0.1:52345'\n")
		commands.WriteString("kwriteconfig6 --file kioslaverc --group 'Proxy Settings' --key httpsProxy 'http://127.0.0.1:52345'\n")
		commands.WriteString("kwriteconfig6 --file kioslaverc --group 'Proxy Settings' --key socksProxy 'socks://127.0.0.1:52306'\n")
		// Notify KDE about the change (best-effort; do not abort setup if dbus-send is unavailable)
		appendKDENotifyCmd(&commands)
		log.Info("Using kwriteconfig6 to configure system proxy (HTTP: 52345, SOCKS: 52306)")
	}

	// Try kwriteconfig5 for KDE Plasma 5
	if checkCommand("kwriteconfig5") {
		hasKDE = true
		commands.WriteString("kwriteconfig5 --file kioslaverc --group 'Proxy Settings' --key ProxyType 1\n")
		commands.WriteString("kwriteconfig5 --file kioslaverc --group 'Proxy Settings' --key httpProxy 'http://127.0.0.1:52345'\n")
		commands.WriteString("kwriteconfig5 --file kioslaverc --group 'Proxy Settings' --key httpsProxy 'http://127.0.0.1:52345'\n")
		commands.WriteString("kwriteconfig5 --file kioslaverc --group 'Proxy Settings' --key socksProxy 'socks://127.0.0.1:52306'\n")
		// Notify KDE about the change (best-effort; do not abort setup if dbus-send is unavailable)
		appendKDENotifyCmd(&commands)
		log.Info("Using kwriteconfig5 to configure system proxy (HTTP: 52345, SOCKS: 52306)")
	}

	if !hasKDE {
		log.Warn("kwriteconfig5/kwriteconfig6 commands not found. KDE applications may not use the system proxy. Please install kwriteconfig if you are using KDE Plasma desktop.")
	}

	if !hasGsettings && !hasKDE {
		return NewErrorSetter(fmt.Errorf("no supported desktop environment found. Please install gsettings (GNOME), kwriteconfig6 (KDE 6), or kwriteconfig5 (KDE 5)"))
	}

	return Setter{
		PreFunc: func() error {
			savedLinuxProxy.mu.Lock()
			defer savedLinuxProxy.mu.Unlock()

			if hasGsettings {
				saveGsettingsState(&savedLinuxProxy.gsettings)
			}
			if hasKDE {
				saveKDEState(&savedLinuxProxy.kde)
			}
			savedLinuxProxy.saved = true
			return nil
		},
		Cmds: commands.String(),
	}
}

func (p *systemProxy) GetCleanCommands() Setter {
	// Only support Linux in lite mode
	if runtime.GOOS != "linux" {
		return Setter{}
	}

	if !conf.GetEnvironmentConfig().Lite {
		return Setter{}
	}

	savedLinuxProxy.mu.Lock()
	saved := savedLinuxProxy.saved
	gs := savedLinuxProxy.gsettings
	kde := savedLinuxProxy.kde
	savedLinuxProxy.mu.Unlock()

	if !saved {
		// No saved state: fall back to original behavior (disable proxy)
		var commands strings.Builder

		if checkCommand("gsettings") {
			commands.WriteString("gsettings set org.gnome.system.proxy mode 'none'\n")
			log.Info("Disabling system proxy via gsettings")
		}

		if checkCommand("kwriteconfig6") {
			commands.WriteString("kwriteconfig6 --file kioslaverc --group 'Proxy Settings' --key ProxyType 0\n")
			appendKDENotifyCmd(&commands)
			log.Info("Disabling system proxy via kwriteconfig6")
		}

		if checkCommand("kwriteconfig5") {
			commands.WriteString("kwriteconfig5 --file kioslaverc --group 'Proxy Settings' --key ProxyType 0\n")
			appendKDENotifyCmd(&commands)
			log.Info("Disabling system proxy via kwriteconfig5")
		}

		return Setter{
			Cmds: commands.String(),
		}
	}

	// Restore original state
	var commands strings.Builder

	// Restore GNOME/gsettings
	if checkCommand("gsettings") && gs.mode != "" {
		commands.WriteString(fmt.Sprintf("gsettings set org.gnome.system.proxy mode %v\n", gs.mode))
		if gs.httpHost != "" {
			commands.WriteString(fmt.Sprintf("gsettings set org.gnome.system.proxy.http host %v\n", gs.httpHost))
		}
		if gs.httpPort != "" {
			commands.WriteString(fmt.Sprintf("gsettings set org.gnome.system.proxy.http port %v\n", gs.httpPort))
		}
		if gs.httpsHost != "" {
			commands.WriteString(fmt.Sprintf("gsettings set org.gnome.system.proxy.https host %v\n", gs.httpsHost))
		}
		if gs.httpsPort != "" {
			commands.WriteString(fmt.Sprintf("gsettings set org.gnome.system.proxy.https port %v\n", gs.httpsPort))
		}
		if gs.socksHost != "" {
			commands.WriteString(fmt.Sprintf("gsettings set org.gnome.system.proxy.socks host %v\n", gs.socksHost))
		}
		if gs.socksPort != "" {
			commands.WriteString(fmt.Sprintf("gsettings set org.gnome.system.proxy.socks port %v\n", gs.socksPort))
		}
		log.Info("Restoring original system proxy via gsettings")
	} else if checkCommand("gsettings") {
		// gsettings exists but no saved state for it: disable
		commands.WriteString("gsettings set org.gnome.system.proxy mode 'none'\n")
		log.Info("Disabling system proxy via gsettings (no saved state)")
	}

	// Restore KDE
	if kde.proxyType != "" {
		restoreKDE := func(cmd string) {
			commands.WriteString(fmt.Sprintf("%s --file kioslaverc --group 'Proxy Settings' --key ProxyType %s\n", cmd, shellSingleQuote(kde.proxyType)))
			restoreKey := func(key, val string) {
				if val != "" {
					commands.WriteString(fmt.Sprintf("%s --file kioslaverc --group 'Proxy Settings' --key %s %s\n", cmd, key, shellSingleQuote(val)))
				} else if kde.proxyType == "1" {
					commands.WriteString(fmt.Sprintf("%s --file kioslaverc --group 'Proxy Settings' --key %s --delete\n", cmd, key))
				}
			}
			restoreKey("httpProxy", kde.httpProxy)
			restoreKey("httpsProxy", kde.httpsProxy)
			restoreKey("socksProxy", kde.socksProxy)
			appendKDENotifyCmd(&commands)
		}

		if checkCommand("kwriteconfig6") {
			restoreKDE("kwriteconfig6")
			log.Info("Restoring original system proxy via kwriteconfig6")
		}
		if checkCommand("kwriteconfig5") {
			restoreKDE("kwriteconfig5")
			log.Info("Restoring original system proxy via kwriteconfig5")
		}
	} else {
		// No saved KDE state: disable KDE proxy if tooling is available
		if checkCommand("kwriteconfig6") {
			commands.WriteString("kwriteconfig6 --file kioslaverc --group 'Proxy Settings' --key ProxyType 0\n")
			appendKDENotifyCmd(&commands)
			log.Info("Disabling system proxy via kwriteconfig6 (no saved state)")
		}
		if checkCommand("kwriteconfig5") {
			commands.WriteString("kwriteconfig5 --file kioslaverc --group 'Proxy Settings' --key ProxyType 0\n")
			appendKDENotifyCmd(&commands)
			log.Info("Disabling system proxy via kwriteconfig5 (no saved state)")
		}
	}

	return Setter{
		Cmds: commands.String(),
		AfterFunc: func() error {
			savedLinuxProxy.mu.Lock()
			savedLinuxProxy.saved = false
			savedLinuxProxy.mu.Unlock()
			return nil
		},
	}
}
