package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// Fake is a scripted provider for tests: each call to Complete answers with
// the next step of the script, so conversations are deterministic and no
// test touches the network.
type Fake struct {
	mu sync.Mutex
	// Steps are answered in order. A step may look at the request (to
	// read an id from a tool result, say) before answering.
	Steps []func(Request) (Response, error)
	// Requests records every call, for assertions.
	Requests []Request
	// Loop, when set, answers every call after the script ran out.
	Loop func(Request) (Response, error)
}

// Name implements Provider.
func (f *Fake) Name() string { return "fake" }

// Complete implements Provider.
func (f *Fake) Complete(_ context.Context, req Request) (Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Requests = append(f.Requests, req)
	n := len(f.Requests) - 1
	switch {
	case n < len(f.Steps):
		return f.Steps[n](req)
	case f.Loop != nil:
		return f.Loop(req)
	}
	return Response{}, fmt.Errorf("fake provider: no step scripted for call %d", n+1)
}

// Calls returns how many times the model was called.
func (f *Fake) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Requests)
}

// Then appends steps to the script.
func (f *Fake) Then(steps ...func(Request) (Response, error)) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Steps = append(f.Steps, steps...)
	return f
}

// Say is a step where the model answers with text.
func Say(text string) func(Request) (Response, error) {
	return func(Request) (Response, error) { return Response{Text: text, Stop: StopEnd}, nil }
}

// Call is a step where the model asks for one tool.
func Call(name string, args any) func(Request) (Response, error) {
	return CallWith(name, func(Request) any { return args })
}

// CallWith is a step where the arguments depend on the request, e.g. an id
// that an earlier tool result returned.
func CallWith(name string, args func(Request) any) func(Request) (Response, error) {
	return func(req Request) (Response, error) {
		raw, err := json.Marshal(args(req))
		if err != nil {
			return Response{}, err
		}
		return Response{ToolCalls: []ToolCall{{ID: fmt.Sprintf("call_%d", len(req.Messages)), Name: name, Arguments: raw}}, Stop: StopTools}, nil
	}
}

// Fail is a step where the provider fails.
func Fail(err error) func(Request) (Response, error) {
	return func(Request) (Response, error) { return Response{}, err }
}

// LastToolResult returns the newest tool result in a request ("" if none).
func LastToolResult(req Request) ToolResult {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if rs := req.Messages[i].ToolResults; len(rs) > 0 {
			return rs[len(rs)-1]
		}
	}
	return ToolResult{}
}
