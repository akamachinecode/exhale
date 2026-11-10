package assistant

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Anthropic defaults.
const (
	// DefaultAnthropicModel is the model used when the owner does not pick
	// one.
	DefaultAnthropicModel = "claude-opus-5-5"
	anthropicURL          = "https://api.anthropic.com"
	anthropicVersion      = "2026-10-21"
	// fallbackBeta lets the API answer with the model Anthropic recommends
	// when the safety classifiers of the requested one decline a request.
	fallbackBeta = "server-side-fallback-2026-07-01"
)

// Anthropic talks to the Claude Messages API.
type Anthropic struct {
	key, model, base string
	hc               *http.Client
	// noFallback is set once the endpoint has rejected the fallback
	// parameter (a gateway, or a model without it), so it is not sent again.
	noFallback atomic.Bool
}

// NewAnthropic returns a client for the Messages API. baseURL is empty for
// Anthropic's own API; set it for a gateway that speaks the same protocol.
func NewAnthropic(key, model, baseURL string, timeout time.Duration) *Anthropic {
	if model == "" {
		model = DefaultAnthropicModel
	}
	if baseURL == "" {
		baseURL = anthropicURL
	}
	return &Anthropic{key: key, model: model, base: trimSlash(baseURL), hc: &http.Client{Timeout: timeout}}
}

// Name implements Provider.
func (a *Anthropic) Name() string { return ProviderAnthropic }

type anthropicBlock struct {
	Type         string            `json:"type"`
	Text         string            `json:"text,omitempty"`
	Source       *anthropicSource  `json:"source,omitempty"`
	ID           string            `json:"id,omitempty"`
	Name         string            `json:"name,omitempty"`
	Input        json.RawMessage   `json:"input,omitempty"`
	ToolUseID    string            `json:"tool_use_id,omitempty"`
	Content      string            `json:"content,omitempty"`
	IsError      bool              `json:"is_error,omitempty"`
	CacheControl map[string]string `json:"cache_control,omitempty"`
}

type anthropicSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // []anthropicBlock, or the raw blocks the API returned
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    []anthropicBlock   `json:"system,omitempty"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Fallbacks string             `json:"fallbacks,omitempty"`
}

type anthropicResponse struct {
	Model      string          `json:"model"`
	Content    json.RawMessage `json:"content"`
	StopReason string          `json:"stop_reason"`
	Usage      struct {
		Input  int `json:"input_tokens"`
		Output int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// supportsFallback reports whether the model has server-side fallbacks for
// declined requests. Models without them reject or ignore the parameter.
func supportsFallback(model string) bool {
	for _, p := range []string{"claude-fable-5", "claude-mythos-5", "claude-opus-5", "claude-sonnet-5-5"} {
		if strings.HasPrefix(model, p) {
			return true
		}
	}
	return false
}

// Complete implements Provider.
func (a *Anthropic) Complete(ctx context.Context, req Request) (Response, error) {
	body := anthropicRequest{Model: a.model, MaxTokens: req.MaxTokens, Messages: anthropicMessages(req.Messages)}
	if req.System != "" {
		// The rules and the tool list are the same for every question, so
		// the provider can cache them between calls.
		body.System = []anthropicBlock{{Type: "text", Text: req.System, CacheControl: map[string]string{"type": "ephemeral"}}}
	}
	for _, t := range req.Tools {
		body.Tools = append(body.Tools, anthropicTool{Name: t.Name, Description: t.Description, InputSchema: t.Schema})
	}
	headers := map[string]string{"x-api-key": a.key, "anthropic-version": anthropicVersion}
	withFallback := a.base == anthropicURL && supportsFallback(a.model) && !a.noFallback.Load()
	if withFallback {
		body.Fallbacks = "default"
		headers["anthropic-beta"] = fallbackBeta
	}
	status, data, err := post(ctx, a.hc, a.base+"/v1/messages", headers, body)
	if err != nil {
		return Response{}, err
	}
	var out anthropicResponse
	jsonErr := json.Unmarshal(data, &out)
	if status != http.StatusOK {
		detail := strings.TrimSpace(string(data))
		if jsonErr == nil && out.Error != nil {
			detail = out.Error.Type + ": " + out.Error.Message
		}
		if withFallback && status == http.StatusBadRequest && (strings.Contains(detail, "fallback") || strings.Contains(detail, "anthropic-beta")) {
			a.noFallback.Store(true)
			return a.Complete(ctx, req)
		}
		return Response{}, statusError(status, detail)
	}
	if jsonErr != nil {
		return Response{}, errKind(KindNoAnswer, "the answer was not JSON")
	}
	res := Response{Raw: out.Content, Model: out.Model, Usage: Usage{Input: out.Usage.Input, Output: out.Usage.Output}}
	var blocks []anthropicBlock
	if len(out.Content) > 0 {
		if err := json.Unmarshal(out.Content, &blocks); err != nil {
			return Response{}, errKind(KindNoAnswer, "the answer had no readable content")
		}
	}
	var text []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				text = append(text, b.Text)
			}
		case "tool_use":
			res.ToolCalls = append(res.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Arguments: b.Input})
		}
	}
	res.Text = strings.TrimSpace(strings.Join(text, "\n"))
	switch out.StopReason {
	case "refusal":
		// Check before reading the content: a declined request may come
		// back empty or cut short.
		return Response{Stop: StopRefused, Model: out.Model, Usage: res.Usage}, errKind(KindRefused, "")
	case "tool_use":
		res.Stop = StopTools
	case "max_tokens", "model_context_window_exceeded":
		res.Stop = StopLength
	default:
		res.Stop = StopEnd
	}
	if len(res.ToolCalls) > 0 && res.Stop == StopEnd {
		res.Stop = StopTools
	}
	return res, nil
}

func anthropicMessages(msgs []Message) []anthropicMessage {
	out := make([]anthropicMessage, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case RoleAssistant:
			if len(m.Raw) > 0 {
				// Reasoning blocks are only valid exactly as they came.
				out = append(out, anthropicMessage{Role: "assistant", Content: m.Raw})
				continue
			}
			var blocks []anthropicBlock
			if m.Text != "" {
				blocks = append(blocks, anthropicBlock{Type: "text", Text: m.Text})
			}
			for _, c := range m.ToolCalls {
				in := c.Arguments
				if len(in) == 0 {
					in = json.RawMessage("{}")
				}
				blocks = append(blocks, anthropicBlock{Type: "tool_use", ID: c.ID, Name: c.Name, Input: in})
			}
			out = append(out, anthropicMessage{Role: "assistant", Content: blocks})
		case RoleTool:
			// Every result of one assistant turn goes in a single user
			// message.
			blocks := make([]anthropicBlock, 0, len(m.ToolResults))
			for _, r := range m.ToolResults {
				blocks = append(blocks, anthropicBlock{Type: "tool_result", ToolUseID: r.CallID, Content: r.Content, IsError: r.IsError})
			}
			out = append(out, anthropicMessage{Role: "user", Content: blocks})
		case RoleUser:
			var blocks []anthropicBlock
			for _, at := range m.Attachments {
				kind := "image"
				if at.MediaType == "application/pdf" {
					kind = "document"
				}
				blocks = append(blocks, anthropicBlock{Type: kind, Source: &anthropicSource{Type: "base64", MediaType: at.MediaType, Data: base64.StdEncoding.EncodeToString(at.Data)}})
			}
			blocks = append(blocks, anthropicBlock{Type: "text", Text: m.Text})
			out = append(out, anthropicMessage{Role: "user", Content: blocks})
		}
	}
	return out
}
