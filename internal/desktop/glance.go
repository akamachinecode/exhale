package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The menu bar icon (ADR 0032) reads the server's API as the owner, with
// the token the server keeps for it in the data directory. These are the
// parts of the API it uses, kept here so the window program links nothing
// of the server.

// TokenFile is the file, in the data directory, where the server keeps the
// menu bar icon's token (auth.DesktopTokenFile).
const TokenFile = "desktop-token"

// ErrNoToken is a menu bar icon that cannot read the API yet: the server
// has no owner (a new install), or its data directory is not this person's
// (the Linux system service). The icon shows what it can and tries again.
var ErrNoToken = errors.New("menu bar: no token to read Exhale with")

// Glance is what GET /api/v1/glance answers (app.Glance).
type Glance struct {
	Language  string         `json:"language"`
	Today     string         `json:"today"`
	Charges   []GlanceCharge `json:"charges"`
	Totals    []string       `json:"totals"`
	Overdue   int            `json:"overdue"`
	KeepAwake KeepAwake      `json:"keepAwake"`
}

// GlanceCharge is one of the next charges, its amount already written.
type GlanceCharge struct {
	SubscriptionID string `json:"subscriptionId"`
	Name           string `json:"name"`
	Amount         string `json:"amount"`
	Date           string `json:"date"`
	DaysUntil      int    `json:"daysUntil"`
	Kind           string `json:"kind"` // renewal, trial or expiry
}

// KeepAwake is the keep-awake setting with its status (app.KeepAwakeInfo).
type KeepAwake struct {
	Mode        string `json:"mode"`
	From        string `json:"from"`
	To          string `json:"to"`
	LidClosed   bool   `json:"lidClosed"`
	OnlyOnPower bool   `json:"onlyOnPower"`
	Session     struct {
		Active bool   `json:"active"`
		Until  string `json:"until"`
	} `json:"session"`
	Status struct {
		State  string `json:"state"`
		Reason string `json:"reason"`
		Until  string `json:"until"`
		Lid    string `json:"lid"`
		Power  struct {
			HasBattery bool `json:"hasBattery"`
			OnBattery  bool `json:"onBattery"`
			Percent    int  `json:"percent"`
		} `json:"power"`
	} `json:"status"`
}

// Client talks to the local server for the menu bar icon.
type Client struct {
	Base    string // the web UI, http://127.0.0.1:8484
	DataDir string // where the token file is
	HTTP    *http.Client
}

// Glance reads the next charges and the keep-awake status.
func (c *Client) Glance(ctx context.Context, limit int) (Glance, error) {
	var g Glance
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/glance?limit=%d", limit), nil, &g, 10*time.Second)
	return g, err
}

// Session starts (minutes 0: until stopped), extends or stops a session that
// keeps the computer awake now.
func (c *Client) Session(ctx context.Context, action string, minutes int) error {
	body := map[string]any{"action": action}
	if minutes > 0 {
		body["minutes"] = minutes
	}
	return c.do(ctx, http.MethodPost, "/api/v1/keep-awake/session", body, nil, 10*time.Second)
}

// SetKeepAwake changes the fields given, such as lidClosed or onlyOnPower.
func (c *Client) SetKeepAwake(ctx context.Context, fields map[string]any) error {
	return c.do(ctx, http.MethodPost, "/api/v1/keep-awake", fields, nil, 10*time.Second)
}

// SetupLid asks the computer for the permission to keep it awake with its
// lid closed: on a Mac the server shows the administrator's password
// dialog, so this waits for the person.
func (c *Client) SetupLid(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/api/v1/keep-awake/lid-setup", map[string]any{}, nil, 5*time.Minute)
}

// token reads the token each time: the server writes a new one when the
// old one is revoked.
func (c *Client) token() (string, error) {
	if c.DataDir == "" {
		return "", ErrNoToken
	}
	b, err := os.ReadFile(filepath.Join(c.DataDir, TokenFile)) //nolint:gosec // Exhale's own data directory
	if err != nil {
		return "", ErrNoToken
	}
	t := strings.TrimSpace(string(b))
	if t == "" {
		return "", ErrNoToken
	}
	return t, nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any, timeout time.Duration) error {
	tok, err := c.token()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(c.Base, "/")+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return ErrNoToken
	case res.StatusCode >= 300:
		var p struct {
			Detail string `json:"detail"`
		}
		if json.Unmarshal(data, &p) == nil && p.Detail != "" {
			return errors.New(p.Detail)
		}
		return fmt.Errorf("exhale answered %s", res.Status)
	case out != nil:
		return json.Unmarshal(data, out)
	}
	return nil
}
