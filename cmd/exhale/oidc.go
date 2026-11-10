package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/auth"
)

// oidcFlags configure sign-in with an OpenID Connect provider (ADR 0006,
// docs/install/sso.md). Environment or flags only: nothing of this is
// stored in the database or editable from the web.
type oidcFlags struct {
	OIDCIssuer           string   `name:"oidc-issuer" env:"EXHALE_OIDC_ISSUER" help:"Single sign-on: issuer URL of the OpenID Connect provider (the one serving /.well-known/openid-configuration)."`
	OIDCClientID         string   `name:"oidc-client-id" env:"EXHALE_OIDC_CLIENT_ID" help:"Single sign-on: client id registered at the provider."`
	OIDCClientSecret     string   `name:"oidc-client-secret" env:"EXHALE_OIDC_CLIENT_SECRET" help:"Single sign-on: client secret. Leave empty for a public client (PKCE only)."`
	OIDCClientSecretFile string   `name:"oidc-client-secret-file" env:"EXHALE_OIDC_CLIENT_SECRET_FILE" type:"path" help:"Single sign-on: file holding the client secret (Docker secrets)."`
	OIDCName             string   `name:"oidc-name" env:"EXHALE_OIDC_NAME" default:"Single sign-on" help:"Single sign-on: provider name on the button (\"Continue with …\")."`
	OIDCScopes           []string `name:"oidc-scopes" env:"EXHALE_OIDC_SCOPES" default:"openid,email,profile" help:"Single sign-on: scopes to ask for."`
	DisablePasswordLogin bool     `name:"disable-password-login" env:"EXHALE_DISABLE_PASSWORD_LOGIN" help:"Only allow single sign-on. Ignored unless single sign-on is configured."`
}

// provider returns the configured provider, or nil when single sign-on is
// off. The client secret is never logged.
func (f *oidcFlags) provider(ctx context.Context, log *slog.Logger, publicURL string) (*auth.OIDC, error) {
	if f.OIDCIssuer == "" && f.OIDCClientID == "" {
		if f.DisablePasswordLogin {
			log.Warn("--disable-password-login is ignored: single sign-on is not configured")
		}
		return nil, nil
	}
	secret := f.OIDCClientSecret
	if f.OIDCClientSecretFile != "" {
		if secret != "" {
			return nil, errors.New("set the client secret or its file, not both")
		}
		b, err := os.ReadFile(f.OIDCClientSecretFile)
		if err != nil {
			return nil, err
		}
		secret = strings.TrimSpace(string(b))
	}
	sso, err := auth.NewOIDC(auth.OIDCConfig{Issuer: f.OIDCIssuer, ClientID: f.OIDCClientID, ClientSecret: secret, Name: f.OIDCName, Scopes: f.OIDCScopes})
	if err != nil {
		return nil, err
	}
	if sso.InsecureIssuer() {
		log.Warn("single sign-on: the issuer is plain http; anyone between Exhale and the provider can forge sign-ins", "issuer", f.OIDCIssuer)
	}
	if publicURL == "" {
		log.Warn("single sign-on only works from this machine until --public-url (EXHALE_PUBLIC_URL) is set")
	}
	// Discovery is retried on every sign-in; this only reports problems early.
	go func() {
		dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if err := sso.Discover(dctx); err != nil {
			log.Warn("single sign-on: the provider is not answering yet; it is tried again on the next sign-in", "err", err)
			return
		}
		log.Info("single sign-on is on", "provider", sso.Name(), "issuer", f.OIDCIssuer, "password_login", !f.DisablePasswordLogin)
	}()
	return sso, nil
}
