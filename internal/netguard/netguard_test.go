package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// names is a resolver with fixed answers.
type names map[string][]string

func (n names) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	ips, ok := n[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	out := make([]net.IPAddr, len(ips))
	for i, ip := range ips {
		out[i] = net.IPAddr{IP: net.ParseIP(ip)}
	}
	return out, nil
}

func TestClassify(t *testing.T) {
	cases := []struct {
		ip   string
		want Kind
	}{
		{"93.184.216.34", Public},
		{"2606:4700:4700::1111", Public},
		{"127.0.0.1", Never},
		{"127.8.9.10", Never},
		{"::1", Never},
		{"0.0.0.0", Never},
		{"::", Never},
		{"169.254.169.254", Never},  // AWS, GCP, Azure metadata
		{"169.254.10.1", Never},     // link-local
		{"fe80::1", Never},          // link-local
		{"fe80::1%eth0", Never},     // with a zone
		{"100.100.100.200", Never},  // Alibaba metadata, inside CGNAT
		{"168.63.129.16", Never},    // Azure platform
		{"fd00:ec2::254", Never},    // AWS metadata over IPv6, inside ULA
		{"224.0.0.1", Never},        // multicast
		{"255.255.255.255", Never},  // broadcast
		{"::ffff:127.0.0.1", Never}, // IPv4-mapped loopback
		{"::ffff:169.254.169.254", Never},
		{"64:ff9b::7f00:1", Never}, // NAT64 to 127.0.0.1
		{"2002:7f00:1::", Never},   // 6to4 to 127.0.0.1
		{"::7f00:1", Never},        // IPv4-compatible 127.0.0.1
		{"10.1.2.3", Private},
		{"172.16.0.1", Private},
		{"172.31.255.255", Private},
		{"172.32.0.1", Public},
		{"192.168.1.20", Private},
		{"100.64.0.1", Private},      // Tailscale
		{"fd12:3456::1", Private},    // unique local
		{"::ffff:10.0.0.1", Private}, // IPv4-mapped private
		{"64:ff9b::a00:1", Private},  // NAT64 to 10.0.0.1
	}
	for _, c := range cases {
		got, _ := Classify(netip.MustParseAddr(c.ip))
		if got != c.want {
			t.Errorf("Classify(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}

func TestCheckURL(t *testing.T) {
	dns := names{
		"hooks.example":    {"93.184.216.34"},
		"nas.lan":          {"192.168.1.20"},
		"sneaky.example":   {"127.0.0.1"},
		"metadata.example": {"169.254.169.254"},
		// One good address does not excuse a bad one.
		"mixed.example": {"93.184.216.34", "10.0.0.5"},
		"v6.example":    {"::ffff:127.0.0.1"},
	}
	cases := []struct {
		name         string
		url          string
		allowPrivate bool
		ok           bool
		blocked      bool // refused by the rules (not a lookup failure)
	}{
		{"public https", "https://hooks.example/abc", false, true, false},
		{"public http with port", "http://hooks.example:8080/x?y=1", false, true, false},
		{"public ip", "https://93.184.216.34/", false, true, false},
		{"ftp", "ftp://hooks.example/", false, false, true},
		{"file", "file:///etc/passwd", false, false, true},
		{"javascript", "javascript:alert(1)", false, false, true},
		{"no scheme", "hooks.example/abc", false, false, true},
		{"empty", "", false, false, true},
		{"loopback ip", "http://127.0.0.1:8484/", false, false, true},
		{"loopback ip, private allowed", "http://127.0.0.1:8484/", true, false, true},
		{"loopback v6", "http://[::1]/", true, false, true},
		{"localhost name", "http://sneaky.example/", true, false, true},
		{"metadata ip", "http://169.254.169.254/latest/meta-data/", true, false, true},
		{"metadata name", "http://metadata.example/", true, false, true},
		{"mapped loopback name", "http://v6.example/", true, false, true},
		{"private ip", "http://192.168.1.20/hook", false, false, true},
		{"private ip, allowed", "http://192.168.1.20/hook", true, true, false},
		{"private name", "http://nas.lan/hook", false, false, true},
		{"private name, allowed", "http://nas.lan/hook", true, true, false},
		{"mixed answers", "https://mixed.example/", false, false, true},
		{"mixed answers, private allowed", "https://mixed.example/", true, true, false},
		{"unknown name", "https://nope.example/", false, false, false},
		{"zero", "http://0.0.0.0/", true, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := &Guard{Resolver: dns, AllowPrivate: func(context.Context) bool { return c.allowPrivate }}
			_, err := g.CheckURL(context.Background(), c.url)
			if (err == nil) != c.ok {
				t.Fatalf("CheckURL(%q) error = %v, want ok=%v", c.url, err, c.ok)
			}
			if err != nil && errors.Is(err, ErrBlocked) != c.blocked {
				t.Fatalf("CheckURL(%q) error = %v, want blocked=%v", c.url, err, c.blocked)
			}
		})
	}
}

// dialTo sends every connection to one test server, whatever address was
// asked for, and records the addresses asked for.
type dialTo struct {
	target string
	mu     sync.Mutex
	asked  []string
}

func (d *dialTo) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	d.mu.Lock()
	d.asked = append(d.asked, addr)
	d.mu.Unlock()
	return (&net.Dialer{}).DialContext(ctx, network, d.target)
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Host+r.URL.Path)
		if r.Host == "hooks.example" {
			http.Redirect(w, r, "http://internal.example/admin", http.StatusTemporaryRedirect)
		}
	}))
	defer srv.Close()
	d := &dialTo{target: srv.Listener.Addr().String()}
	g := &Guard{Resolver: names{"hooks.example": {"93.184.216.34"}, "internal.example": {"10.0.0.5"}}, Dial: d.dial}

	res, err := g.Client(5*time.Second).Post("http://hooks.example/hook", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want the redirect itself", res.StatusCode)
	}
	if len(hits) != 1 || hits[0] != "hooks.example/hook" {
		t.Fatalf("requests = %v, want only the first", hits)
	}
}

