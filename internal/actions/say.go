package actions

import (
	"errors"
	"strings"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/i18n"
)

// Texts written outside this package — the service's errors, the
// assistant's cards — are domain.Text values. They are rendered here with
// the same catalog as tr (ADR 0023); servertexts_test.go keeps it complete.

// Say renders t in the given language.
func Say(locale string, t domain.Text) string {
	return t.Render(func(format string) string { return i18n.Translate(locale, format) })
}

// ErrorText is what a failure says to a person, in their language: the
// sentence of a domain.Error, or the error's own text without the sentinel
// in front.
func ErrorText(locale string, err error) string {
	if t, ok := domain.TextOf(err); ok {
		return domain.Capitalize(Say(locale, t))
	}
	msg := err.Error()
	for _, sentinel := range []error{domain.ErrValidation, domain.ErrNotFound, domain.ErrConflict, app.ErrForbidden} {
		if errors.Is(err, sentinel) {
			msg = strings.TrimPrefix(msg, sentinel.Error()+": ")
		}
	}
	return domain.Capitalize(msg)
}
