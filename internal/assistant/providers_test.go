package assistant

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder is an HTTP fake of a model API: it records every request and
// answers with the next scripted response.
type recorder struct {
	mu      sync.Mutex
	bodies  []map[string]any
	headers []http.Header
	paths   []string
	answers []func(w http.ResponseWriter)
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	raw, _ := io.ReadAll(req.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	r.mu.Lock()
	r.bodies = append(r.bodies, body)
	r.headers = append(r.headers, req.Header.Clone())
	r.paths = append(r.paths, req.URL.Path)
	n := len(r.bodies) - 1
	var answer func(w http.ResponseWriter)
	if n < len(r.answers) {
		answer = r.answers[n]
	}
	r.mu.Unlock()
	if answer == nil {
		http.Error(w, `{"error":"no answer scripted"}`, http.StatusTeapot)
		return
	}
	answer(w)
}

func reply(status int, body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

func serve(t *testing.T, answers ...func(http.ResponseWriter)) (*recorder, string) {
	t.Helper()
	retryWait = 5 * time.Millisecond
	r := &recorder{answers: answers}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return r, srv.URL
}

var weather = []Tool{{Name: "list_subscriptions", Description: "Lists subscriptions.", Schema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)}}

// TestAnthropicToolRoundTrip checks both directions of the Messages API:
// what is sent (headers, system, tools, an attachment, a tool result, the
// assistant turn exactly as it came) and what is read back.
func TestAnthropicToolRoundTrip(t *testing.T) {
	// The first answer has a thinking block: it must be sent back untouched.
	first := `{"id":"msg_1","model":"claude-opus-5-5","role":"assistant","stop_reason":"tool_use","usage":{"input_tokens":120,"output_tokens":30},
	  "content":[{"type":"thinking","thinking":"","signature":"sig-abc"},{"type":"text","text":"Let me look."},{"type":"tool_use","id":"toolu_1","name":"list_subscriptions","input":{"query":"netflix"}}]}`
	second := `{"id":"msg_2","model":"claude-opus-5-5","role":"assistant","stop_reason":"end_turn","usage":{"input_tokens":200,"output_tokens":12},
	  "content":[{"type":"text","text":"Netflix costs 13.99 EUR."}]}`
	rec, url := serve(t, reply(200, first), reply(200, second))
	a := NewAnthropic("sk-ant-test", "", url, 5*time.Second)
	ctx := context.Background()

	req := Request{System: "rules", Tools: weather, MaxTokens: 1000, Messages: []Message{
		{Role: RoleUser, Text: "how much is netflix?", Attachments: []Attachment{{MediaType: "image/png", Data: []byte("png")}, {MediaType: "application/pdf", Data: []byte("pdf")}}},
	}}
	res, err := a.Complete(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stop != StopTools || res.Text != "Let me look." || len(res.ToolCalls) != 1 || res.ToolCalls[0].ID != "toolu_1" || string(res.ToolCalls[0].Arguments) != `{"query":"netflix"}` || res.Usage.Input != 120 {
		t.Fatalf("first response: %+v", res)
	}
	req.Messages = append(req.Messages,
		Message{Role: RoleAssistant, Text: res.Text, ToolCalls: res.ToolCalls, Raw: res.Raw},
		Message{Role: RoleTool, ToolResults: []ToolResult{{CallID: "toolu_1", Content: `{"subscriptions":[]}`}, {CallID: "toolu_2", Content: "error: nope", IsError: true}}},
	)
	res, err = a.Complete(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stop != StopEnd || res.Text != "Netflix costs 13.99 EUR." || len(res.ToolCalls) != 0 {
		t.Fatalf("second response: %+v", res)
	}

	h := rec.headers[0]
	if h.Get("x-api-key") != "sk-ant-test" || h.Get("anthropic-version") != anthropicVersion || rec.paths[0] != "/v1/messages" {
		t.Errorf("headers: %v path %s", h, rec.paths[0])
	}
	if h.Get("anthropic-beta") != "" {
		t.Error("the fallback beta must only go to Anthropic's own API")
	}
	b := rec.bodies[0]
	if b["model"] != DefaultAnthropicModel || b["max_tokens"].(float64) != 1000 {
		t.Errorf("model and max_tokens: %v %v", b["model"], b["max_tokens"])
	}
	for _, banned := range []string{"temperature", "top_p", "top_k", "thinking", "tool_choice"} {
		if _, ok := b[banned]; ok {
			t.Errorf("%s must not be sent: current models reject it", banned)
		}
	}
	sys := b["system"].([]any)[0].(map[string]any)
	if sys["text"] != "rules" || sys["cache_control"].(map[string]any)["type"] != "ephemeral" {
		t.Errorf("system: %v", sys)
	}
	tool := b["tools"].([]any)[0].(map[string]any)
	if tool["name"] != "list_subscriptions" || tool["input_schema"].(map[string]any)["type"] != "object" {
		t.Errorf("tool: %v", tool)
	}
	content := b["messages"].([]any)[0].(map[string]any)["content"].([]any)
	img, doc, text := content[0].(map[string]any), content[1].(map[string]any), content[2].(map[string]any)
	if img["type"] != "image" || img["source"].(map[string]any)["media_type"] != "image/png" || img["source"].(map[string]any)["data"] != "cG5n" {
		t.Errorf("image block: %v", img)
	}
	if doc["type"] != "document" || doc["source"].(map[string]any)["media_type"] != "application/pdf" || text["type"] != "text" {
		t.Errorf("document must come before the text: %v", content)
	}

	msgs := rec.bodies[1]["messages"].([]any)
	back := msgs[1].(map[string]any)
	blocks := back["content"].([]any)
	if back["role"] != "assistant" || len(blocks) != 3 || blocks[0].(map[string]any)["signature"] != "sig-abc" {
		t.Errorf("the assistant turn must go back exactly as it came, thinking block included: %v", back)
	}
	results := msgs[2].(map[string]any)
	rs := results["content"].([]any)
	if results["role"] != "user" || len(rs) != 2 {
		t.Fatalf("every tool result goes in one user message: %v", results)
	}
	if r := rs[0].(map[string]any); r["type"] != "tool_result" || r["tool_use_id"] != "toolu_1" || r["is_error"] != nil {
		t.Errorf("tool result: %v", r)
	}
	if r := rs[1].(map[string]any); r["is_error"] != true {
		t.Errorf("a failed tool must be marked: %v", r)
	}
}

func TestAnthropicErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		kind   Kind
	}{
		{"bad key", 401, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`, KindAuth},
		{"unknown model", 404, `{"type":"error","error":{"type":"not_found_error","message":"model: claude-nope"}}`, KindModel},
		{"bad request", 400, `{"type":"error","error":{"type":"invalid_request_error","message":"messages: empty"}}`, KindRejected},
		{"too large", 413, `{"type":"error","error":{"type":"request_too_large","message":"too large"}}`, KindTooLarge},
		{"not json", 200, `<html>proxy</html>`, KindNoAnswer},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, url := serve(t, reply(c.status, c.body))
			_, err := NewAnthropic("k", "m", url, 5*time.Second).Complete(context.Background(), Request{MaxTokens: 10, Messages: []Message{{Role: RoleUser, Text: "hi"}}})
			if !IsKind(err, c.kind) {
				t.Fatalf("got %v", err)
			}
			if strings.Contains(Explain(err), "x-api-key") {
				t.Error("the sentence for the person must not quote the provider")
			}
		})
	}
	t.Run("refusal", func(t *testing.T) {
		_, url := serve(t, reply(200, `{"model":"claude-opus-5-5","stop_reason":"refusal","content":[],"usage":{"input_tokens":5,"output_tokens":0}}`))
		_, err := NewAnthropic("k", "m", url, 5*time.Second).Complete(context.Background(), Request{MaxTokens: 10, Messages: []Message{{Role: RoleUser, Text: "hi"}}})
		if !IsKind(err, KindRefused) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("cut short", func(t *testing.T) {
		_, url := serve(t, reply(200, `{"model":"m","stop_reason":"max_tokens","content":[{"type":"text","text":"Half an ans"}],"usage":{}}`))
		res, err := NewAnthropic("k", "m", url, 5*time.Second).Complete(context.Background(), Request{MaxTokens: 10, Messages: []Message{{Role: RoleUser, Text: "hi"}}})
		if err != nil || res.Stop != StopLength || res.Text != "Half an ans" {
			t.Fatalf("got %+v %v", res, err)
		}
	})
	t.Run("retries once when overloaded", func(t *testing.T) {
		rec, url := serve(t, reply(529, `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`), reply(200, `{"model":"m","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{}}`))
		res, err := NewAnthropic("k", "m", url, 5*time.Second).Complete(context.Background(), Request{MaxTokens: 10, Messages: []Message{{Role: RoleUser, Text: "hi"}}})
		if err != nil || res.Text != "ok" || len(rec.bodies) != 2 {
			t.Fatalf("got %+v %v after %d calls", res, err, len(rec.bodies))
		}
	})
	t.Run("gives up after the retry", func(t *testing.T) {
		rec, url := serve(t, reply(500, `{}`), reply(500, `{}`), reply(200, `{}`))
		_, err := NewAnthropic("k", "m", url, 5*time.Second).Complete(context.Background(), Request{MaxTokens: 10, Messages: []Message{{Role: RoleUser, Text: "hi"}}})
		if !IsKind(err, KindOverloaded) || len(rec.bodies) != 2 {
			t.Fatalf("got %v after %d calls", err, len(rec.bodies))
		}
	})
	t.Run("rate limited with a long wait is not retried", func(t *testing.T) {
		rec, url := serve(t, func(w http.ResponseWriter) {
			w.Header().Set("Retry-After", "60")
			reply(429, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`)(w)
		})
		_, err := NewAnthropic("k", "m", url, 5*time.Second).Complete(context.Background(), Request{MaxTokens: 10, Messages: []Message{{Role: RoleUser, Text: "hi"}}})
		if !IsKind(err, KindRateLimited) || len(rec.bodies) != 1 {
			t.Fatalf("got %v after %d calls", err, len(rec.bodies))
		}
	})
	t.Run("timeout", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(300 * time.Millisecond):
			}
		}))
		defer srv.Close()
		_, err := NewAnthropic("k", "m", srv.URL, 50*time.Millisecond).Complete(context.Background(), Request{MaxTokens: 10, Messages: []Message{{Role: RoleUser, Text: "hi"}}})
		if !IsKind(err, KindNoAnswer) || !isTimeout(err) {
			t.Fatalf("got %v", err)
		}
		if !strings.Contains(Explain(err), "did not answer") {
			t.Errorf("sentence: %s", Explain(err))
		}
	})
}

// TestAnthropicFallback: on Anthropic's own API the request opts into the
// server-side fallback for declined requests, and stops sending it once the
// endpoint rejects it.
func TestAnthropicFallback(t *testing.T) {
	rec, url := serve(t,
		reply(400, `{"type":"error","error":{"type":"invalid_request_error","message":"Unexpected value(s) for the anthropic-beta header."}}`),
		reply(200, `{"model":"claude-opus-5-5","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{}}`),
		reply(200, `{"model":"claude-opus-5-5","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{}}`),
	)
	a := NewAnthropic("k", "", "", 5*time.Second)
	a.hc.Transport = rewrite{to: url} // Anthropic's own address, answered by the fake
	req := Request{MaxTokens: 10, Messages: []Message{{Role: RoleUser, Text: "hi"}}}
	if _, err := a.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if rec.bodies[0]["fallbacks"] != "default" || rec.headers[0].Get("anthropic-beta") != fallbackBeta {
		t.Errorf("the first request must opt into the fallback: %v", rec.bodies[0])
	}
	for i := 1; i <= 2; i++ {
		if _, has := rec.bodies[i]["fallbacks"]; has || rec.headers[i].Get("anthropic-beta") != "" {
			t.Errorf("request %d still sends the fallback after it was rejected", i+1)
		}
	}
	if supportsFallback("claude-haiku-5-5") || !supportsFallback("claude-sonnet-5-5") {
		t.Error("fallbacks go only to the models that have them")
	}
}

// rewrite sends every request to another address, keeping the path.
type rewrite struct{ to string }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	u := strings.TrimPrefix(r.to, "http://")
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = "http", u
	return http.DefaultTransport.RoundTrip(req)
}

// TestOpenAIToolRoundTrip checks both directions of chat completions.
func TestOpenAIToolRoundTrip(t *testing.T) {
	first := `{"model":"gpt-test","choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,
	  "tool_calls":[{"id":"call_1","type":"function","function":{"name":"list_subscriptions","arguments":"{\"query\":\"netflix\"}"}}]}}],"usage":{"prompt_tokens":90,"completion_tokens":20}}`
	second := `{"model":"gpt-test","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"Netflix costs 13.99 EUR."}}],"usage":{"prompt_tokens":150,"completion_tokens":9}}`
	rec, url := serve(t, reply(200, first), reply(200, second))
	o := NewOpenAI("sk-test", "gpt-test", url+"/v1/", 5*time.Second)
	ctx := context.Background()
	req := Request{System: "rules", Tools: weather, MaxTokens: 500, Messages: []Message{
		{Role: RoleUser, Text: "how much is netflix?", Attachments: []Attachment{{MediaType: "image/jpeg", Data: []byte("jpg")}}},
	}}
	res, err := o.Complete(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stop != StopTools || len(res.ToolCalls) != 1 || res.ToolCalls[0].ID != "call_1" || string(res.ToolCalls[0].Arguments) != `{"query":"netflix"}` || res.Usage.Output != 20 {
		t.Fatalf("first response: %+v", res)
	}
	req.Messages = append(req.Messages,
		Message{Role: RoleAssistant, ToolCalls: res.ToolCalls},
		Message{Role: RoleTool, ToolResults: []ToolResult{{CallID: "call_1", Content: `{"subscriptions":[]}`}}},
	)
	res, err = o.Complete(ctx, req)
	if err != nil || res.Stop != StopEnd || res.Text != "Netflix costs 13.99 EUR." {
		t.Fatalf("second response: %+v %v", res, err)
	}

	if rec.paths[0] != "/v1/chat/completions" || rec.headers[0].Get("Authorization") != "Bearer sk-test" {
		t.Errorf("path %s, auth %q", rec.paths[0], rec.headers[0].Get("Authorization"))
	}
	b := rec.bodies[0]
	if b["model"] != "gpt-test" || b["max_tokens"].(float64) != 500 {
		t.Errorf("body: %v", b)
	}
	msgs := b["messages"].([]any)
	if sys := msgs[0].(map[string]any); sys["role"] != "system" || sys["content"] != "rules" {
		t.Errorf("system message: %v", sys)
	}
	parts := msgs[1].(map[string]any)["content"].([]any)
	if parts[0].(map[string]any)["type"] != "text" || parts[1].(map[string]any)["image_url"].(map[string]any)["url"] != "data:image/jpeg;base64,anBn" {
		t.Errorf("picture: %v", parts)
	}
	fn := b["tools"].([]any)[0].(map[string]any)
	if fn["type"] != "function" || fn["function"].(map[string]any)["name"] != "list_subscriptions" {
		t.Errorf("tool: %v", fn)
	}
	msgs = rec.bodies[1]["messages"].([]any)
	call := msgs[2].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	if call["id"] != "call_1" || call["function"].(map[string]any)["arguments"] != `{"query":"netflix"}` {
		t.Errorf("the assistant's tool call must go back with its arguments as a JSON string: %v", call)
	}
	if tr := msgs[3].(map[string]any); tr["role"] != "tool" || tr["tool_call_id"] != "call_1" {
		t.Errorf("tool result: %v", tr)
	}
}

func TestOpenAICompatibleServers(t *testing.T) {
	ask := Request{MaxTokens: 100, Messages: []Message{{Role: RoleUser, Text: "hi"}}}
	t.Run("local server: no key, bare address, arguments as an object, no call id", func(t *testing.T) {
		rec, url := serve(t, reply(200, `{"model":"llama3.1","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"",
		  "tool_calls":[{"function":{"name":"list_subscriptions","arguments":{"query":"x"}}}]}}]}`))
		res, err := NewOpenAI("", "llama3.1", url, 5*time.Second).Complete(context.Background(), ask)
		if err != nil {
			t.Fatal(err)
		}
		if rec.paths[0] != "/v1/chat/completions" {
			t.Errorf("a bare address gets /v1: %s", rec.paths[0])
		}
		if _, has := rec.headers[0]["Authorization"]; has {
			t.Error("no key, no Authorization header")
		}
		if res.Stop != StopTools || res.ToolCalls[0].ID == "" || string(res.ToolCalls[0].Arguments) != `{"query":"x"}` {
			t.Fatalf("response: %+v", res)
		}
	})
	t.Run("a model that wants max_completion_tokens", func(t *testing.T) {
		rec, url := serve(t,
			reply(400, `{"error":{"message":"Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead.","type":"invalid_request_error"}}`),
			reply(200, `{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]}`),
			reply(200, `{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]}`),
		)
		o := NewOpenAI("k", "gpt-next", url, 5*time.Second)
		for range 2 {
			if res, err := o.Complete(context.Background(), ask); err != nil || res.Text != "ok" {
				t.Fatalf("got %+v %v", res, err)
			}
		}
		for i := 1; i <= 2; i++ {
			if _, old := rec.bodies[i]["max_tokens"]; old || rec.bodies[i]["max_completion_tokens"].(float64) != 100 {
				t.Errorf("request %d: %v", i+1, rec.bodies[i])
			}
		}
	})
	t.Run("errors", func(t *testing.T) {
		for status, kind := range map[int]Kind{401: KindAuth, 404: KindModel, 429: KindRateLimited, 400: KindRejected} {
			_, url := serve(t, func(w http.ResponseWriter) {
				w.Header().Set("Retry-After", "30")
				reply(status, `{"error":"model 'nope' not found"}`)(w)
			})
			_, err := NewOpenAI("k", "nope", url, 5*time.Second).Complete(context.Background(), ask)
			if !IsKind(err, kind) {
				t.Errorf("%d: got %v", status, err)
			}
			if e, ok := err.(*Error); !ok || !strings.Contains(e.Detail, "not found") {
				t.Errorf("%d: the plain error text of a local server must be kept for the settings test: %v", status, err)
			}
		}
	})
	t.Run("no choices", func(t *testing.T) {
		_, url := serve(t, reply(200, `{"choices":[]}`))
		if _, err := NewOpenAI("k", "m", url, 5*time.Second).Complete(context.Background(), ask); !IsKind(err, KindNoAnswer) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("server down", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		srv.Close()
		if _, err := NewOpenAI("", "m", srv.URL, time.Second).Complete(context.Background(), ask); !IsKind(err, KindNoAnswer) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("a PDF goes as a file part", func(t *testing.T) {
		rec, url := serve(t, reply(200, `{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]}`))
		_, err := NewOpenAI("k", "m", url, 5*time.Second).Complete(context.Background(), Request{MaxTokens: 10, Messages: []Message{{Role: RoleUser, Text: "read", Attachments: []Attachment{{MediaType: "application/pdf", Data: []byte("pdf")}}}}})
		if err != nil {
			t.Fatal(err)
		}
		part := rec.bodies[0]["messages"].([]any)[0].(map[string]any)["content"].([]any)[1].(map[string]any)
		if part["type"] != "file" || !strings.HasPrefix(part["file"].(map[string]any)["file_data"].(string), "data:application/pdf;base64,") {
			t.Errorf("part: %v", part)
		}
	})
}
