package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/akamachinecode/exhale/internal/assistant"
)

// stream posts to the streaming endpoint and returns its events in order.
func (b *browser) stream(text string) (events []string, data []map[string]any) {
	b.t.Helper()
	body, _ := json.Marshal(map[string]any{"text": text})
	req, _ := http.NewRequest("POST", b.base+"/api/v1/assistant/messages/stream", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	res, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
		b.t.Fatalf("stream: %d %s", res.StatusCode, raw)
	}
	for _, block := range strings.Split(strings.TrimSpace(string(raw)), "\n\n") {
		name, payload, _ := strings.Cut(block, "\n")
		var v map[string]any
		_ = json.Unmarshal([]byte(strings.TrimPrefix(payload, "data: ")), &v)
		events = append(events, strings.TrimPrefix(name, "event: "))
		data = append(data, v)
	}
	return events, data
}

// TestAskExhaleStreams: the web gets the answer as it is written, then the
// finished turn with the card Exhale wrote.
func TestAskExhaleStreams(t *testing.T) {
	srv, _, asst := assistantServer(t)
	owner, _ := household(t, srv.URL)
	model := &assistant.Fake{}
	asst.SetProvider(model)
	model.Then(
		func(assistant.Request) (assistant.Response, error) {
			return assistant.Response{Text: "One moment.", ToolCalls: []assistant.ToolCall{{ID: "c1", Name: "create_subscription", Arguments: json.RawMessage(`{"name":"Netflix","price":"13.99","currency":"EUR","firstCharge":"2026-11-12"}`)}}, Stop: assistant.StopTools}, nil
		},
		assistant.Say("I prepared Netflix."),
	)
	events, data := owner.stream("add netflix")
	got := strings.Join(events, ",")
	if got != "delta,delta,text,delta,delta,turn" {
		t.Fatalf("events: %s %v", got, data)
	}
	if data[0]["text"] != "One " || data[2]["text"] != "I " || data[3]["text"] != "prepared " {
		t.Errorf("pieces: %v", data)
	}
	turn := data[5]
	if turn["text"] != "I prepared Netflix." || len(turn["proposals"].([]any)) != 1 {
		t.Errorf("turn: %v", turn)
	}

	// A failure arrives as an error event with its translatable text.
	asst.SetProvider(&assistant.Fake{Steps: []func(assistant.Request) (assistant.Response, error){assistant.Fail(&assistant.Error{Kind: assistant.KindRateLimited})}})
	events, data = owner.stream("hi")
	if strings.Join(events, ",") != "error" || data[0]["status"] != float64(429) || data[0]["text"].(map[string]any)["key"] != "The model provider is asking to slow down. Try again in a minute." {
		t.Errorf("error: %v %v", events, data)
	}
}
