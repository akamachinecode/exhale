package assistant

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// sse answers with a stream of Server-Sent Events, flushed one by one.
func sse(events ...string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		for _, e := range events {
			_, _ = io.WriteString(w, e+"\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}
}

func ev(name, data string) string { return "event: " + name + "\ndata: " + data }

// TestAnthropicStream reads a streamed answer with reasoning, text and a
// tool call into the same Response a whole answer gives, and sends the
// assistant turn back with its reasoning block intact.
func TestAnthropicStream(t *testing.T) {
	rec, url := serve(t, sse(
		ev("message_start", `{"type":"message_start","message":{"id":"msg_1","model":"claude-opus-5-5","usage":{"input_tokens":321,"output_tokens":1}}}`),
		`: keep-alive`,
		ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`),
		ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Look it up."}}`),
		ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-xyz"}}`),
		ev("content_block_stop", `{"type":"content_block_stop","index":0}`),
		ev("ping", `{"type":"ping"}`),
		ev("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`),
		ev("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Let me "}}`),
		ev("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"look."}}`),
		ev("content_block_stop", `{"type":"content_block_stop","index":1}`),
		ev("content_block_start", `{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"list_subscriptions","input":{}}}`),
		ev("content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"query\":"}}`),
		ev("content_block_delta", `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":" \"netflix\"}"}}`),
		ev("content_block_stop", `{"type":"content_block_stop","index":2}`),
		ev("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":42}}`),
		ev("message_stop", `{"type":"message_stop"}`),
	), sse(
		ev("message_start", `{"type":"message_start","message":{"model":"claude-opus-5-5","usage":{"input_tokens":400,"output_tokens":1}}}`),
		ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
		ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"13.99 EUR."}}`),
		ev("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`),
		ev("message_stop", `{"type":"message_stop"}`),
	))
	a := NewAnthropic("k", "", url, 5*time.Second)
	var got []string
	req := Request{Tools: weather, MaxTokens: 100, Messages: []Message{{Role: RoleUser, Text: "netflix?"}}}
	res, err := a.Stream(context.Background(), req, func(d string) { got = append(got, d) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "Let me |look." {
		t.Errorf("text pieces: %q", got)
	}
	if res.Text != "Let me look." || res.Stop != StopTools || res.Usage != (Usage{Input: 321, Output: 42}) || res.Model != "claude-opus-5-5" {
		t.Errorf("response: %+v", res)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].ID != "toolu_1" || string(res.ToolCalls[0].Arguments) != `{"query":"netflix"}` {
		t.Fatalf("tool calls: %+v", res.ToolCalls)
	}
	if rec.bodies[0]["stream"] != true || rec.headers[0].Get("Accept") != "text/event-stream" {
		t.Errorf("request did not ask for a stream: %v", rec.bodies[0])
	}

	// The turn goes back as it came: the reasoning block with its
	// signature, in its place.
	req.Messages = append(req.Messages, Message{Role: RoleAssistant, Text: res.Text, ToolCalls: res.ToolCalls, Raw: res.Raw},
		Message{Role: RoleTool, ToolResults: []ToolResult{{CallID: "toolu_1", Name: "list_subscriptions", Content: "[]"}}})
	if _, err := a.Stream(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	sent, _ := json.Marshal(rec.bodies[1]["messages"].([]any)[1])
	for _, want := range []string{`"signature":"sig-xyz"`, `"thinking":"Look it up."`, `"input":{"query":"netflix"}`} {
		if !strings.Contains(string(sent), want) {
			t.Errorf("assistant turn sent back without %s: %s", want, sent)
		}
	}
}

// TestAnthropicStreamFailures: an error event mid-stream, a refusal, a
// stream cut short, and a gateway that ignores "stream" and answers JSON.
func TestAnthropicStreamFailures(t *testing.T) {
	_, url := serve(t,
		sse(ev("message_start", `{"type":"message_start","message":{"model":"m","usage":{}}}`),
			ev("error", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)),
		sse(ev("message_start", `{"type":"message_start","message":{"model":"m","usage":{}}}`),
			ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
			ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Sure, here is"}}`),
			ev("message_delta", `{"type":"message_delta","delta":{"stop_reason":"refusal"},"usage":{"output_tokens":3}}`),
			ev("message_stop", `{"type":"message_stop"}`)),
		sse(ev("message_start", `{"type":"message_start","message":{"model":"m","usage":{}}}`),
			ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)),
		reply(200, `{"model":"m","stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":2},"content":[{"type":"text","text":"Whole."}]}`),
	)
	a := NewAnthropic("k", "m", url, 5*time.Second)
	req := Request{MaxTokens: 10, Messages: []Message{{Role: RoleUser, Text: "hi"}}}
	if _, err := a.Stream(context.Background(), req, nil); !IsKind(err, KindOverloaded) {
		t.Errorf("error event: %v", err)
	}
	if _, err := a.Stream(context.Background(), req, nil); !IsKind(err, KindRefused) {
		t.Errorf("refusal: %v", err)
	}
	if _, err := a.Stream(context.Background(), req, nil); !IsKind(err, KindNoAnswer) {
		t.Errorf("cut short: %v", err)
	}
	var got string
	res, err := a.Stream(context.Background(), req, func(d string) { got += d })
	if err != nil || res.Text != "Whole." || got != "Whole." {
		t.Errorf("JSON instead of a stream: %+v %v %q", res, err, got)
	}
}

// TestOpenAIStream reads text and a tool call split across chunks, with
// the usage of the last chunk; a server that rejects stream_options is
// asked again without it.
func TestOpenAIStream(t *testing.T) {
	rec, url := serve(t,
		reply(400, `{"error":{"message":"Unrecognized request argument supplied: stream_options"}}`),
		sse(
			`data: {"model":"llama3.1","choices":[{"index":0,"delta":{"role":"assistant","content":"Che"}}]}`,
			`data: {"model":"llama3.1","choices":[{"index":0,"delta":{"content":"cking."}}]}`,
			`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"list_subscriptions","arguments":""}}]}}]}`,
			`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"query\""}}]}}]}`,
			`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"x\"}"}}]}}]}`,
			`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"name":"get_household","arguments":{}}}]}}]}`,
			`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: {"choices":[],"usage":{"prompt_tokens":90,"completion_tokens":12}}`,
			`data: [DONE]`,
		),
	)
	o := NewOpenAI("", "llama3.1", url, 5*time.Second)
	var got string
	res, err := o.Stream(context.Background(), Request{Tools: weather, MaxTokens: 50, Messages: []Message{{Role: RoleUser, Text: "x"}}}, func(d string) { got += d })
	if err != nil {
		t.Fatal(err)
	}
	if got != "Checking." || res.Text != "Checking." || res.Stop != StopTools || res.Usage != (Usage{Input: 90, Output: 12}) {
		t.Errorf("response: %+v (streamed %q)", res, got)
	}
	if len(res.ToolCalls) != 2 || string(res.ToolCalls[0].Arguments) != `{"query":"x"}` || res.ToolCalls[1].Name != "get_household" || res.ToolCalls[1].ID != "call_2" {
		t.Errorf("tool calls: %+v", res.ToolCalls)
	}
	if _, ok := rec.bodies[0]["stream_options"]; !ok {
		t.Error("the first request asks for usage")
	}
	if _, ok := rec.bodies[1]["stream_options"]; ok || rec.bodies[1]["stream"] != true {
		t.Errorf("the retry: %v", rec.bodies[1])
	}
}

// TestAskStreams: the person sees each call's text as it is written; a
// call after tools starts again; the cards come only with the finished turn.
func TestAskStreams(t *testing.T) {
	w := newWorld(t)
	w.fake.Then(
		func(Request) (Response, error) {
			return Response{Text: "Let me check.", ToolCalls: []ToolCall{{ID: "c1", Name: "create_subscription", Arguments: json.RawMessage(`{"name":"Netflix","price":"13.99","currency":"EUR","firstCharge":"2026-11-12"}`)}}, Stop: StopTools}, nil
		},
		Say("I prepared Netflix for you."),
	)
	var seen []string
	turn, err := w.a.AskStream(w.ctx, w.owner, "c", "add netflix", func(text string) { seen = append(seen, text) })
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Let ", "Let me ", "Let me check.", "I ", "I prepared ", "I prepared Netflix ", "I prepared Netflix for ", "I prepared Netflix for you."}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Errorf("progress:\n %q\nwant\n %q", seen, want)
	}
	if turn.Text != "I prepared Netflix for you." || len(turn.Proposals) != 1 {
		t.Errorf("turn: %+v", turn)
	}
}
