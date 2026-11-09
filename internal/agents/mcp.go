// Package agents (piece ⑰) makes Exhale agent-ready: an MCP server that
// exposes every use case as a tool, the household briefing as a resource,
// and ready-made prompts (ADR 0018).
package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/domain"
)

// ActorFunc resolves who is calling. extra carries the HTTP headers (bearer
// token) for HTTP sessions; it is nil over stdio.
type ActorFunc func(ctx context.Context, extra *mcp.RequestExtra, session *mcp.ServerSession) (app.Actor, error)

// ClientName returns the name the MCP client announced, e.g. "claude-ai".
func ClientName(session *mcp.ServerSession) string {
	if session == nil {
		return ""
	}
	if p := session.InitializeParams(); p != nil && p.ClientInfo != nil {
		if p.ClientInfo.Title != "" {
			return p.ClientInfo.Title
		}
		return p.ClientInfo.Name
	}
	return ""
}

// Instructions are sent to every MCP client on connection.
const Instructions = `Exhale manages this household's recurring subscriptions and their charges.
Start by reading the resource exhale://context (or calling get_context): it has today's date, every subscription and charge id, what needs attention and the rules for edits.
Every write tool accepts dryRun=true: preview first, show the user the result, then apply. Every write returns an auditId that the undo tool reverts.
Destructive tools (cancel, delete) need confirm=true unless your token is trusted. Never invent ids.`

// NewServer returns an MCP server exposing the whole use-case catalog.
func NewServer(svc *app.Service, version string, actor ActorFunc) (*mcp.Server, error) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "exhale", Title: "Exhale", Version: version}, &mcp.ServerOptions{Instructions: Instructions})
	for _, b := range app.Catalog() {
		tool, err := toolFor(b)
		if err != nil {
			return nil, err
		}
		srv.AddTool(tool, handlerFor(svc, b, actor))
	}
	addResources(srv, svc, actor)
	addPrompts(srv)
	return srv, nil
}

func toolFor(b app.Binding) (*mcp.Tool, error) {
	in, err := jsonschema.ForType(b.In, &jsonschema.ForOptions{})
	if err != nil {
		return nil, fmt.Errorf("tool %s: input schema: %w", b.Name, err)
	}
	if in.Properties == nil {
		in.Properties = map[string]*jsonschema.Schema{}
	}
	applyTags(in, b.In)
	if b.Destructive {
		in.Properties["confirm"] = &jsonschema.Schema{Type: "boolean", Description: "Set to true to apply after showing the user the preview. Without it (and without a trusted token) the call only previews."}
	}
	out, err := jsonschema.ForType(b.Out, &jsonschema.ForOptions{})
	if err != nil {
		return nil, fmt.Errorf("tool %s: output schema: %w", b.Name, err)
	}
	destructive := b.Destructive
	openWorld := false
	return &mcp.Tool{
		Name: b.Name, Title: b.Title, Description: b.Description, InputSchema: in, OutputSchema: out,
		Annotations: &mcp.ToolAnnotations{
			Title: b.Title, ReadOnlyHint: !b.Write, DestructiveHint: &destructive,
			IdempotentHint: b.Idempotent || !b.Write, OpenWorldHint: &openWorld,
		},
	}, nil
}

// applyTags copies the constraints declared as struct tags for the REST API
// (enum, minimum, maximum, maxLength) into the tool's input schema, so an
// agent sees the allowed values instead of guessing them.
func applyTags(schema *jsonschema.Schema, t reflect.Type) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous {
			applyTags(schema, f.Type)
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		prop := schema.Properties[name]
		if name == "" || name == "-" || prop == nil {
			continue
		}
		if v := f.Tag.Get("enum"); v != "" {
			prop.Enum = nil
			for _, e := range strings.Split(v, ",") {
				prop.Enum = append(prop.Enum, e)
			}
		}
		if v, err := strconv.ParseFloat(f.Tag.Get("minimum"), 64); err == nil {
			prop.Minimum = &v
		}
		if v, err := strconv.ParseFloat(f.Tag.Get("maximum"), 64); err == nil {
			prop.Maximum = &v
		}
		if v, err := strconv.Atoi(f.Tag.Get("maxLength")); err == nil {
			prop.MaxLength = &v
		}
	}
}

