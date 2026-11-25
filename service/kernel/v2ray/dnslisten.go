package v2ray

// 本文件保存原 specialMode 包中与 redirect 透明代理相关的 DNS 监听辅助函数。
// supervisor / fakedns 相关代码已随 specialMode 包一同移除。

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/v2rayA/v2rayA/common/netTools/netstat"
	"github.com/v2rayA/v2rayA/common/netTools/ports"
	"github.com/v2rayA/v2rayA/conf"
	"github.com/v2rayA/v2rayA/db/configure"
)

var (
	DnsPortCheckFailedErr = fmt.Errorf("failed to check dns port occupation")
	DnsPortOccupied       = fmt.Errorf("dns port 53 is occupied")
	couldLocalDnsListen   = CouldLocalDnsListen
)

// shouldLocalDnsListen reports whether the given setting requires local DNS
// hijacking (listening on 127.2.0.17:53, iptables REDIRECT --dport 53, and
// /etc/resolv.conf takeover).
//
// Only Linux Redirect and TProxy modes use local DNS hijacking; system proxy,
// TinyTun, --lite, and non-Linux platforms must never hijack system DNS.
func shouldLocalDnsListen(setting *configure.Setting) bool {
	if setting == nil {
		setting = configure.GetSettingNotNil()
	}
	if runtime.GOOS != "linux" {
		return false
	}
	if setting.Transparent == configure.TransparentClose {
		return false
	}
	if setting.TransparentType == configure.TransparentSystemProxy || setting.TransparentType == configure.TransparentTun {
		return false
	}
	if conf.GetEnvironmentConfig().Lite {
		return false
	}
	return true
}

// ShouldLocalDnsListen 在透明代理（Redirect/TProxy）启用时返回 true，
// 表示需要劫持 /etc/resolv.conf 将系统 DNS 指向 127.2.0.17:53，
// 使 DNS 流量可以被 iptables/nftables 规则捕获后重定向到 DNS 模块端口。
//
// 不依赖 xray 的 dns-in/dns-out，DNS 模块直接查询上游并返回结果。
func ShouldLocalDnsListen() bool {
	return shouldLocalDnsListen(configure.GetSettingNotNil())
}

var couldListenCache struct {
	couldListenLocalhost bool
	err                  error
	lastUpdate           time.Time
	mu                   sync.Mutex
}

func probeBindLocalDnsAddr() error {
	pc, err := net.ListenPacket("udp4", "127.2.0.17:53")
	if err != nil {
		return err
	}
	_ = pc.Close()
	ln, err := net.Listen("tcp4", "127.2.0.17:53")
	if err != nil {
		return err
	}
	_ = ln.Close()
	return nil
}

// CouldLocalDnsListen 检查 53 端口是否可用，结果缓存 3 秒。
func CouldLocalDnsListen() (couldListenLocalhost bool, err error) {
	couldListenCache.mu.Lock()
	defer couldListenCache.mu.Unlock()
	if time.Since(couldListenCache.lastUpdate) < 3*time.Second {
		return couldListenCache.couldListenLocalhost, couldListenCache.err
	}
	defer func() {
		couldListenCache.lastUpdate = time.Now()
		couldListenCache.couldListenLocalhost = couldListenLocalhost
		couldListenCache.err = err
	}()
	if runtime.GOOS != "linux" {
		return false, fmt.Errorf("%w: local dns listen is only supported on linux", DnsPortCheckFailedErr)
	}
	occupied, sockets, err := ports.IsPortOccupied([]string{"53:udp,tcp"})
	if err != nil {
		return false, fmt.Errorf("%w: %v", DnsPortCheckFailedErr, err)
	}
	if err = netstat.FillProcesses(sockets); err != nil {
		return false, fmt.Errorf("%w: %v. please try again later", DnsPortCheckFailedErr, err)
	}
	var occupiedErr error
	ownedByChild := false
	localDnsIP := net.ParseIP("127.2.0.17")
	ownPID := strconv.Itoa(os.Getpid())
	if occupied {
		for _, socket := range sockets {
			isTargetIP := socket.LocalAddress.IP.Equal(localDnsIP) || socket.LocalAddress.IP.IsUnspecified()
			p := socket.Proc
			if p == nil {
				if isTargetIP {
					return false, fmt.Errorf("%w", DnsPortOccupied)
				}
				continue
			}
			if p.PPID == ownPID {
				if isTargetIP {
					ownedByChild = true
				}
				continue
			}
			occupiedErr = fmt.Errorf("%w by %v(%v)", DnsPortOccupied, p.Name, p.PID)
			if isTargetIP {
				return false, occupiedErr
			}
		}
	}
	if !ownedByChild {
		if bindErr := probeBindLocalDnsAddr(); bindErr != nil {
			return false, fmt.Errorf("%w: %v", DnsPortOccupied, bindErr)
		}
	}
	return true, occupiedErr
}
