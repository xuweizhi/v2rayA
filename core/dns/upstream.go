package dns

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// BuildUpstreamKey builds a composite key for an upstream instance,
// combining the address and proxy channel tag.
// This ensures that the same DNS server queried through different
// proxy channels is treated as separate instances.
//
// Examples:
//
//	BuildUpstreamKey("8.8.8.8:53", "")       → "8.8.8.8:53|"
//	BuildUpstreamKey("8.8.8.8:53", "direct") → "8.8.8.8:53|direct"
//	BuildUpstreamKey("8.8.8.8:53", "proxy")  → "8.8.8.8:53|proxy"
func BuildUpstreamKey(addr, proxyTag string) string {
	return fmt.Sprintf("%s|%s", addr, proxyTag)
}

// UpstreamInstance represents a single DNS upstream server instance.
type UpstreamInstance struct {
	DialAddress    string
	Fallback       []string
	attemptTimeout time.Duration
	// ID is the unique identifier for this upstream.
	ID string
	// Addr is the upstream server address (e.g., "8.8.8.8:53").
	Addr string
	// Protocol is the transport protocol: udp, tcp, dot, doh.
	Protocol string
	// ProxyTag is the proxy channel tag for routing.
	ProxyTag string
	// Client is the DNS client used to send queries.
	Client *dns.Client
	// Healthy indicates whether the upstream is currently healthy.
	Healthy bool
	// Bootstrap indicates whether this upstream is used for bootstrap resolution.
	Bootstrap bool
}

// CompositeKey returns the composite key for this instance.
func (u *UpstreamInstance) CompositeKey() string {
	return BuildUpstreamKey(u.Addr, u.ProxyTag)
}

// UpstreamManager manages a collection of DNS upstream servers.
// It provides methods for retrieving upstream instances and sending queries.
// Each upstream instance is identified by a composite key of (addr, proxyTag),
// allowing the same DNS server to be used through different proxy channels
// as independent instances with separate connection pools and health states.
type UpstreamManager struct {
	upstreams         map[string]*UpstreamInstance // Composite key → instance
	upstreamsByID     map[string]*UpstreamInstance // ID → instance (backward compatibility)
	proxyAddrResolver ProxyAddrResolver            // Resolver for proxy tag → SOCKS5 address (legacy)
	dispatcher        RouteDispatcher              // xray-core internal routing dispatcher
	mu                sync.RWMutex
}

// NewUpstreamManager creates a new UpstreamManager from a list of upstream configurations.
// Each upstream configuration produces an instance keyed by (addr, proxyTag).
// If two configs share the same addr but have different proxy tags, they are
// stored as separate instances.
func NewUpstreamManager(configs []UpstreamConfig) *UpstreamManager {
	m := &UpstreamManager{
		upstreams:     make(map[string]*UpstreamInstance),
		upstreamsByID: make(map[string]*UpstreamInstance),
	}

	for _, cfg := range configs {
		id := cfg.ID
		if id == "" {
			id = cfg.Addr
		}

		net := cfg.Protocol
		if net == "" {
			net = "udp"
		}

		proxyTag := cfg.ProxyTag
		if proxyTag == "" {
			proxyTag = "direct"
		}

		instance := &UpstreamInstance{
			ID:          id,
			DialAddress: cfg.DialAddress,
			Fallback:    cfg.Fallback,
			Addr:        cfg.Addr,
			Protocol:    net,
			ProxyTag:    proxyTag,
			Healthy:     true,
			Bootstrap:   cfg.Bootstrap,
			Client:      newMarkedDnsClient(net),
		}

		compositeKey := instance.CompositeKey()
		m.upstreams[compositeKey] = instance

		// Also index by ID for backward compatibility.
		// If multiple instances share the same ID, the last one wins.
		m.upstreamsByID[id] = instance
	}

	log.Printf("[dns upstream] created manager with %d upstreams", len(configs))
	return m
}

