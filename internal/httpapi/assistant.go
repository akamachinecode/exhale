package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/assistant"
	"github.com/akamachinecode/exhale/internal/domain"
)

// The built-in assistant (ADR 0018, part B): "Ask Exhale" in the web and
// its settings. Like the auth endpoints these are for people in the web UI
// and are not in the op catalog: an agent connected over MCP is already a
// model and does not talk to another one, and no agent handles the key.

// The API names its schemas after the Go types. These give the assistant's
// their own names in the OpenAPI document ("AssistantStatus", not "Status").
type (
	AssistantTurn     assistant.Turn
	AssistantOutcome  assistant.Outcome
	AssistantStatus   assistant.Status
	AssistantSettings assistant.Settings
	AssistantTest     assistant.TestResult
)

// assistantError turns a failure of the assistant into an HTTP problem
// whose detail is a sentence the web can show.
func assistantError(err error) error {
	var e *assistant.Error
	if !errors.As(err, &e) {
		return httpError(err)
	}
	msg := assistant.Explain(err)
	status := http.StatusBadGateway
	switch e.Kind {
	case assistant.KindNotConfigured:
		status = http.StatusConflict
	case assistant.KindBusy, assistant.KindRateLimited:
		status = http.StatusTooManyRequests
	case assistant.KindTooLarge:
		status = http.StatusRequestEntityTooLarge
	case assistant.KindExpired:
		status = http.StatusNotFound
	case assistant.KindNoAnswer:
		status = http.StatusGatewayTimeout
	case assistant.KindAuth, assistant.KindModel, assistant.KindRejected, assistant.KindRefused, assistant.KindOverloaded, assistant.KindOther:
	}
	return newProblem(status, msg)
}

func (a *authAPI) assistantOwner(ctx context.Context) error {
	actor, err := a.member(ctx)
	if err != nil {
		return err
	}
	m, err := a.svc.Store().Q.GetMember(ctx, actor.MemberID)
	if err != nil || m.Role != string(domain.RoleOwner) {
		return huma.Error403Forbidden("only a household owner can set up the assistant")
	}
	return nil
}

// webConversation is the conversation of a member's "Ask Exhale" panel.
func webConversation(actor app.Actor) string { return "web_" + actor.MemberID }

