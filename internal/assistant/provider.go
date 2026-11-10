// Package assistant is the model Exhale itself uses (ADR 0018, part B):
// plain language in chat and in the web, and reading statements and
// screenshots into the import review list.
//
// It is off until the owner configures a provider and a key; nothing is
// sent anywhere otherwise. The model acts through the same op catalog as
// MCP, as the member who is talking, and every change it prepares is
// applied only after that member confirms it.
package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Providers the owner can pick.
const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai" // any OpenAI-compatible endpoint
)

// Role says who wrote a message.
type Role string

// Roles of a conversation.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	// RoleTool carries the results of the tool calls of the assistant
	// message before it.
	RoleTool Role = "tool"
)

// Attachment is a file the person sent with a message: a picture or a PDF.
type Attachment struct {
	MediaType string // image/png, image/jpeg, image/webp, application/pdf
	Data      []byte
}

// ToolCall is a use case the model wants to run.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// ToolResult answers a ToolCall.
type ToolResult struct {
	CallID  string
	Name    string
	Content string
	IsError bool
}

// Message is one turn of a conversation, in a form every provider can send.
type Message struct {
	Role        Role
	Text        string
	Attachments []Attachment // user messages
	ToolCalls   []ToolCall   // assistant messages
	ToolResults []ToolResult // tool messages
	// Raw is the provider's own form of an assistant message. It is sent
	// back as it came while the same question is being answered, because
	// providers that return reasoning blocks reject a history that was
	// rebuilt without them. It is never kept between questions.
	Raw json.RawMessage
}

// Tool is a use case offered to the model.
type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage // JSON Schema of the arguments
}

// Request is one call to a model.
type Request struct {
	System    string
	Messages  []Message
	Tools     []Tool
	MaxTokens int
}

// Stop says why the model stopped writing.
type Stop string

// Reasons a model stops.
const (
	StopEnd     Stop = "end"     // it finished its answer
	StopTools   Stop = "tools"   // it wants tools to run
	StopLength  Stop = "length"  // it hit the token limit
	StopRefused Stop = "refused" // the provider declined to answer
)

// Usage counts the tokens of one call.
type Usage struct {
	Input  int
	Output int
}

// Response is what the model answered: text, tool calls, or both.
type Response struct {
	Text      string
	ToolCalls []ToolCall
	Raw       json.RawMessage
	Stop      Stop
	Usage     Usage
	Model     string
}

// Provider talks to one kind of model API.
type Provider interface {
	// Name is the provider id: "anthropic" or "openai".
	Name() string
	Complete(ctx context.Context, req Request) (Response, error)
}

// Kind classifies what went wrong, so the person gets a sentence they can
// act on instead of an HTTP status.
type Kind int

// Kinds of failure.
const (
	KindOther         Kind = iota
	KindNotConfigured      // the owner has not set the assistant up
	KindAuth               // the key was rejected
	KindNoAnswer           // timeout or the provider could not be reached
	KindRateLimited        // the provider asks to slow down
	KindBusy               // Exhale's own per-member limit
	KindModel              // the model does not exist or is not available
	KindRejected           // the provider refused the request as invalid
	KindTooLarge           // the request is larger than the provider accepts
	KindRefused            // the model declined to answer
	KindOverloaded         // the provider is failing or overloaded
	KindExpired            // the proposal is gone
)

// Error is a failure of the assistant. Detail is the provider's own
// message, for logs and for the settings test; it is not shown in chat.
type Error struct {
	Kind   Kind
	Status int
	Detail string
}

func (e *Error) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("assistant: %s (%s)", Explain(e), e.Detail)
	}
	return "assistant: " + Explain(e)
}

// Explain turns an error into one sentence for the person.
func Explain(err error) string {
	var e *Error
	if !errors.As(err, &e) {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return "The model did not answer in time. Try again."
		}
		return "Something went wrong talking to the model."
	}
	switch e.Kind {
	case KindNotConfigured:
		return "The built-in assistant is off. An owner can turn it on in Settings."
	case KindAuth:
		return "The model provider rejected the API key. An owner can fix it in Settings."
	case KindNoAnswer:
		return "The model did not answer. Try again in a moment."
	case KindRateLimited:
		return "The model provider is asking to slow down. Try again in a minute."
	case KindBusy:
		return "That is a lot of messages in a short time. Wait a minute and try again."
	case KindModel:
		return "The model set in Settings does not exist or is not available with this key."
	case KindRejected:
		return "The model provider did not accept the request."
	case KindTooLarge:
		return "That is more than the model accepts in one request."
	case KindRefused:
		return "The model declined to answer that."
	case KindOverloaded:
		return "The model provider is having trouble. Try again in a moment."
	case KindExpired:
		return "This proposal has expired. Ask again."
	case KindOther:
	}
	return "Something went wrong talking to the model."
}

func errKind(k Kind, detail string) *Error { return &Error{Kind: k, Detail: detail} }

// IsKind reports whether err is an assistant failure of kind k.
func IsKind(err error, k Kind) bool {
	var e *Error
	return errors.As(err, &e) && e.Kind == k
}
