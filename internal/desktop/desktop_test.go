package desktop

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAgentPlist(t *testing.T) {
	p := agentPlist("/Applications/Exhale & Co.app/Contents/MacOS/exhale", "/Users/a/Library/Logs/Exhale.log")
	// It must be a well-formed plist, with the path escaped.
	var v struct {
		Dict struct {
			Keys    []string `xml:"key"`
			Strings []string `xml:"string"`
			Array   struct {
				Strings []string `xml:"string"`
			} `xml:"array"`
		} `xml:"dict"`
	}
	if err := xml.Unmarshal(p, &v); err != nil {
		t.Fatalf("not XML: %v\n%s", err, p)
	}
	if got := v.Dict.Array.Strings; !slices.Equal(got, []string{"/Applications/Exhale & Co.app/Contents/MacOS/exhale", "serve"}) {
		t.Errorf("ProgramArguments = %q", got)
	}
	for _, k := range []string{"Label", "AssociatedBundleIdentifiers", "RunAtLoad", "KeepAlive", "StandardErrorPath"} {
		if !slices.Contains(v.Dict.Keys, k) {
			t.Errorf("missing key %s", k)
		}
	}
	if v.Dict.Strings[0] != ID {
		t.Errorf("Label = %q, want %q", v.Dict.Strings[0], ID)
	}
}

func TestBrowseAddr(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:8484": "127.0.0.1:8484",
		"0.0.0.0:8484":   "127.0.0.1:8484",
		":9000":          "127.0.0.1:9000",
		"[::]:8484":      "127.0.0.1:8484",
		"10.0.0.2:8484":  "10.0.0.2:8484",
	} {
		if got := browseAddr(in); got != want {
			t.Errorf("browseAddr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseAppleLanguages(t *testing.T) {
	got := parseAppleLanguages("(\n    \"es-ES\",\n    en,\n    \"pt-BR\"\n)\n")
	if want := []string{"es-ES", "en", "pt-BR"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLanguagesFromEnv(t *testing.T) {
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "C")
	t.Setenv("LANG", "es_ES.UTF-8")
	t.Setenv("LANGUAGE", "es:en")
	if got, want := languagesFromEnv(), []string{"es_ES", "es", "en"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWaitForWantsTheNewVersion(t *testing.T) {
	// The old server answers twice, then the new one takes over.
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := "0.1.0"
		if calls.Add(1) > 2 {
			v = "0.2.0"
		}
		w.Header().Set(VersionHeader, v)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	if err := waitFor(context.Background(), srv.URL, "0.2.0", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if calls.Load() < 3 {
		t.Errorf("returned before the new version answered (%d calls)", calls.Load())
	}
}

func TestWaitForGivesUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "starting", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	err := waitFor(context.Background(), srv.URL, "", 600*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("err = %v", err)
	}
}

func TestProbeIgnoresOtherServers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>something else</html>"))
	}))
	defer srv.Close()
	if up, _ := probe(context.Background(), srv.URL); up {
		t.Error("a server that is not Exhale counted as Exhale")
	}
}
