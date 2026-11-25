package proxyWithHttp

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"github.com/v2rayA/v2rayA/pkg/util/log"
	"golang.org/x/net/proxy"
	"net"
	"net/http"
	"net/url"
)

type direct struct{}

// Direct is a direct proxy: one that makes network connections directly.
var Direct = direct{}

func (direct) Dial(network, addr string) (net.Conn, error) {
	return net.Dial(network, addr)
}

// httpsDialer
type httpsDialer struct {
	forward proxy.Dialer
}

// HTTPSDialer is a https proxy: one that makes network connections on tls.
var HttpsDialer = httpsDialer{}
var TlsConfig = &tls.Config{}

func (d httpsDialer) Dial(network, addr string) (c net.Conn, err error) {
	fwd := d.forward
	if fwd == nil {
		fwd = Direct
	}
	rawConn, err := fwd.Dial("tcp", addr)
	if err != nil {
		log.Warn("%v", err)
		return nil, err
	}
	cfg := TlsConfig.Clone()
	if cfg == nil {
		cfg = &tls.Config{}
	}
	if cfg.ServerName == "" {
		if host, _, splitErr := net.SplitHostPort(addr); splitErr == nil {
			cfg.ServerName = host
		} else {
			cfg.ServerName = addr
		}
	}
	tlsConn := tls.Client(rawConn, cfg)
	if err = tlsConn.Handshake(); err != nil {
		rawConn.Close()
		log.Warn("%v", err)
		return nil, err
	}
	return tlsConn, nil
}

// bufferedConn wraps a net.Conn with a bufio.Reader so any bytes buffered
// ahead during the HTTP CONNECT response read are not lost.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (bc *bufferedConn) Read(p []byte) (int, error) {
	return bc.r.Read(p)
}

// proxyWithHttp is a HTTP/HTTPS connect proxy.
type httpProxy struct {
	host     string
	haveAuth bool
	username string
	password string
	forward  proxy.Dialer
}

func newHTTPSProxy(uri *url.URL, forward proxy.Dialer) (proxy.Dialer, error) {
	if forward == nil {
		forward = Direct
	}
	return newHTTPProxy(uri, httpsDialer{forward: forward})
}

func newHTTPProxy(uri *url.URL, forward proxy.Dialer) (proxy.Dialer, error) {
	s := new(httpProxy)
	s.host = uri.Host
	s.forward = forward
	if uri.User != nil {
		s.haveAuth = true
		s.username = uri.User.Username()
		s.password, _ = uri.User.Password()
	}

	return s, nil
}

func (s *httpProxy) Dial(network, addr string) (net.Conn, error) {
	// Dial and create the https client connection.
	c, err := s.forward.Dial("tcp", s.host)
	if err != nil {
		return nil, err
	}

	// HACK. http.ReadRequest also does this.
	reqURL, err := url.Parse("http://" + addr)
	if err != nil {
		c.Close()
		return nil, err
	}
	reqURL.Scheme = ""

	req, err := http.NewRequest("CONNECT", reqURL.String(), nil)
	if err != nil {
		c.Close()
		return nil, err
	}
	req.Close = false
	if s.haveAuth {
		req.SetBasicAuth(s.username, s.password)
		req.Header.Set("Proxy-Authorization", req.Header.Get("Authorization"))
		req.Header.Del("Authorization")
	}
	req.Header.Set("User-Agent", "v2rayA")

	err = req.Write(c)
	if err != nil {
		c.Close()
		return nil, err
	}

	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		c.Close()
		if resp != nil {
			resp.Body.Close()
		}
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.Close()
		resp.Body.Close()
		err = fmt.Errorf("Connect server using proxy error, StatusCode [%d]", resp.StatusCode)
		return nil, err
	}

	if br.Buffered() > 0 {
		return &bufferedConn{Conn: c, r: br}, nil
	}
	return c, nil
}

func FromURL(u *url.URL, forward proxy.Dialer) (proxy.Dialer, error) {
	return proxy.FromURL(u, forward)
}

func FromEnvironment() proxy.Dialer {
	return proxy.FromEnvironment()
}

func init() {
	proxy.RegisterDialerType("http", newHTTPProxy)
	proxy.RegisterDialerType("https", newHTTPSProxy)
}
