package assistant

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// DefaultOpenAIURL is used when the owner picks the OpenAI-compatible
// provider without an address.
const DefaultOpenAIURL = "https://api.openai.com/v1"

// OpenAI talks to any endpoint that speaks the chat completions protocol:
// OpenAI, OpenRouter, Ollama, LM Studio, vLLM and others.
type OpenAI struct {
	key, model, base string
	hc               *http.Client
	// completionTokens is set once the endpoint asked for
	// max_completion_tokens instead of max_tokens (newer OpenAI models).
	completionTokens atomic.Bool
}

// NewOpenAI returns a client for a chat completions endpoint. key may be
// empty for local servers.
func NewOpenAI(key, model, baseURL string, timeout time.Duration) *OpenAI {
	return &OpenAI{key: key, model: model, base: openAIBase(baseURL), hc: &http.Client{Timeout: timeout}}
}

// openAIBase accepts "http://localhost:11434" as well as ".../v1": local
// servers are usually written without the path.
func openAIBase(s string) string {
	s = trimSlash(s)
	if s == "" {
		return DefaultOpenAIURL
	}
	if u, err := url.Parse(s); err == nil && (u.Path == "" || u.Path == "/") {
		return s + "/v1"
	}
	return s
}

// Name implements Provider.
func (o *OpenAI) Name() string { return ProviderOpenAI }

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    any              `json:"content"` // string, []openAIPart or nil
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIPart struct {
	Type     string            `json:"type"`
	Text     string            `json:"text,omitempty"`
	ImageURL map[string]string `json:"image_url,omitempty"`
	File     map[string]string `json:"file,omitempty"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name string `json:"name"`
		// Arguments is a JSON string by the protocol; some local servers
		// send the object itself.
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type openAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type openAIRequest struct {
	Model               string          `json:"model"`
	Messages            []openAIMessage `json:"messages"`
	Tools               []openAITool    `json:"tools,omitempty"`
	MaxTokens           int             `json:"max_tokens,omitempty"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
}

type openAIResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content   *string          `json:"content"`
			ToolCalls []openAIToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		Input  int `json:"prompt_tokens"`
		Output int `json:"completion_tokens"`
	} `json:"usage"`
	Error json.RawMessage `json:"error"`
}

// Complete implements Provider.
func (o *OpenAI) Complete(ctx context.Context, req Request) (Response, error) {
	body := openAIRequest{Model: o.model, Messages: openAIMessages(req)}
	if o.completionTokens.Load() {
		body.MaxCompletionTokens = req.MaxTokens
	} else {
		body.MaxTokens = req.MaxTokens
	}
	for _, t := range req.Tools {
		var ot openAITool
		ot.Type = "function"
		ot.Function.Name, ot.Function.Description, ot.Function.Parameters = t.Name, t.Description, t.Schema
		body.Tools = append(body.Tools, ot)
	}
	headers := map[string]string{}
	if o.key != "" {
		headers["Authorization"] = "Bearer " + o.key
	}
	status, data, err := post(ctx, o.hc, o.base+"/chat/completions", headers, body)
	if err != nil {
		return Response{}, err
	}
	var out openAIResponse
	jsonErr := json.Unmarshal(data, &out)
	if status != http.StatusOK {
		detail := openAIErrorText(out.Error, data)
		if status == http.StatusBadRequest && !o.completionTokens.Load() && strings.Contains(detail, "max_completion_tokens") {
			o.completionTokens.Store(true)
			return o.Complete(ctx, req)
		}
		return Response{}, statusError(status, detail)
	}
	if jsonErr != nil || len(out.Choices) == 0 {
		return Response{}, errKind(KindNoAnswer, "the answer had no choices")
	}
	c := out.Choices[0]
	res := Response{Model: out.Model, Usage: Usage{Input: out.Usage.Input, Output: out.Usage.Output}}
	if c.Message.Content != nil {
		res.Text = strings.TrimSpace(*c.Message.Content)
	}
	for i, tc := range c.Message.ToolCalls {
		args := tc.Function.Arguments
		var s string
		if json.Unmarshal(args, &s) == nil {
			args = json.RawMessage(s)
		}
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", i+1)
		}
		res.ToolCalls = append(res.ToolCalls, ToolCall{ID: id, Name: tc.Function.Name, Arguments: args})
	}
	switch {
	case len(res.ToolCalls) > 0:
		res.Stop = StopTools
	case c.FinishReason == "length":
		res.Stop = StopLength
	case c.FinishReason == "content_filter":
		return Response{Stop: StopRefused, Model: out.Model, Usage: res.Usage}, errKind(KindRefused, "")
	default:
		res.Stop = StopEnd
	}
	return res, nil
}

// openAIErrorText reads {"error":{"message":…}} and the plain
// {"error":"…"} some servers send.
func openAIErrorText(raw json.RawMessage, body []byte) string {
	var obj struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Message != "" {
		return obj.Message
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		return s
	}
	return strings.TrimSpace(string(body))
}

func openAIMessages(req Request) []openAIMessage {
	out := make([]openAIMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		out = append(out, openAIMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case RoleAssistant:
			msg := openAIMessage{Role: "assistant"}
			if m.Text != "" {
				msg.Content = m.Text
			}
			for _, c := range m.ToolCalls {
				args := c.Arguments
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				quoted, _ := json.Marshal(string(args))
				tc := openAIToolCall{ID: c.ID, Type: "function"}
				tc.Function.Name, tc.Function.Arguments = c.Name, quoted
				msg.ToolCalls = append(msg.ToolCalls, tc)
			}
			out = append(out, msg)
		case RoleTool:
			for _, r := range m.ToolResults {
				content := r.Content
				if r.IsError && !strings.HasPrefix(content, "error") {
					content = "error: " + content
				}
				out = append(out, openAIMessage{Role: "tool", ToolCallID: r.CallID, Content: content})
			}
		case RoleUser:
			if len(m.Attachments) == 0 {
				out = append(out, openAIMessage{Role: "user", Content: m.Text})
				continue
			}
			parts := []openAIPart{{Type: "text", Text: m.Text}}
			for _, at := range m.Attachments {
				uri := "data:" + at.MediaType + ";base64," + base64.StdEncoding.EncodeToString(at.Data)
				if at.MediaType == "application/pdf" {
					parts = append(parts, openAIPart{Type: "file", File: map[string]string{"filename": "statement.pdf", "file_data": uri}})
					continue
				}
				parts = append(parts, openAIPart{Type: "image_url", ImageURL: map[string]string{"url": uri}})
			}
			out = append(out, openAIMessage{Role: "user", Content: parts})
		}
	}
	return out
}
