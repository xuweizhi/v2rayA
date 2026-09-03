package httpClient

import (
	"fmt"
	"github.com/v2rayA/v2rayA/common"
	"github.com/v2rayA/v2rayA/db/configure"
	"github.com/v2rayA/v2rayA/kernel/v2ray"
	proxyWithHttp2 "github.com/v2rayA/v2rayA/pkg/util/proxyWithHttp"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func GetHttpClientWithProxy(proxyURL string) (client *http.Client, err error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return
	}
	dialer, err := proxyWithHttp2.FromURL(u, proxyWithHttp2.Direct)
	if err != nil {
		return
	}
	httpTransport := &http.Transport{
		Dial: dialer.Dial,
	}
	client = &http.Client{Transport: httpTransport}
	return
}

func GetHttpClientWithv2rayAProxy() (client *http.Client, err error) {
	host := "127.0.0.1"
	//是否在docker环境
	if common.IsDocker() {
		//连接网关，即宿主机的端口
		out, err := exec.Command("sh", "-c", "ip route list default|head -n 1|awk '{print $3}'").Output()
		if err == nil {
			host = strings.TrimSpace(string(out))
		} else {
			return nil, fmt.Errorf("failed to get gateway: %v", err)
		}
	}
	return GetHttpClientWithProxy("socks5://" + net.JoinHostPort(host, strconv.Itoa(configure.GetPortsNotNil().Socks5)))
}

func GetHttpClientWithv2rayAPac() (client *http.Client, err error) {
	host := "127.0.0.1"
	//是否在docker环境
	if common.IsDocker() {
		//连接网关，即宿主机的端口
		out, err := exec.Command("sh", "-c", "ip route|grep default|awk '{print $3}'").Output()
		if err == nil {
			host = strings.TrimSpace(string(out))
		} else {
			return nil, fmt.Errorf("failed to get gateway: %v", err)
		}
	}
	return GetHttpClientWithProxy("http://" + net.JoinHostPort(host, strconv.Itoa(configure.GetPortsNotNil().HttpWithPac)))
}

func GetHttpClientAutomatically() (c *http.Client) {
	setting := configure.GetSettingNotNil()
	if !v2ray.ProcessManager.Running() || configure.GetConnectedServers() == nil || v2ray.IsTransparentOn(setting) {
		return http.DefaultClient
	}
	var err error
	switch setting.ProxyModeWhenSubscribe {
	case configure.ProxyModePac:
		c, err = GetHttpClientWithv2rayAPac()
		if err != nil {
			return http.DefaultClient
		}
	case configure.ProxyModeProxy:
		c, err = GetHttpClientWithv2rayAProxy()
		if err != nil {
			return http.DefaultClient
		}
	default:
		c = http.DefaultClient
	}
	return c
}

// GetHttpClientDirect builds a client that ignores HTTP(S)_PROXY and, while
// transparent proxying is active on Linux, marks sockets with v2rayA's bypass
// mark so they do not get captured by the transparent rules again.
func GetHttpClientDirect() *http.Client {
	return newDirectHTTPClient(v2ray.IsTransparentOn(configure.GetSettingNotNil()))
}

func newDirectHTTPClient(bypassTransparent bool) *http.Client {
	transport := &http.Transport{
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = defaultTransport.Clone()
	}
	// A nil Proxy explicitly disables ProxyFromEnvironment.
	transport.Proxy = nil
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	if bypassTransparent {
		dialer.Control = directSocketControl
	}
	transport.DialContext = dialer.DialContext
	return &http.Client{Transport: transport}
}

// GetHttpClientsForWebdav returns the ordered routes for a WebDAV operation.
// Prefer modes retry only transport failures through the next route; HTTP
// status errors (for example an invalid WebDAV password) are handled by the
// caller and are not retried through another network path.
func GetHttpClientsForWebdav(mode configure.WebdavConnectionMode) ([]*http.Client, error) {
	direct := GetHttpClientDirect()
	proxy := func() (*http.Client, error) {
		if !v2ray.ProcessManager.Running() || configure.GetConnectedServers() == nil {
			return nil, fmt.Errorf("v2rayA proxy is not connected")
		}
		return GetHttpClientWithv2rayAProxy()
	}

	switch mode {
	case configure.WebdavConnectionFollowSubscription:
		return []*http.Client{GetHttpClientAutomatically()}, nil
	case configure.WebdavConnectionOnlyDirect:
		return []*http.Client{direct}, nil
	case configure.WebdavConnectionOnlyProxy:
		client, err := proxy()
		if err != nil {
			return nil, err
		}
		return []*http.Client{client}, nil
	case configure.WebdavConnectionPreferDirect:
		clients := []*http.Client{direct}
		if client, err := proxy(); err == nil {
			clients = append(clients, client)
		}
		return clients, nil
	case configure.WebdavConnectionPreferProxy:
		if client, err := proxy(); err == nil {
			return []*http.Client{client, direct}, nil
		}
		return []*http.Client{direct}, nil
	default:
		return nil, fmt.Errorf("invalid WebDAV connection mode %q", mode)
	}
}
