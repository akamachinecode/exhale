package assistant

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Keys of the settings table. The API key is saved there like the Telegram
// bot token: it never leaves the server and no endpoint returns it.
const (
	settingProvider = "assistant_provider"
	settingKey      = "assistant_api_key"
	settingModel    = "assistant_model"
	settingBaseURL  = "assistant_base_url"
	settingEnabled  = "assistant_enabled" // "1", "0" or "" (on once configured)
)

// Env is the configuration given by environment variables. Each value that
// is set wins over the one saved from the web.
type Env struct {
	Provider string // EXHALE_ASSISTANT_PROVIDER
	APIKey   string // EXHALE_ASSISTANT_API_KEY
	Model    string // EXHALE_ASSISTANT_MODEL
	BaseURL  string // EXHALE_ASSISTANT_BASE_URL
}

// Config is the effective configuration.
type Config struct {
	Provider string
	APIKey   string
	Model    string
	BaseURL  string
	Enabled  bool
}

// Ready reports whether a provider can be built from c.
func (c Config) Ready() bool {
	switch c.Provider {
	case ProviderAnthropic:
		return c.APIKey != ""
	case ProviderOpenAI:
		return c.Model != ""
	}
	return false
}

// Status describes the assistant for the settings UI. It never carries the
// API key, only whether one is saved.
type Status struct {
	Enabled      bool        `json:"enabled" doc:"the assistant answers: it is configured and switched on"`
	Configured   bool        `json:"configured" doc:"a provider, and what it needs, is set"`
	On           bool        `json:"on" doc:"the on/off switch, whatever the configuration"`
	Provider     string      `json:"provider,omitempty" enum:"anthropic,openai"`
	Model        string      `json:"model,omitempty"`
	BaseURL      string      `json:"baseUrl,omitempty"`
	HasKey       bool        `json:"hasKey" doc:"an API key is saved; it is never returned"`
	Locked       []string    `json:"locked" doc:"fields set by environment variables on the server, not editable here: provider, apiKey, model, baseUrl"`
	DefaultModel string      `json:"defaultModel" doc:"the model used with Anthropic when none is given"`
	Price        *Price      `json:"price,omitempty" doc:"the price used to estimate the cost of the current model; absent when it is not known"`
	PriceSource  string      `json:"priceSource" enum:"default,settings,unknown" doc:"where the price comes from: Exhale's table, the owner, or nowhere (tokens only)"`
	Month        *MonthUsage `json:"month,omitempty" doc:"what the assistant was asked this month"`
}

// Settings is what the owner saves from the web.
type Settings struct {
	Provider string `json:"provider" enum:"anthropic,openai" doc:"anthropic for Claude; openai for any OpenAI-compatible endpoint (OpenAI, OpenRouter, Ollama, LM Studio…)"`
	APIKey   string `json:"apiKey,omitempty" doc:"leave empty to keep the saved key"`
	Model    string `json:"model,omitempty" doc:"model id; required for openai, optional for anthropic"`
	BaseURL  string `json:"baseUrl,omitempty" doc:"address of the endpoint, e.g. http://localhost:11434/v1 for Ollama; empty for the provider's own API"`
	Enabled  *bool  `json:"enabled,omitempty" doc:"on/off switch; on when omitted"`
	Price    *Price `json:"price,omitempty" doc:"the price of the model, to estimate costs; both empty forgets it. Omit to keep it"`
}

func (a *Assistant) setting(ctx context.Context, key string) string {
	v, err := a.svc.Store().Q.GetSetting(ctx, key)
	if err != nil {
		return ""
	}
	return v
}

// Config returns the effective configuration: what the web saved, with the
// environment's values on top.
func (a *Assistant) Config(ctx context.Context) Config {
	pick := func(env, key string) string {
		if env != "" {
			return env
		}
		return a.setting(ctx, key)
	}
	c := Config{
		Provider: strings.ToLower(pick(a.env.Provider, settingProvider)),
		APIKey:   pick(a.env.APIKey, settingKey),
		Model:    pick(a.env.Model, settingModel),
		BaseURL:  pick(a.env.BaseURL, settingBaseURL),
	}
	if c.Provider == "" && a.env.APIKey != "" {
		c.Provider = ProviderAnthropic // a key alone means Claude
	}
	if c.Provider == ProviderAnthropic && c.Model == "" {
		c.Model = DefaultAnthropicModel
	}
	c.Enabled = a.setting(ctx, settingEnabled) != "0"
	return c
}

// Status reports the configuration without the key.
func (a *Assistant) Status(ctx context.Context) Status {
	c := a.Config(ctx)
	st := Status{
		Configured: c.Ready(), On: c.Enabled, Enabled: a.Enabled(ctx), Provider: c.Provider, Model: c.Model, BaseURL: c.BaseURL,
		HasKey: c.APIKey != "", Locked: []string{}, DefaultModel: DefaultAnthropicModel, PriceSource: "unknown",
	}
	if c.Model != "" {
		if p, src := a.priceFor(ctx, c.Model); src != "" {
			st.Price, st.PriceSource = &p, src
		}
	}
	for name, v := range map[string]string{"provider": a.env.Provider, "apiKey": a.env.APIKey, "model": a.env.Model, "baseUrl": a.env.BaseURL} {
		if v != "" {
			st.Locked = append(st.Locked, name)
		}
	}
	sortStrings(st.Locked)
	return st
}

// Enabled reports whether the assistant answers: configured and switched
// on. While it is false nothing is sent to any provider.
func (a *Assistant) Enabled(ctx context.Context) bool {
	if a.fixedProvider() != nil {
		return true
	}
	c := a.Config(ctx)
	return c.Enabled && c.Ready()
}

