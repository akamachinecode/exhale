package testutil

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/akamachinecode/exhale/internal/netguard"
)

// Internet is a pretend public internet for tests of code behind a
// netguard.Guard: host names resolve to public addresses, and connections
// to them land on local test servers. The guard's rules stay untouched, so
// a test server is never reached through its real loopback address.
type Internet struct {
	mu      sync.Mutex
	ips     map[string]string // host → pretend public IP
	targets map[string]string // pretend public IP → real listener address
	private map[string]string // host → private IP (never dialed)
}

// NewInternet returns an empty one.
func NewInternet() *Internet {
	return &Internet{ips: map[string]string{}, targets: map[string]string{}, private: map[string]string{}}
}

// Host makes name resolve to a public address served by the listener at
// addr (an httptest server's Listener.Addr().String()).
func (n *Internet) Host(name, addr string) *Internet {
	n.mu.Lock()
	defer n.mu.Unlock()
	ip := fmt.Sprintf("93.184.216.%d", len(n.ips)+10)
	n.ips[name], n.targets[ip] = ip, addr
	return n
}

// PrivateHost makes name resolve to an address of a private network.
func (n *Internet) PrivateHost(name, ip string) *Internet {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.private[name] = ip
	return n
}

// LookupIPAddr implements netguard.Resolver.
func (n *Internet) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if ip, ok := n.ips[host]; ok {
		return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil
	}
	if ip, ok := n.private[host]; ok {
		return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func (n *Internet) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	target, ok := n.targets[host]
	n.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("test internet: nothing listens on %s", addr)
	}
	return (&net.Dialer{}).DialContext(ctx, network, target)
}

// Guard returns a guard that resolves and connects through this internet.
// allowPrivate may be nil (private networks refused).
func (n *Internet) Guard(allowPrivate func(context.Context) bool) *netguard.Guard {
	return &netguard.Guard{Resolver: n, Dial: n.dial, AllowPrivate: allowPrivate}
}