// GetUpstream returns the upstream instance with the given ID.
// Returns false if the upstream is not found.
// This uses the ID-based index for backward compatibility.
func (m *UpstreamManager) GetUpstream(id string) (*UpstreamInstance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	u, ok := m.upstreamsByID[id]
	if !ok {
		// Fallback: try direct lookup in upstreams map
		u, ok = m.upstreams[id]
	}
	return u, ok
}

// GetUpstreamByKey returns the upstream instance with the given composite key
// (addr|proxyTag). This provides direct access to a specific channel's instance.
func (m *UpstreamManager) GetUpstreamByKey(compositeKey string) (*UpstreamInstance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	u, ok := m.upstreams[compositeKey]
	return u, ok
}

// GetOrCreateUpstream retrieves an existing upstream instance for the given
// configuration, or creates a new one if it doesn't exist.
// The instance is identified by the composite key of (Addr, ProxyTag).
// This allows the same DNS server address to have separate instances
// for different proxy channels.
func (m *UpstreamManager) GetOrCreateUpstream(config UpstreamConfig) *UpstreamInstance {
	proxyTag := config.ProxyTag
	if proxyTag == "" {
		proxyTag = "direct"
	}

	compositeKey := BuildUpstreamKey(config.Addr, proxyTag)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if an instance already exists for this composite key.
	if existing, ok := m.upstreams[compositeKey]; ok {
		return existing
	}

	// Create a new instance.
	id := config.ID
	if id == "" {
		id = config.Addr
	}

	net := config.Protocol
	if net == "" {
		net = "udp"
	}

	instance := &UpstreamInstance{
		ID:          id,
		DialAddress: config.DialAddress,
		Fallback:    config.Fallback,
		Addr:        config.Addr,
		Protocol:    net,
		ProxyTag:    proxyTag,
		Healthy:     true,
		Bootstrap:   config.Bootstrap,
		Client:      newMarkedDnsClient(net),
	}

	m.upstreams[compositeKey] = instance
	m.upstreamsByID[id] = instance

	log.Printf("[dns upstream] created instance: key=%s addr=%s proxyTag=%s",
		compositeKey, config.Addr, proxyTag)

	return instance
}

// HealthCheck performs a health check on all upstream servers.
// Returns a map of composite key → health status.
func (m *UpstreamManager) HealthCheck() map[string]bool {
	return m.healthCheck("")
}

func (m *UpstreamManager) HealthCheckByProxyTag(proxyTag string) map[string]bool {
	return m.healthCheck(proxyTag)
}

func (m *UpstreamManager) healthCheck(proxyTag string) map[string]bool {
	// Network IO must not hold the manager lock. Publish new immutable instances
	// so readers already holding an upstream never race with health-state writes.
	m.mu.RLock()
	snapshot := make(map[string]*UpstreamInstance, len(m.upstreams))
	for key, upstream := range m.upstreams {
		if proxyTag == "" || upstream.ProxyTag == proxyTag {
			snapshot[key] = upstream
		}
	}
	m.mu.RUnlock()
	results := make(map[string]bool, len(snapshot))
	for key, upstream := range snapshot {
		healthy := m.pingUpstream(upstream)
		updated := *upstream
		updated.Healthy = healthy
		m.mu.Lock()
		if m.upstreams[key] == upstream {
			m.upstreams[key] = &updated
			for id, indexed := range m.upstreamsByID {
				if indexed == upstream {
					m.upstreamsByID[id] = &updated
				}
			}
		}
		m.mu.Unlock()
		results[key] = healthy
	}
	return results
}

func (m *UpstreamManager) pingUpstream(upstream *UpstreamInstance) bool {
	// Probe the configured transport and route, including DoH/DoT and proxies.
	response, err := m.exchangeOne(upstream, &DnsQuery{Name: ".", QType: QueryType(dns.TypeSOA)})
	return err == nil && response != nil && response.Rcode == dns.RcodeSuccess
}

// ListUpstreams returns a snapshot of all upstream instances.
func (m *UpstreamManager) ListUpstreams() []*UpstreamInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()

	instances := make([]*UpstreamInstance, 0, len(m.upstreams))
	for _, u := range m.upstreams {
		instances = append(instances, u)
	}
	return instances
}