// Configure validates and saves the owner's settings.
func (a *Assistant) Configure(ctx context.Context, in Settings) (Status, error) {
	in.Provider = strings.ToLower(strings.TrimSpace(in.Provider))
	in.APIKey, in.Model, in.BaseURL = strings.TrimSpace(in.APIKey), strings.TrimSpace(in.Model), strings.TrimSpace(in.BaseURL)
	if a.env.Provider != "" {
		in.Provider = strings.ToLower(a.env.Provider)
	}
	if in.Provider != ProviderAnthropic && in.Provider != ProviderOpenAI {
		return Status{}, domain.Invalid("pick a provider: Anthropic or an OpenAI-compatible endpoint")
	}
	if in.BaseURL != "" {
		u, err := url.Parse(in.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return Status{}, domain.Invalid("the address must start with http:// or https://")
		}
	}
	key := in.APIKey
	if key == "" && in.Provider == a.setting(ctx, settingProvider) {
		key = a.setting(ctx, settingKey) // unchanged
	}
	hasKey := key != "" || a.env.APIKey != ""
	if in.Provider == ProviderAnthropic && !hasKey {
		return Status{}, domain.Invalid("paste an API key from console.anthropic.com")
	}
	if in.Provider == ProviderOpenAI && in.Model == "" && a.env.Model == "" {
		return Status{}, domain.Invalid("say which model to use, e.g. gpt-5 or llama3.1")
	}
	enabled := "1"
	if in.Enabled != nil && !*in.Enabled {
		enabled = "0"
	}
	q := a.svc.Store().Q
	for k, v := range map[string]string{settingProvider: in.Provider, settingKey: key, settingModel: in.Model, settingBaseURL: in.BaseURL, settingEnabled: enabled} {
		if err := q.PutSetting(ctx, db.PutSettingParams{Key: k, Value: v}); err != nil {
			return Status{}, err
		}
	}
	model := in.Model
	if a.env.Model != "" {
		model = a.env.Model
	}
	if model == "" && in.Provider == ProviderAnthropic {
		model = DefaultAnthropicModel
	}
	if err := a.setPrice(ctx, model, in.Price); err != nil {
		return Status{}, err
	}
	// The key and the messages are never logged; the provider and model are.
	a.log.Info("built-in assistant configured from the web", "provider", in.Provider, "model", in.Model, "enabled", enabled == "1")
	return a.Status(ctx), nil
}

// Remove forgets everything saved from the web, including the key.
func (a *Assistant) Remove(ctx context.Context) (Status, error) {
	q := a.svc.Store().Q
	for _, k := range []string{settingProvider, settingKey, settingModel, settingBaseURL, settingEnabled} {
		if err := q.DeleteSetting(ctx, k); err != nil {
			return Status{}, err
		}
	}
	a.log.Info("built-in assistant removed from the web")
	return a.Status(ctx), nil
}

// provider returns the client for the current configuration. The client is
// kept while the configuration stays the same, so what it learned about
// the endpoint (see OpenAI.completionTokens) is not lost between calls.
func (a *Assistant) provider(ctx context.Context) (Provider, error) {
	if p := a.fixedProvider(); p != nil {
		return p, nil
	}
	c := a.Config(ctx)
	if !c.Enabled || !c.Ready() {
		return nil, errKind(KindNotConfigured, "")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cached != nil && a.cachedFor == c {
		return a.cached, nil
	}
	var p Provider
	switch c.Provider {
	case ProviderAnthropic:
		p = NewAnthropic(c.APIKey, c.Model, c.BaseURL, callTimeout)
	default:
		p = NewOpenAI(c.APIKey, c.Model, c.BaseURL, callTimeout)
	}
	a.cached, a.cachedFor = p, c
	return p, nil
}

// TestResult is the outcome of the settings test.
type TestResult struct {
	OK      bool        `json:"ok"`
	Message domain.Text `json:"message" doc:"a translatable text"`
	Model   string      `json:"model,omitempty" doc:"the model that answered"`
}

// Test makes one small call to the configured model, with a tool, and says
// in a sentence whether chat will work. It works while the switch is off,
// so the owner can test before turning it on.
func (a *Assistant) Test(ctx context.Context) TestResult {
	p := a.fixedProvider()
	if p == nil {
		c := a.Config(ctx)
		if !c.Ready() {
			return TestResult{Message: domain.T("Save a provider first.")}
		}
		c.Enabled = true
		switch c.Provider {
		case ProviderAnthropic:
			p = NewAnthropic(c.APIKey, c.Model, c.BaseURL, 45*time.Second)
		default:
			p = NewOpenAI(c.APIKey, c.Model, c.BaseURL, 45*time.Second)
		}
	}
	res, err := p.Complete(ctx, Request{
		System:    "This is a connection test from Exhale. Call the ping tool once.",
		Messages:  []Message{{Role: RoleUser, Text: "Call the ping tool."}},
		Tools:     []Tool{{Name: "ping", Description: "Answers the connection test.", Schema: []byte(`{"type":"object","properties":{},"additionalProperties":false}`)}},
		MaxTokens: 1024,
	})
	if err != nil {
		msg := Explain(err)
		if e, ok := err.(*Error); ok && e.Detail != "" {
			// The provider's own words help the owner fix the setting.
			msg = domain.T("%s (%s)", msg, e.Detail)
		}
		return TestResult{Message: msg}
	}
	if len(res.ToolCalls) == 0 {
		return TestResult{OK: true, Model: res.Model, Message: domain.T("The model answers, but it did not use the test tool. Asking questions and making changes needs a model that supports tools.")}
	}
	return TestResult{OK: true, Model: res.Model, Message: domain.T("The model answers and can use Exhale's tools.")}
}
