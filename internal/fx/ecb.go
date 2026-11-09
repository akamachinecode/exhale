// Package fx (piece ⑤) downloads the European Central Bank's daily reference
// rates: free, no API key, about 30 currencies (ADR 0008). Rates are stored
// with their date so past charges can be valued at their own day's rate.
package fx

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Sources: the latest day, and the last 90 days for backfill.
const (
	DailyURL = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml"
	Hist90   = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-hist-90d.xml"
)

// Rate is units of Currency per 1 EUR on Day.
type Rate struct {
	Day      string
	Currency string
	Rate     string
}

type envelope struct {
	Days []struct {
		Time  string `xml:"time,attr"`
		Rates []struct {
			Currency string `xml:"currency,attr"`
			Rate     string `xml:"rate,attr"`
		} `xml:"Cube"`
	} `xml:"Cube>Cube"`
}

// Parse reads an ECB eurofxref XML document.
func Parse(r io.Reader) ([]Rate, error) {
	var env envelope
	if err := xml.NewDecoder(r).Decode(&env); err != nil {
		return nil, fmt.Errorf("fx: parse ECB rates: %w", err)
	}
	var out []Rate
	for _, d := range env.Days {
		for _, r := range d.Rates {
			out = append(out, Rate{Day: d.Time, Currency: r.Currency, Rate: r.Rate})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("fx: no rates in document")
	}
	return out, nil
}

// Updater keeps the rates table fresh. It is a supervised service.
type Updater struct {
	St     *store.Store
	Log    *slog.Logger
	Client *http.Client
	Every  time.Duration
}

func (u *Updater) String() string { return "fx" }

// Serve refreshes now and then every u.Every (default 12 h). Failures are
// logged and retried; the app keeps working with the last known rates.
func (u *Updater) Serve(ctx context.Context) error {
	every := u.Every
	if every == 0 {
		every = 12 * time.Hour
	}
	for {
		if err := u.Refresh(ctx); err != nil {
			u.Log.Warn("exchange rates not updated; using the last known ones", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(every):
		}
	}
}

// Refresh downloads the latest rates (and 90 days of history on first run).
func (u *Updater) Refresh(ctx context.Context) error {
	url := DailyURL
	if _, err := u.St.Q.FxRateOnOrBefore(ctx, db.FxRateOnOrBeforeParams{Currency: "USD", Day: "9999-12-31"}); err != nil {
		url = Hist90
	}
	client := u.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("fx: ECB answered %s", res.Status)
	}
	rates, err := Parse(res.Body)
	if err != nil {
		return err
	}
	return Save(ctx, u.St, rates)
}

// Save stores rates.
func Save(ctx context.Context, st *store.Store, rates []Rate) error {
	return st.Tx(ctx, func(q *db.Queries) error {
		for _, r := range rates {
			if err := q.UpsertFxRate(ctx, db.UpsertFxRateParams{Day: r.Day, Currency: r.Currency, Rate: r.Rate, Source: "ecb"}); err != nil {
				return err
			}
		}
		return nil
	})
}
