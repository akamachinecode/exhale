//go:build desktop

package window

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/desktop"
)

// fakeServer answers /healthz with the version in v; "" means it is down.
func fakeServer(t *testing.T, v *atomic.Value) *bridge {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		version := v.Load().(string)
		if version == "" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set(desktop.VersionHeader, version)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	base, _ := url.Parse(srv.URL)
	return &bridge{base: base, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func fastPolls(t *testing.T, newTimeout, backTimeout time.Duration) {
	t.Helper()
	poll, n, back := serverPollEvery, serverNewTimeout, serverBackTimeout
	serverPollEvery, serverNewTimeout, serverBackTimeout = 5*time.Millisecond, newTimeout, backTimeout
	t.Cleanup(func() { serverPollEvery, serverNewTimeout, serverBackTimeout = poll, n, back })
}

// A package replaces the window before it restarts the server: the window
// reopens only once the new server answers, never while it is down.
func TestWaitForServerWaitsForTheNewVersion(t *testing.T) {
	fastPolls(t, time.Minute, time.Minute)
	var v atomic.Value
	v.Store("0.6.0")
	b := fakeServer(t, &v)
	done := make(chan struct{})
	go func() { b.waitForServer(context.Background(), "0.6.0"); close(done) }()

	v.Store("") // the package stops the server
	select {
	case <-done:
		t.Fatal("reopened while the server was down")
	case <-time.After(50 * time.Millisecond):
	}
	v.Store("0.7.0") // and starts the new one
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("did not reopen once the new server answered")
	}
}

// Only the window changed: it reopens once the old server still answers.
func TestWaitForServerSameVersion(t *testing.T) {
	fastPolls(t, 30*time.Millisecond, time.Minute)
	var v atomic.Value
	v.Store("0.7.0")
	b := fakeServer(t, &v)
	start := time.Now()
	b.waitForServer(context.Background(), "0.7.0")
	if time.Since(start) < 30*time.Millisecond {
		t.Error("reopened before giving the server time to restart")
	}
}

// A server that never comes back does not keep the old window forever.
func TestWaitForServerGivesUp(t *testing.T) {
	fastPolls(t, 10*time.Millisecond, 30*time.Millisecond)
	var v atomic.Value
	v.Store("")
	b := fakeServer(t, &v)
	done := make(chan struct{})
	go func() { b.waitForServer(context.Background(), "0.6.0"); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("waited forever for a server that is down")
	}
}

// The window an older one reopened waits for the server, and opens anyway
// when it never comes.
func TestWaitForAnswer(t *testing.T) {
	fastPolls(t, time.Minute, time.Minute)
	var v atomic.Value
	v.Store("")
	b := fakeServer(t, &v)
	done := make(chan struct{})
	go func() { b.waitForAnswer(context.Background(), time.Minute); close(done) }()
	select {
	case <-done:
		t.Fatal("opened while the server was down")
	case <-time.After(50 * time.Millisecond):
	}
	v.Store("0.8.0")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("did not open once the server answered")
	}

	v.Store("")
	start := time.Now()
	b.waitForAnswer(context.Background(), 30*time.Millisecond)
	if time.Since(start) > time.Second {
		t.Error("waited past its bound")
	}
}
