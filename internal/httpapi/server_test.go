package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/store"
)

func setup(t *testing.T, trustLocal bool) (*httptest.Server, *app.Service, app.Actor) {
	t.Helper()
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := app.New(st, bus.New(), nil, func() time.Time { return time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC) })
	owner, err := svc.Bootstrap(ctx, app.BootstrapInput{HouseholdName: "Home", OwnerName: "Alex", Currency: "EUR", Timezone: "Europe/Madrid", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	h, _, err := New(svc, Config{Version: "test", TrustLocal: trustLocal})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, svc, owner
}

func do(t *testing.T, method, url string, body any, header http.Header) (int, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, r)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header[k] = v
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestRESTFlow(t *testing.T) {
	srv, _, _ := setup(t, true)
	code, created := do(t, "POST", srv.URL+"/api/v1/subscriptions", map[string]any{"name": "Spotify", "price": "10.99", "currency": "EUR", "interval": 1, "unit": "month", "firstCharge": "2026-11-08"}, nil)
	if code != 200 {
		t.Fatalf("create: %d %v", code, created)
	}
	id := created["subscription"].(map[string]any)["id"].(string)
	code, got := do(t, "GET", srv.URL+"/api/v1/subscriptions/"+id, nil, nil)
	if code != 200 || len(got["upcoming"].([]any)) == 0 {
		t.Fatalf("get: %d %v", code, got)
	}
	code, list := do(t, "GET", srv.URL+"/api/v1/subscriptions?status=active", nil, nil)
	if code != 200 || len(list["subscriptions"].([]any)) != 1 {
		t.Fatalf("list: %d %v", code, list)
	}
	code, bad := do(t, "POST", srv.URL+"/api/v1/subscriptions", map[string]any{"name": "X", "price": "1", "currency": "EUR", "interval": 1, "unit": "fortnight", "firstCharge": "2026-11-08"}, nil)
	if code != 422 {
		t.Fatalf("invalid unit: %d %v", code, bad)
	}
	code, _ = do(t, "GET", srv.URL+"/api/v1/subscriptions/sub_missing", nil, nil)
	if code != 404 {
		t.Fatalf("missing: %d", code)
	}
}

func TestOpenAPIParity(t *testing.T) {
	srv, _, _ := setup(t, true)
	res, err := http.Get(srv.URL + "/api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	for _, op := range app.Catalog() {
		if !strings.Contains(string(b), `"operationId":"`+op.Name+`"`) {
			t.Errorf("use case %s missing from the REST API", op.Name)
		}
	}
}

func TestTokensRequiredRemotely(t *testing.T) {
	srv, svc, owner := setup(t, false)
	if code, _ := do(t, "GET", srv.URL+"/api/v1/context", nil, nil); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	tok, err := auth.CreateToken(context.Background(), svc.Store(), owner.MemberID, "Claude", app.ScopeRead)
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{"Authorization": {"Bearer " + tok}}
	if code, _ := do(t, "GET", srv.URL+"/api/v1/context", nil, h); code != 200 {
		t.Fatalf("read token on read: %d", code)
	}
	if code, _ := do(t, "POST", srv.URL+"/api/v1/categories", map[string]any{"name": "New"}, h); code != 403 {
		t.Fatalf("read token on write: %d", code)
	}

	// The same token works for MCP over HTTP.
	client := mcp.NewClient(&mcp.Implementation{Name: "claude-test", Version: "1"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{tok}}}
	cs, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != len(app.Catalog()) {
		t.Fatalf("tools over HTTP: %v (%d)", err, len(tools.Tools))
	}
	res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "exhale://context"})
	if err != nil || !strings.Contains(res.Contents[0].Text, "Exhale household") {
		t.Fatalf("context over HTTP: %v", err)
	}
}

type bearer struct{ tok string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.tok)
	return http.DefaultTransport.RoundTrip(r)
}
