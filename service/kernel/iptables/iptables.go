package iptables

import (
	"strings"
	"sync"
	"time"

	"github.com/v2rayA/v2rayA/common"
	"github.com/v2rayA/v2rayA/common/cmds"
)

// http://briteming.hatenablog.com/entry/2019/06/18/175518

const watcherInterval = 3 * time.Second

var (
	watcher   *LocalIPWatcher
	watcherMu sync.Mutex
	mutex     sync.Mutex
)

type Setter struct {
	Cmds      string
	AfterFunc func() error
	PreFunc   func() error
}

func NewErrorSetter(err error) Setter {
	return Setter{PreFunc: func() error {
		return err
	}}
}

type proxySetter interface {
	GetSetupCommands() Setter
	GetCleanCommands() Setter
	AddIPWhitelist(cidr string)
	RemoveIPWhitelist(cidr string)
}

// watch interface changes and add specific IPs to whitelist on iptables
func SetWatcher(setter proxySetter) {
	watcherMu.Lock()
	defer watcherMu.Unlock()
	if watcher != nil {
		watcher.Close()
	}
	watcher = NewLocalIPWatcher(watcherInterval, setter.AddIPWhitelist, setter.RemoveIPWhitelist)
}

func CloseWatcher() {
	watcherMu.Lock()
	defer watcherMu.Unlock()
	if watcher != nil {
		watcher.Close()
		watcher = nil
	}
}

func rewriteIptablesCommands(commands string, docker bool, hasCmd func(string) bool, nftSupported func() bool) string {
	if !strings.Contains(commands, "iptables") && !strings.Contains(commands, "ip6tables") {
		return commands
	}
	if docker {
		if hasCmd("iptables-legacy") {
			commands = strings.ReplaceAll(commands, "iptables", "iptables-legacy")
		}
		if hasCmd("ip6tables-legacy") {
			commands = strings.ReplaceAll(commands, "ip6tables", "ip6tables-legacy")
		}
		return commands
	}
	needV4Nft := !hasCmd("iptables")
	needV6Nft := !hasCmd("ip6tables")
	if !needV4Nft || !needV6Nft {
		if nftSupported() {
			needV4Nft = true
			needV6Nft = true
		}
	}
	if needV4Nft && hasCmd("iptables-nft") {
		commands = strings.ReplaceAll(commands, "iptables", "iptables-nft")
	}
	if needV6Nft && hasCmd("ip6tables-nft") {
		commands = strings.ReplaceAll(commands, "ip6tables", "ip6tables-nft")
	}
	return commands
}

func (c Setter) Run(stopAtError bool) error {
	mutex.Lock()
	defer mutex.Unlock()
	commands := rewriteIptablesCommands(c.Cmds, common.IsDocker(), cmds.IsCommandValid, IsNftablesSupported)
	var errs []error
	if c.PreFunc != nil {
		e := c.PreFunc()
		if e != nil {
			errs = append(errs, e)
			if stopAtError && len(errs) > 0 {
				return errs[0]
			}
		}
	}
	if len(commands) > 0 {
		e := cmds.ExecCommands(commands, stopAtError)
		if e != nil {
			errs = append(errs, e)
			if stopAtError && len(errs) > 0 {
				return errs[0]
			}
		}
	}
	if c.AfterFunc != nil {
		e := c.AfterFunc()
		if e != nil {
			errs = append(errs, e)
			if stopAtError && len(errs) > 0 {
				return errs[0]
			}
		}
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}
