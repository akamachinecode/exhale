package main

import (
	"testing"

	"github.com/alecthomas/kong"
)

func parse(t *testing.T, args ...string) serveCmd {
	t.Helper()
	var c cli
	k, err := kong.New(&c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.Parse(args); err != nil {
		t.Fatal(err)
	}
	return c.Serve
}

// The notify-only channels read their settings from the environment; an
// unset EXHALE_ALLOW_PRIVATE_DESTINATIONS must stay "not decided here" so
// the owner can choose in the web.
func TestChannelFlags(t *testing.T) {
	if s := parse(t, "serve"); s.AllowPrivateDestinations != nil || s.PublicURL != "" || s.SMTPHost != "" || s.SMTPTLS != "starttls" {
		t.Fatalf("defaults = %+v", s)
	}

	t.Setenv("EXHALE_PUBLIC_URL", "https://exhale.example.com")
	t.Setenv("EXHALE_SMTP_HOST", "smtp.example.com")
	t.Setenv("EXHALE_SMTP_PORT", "465")
	t.Setenv("EXHALE_SMTP_TLS", "tls")
	t.Setenv("EXHALE_NTFY_URL", "https://ntfy.example.com")
	t.Setenv("EXHALE_ALLOW_PRIVATE_DESTINATIONS", "false")
	s := parse(t, "serve")
	if s.PublicURL != "https://exhale.example.com" || s.SMTPHost != "smtp.example.com" || s.SMTPPort != 465 || s.SMTPTLS != "tls" || s.NtfyURL != "https://ntfy.example.com" {
		t.Fatalf("from the environment = %+v", s)
	}
	if s.AllowPrivateDestinations == nil || *s.AllowPrivateDestinations {
		t.Fatalf("EXHALE_ALLOW_PRIVATE_DESTINATIONS=false = %v", s.AllowPrivateDestinations)
	}

	t.Setenv("EXHALE_ALLOW_PRIVATE_DESTINATIONS", "true")
	if s := parse(t, "serve"); s.AllowPrivateDestinations == nil || !*s.AllowPrivateDestinations {
		t.Fatalf("EXHALE_ALLOW_PRIVATE_DESTINATIONS=true = %v", s.AllowPrivateDestinations)
	}
	if s := parse(t, "serve", "--no-allow-private-destinations"); s.AllowPrivateDestinations == nil || *s.AllowPrivateDestinations {
		t.Fatalf("--no-allow-private-destinations = %v", s.AllowPrivateDestinations)
	}
}
