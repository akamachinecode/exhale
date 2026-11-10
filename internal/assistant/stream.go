package assistant

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Streaming: the answer arrives as it is written, over Server-Sent Events,
// for both protocols. Tool calls and usage are put together from the
// events, so the result is the same Response that Complete returns and the
// loop does not care which one ran. Complete stays: a provider that cannot
// stream, or an endpoint that answers a streaming request with plain JSON,
// still works.

// Streamer is a provider that can send its answer as it is written. onText
// is called with each piece of answer text (not reasoning, not tool
// arguments), in order.
type Streamer interface {
	Stream(ctx context.Context, req Request, onText func(delta string)) (Response, error)
}

// openStream posts body and returns the open response when the provider
// answered 200. Otherwise it returns the status and body, after one retry
// for the statuses worth it (as post does).
func openStream(ctx context.Context, hc *http.Client, url string, headers map[string]string, body any) (res *http.Response, status int, data []byte, err error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, 0, nil, err
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return nil, 0, nil, errKind(KindRejected, "the address of the model provider is not valid")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("User-Agent", userAgent)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		res, err := hc.Do(req)
		if err != nil {
			return nil, 0, nil, errKind(KindNoAnswer, err.Error())
		}
		if res.StatusCode == http.StatusOK {
			return res, res.StatusCode, nil, nil
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes))
		res.Body.Close()
		if err != nil {
			return nil, 0, nil, errKind(KindNoAnswer, err.Error())
		}
		var wait time.Duration
		if s, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil {
			wait = time.Duration(s) * time.Second
		}
		retryable := res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500
		if !retryable || attempt > 0 || wait > 5*time.Second {
			return nil, res.StatusCode, data, nil
		}
		select {
		case <-ctx.Done():
			return nil, 0, nil, errKind(KindNoAnswer, ctx.Err().Error())
		case <-time.After(max(wait, retryWait)):
		}
	}
}

// isEventStream reports whether the provider really streams: some servers
// ignore "stream": true and answer with the whole JSON.
func isEventStream(res *http.Response) bool {
	mt, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	return mt == "text/event-stream"
}

// sseEvent is one Server-Sent Event.
type sseEvent struct {
	Name string
	Data []byte
}

// readEvents calls fn for each event of r until the stream ends, fn returns
// false, or more than maxResponseBytes were read.
func readEvents(r io.Reader, fn func(sseEvent) bool) error {
	br := bufio.NewReaderSize(io.LimitReader(r, maxResponseBytes), 64<<10)
	var ev sseEvent
	var data bytes.Buffer
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimRight(line, "\r\n")
			switch {
			case len(line) == 0:
				if data.Len() > 0 || ev.Name != "" {
					ev.Data = bytes.TrimSuffix(data.Bytes(), []byte("\n"))
					if !fn(ev) {
						return nil
					}
				}
				ev, data = sseEvent{}, bytes.Buffer{}
			case line[0] == ':':
				// a comment, kept for keep-alive
			default:
				field, value, _ := bytes.Cut(line, []byte(":"))
				value = bytes.TrimPrefix(value, []byte(" "))
				switch string(field) {
				case "event":
					ev.Name = string(value)
				case "data":
					data.Write(value)
					data.WriteByte('\n')
				}
			}
		}
		if err == io.EOF {
			if data.Len() > 0 {
				ev.Data = bytes.TrimSuffix(data.Bytes(), []byte("\n"))
				fn(ev)
			}
			return nil
		}
		if err != nil {
			return errKind(KindNoAnswer, err.Error())
		}
	}
}

// --- Anthropic Messages ------------------------------------------------------------

// Stream implements Streamer.
func (a *Anthropic) Stream(ctx context.Context, req Request, onText func(string)) (Response, error) {
	body, headers, withFallback := a.build(req)
	body.Stream = true
	res, status, data, err := openStream(ctx, a.hc, a.base+"/v1/messages", headers, body)
	if err != nil {
		return Response{}, err
	}
	if res == nil {
		retry, err := a.failed(status, data, withFallback)
		if retry {
			return a.Stream(ctx, req, onText)
		}
		return Response{}, err
	}
	defer res.Body.Close()
	if !isEventStream(res) {
		var out anthropicResponse
		data, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes))
		if err != nil || json.Unmarshal(data, &out) != nil {
			return Response{}, errKind(KindNoAnswer, "the answer was not JSON")
		}
		r, err := anthropicResult(out)
		if err == nil && r.Text != "" && onText != nil {
			onText(r.Text)
		}
		return r, err
	}
	return readAnthropicStream(res.Body, onText)
}

