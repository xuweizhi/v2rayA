package dns

import (
	"bytes"
	"crypto/rand"
	"log"
	"net"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// A query that leaves this module carries a token in an EDNS0 local option. If
// a query arrives carrying the same token, it is one of ours that an upstream
// sent back — the classic configuration where a local resolver forwards to
// v2rayA while a v2rayA rule points at that resolver. Without this check the
// two forward the same name to each other until one of them runs out of
// memory. The firewall-level self-redirect is handled by the 0x80 mark; this
// is the resolver-level counterpart.
//
// 65042 is inside the local/experimental range of EDNS0 option codes
// (65001-65534) reserved by RFC 6891 for exactly this kind of private use.
const loopOptionCode = 65042

var loopToken = sync.OnceValue(func() []byte {
	token := make([]byte, 8)
	rand.Read(token)
	return token
})

// markOutgoing attaches this process's token to a query that is about to be
// sent upstream. It keeps the single OPT record miekg/dns expects.
func markOutgoing(msg *dns.Msg) {
	if msg == nil {
		return
	}
	opt := msg.IsEdns0()
	if opt == nil {
		msg.SetEdns0(4096, false)
		opt = msg.IsEdns0()
	}
	for _, o := range opt.Option {
		if local, ok := o.(*dns.EDNS0_LOCAL); ok && local.Code == loopOptionCode {
			local.Data = loopToken()
			return
		}
	}
	opt.Option = append(opt.Option, &dns.EDNS0_LOCAL{
		Code: loopOptionCode,
		Data: loopToken(),
	})
}

// carriesOwnToken reports whether msg is a query this process already sent
// upstream, which means it has come back around a resolver loop.
func carriesOwnToken(msg *dns.Msg) bool {
	if msg == nil {
		return false
	}
	opt := msg.IsEdns0()
	if opt == nil {
		return false
	}
	token := loopToken()
	for _, o := range opt.Option {
		local, ok := o.(*dns.EDNS0_LOCAL)
		if ok && local.Code == loopOptionCode && bytes.Equal(local.Data, token) {
			return true
		}
	}
	return false
}

// loopDetected logs the loop at most once every loopLogInterval: the two
// resolvers can exchange thousands of queries a second, and a log line per
// query is its own denial of service.
func loopDetected(q dns.Question, client net.Addr) {
	loopLogMu.Lock()
	defer loopLogMu.Unlock()
	loopSuppressed++
	if !loopLastLog.IsZero() && time.Since(loopLastLog) < loopLogInterval {
		return
	}
	log.Printf("[dns] loop detected: %s %s came back from %v carrying this instance's token; "+
		"check that the upstream for this name is not a resolver that forwards back to v2rayA "+
		"(%d such queries refused so far)",
		dns.Type(q.Qtype).String(), q.Name, client, loopSuppressed)
	loopLastLog = time.Now()
}

const loopLogInterval = 10 * time.Second

var (
	loopLogMu      sync.Mutex
	loopLastLog    time.Time
	loopSuppressed int
)