// Even a client that follows redirects cannot be led to a private address:
// the rules run again for every connection.
func TestRedirectToPrivateAddressIsRefused(t *testing.T) {
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Host+r.URL.Path)
		switch r.Host {
		case "hooks.example":
			http.Redirect(w, r, "http://internal.example/admin", http.StatusTemporaryRedirect)
		case "bounce.example":
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
		}
	}))
	defer srv.Close()
	d := &dialTo{target: srv.Listener.Addr().String()}
	g := &Guard{
		Resolver: names{"hooks.example": {"93.184.216.34"}, "bounce.example": {"93.184.216.35"}, "internal.example": {"10.0.0.5"}},
		Dial:     d.dial,
	}
	following := &http.Client{Transport: g.Transport()}

	for _, start := range []string{"http://hooks.example/hook", "http://bounce.example/hook"} {
		hits = nil
		res, err := following.Get(start)
		if err == nil {
			res.Body.Close()
			t.Fatalf("%s: the redirect was followed", start)
		}
		if !errors.Is(err, ErrBlocked) {
			t.Fatalf("%s: error = %v, want ErrBlocked", start, err)
		}
		if len(hits) != 1 {
			t.Fatalf("%s: requests = %v, want only the first", start, hits)
		}
	}
	for _, addr := range d.asked {
		if !strings.HasPrefix(addr, "93.184.216.") {
			t.Fatalf("dialed %s", addr)
		}
	}

	// With private networks allowed the private redirect goes through, the
	// metadata one still does not.
	g.AllowPrivate = func(context.Context) bool { return true }
	res, err := following.Get("http://hooks.example/hook")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if _, err := following.Get("http://bounce.example/hook"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("metadata redirect: error = %v, want ErrBlocked", err)
	}
}

// The connection goes to the address that was checked, so a name cannot
// answer with a public address for the check and a private one for the dial.
func TestDialsTheCheckedAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	d := &dialTo{target: srv.Listener.Addr().String()}
	g := &Guard{Resolver: names{"hooks.example": {"93.184.216.34"}}, Dial: d.dial}
	res, err := g.Client(5 * time.Second).Get("http://hooks.example:8123/x")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if len(d.asked) != 1 || d.asked[0] != "93.184.216.34:8123" {
		t.Fatalf("dialed %v, want the resolved address", d.asked)
	}
}

func TestCauseHidesTheURL(t *testing.T) {
	g := &Guard{Resolver: names{"hooks.example": {"10.0.0.5"}}}
	_, err := g.Client(time.Second).Get("http://hooks.example/secret-token-123")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "secret-token-123") {
		t.Fatalf("precondition: http errors carry the URL, got %v", err)
	}
	if got := Cause(err).Error(); strings.Contains(got, "secret-token-123") || !errors.Is(Cause(err), ErrBlocked) {
		t.Fatalf("Cause = %q", got)
	}
	if Host("https://discord.com/api/webhooks/1/secret") != "discord.com" {
		t.Fatal("Host")
	}
}