// anthropicEvent is any event of a Messages stream; each type fills some
// of the fields.
type anthropicEvent struct {
	Type    string `json:"type"`
	Message struct {
		Model string `json:"model"`
		Usage struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Index        int             `json:"index"`
	ContentBlock json.RawMessage `json:"content_block"`
	Delta        struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *struct {
		Input  int `json:"input_tokens"`
		Output int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// streamBlock is a content block being put together. The block is kept as
// the provider sent it (a map that keeps every field, numbers exact), so
// reasoning blocks can be sent back unchanged.
type streamBlock struct {
	fields map[string]any
	text   strings.Builder // text or thinking
	json   strings.Builder // partial tool input
}

func readAnthropicStream(r io.Reader, onText func(string)) (Response, error) {
	var out anthropicResponse
	blocks := map[int]*streamBlock{}
	var failure error
	done := false
	err := readEvents(r, func(e sseEvent) bool {
		var ev anthropicEvent
		if json.Unmarshal(e.Data, &ev) != nil {
			return true // a keep-alive or something new: ignore it
		}
		switch ev.Type {
		case "message_start":
			out.Model = ev.Message.Model
			out.Usage.Input, out.Usage.Output = ev.Message.Usage.Input, ev.Message.Usage.Output
		case "content_block_start":
			dec := json.NewDecoder(bytes.NewReader(ev.ContentBlock))
			dec.UseNumber()
			b := &streamBlock{}
			if dec.Decode(&b.fields) != nil || b.fields == nil {
				b.fields = map[string]any{}
			}
			blocks[ev.Index] = b
		case "content_block_delta":
			b := blocks[ev.Index]
			if b == nil {
				b = &streamBlock{fields: map[string]any{}}
				blocks[ev.Index] = b
			}
			switch ev.Delta.Type {
			case "text_delta":
				b.text.WriteString(ev.Delta.Text)
				if onText != nil && ev.Delta.Text != "" {
					onText(ev.Delta.Text)
				}
			case "input_json_delta":
				b.json.WriteString(ev.Delta.PartialJSON)
			case "thinking_delta":
				b.text.WriteString(ev.Delta.Thinking)
			case "signature_delta":
				b.fields["signature"] = ev.Delta.Signature
			}
		case "message_delta":
			if ev.Delta.StopReason != "" {
				out.StopReason = ev.Delta.StopReason
			}
			if ev.Usage != nil {
				out.Usage.Output = max(out.Usage.Output, ev.Usage.Output)
				out.Usage.Input = max(out.Usage.Input, ev.Usage.Input)
			}
		case "message_stop":
			done = true
			return false
		case "error":
			detail := "the stream failed"
			if ev.Error != nil {
				detail = ev.Error.Type + ": " + ev.Error.Message
			}
			kind := KindOverloaded
			if ev.Error != nil && strings.Contains(ev.Error.Type, "rate_limit") {
				kind = KindRateLimited
			}
			failure = errKind(kind, detail)
			return false
		}
		return true
	})
	if failure != nil {
		return Response{}, failure
	}
	if err != nil {
		return Response{}, err
	}
	if !done && out.StopReason == "" {
		return Response{}, errKind(KindNoAnswer, "the stream ended before the answer did")
	}
	// Put the blocks together in their order, as the whole answer has them.
	idx := make([]int, 0, len(blocks))
	for i := range blocks {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	content := make([]map[string]any, 0, len(idx))
	for _, i := range idx {
		b := blocks[i]
		switch b.fields["type"] {
		case "text":
			b.fields["text"] = b.text.String()
		case "thinking":
			b.fields["thinking"] = b.text.String()
		case "tool_use", "server_tool_use":
			in := strings.TrimSpace(b.json.String())
			if in == "" {
				in = "{}"
			}
			if !json.Valid([]byte(in)) {
				return Response{}, errKind(KindNoAnswer, "a tool call arrived incomplete")
			}
			b.fields["input"] = json.RawMessage(in)
		}
		content = append(content, b.fields)
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return Response{}, err
	}
	out.Content = raw
	return anthropicResult(out)
}

// --- OpenAI-compatible chat completions -----------------------------------------------

// Stream implements Streamer.
func (o *OpenAI) Stream(ctx context.Context, req Request, onText func(string)) (Response, error) {
	body, headers := o.build(req)
	body.Stream = true
	if !o.noStreamUsage.Load() {
		body.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	res, status, data, err := openStream(ctx, o.hc, o.base+"/chat/completions", headers, body)
	if err != nil {
		return Response{}, err
	}
	if res == nil {
		if status == http.StatusBadRequest && !o.noStreamUsage.Load() && strings.Contains(string(data), "stream_options") {
			o.noStreamUsage.Store(true) // an older server: no usage, still a stream
			return o.Stream(ctx, req, onText)
		}
		retry, err := o.failed(status, data)
		if retry {
			return o.Stream(ctx, req, onText)
		}
		return Response{}, err
	}
	defer res.Body.Close()
	if !isEventStream(res) {
		var out openAIResponse
		data, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes))
		if err != nil || json.Unmarshal(data, &out) != nil || len(out.Choices) == 0 {
			return Response{}, errKind(KindNoAnswer, "the answer had no choices")
		}
		r, err := openAIResult(out)
		if err == nil && r.Text != "" && onText != nil {
			onText(r.Text)
		}
		return r, err
	}
	return readOpenAIStream(res.Body, onText)
}

type openAIChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content   *string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		Input  int `json:"prompt_tokens"`
		Output int `json:"completion_tokens"`
	} `json:"usage"`
	Error json.RawMessage `json:"error"`
}

type openAIStreamCall struct {
	id, name string
	args     strings.Builder
	whole    json.RawMessage // a server that sends the arguments as an object
}

func readOpenAIStream(r io.Reader, onText func(string)) (Response, error) {
	var res Response
	var text strings.Builder
	calls := map[int]*openAIStreamCall{}
	finish := ""
	var failure error
	done := false
	err := readEvents(r, func(e sseEvent) bool {
		if string(e.Data) == "[DONE]" {
			done = true
			return false
		}
		var ch openAIChunk
		if json.Unmarshal(e.Data, &ch) != nil {
			return true
		}
		if len(ch.Error) > 0 && string(ch.Error) != "null" {
			failure = errKind(KindOverloaded, openAIErrorText(ch.Error, e.Data))
			return false
		}
		if ch.Model != "" {
			res.Model = ch.Model
		}
		if ch.Usage != nil {
			res.Usage = Usage{Input: ch.Usage.Input, Output: ch.Usage.Output}
		}
		for _, c := range ch.Choices {
			if c.Delta.Content != nil && *c.Delta.Content != "" {
				text.WriteString(*c.Delta.Content)
				if onText != nil {
					onText(*c.Delta.Content)
				}
			}
			for _, tc := range c.Delta.ToolCalls {
				call := calls[tc.Index]
				if call == nil {
					call = &openAIStreamCall{}
					calls[tc.Index] = call
				}
				if tc.ID != "" {
					call.id = tc.ID
				}
				if tc.Function.Name != "" {
					call.name += tc.Function.Name
				}
				var piece string
				switch {
				case len(tc.Function.Arguments) == 0 || string(tc.Function.Arguments) == "null":
				case json.Unmarshal(tc.Function.Arguments, &piece) == nil:
					call.args.WriteString(piece)
				default:
					call.whole = tc.Function.Arguments
				}
			}
			if c.FinishReason != nil && *c.FinishReason != "" {
				finish = *c.FinishReason
			}
		}
		return true
	})
	if failure != nil {
		return Response{}, failure
	}
	if err != nil {
		return Response{}, err
	}
	if !done && finish == "" {
		return Response{}, errKind(KindNoAnswer, "the stream ended before the answer did")
	}
	idx := make([]int, 0, len(calls))
	for i := range calls {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for n, i := range idx {
		c := calls[i]
		args := c.whole
		if len(args) == 0 {
			s := strings.TrimSpace(c.args.String())
			if s == "" {
				s = "{}"
			}
			if !json.Valid([]byte(s)) {
				return Response{}, errKind(KindNoAnswer, "a tool call arrived incomplete")
			}
			args = json.RawMessage(s)
		}
		id := c.id
		if id == "" {
			id = fmt.Sprintf("call_%d", n+1)
		}
		res.ToolCalls = append(res.ToolCalls, ToolCall{ID: id, Name: c.name, Arguments: args})
	}
	res.Text = strings.TrimSpace(text.String())
	switch {
	case len(res.ToolCalls) > 0:
		res.Stop = StopTools
	case finish == "length":
		res.Stop = StopLength
	case finish == "content_filter":
		return Response{Stop: StopRefused, Model: res.Model, Usage: res.Usage}, errKind(KindRefused, "")
	default:
		res.Stop = StopEnd
	}
	return res, nil
}
