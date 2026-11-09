package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/store"
)

func connect(t *testing.T, scope app.Scope) (*mcp.ClientSession, *app.Service, app.Actor) {
	t.Helper()
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	svc := app.New(st, bus.New(), nil, func() time.Time { return now })
	owner, err := svc.Bootstrap(ctx, app.BootstrapInput{HouseholdName: "Home", OwnerName: "Alex", Currency: "EUR", Timezone: "Europe/Madrid", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	agent := owner
	agent.Kind, agent.Scope, agent.Label = app.ActorAgent, scope, "test agent (MCP)"
	srv, err := NewServer(svc, "test", func(context.Context, *mcp.RequestExtra, *mcp.ServerSession) (app.Actor, error) { return agent, nil })
	if err != nil {
		t.Fatal(err)
	}
	ct, st2 := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st2, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, svc, owner
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, map[string]any) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError {
		t.Fatalf("%s failed: %s", name, text)
	}
	var out map[string]any
	b, _ := json.Marshal(res.StructuredContent)
	_ = json.Unmarshal(b, &out)
	return text, out
}

// TestParityEveryUseCaseIsATool is the parity rule of ADR 0018.
func TestParityEveryUseCaseIsATool(t *testing.T) {
	cs, _, _ := connect(t, app.ScopeWrite)
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]*mcp.Tool{}
	for _, tl := range tools.Tools {
		have[tl.Name] = tl
	}
	for _, b := range app.Catalog() {
		tl, ok := have[b.Name]
		if !ok {
			t.Errorf("use case %s is not exposed over MCP", b.Name)
			continue
		}
		if tl.Annotations.ReadOnlyHint == b.Write {
			t.Errorf("%s: readOnlyHint does not match", b.Name)
		}
	}
}

func TestAgentWorkflow(t *testing.T) {
	cs, _, _ := connect(t, app.ScopeWrite)
	ctx := context.Background()

	res, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "exhale://context"})
	if err != nil || !strings.Contains(res.Contents[0].Text, "How Exhale works") {
		t.Fatalf("context resource: %v", err)
	}

	_, preview := call(t, cs, "create_subscription", map[string]any{"name": "Netflix", "price": "19.99", "currency": "EUR", "interval": 1, "unit": "month", "firstCharge": "2026-11-12", "dryRun": true})
	if preview["subscription"] == nil {
		t.Fatalf("preview = %v", preview)
	}
	_, list := call(t, cs, "list_subscriptions", map[string]any{})
	if n := len(list["subscriptions"].([]any)); n != 0 {
		t.Fatalf("dry run saved %d subscriptions", n)
	}

	_, created := call(t, cs, "create_subscription", map[string]any{"name": "Netflix", "price": "19.99", "currency": "EUR", "interval": 1, "unit": "month", "firstCharge": "2026-11-12"})
	sub := created["subscription"].(map[string]any)
	id := sub["id"].(string)

	text, _ := call(t, cs, "set_subscription_status", map[string]any{"id": id, "action": "cancel_now"})
	if !strings.HasPrefix(text, "PREVIEW ONLY") {
		t.Fatalf("destructive tool without confirm must only preview, got %.80s", text)
	}
	_, after := call(t, cs, "get_subscription", map[string]any{"id": id})
	if after["subscription"].(map[string]any)["status"] != "active" {
		t.Fatal("preview cancelled the subscription")
	}
	call(t, cs, "set_subscription_status", map[string]any{"id": id, "action": "cancel_now", "confirm": true})
	_, after = call(t, cs, "get_subscription", map[string]any{"id": id})
	if after["subscription"].(map[string]any)["status"] != "cancelled" {
		t.Fatal("confirmed cancel did not apply")
	}
}

func TestReadOnlyTokenGetsReadableError(t *testing.T) {
	cs, _, _ := connect(t, app.ScopeRead)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_subscription", Arguments: map[string]any{"name": "X", "price": "1", "currency": "EUR", "unit": "month", "firstCharge": "2026-12-01"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "read-only") {
		t.Fatalf("expected a read-only error, got %+v", res)
	}
}
