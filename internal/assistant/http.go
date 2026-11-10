package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Both provider clients are a few hundred lines over net/http instead of
// the official SDKs: see "Implementation notes" in ADR 0018 for the
// measurement behind that choice.

const (
	// maxResponseBytes bounds what is read from a provider.
	maxResponseBytes = 8 << 20
	userAgent        = "exhale-assistant"
)

// retryWait is the pause before the one retry (tests shorten it).
var retryWait = time.Second

// post sends body as JSON and returns the response body. Statuses that are
// worth one more try (429 with a short wait, 5xx, 529) are retried once.
func post(ctx context.Context, hc *http.Client, url string, headers map[string]string, body any) (status int, data []byte, err error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	for attempt := 0; ; attempt++ {
		status, data, wait, err := postOnce(ctx, hc, url, headers, payload)
		if err != nil {
			return 0, nil, err
		}
		retryable := status == http.StatusTooManyRequests || status >= 500
		if !retryable || attempt > 0 || wait > 5*time.Second {
			return status, data, nil
		}
		select {
		case <-ctx.Done():
			return 0, nil, errKind(KindNoAnswer, ctx.Err().Error())
		case <-time.After(max(wait, retryWait)):
		}
	}
}

func postOnce(ctx context.Context, hc *http.Client, url string, headers map[string]string, payload []byte) (int, []byte, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, 0, errKind(KindRejected, "the address of the model provider is not valid")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := hc.Do(req)
	if err != nil {
		// Timeouts, refused connections, DNS: the model did not answer.
		// The error text may hold the URL, never the key.
		return 0, nil, 0, errKind(KindNoAnswer, err.Error())
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes))
	if err != nil {
		return 0, nil, 0, errKind(KindNoAnswer, err.Error())
	}
	var wait time.Duration
	if s, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil {
		wait = time.Duration(s) * time.Second
	}
	return res.StatusCode, data, wait, nil
}

// statusError maps an HTTP status to a failure the person can understand.
func statusError(status int, detail string) *Error {
	e := &Error{Status: status, Detail: truncate(detail, 300)}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		e.Kind = KindAuth
	case status == http.StatusNotFound:
		e.Kind = KindModel
	case status == http.StatusRequestEntityTooLarge:
		e.Kind = KindTooLarge
	case status == http.StatusTooManyRequests:
		e.Kind = KindRateLimited
	case status == http.StatusPaymentRequired:
		e.Kind = KindAuth
	case status >= 500:
		e.Kind = KindOverloaded
	default:
		e.Kind = KindRejected
	}
	return e
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}

func isTimeout(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Kind == KindNoAnswer
}

func trimSlash(s string) string { return strings.TrimRight(strings.TrimSpace(s), "/") }