func registerAssistant(api huma.API, a *authAPI) {
	asst := a.cfg.Assistant
	op := func(id, method, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: "/api/v1/assistant" + path, Summary: summary, Tags: []string{"Assistant"}}
	}

	type askIn struct {
		Text  string `json:"text" maxLength:"4000" doc:"what the person typed"`
		Reset bool   `json:"reset,omitempty" doc:"forget the conversation first; with an empty text it only forgets"`
	}
	huma.Register(api, op("assistant_message", http.MethodPost, "/messages", "Ask Exhale in plain language"),
		func(ctx context.Context, in *struct{ Body askIn }) (*resp[AssistantTurn], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			if in.Body.Reset {
				asst.Reset(ctx, actor, webConversation(actor))
			}
			turn, err := asst.Ask(ctx, actor, webConversation(actor), in.Body.Text)
			if err != nil {
				return nil, assistantError(err)
			}
			return &resp[AssistantTurn]{Body: AssistantTurn(turn)}, nil
		})

	type decideIn struct {
		Action string `json:"action" enum:"confirm,discard,undo" doc:"confirm applies the change, discard drops it, undo reverts one already applied"`
	}
	huma.Register(api, op("assistant_decide", http.MethodPost, "/proposals/{id}", "Confirm, discard or undo a change the assistant prepared"),
		func(ctx context.Context, in *struct {
			ID   string `path:"id"`
			Body decideIn
		}) (*resp[AssistantOutcome], error) {
			actor, err := a.member(ctx)
			if err != nil {
				return nil, err
			}
			var out assistant.Outcome
			switch in.Body.Action {
			case "undo":
				out, err = asst.Undo(ctx, actor, in.ID)
			default:
				out, err = asst.Decide(ctx, actor, in.ID, in.Body.Action == "confirm")
			}
			if err != nil {
				return nil, assistantError(err)
			}
			return &resp[AssistantOutcome]{Body: AssistantOutcome(out)}, nil
		})

	huma.Register(api, settingsOp("settings_assistant", http.MethodGet, "/assistant", "Built-in assistant status"),
		func(ctx context.Context, _ *struct{}) (*resp[AssistantStatus], error) {
			if _, err := a.member(ctx); err != nil {
				return nil, err
			}
			return &resp[AssistantStatus]{Body: AssistantStatus(asst.Status(ctx))}, nil
		})
	huma.Register(api, settingsOp("settings_set_assistant", http.MethodPost, "/assistant", "Set up the built-in assistant (owners only)"),
		func(ctx context.Context, in *struct{ Body AssistantSettings }) (*resp[AssistantStatus], error) {
			if err := a.assistantOwner(ctx); err != nil {
				return nil, err
			}
			st, err := asst.Configure(ctx, assistant.Settings(in.Body))
			if err != nil {
				return nil, httpError(err)
			}
			return &resp[AssistantStatus]{Body: AssistantStatus(st)}, nil
		})
	huma.Register(api, settingsOp("settings_test_assistant", http.MethodPost, "/assistant/test", "Make one small call to the configured model (owners only)"),
		func(ctx context.Context, _ *struct{}) (*resp[AssistantTest], error) {
			if err := a.assistantOwner(ctx); err != nil {
				return nil, err
			}
			return &resp[AssistantTest]{Body: AssistantTest(asst.Test(ctx))}, nil
		})
	huma.Register(api, settingsOp("settings_remove_assistant", http.MethodPost, "/assistant/remove", "Forget the assistant's settings and key (owners only)"),
		func(ctx context.Context, _ *struct{}) (*resp[AssistantStatus], error) {
			if err := a.assistantOwner(ctx); err != nil {
				return nil, err
			}
			st, err := asst.Remove(ctx)
			if err != nil {
				return nil, httpError(err)
			}
			return &resp[AssistantStatus]{Body: AssistantStatus(st)}, nil
		})
}

// assistantStream is "Ask Exhale" with the answer streamed as it is
// written (Server-Sent Events over a POST, read by the web with fetch):
//
//	event: delta  data: {"text":"more text"}       appended to the answer
//	event: text   data: {"text":"the whole text"}  replaces it (a new call after tools)
//	event: turn   data: AssistantTurn              the finished answer, with its cards
//	event: error  data: a problem, as the other endpoints send
//
// The cards arrive only with the turn: they are written by Exhale once the
// model has finished. POST /api/v1/assistant/messages stays for clients
// that do not stream.
func assistantStream(a *authAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorFrom(r.Context())
		if !ok || actor.Kind != app.ActorMember {
			writeProblem(w, http.StatusUnauthorized, "sign in first")
			return
		}
		var in struct {
			Text  string `json:"text"`
			Reset bool   `json:"reset"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
			writeProblem(w, http.StatusBadRequest, "send {\"text\": …} as JSON")
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeProblem(w, http.StatusInternalServerError, "streaming unsupported")
			return
		}
		asst := a.cfg.Assistant
		if in.Reset {
			asst.Reset(r.Context(), actor, webConversation(actor))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		send := func(event string, v any) {
			b, _ := json.Marshal(v)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
			flusher.Flush()
		}
		sent := ""
		progress := func(text string) {
			if strings.HasPrefix(text, sent) {
				send("delta", map[string]string{"text": text[len(sent):]})
			} else {
				send("text", map[string]string{"text": text})
			}
			sent = text
		}
		turn, err := asst.AskStream(r.Context(), actor, webConversation(actor), in.Text, progress)
		if err != nil {
			send("error", assistantError(err))
			return
		}
		send("turn", AssistantTurn(turn))
	}
}