func handlerFor(svc *app.Service, b app.Binding, actorFn ActorFunc) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		actor, err := actorFn(ctx, req.Extra, req.Session)
		if err != nil {
			return toolError(err), nil
		}
		raw := req.Params.Arguments
		previewOnly := false
		if b.Destructive && actor.Scope != app.ScopeTrusted {
			var args map[string]any
			_ = json.Unmarshal(raw, &args)
			if confirmed, _ := args["confirm"].(bool); !confirmed {
				args["dryRun"] = true
				previewOnly = true
			}
			delete(args, "confirm")
			raw, _ = json.Marshal(args)
		}
		out, err := b.Invoke(svc, ctx, actor, raw)
		if err != nil {
			return toolError(err), nil
		}
		text, _ := json.MarshalIndent(out, "", "  ")
		msg := string(text)
		if previewOnly {
			msg = "PREVIEW ONLY — nothing was changed. Show this to the user and call again with confirm=true to apply.\n" + msg
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: msg}}, StructuredContent: out}, nil
	}
}

// toolError reports errors inside the result so the model can read them and
// recover, as the MCP spec recommends.
func toolError(err error) *mcp.CallToolResult {
	hint := ""
	switch {
	case errors.Is(err, domain.ErrNotFound):
		hint = " (check the id with list_subscriptions or get_context)"
	case errors.Is(err, domain.ErrConflict):
		hint = " (the data changed meanwhile; read it again and retry)"
	case errors.Is(err, app.ErrForbidden):
		hint = " (this token is read-only)"
	}
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "error: " + err.Error() + hint}}}
}

func addResources(srv *mcp.Server, svc *app.Service, actorFn ActorFunc) {
	srv.AddResource(&mcp.Resource{
		URI: "exhale://context", Name: "context", Title: "Household briefing", MIMEType: "text/markdown",
		Description: "Live briefing: members, subscriptions with ids, next 30 days, what needs attention and the editing rules. Read it first.",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		a, err := actorFn(ctx, req.Extra, req.Session)
		if err != nil {
			return nil, err
		}
		c, err := svc.BuildContext(ctx, a, struct{}{})
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "text/markdown", Text: c.Markdown}}}, nil
	})
	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "exhale://subscriptions/{id}", Name: "subscription", Title: "Subscription", MIMEType: "application/json",
		Description: "A subscription with its upcoming charges and history.",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		a, err := actorFn(ctx, req.Extra, req.Session)
		if err != nil {
			return nil, err
		}
		id := strings.TrimPrefix(req.Params.URI, "exhale://subscriptions/")
		d, err := svc.GetSubscription(ctx, a, app.GetSubscriptionInput{ID: id})
		if err != nil {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		b, _ := json.MarshalIndent(d, "", "  ")
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "application/json", Text: string(b)}}}, nil
	})
}

var prompts = []struct{ name, title, text string }{
	{"monthly_review", "Monthly review", "Read exhale://context. Review this month's charges: confirm with me the ones that are unconfirmed (paid, failed or skipped), point out anything that moved or changed price, and summarize what I will pay in the next 30 days."},
	{"find_savings", "Find savings", "Read exhale://context and spending_summary. Find subscriptions I could cancel, downgrade, share with the household or pay yearly instead of monthly. Estimate the yearly saving of each idea. Do not change anything without asking me."},
	{"audit_price_increases", "Audit price increases", "For each subscription, compare the amounts in its charge history (get_subscription) and list every price change with its date and percentage. Flag increases above 10%."},
	{"clean_up_categories", "Clean up categories", "Read get_household and list_subscriptions. Propose a category for every uncategorized subscription and merges for duplicated categories. Preview each change with dryRun before applying it."},
}

func addPrompts(srv *mcp.Server) {
	for _, p := range prompts {
		srv.AddPrompt(&mcp.Prompt{Name: p.name, Title: p.title, Description: p.text}, func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{Description: p.title, Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: p.text}}}}, nil
		})
	}
}
