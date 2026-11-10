// Package netguard keeps HTTP requests to addresses chosen by users (webhook
// URLs, ntfy servers) away from the server's own machine and network. Any
// member can add a destination, so without it a URL such as
// http://169.254.169.254/ would let them read cloud credentials through
// Exhale (server-side request forgery).
//
// The rules: http and https only; loopback, link-local and cloud-metadata
// addresses are never reachable; private networks only when the owner allows
// them. The check runs when a connection is opened, on the address actually
// dialed, so a DNS name that changes its answer or a redirect cannot get
// around it.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// ErrBlocked marks a destination refused by the rules (as opposed to one
// that could not be reached).
var ErrBlocked = errors.New("destination not allowed")

// Resolver looks up the addresses of a host name. *net.Resolver implements
// it; tests inject their own.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// Guard applies the rules. The zero value refuses private networks and uses
// the system resolver.
type Guard struct {
	// AllowPrivate reports whether private-network addresses (10/8,
	// 192.168/16, fc00::/7…) may be reached right now. Nil means no.
	AllowPrivate func(ctx context.Context) bool
	// Resolver defaults to net.DefaultResolver.
	Resolver Resolver
	// Dial opens the connection to an address that already passed the
	// rules. It defaults to a net.Dialer; tests replace it.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

type rng struct {
	prefix netip.Prefix
	what   string
}

func ranges(what string, cidrs ...string) []rng {
	out := make([]rng, len(cidrs))
	for i, c := range cidrs {
		out[i] = rng{netip.MustParsePrefix(c), what}
	}
	return out
}

// never is checked before private, so metadata addresses inside private
// ranges stay refused when private networks are allowed.
var never = append(append(append(append(
	ranges("this machine", "127.0.0.0/8", "::1/128", "0.0.0.0/8", "::/128"),
	ranges("a link-local address", "169.254.0.0/16", "fe80::/10")...),
	ranges("a cloud metadata service", "100.100.100.200/32", "168.63.129.16/32", "fd00:ec2::/32")...),
	ranges("a multicast address", "224.0.0.0/4", "ff00::/8")...),
	ranges("a reserved address", "192.0.0.0/24", "240.0.0.0/4")...)

var private = ranges("a private network",
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"100.64.0.0/10", // carrier-grade NAT, also Tailscale
	"198.18.0.0/15", // benchmarking
	"fc00::/7",      // unique local
)

var (
	nat64      = netip.MustParsePrefix("64:ff9b::/96")
	sixToFour  = netip.MustParsePrefix("2002::/16")
	v4Compat   = netip.MustParsePrefix("::/96")
	unspecLoop = netip.MustParsePrefix("::/127") // :: and ::1 are not embedded IPv4
)

// embedded returns the IPv4 address carried inside an IPv6 one (mapped,
// NAT64, 6to4, IPv4-compatible), which is where the packet ends up.
func embedded(a netip.Addr) (netip.Addr, bool) {
	if a.Is4In6() {
		return a.Unmap(), true
	}
	if !a.Is6() {
		return a, false
	}
	b := a.As16()
	switch {
	case nat64.Contains(a), v4Compat.Contains(a) && !unspecLoop.Contains(a):
		return netip.AddrFrom4([4]byte(b[12:16])), true
	case sixToFour.Contains(a):
		return netip.AddrFrom4([4]byte(b[2:6])), true
	}
	return a, false
}

// Kind says how an address is treated.
type Kind int

const (
	Public Kind = iota
	Private
	Never
)

// Classify returns how the rules treat an address and, when it is not
// public, what it is ("a private network", "this machine"…).
func Classify(a netip.Addr) (Kind, string) {
	a = a.WithZone("")
	if v4, ok := embedded(a); ok {
		a = v4
	}
	for _, r := range never {
		if r.prefix.Contains(a) {
			return Never, r.what
		}
	}
	for _, r := range private {
		if r.prefix.Contains(a) {
			return Private, r.what
		}
	}
	return Public, ""
}

func (g *Guard) allowPrivate(ctx context.Context) bool {
	return g != nil && g.AllowPrivate != nil && g.AllowPrivate(ctx)
}

func (g *Guard) check(ctx context.Context, host string, a netip.Addr) error {
	kind, what := Classify(a)
	switch {
	case kind == Never:
		return fmt.Errorf("%w: %s is %s", ErrBlocked, host, what)
	case kind == Private && !g.allowPrivate(ctx):
		return fmt.Errorf("%w: %s is on %s; the owner can allow private destinations in Settings", ErrBlocked, host, what)
	}
	return nil
}

// resolve returns the addresses of host after checking every one of them: a
// name with one forbidden address is refused as a whole.
func (g *Guard) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	host = strings.TrimSuffix(host, ".")
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{a}
	} else {
		var r Resolver = net.DefaultResolver
		if g != nil && g.Resolver != nil {
			r = g.Resolver
		}
		ips, err := r.LookupIPAddr(ctx, host)
		if err != nil {
			var dns *net.DNSError
			if errors.As(err, &dns) && dns.IsNotFound {
				return nil, fmt.Errorf("%s does not exist", host)
			}
			return nil, fmt.Errorf("could not look up %s: %w", host, err)
		}
		for _, ip := range ips {
			if a, ok := netip.AddrFromSlice(ip.IP); ok {
				addrs = append(addrs, a.Unmap())
			}
		}
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%s has no address", host)
	}
	for _, a := range addrs {
		if err := g.check(ctx, host, a); err != nil {
			return nil, err
		}
	}
	return addrs, nil
}

// CheckURL validates an address a user typed: a well-formed http or https
// URL whose host passes the rules today. It returns the parsed URL.
func (g *Guard) CheckURL(ctx context.Context, raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%w: that is not a web address; it should look like https://example.com/…", ErrBlocked)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%w: only http and https addresses work", ErrBlocked)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("%w: the address has no host", ErrBlocked)
	}
	if _, err := g.resolve(ctx, u.Hostname()); err != nil {
		return nil, err
	}
	return u, nil
}

// DialContext resolves the host itself, checks every address and connects
// to the checked address (never back to the name).
func (g *Guard) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	addrs, err := g.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	dial := (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	if g != nil && g.Dial != nil {
		dial = g.Dial
	}
	var first error
	for _, a := range addrs {
		conn, err := dial(ctx, network, net.JoinHostPort(a.String(), port))
		if err == nil {
			return conn, nil
		}
		if first == nil {
			first = err
		}
	}
	return nil, first
}

// Transport returns an HTTP transport whose every connection goes through
// the guard. It ignores proxy settings: a proxy would connect on our behalf
// to an address we could not check.
func (g *Guard) Transport() *http.Transport {
	return &http.Transport{
		DialContext:           g.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		// One connection per request: reminders are rare, and a pooled
		// connection would outlive a change of the rules.
		DisableKeepAlives: true,
	}
}

// Client returns an HTTP client for user-chosen addresses. It does not
// follow redirects: the response of the address the user gave is the answer.
func (g *Guard) Client(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport:     g.Transport(),
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Cause strips the URL from an HTTP client error, leaving what went wrong.
// Webhook URLs carry secrets and must not reach logs or the outbox.
func Cause(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		if ue.Timeout() {
			return errors.New("timed out")
		}
		return ue.Err
	}
	return err
}

// Host returns the host of a URL for logs and errors ("" if it has none).
func Host(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
