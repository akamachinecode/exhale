package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/akamachinecode/exhale/internal/domain"
)

// TestOffUntilConfigured: nothing answers, and nothing is sent, until an
// owner sets a provider.
func TestOffUntilConfigured(t *testing.T) {
	w := newWorld(t)
	w.a.SetProvider(nil)
	if w.a.Enabled(w.ctx) {
		t.Fatal("the assistant must be off by default")
	}
	if _, err := w.a.Ask(w.ctx, w.owner, "c", "hello"); !IsKind(err, KindNotConfigured) {
		t.Fatalf("got %v", err)
	}
	if st := w.a.Status(w.ctx); st.Enabled || st.Configured || st.HasKey || len(st.Locked) != 0 {
		t.Fatalf("status: %+v", st)
	}
}

func TestConfigureFromTheWeb(t *testing.T) {
	w := newWorld(t)
	w.a.SetProvider(nil)
	bad := []Settings{
		{Provider: "gemini"},
		{Provider: ProviderAnthropic},                                   // no key
		{Provider: ProviderOpenAI, BaseURL: "http://localhost:11434"},   // no model
		{Provider: ProviderOpenAI, Model: "m", BaseURL: "localhost:11"}, // not an address
	}
	for _, in := range bad {
		if _, err := w.a.Configure(w.ctx, in); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%+v: %v", in, err)
		}
	}
	st, err := w.a.Configure(w.ctx, Settings{Provider: ProviderAnthropic, APIKey: " sk-ant-secret "})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Enabled || !st.Configured || !st.HasKey || st.Model != DefaultAnthropicModel || st.Provider != ProviderAnthropic {
		t.Fatalf("status: %+v", st)
	}
	if b, _ := json.Marshal(st); strings.Contains(string(b), "sk-ant-secret") {
		t.Fatalf("the key is in the status: %s", b)
	}
	if c := w.a.Config(w.ctx); c.APIKey != "sk-ant-secret" {
		t.Fatalf("key saved as %q", c.APIKey)
	}
	// Saving again without a key keeps it; the switch turns it off.
	off := false
	st, err = w.a.Configure(w.ctx, Settings{Provider: ProviderAnthropic, Model: "claude-sonnet-5-5", Enabled: &off})
	if err != nil || st.Enabled || !st.Configured || !st.HasKey || st.Model != "claude-sonnet-5-5" || st.On {
		t.Fatalf("status: %+v %v", st, err)
	}
	if _, err := w.a.Ask(w.ctx, w.owner, "c", "hello"); !IsKind(err, KindNotConfigured) {
		t.Fatalf("switched off, but: %v", err)
	}
	// Changing provider does not carry the old key over to another company.
	st, err = w.a.Configure(w.ctx, Settings{Provider: ProviderOpenAI, Model: "llama3.1", BaseURL: "http://localhost:11434"})
	if err != nil || st.HasKey || !st.Enabled {
		t.Fatalf("status: %+v %v", st, err)
	}
	if st, _ := w.a.Remove(w.ctx); st.Configured || st.Enabled || st.Provider != "" {
		t.Fatalf("after removing: %+v", st)
	}
}

func TestEnvironmentWins(t *testing.T) {
	w := newWorld(t)
	a, err := New(w.svc, nil, Env{APIKey: "sk-env", Model: "claude-haiku-5-5"})
	if err != nil {
		t.Fatal(err)
	}
	st := a.Status(w.ctx)
	if !st.Enabled || st.Provider != ProviderAnthropic || st.Model != "claude-haiku-5-5" || strings.Join(st.Locked, ",") != "apiKey,model" {
		t.Fatalf("a key in the environment is enough: %+v", st)
	}
	// The web cannot replace what the server's environment sets.
	if _, err := a.Configure(w.ctx, Settings{Provider: ProviderAnthropic, APIKey: "sk-web", Model: "other"}); err != nil {
		t.Fatal(err)
	}
	if c := a.Config(w.ctx); c.APIKey != "sk-env" || c.Model != "claude-haiku-5-5" {
		t.Fatalf("config: %+v", c)
	}
	// The switch still works.
	off := false
	if st, _ := a.Configure(w.ctx, Settings{Provider: ProviderAnthropic, Enabled: &off}); st.Enabled {
		t.Fatalf("the switch must turn it off: %+v", st)
	}
}

// TestSettingsTest: the Test button makes one small call through the real
// client and says whether the model can use tools.
func TestSettingsTest(t *testing.T) {
	w := newWorld(t)
	w.a.SetProvider(nil)
	if res := w.a.Test(w.ctx); res.OK {
		t.Fatalf("nothing is configured: %+v", res)
	}
	rec, url := serve(t,
		reply(200, `{"model":"llama3.1","choices":[{"finish_reason":"tool_calls","message":{"content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"ping","arguments":"{}"}}]}}]}`),
		reply(200, `{"model":"tiny","choices":[{"finish_reason":"stop","message":{"content":"OK"}}]}`),
		reply(http.StatusUnauthorized, `{"error":{"message":"Incorrect API key provided"}}`),
	)
	off := false
	if _, err := w.a.Configure(w.ctx, Settings{Provider: ProviderOpenAI, Model: "llama3.1", BaseURL: url, APIKey: "k", Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if res := w.a.Test(context.Background()); !res.OK || res.Model != "llama3.1" || !strings.Contains(res.Message.String(), "can use") {
		t.Fatalf("a model with tools, tested while switched off: %+v", res)
	}
	if res := w.a.Test(w.ctx); !res.OK || !strings.Contains(res.Message.String(), "did not use the test tool") {
		t.Fatalf("a model without tools: %+v", res)
	}
	if res := w.a.Test(w.ctx); res.OK || !strings.Contains(res.Message.String(), "rejected the API key") || !strings.Contains(res.Message.String(), "Incorrect API key") {
		t.Fatalf("a bad key: %+v", res)
	}
	if rec.bodies[0]["model"] != "llama3.1" || len(rec.bodies[0]["tools"].([]any)) != 1 {
		t.Errorf("test request: %v", rec.bodies[0])
	}
}
