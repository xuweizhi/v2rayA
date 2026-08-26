package v2ray

import (
	"strings"
	"sync"
	"time"
)

// ConnectionEvent is one parsed core connection log line.
type ConnectionEvent struct {
	Time     time.Time `json:"time"`
	Network  string    `json:"network"`
	Source   string    `json:"source"`
	Dest     string    `json:"dest"`
	Inbound  string    `json:"inbound"`
	Outbound string    `json:"outbound"`
}

const connectionRingCapacity = 2000

var connLogMu sync.Mutex
var connLogRing = make([]ConnectionEvent, 0, connectionRingCapacity)

// parseConnectionLine extracts a connection event from a core access log
// line of the form:
//
//	from tcp:127.0.0.1:43496 accepted tcp:www.gstatic.com:80 [socks -> proxy]
func parseConnectionLine(line string) (ev ConnectionEvent, ok bool) {
	if !strings.HasPrefix(line, "from ") {
		return ev, false
	}
	rest := strings.TrimPrefix(line, "from ")
	idx := strings.Index(rest, " accepted ")
	if idx < 0 {
		return ev, false
	}
	ev.Source = rest[:idx]
	tail := rest[idx+len(" accepted "):]
	bracket := strings.LastIndex(tail, " [")
	if bracket < 0 || !strings.HasSuffix(tail, "]") {
		return ev, false
	}
	dest := tail[:bracket]
	tags := tail[bracket+2 : len(tail)-1]
	if arrow := strings.Index(tags, " -> "); arrow > 0 {
		ev.Inbound = tags[:arrow]
		ev.Outbound = tags[arrow+4:]
	} else {
		ev.Inbound = tags
	}
	// Extract the network from the destination prefix like "tcp:" / "udp:".
	ev.Dest = dest
	if colon := strings.Index(dest, ":"); colon > 0 {
		ev.Network = dest[:colon]
	} else if strings.HasPrefix(dest, "http") {
		ev.Network = "http"
	}
	ev.Time = time.Now()
	return ev, true
}

// recordConnectionLine parses a core log line and appends it to the ring
// buffer when it describes a connection.
func recordConnectionLine(line string) {
	ev, ok := parseConnectionLine(line)
	if !ok {
		return
	}
	connLogMu.Lock()
	defer connLogMu.Unlock()
	if len(connLogRing) >= connectionRingCapacity {
		connLogRing = append(connLogRing[:0], connLogRing[1:]...)
	}
	connLogRing = append(connLogRing, ev)
}

// GetRecentConnections returns up to limit most recent connection events,
// newest first.
func GetRecentConnections(limit int) []ConnectionEvent {
	connLogMu.Lock()
	defer connLogMu.Unlock()
	if limit <= 0 || limit > len(connLogRing) {
		limit = len(connLogRing)
	}
	out := make([]ConnectionEvent, limit)
	n := 0
	for i := len(connLogRing) - 1; i >= 0 && n < limit; i-- {
		out[n] = connLogRing[i]
		n++
	}
	return out[:n]
}
