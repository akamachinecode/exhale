package httpapi

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/importer"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Data in and out: file uploads to import, CSV downloads and the calendar
// feed. They are not in the op catalog because they deal in files and
// links, not JSON; agents use import_subscriptions, export_data and
// calendar instead.

// --- import: read a file into a review list ---------------------------------------

func parseImport(svc *app.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorFrom(r.Context())
		if !ok {
			writeProblem(w, http.StatusUnauthorized, "sign in or send a bearer token")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, importer.MaxFileSize+1<<20)
		var data []byte
		var err error
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			f, _, ferr := r.FormFile("file")
			if ferr != nil {
				writeProblem(w, http.StatusBadRequest, "attach the file in a field named file")
				return
			}
			defer f.Close()
			data, err = io.ReadAll(f)
		} else {
			data, err = io.ReadAll(r.Body)
		}
		if err != nil || len(data) > importer.MaxFileSize {
			writeProblem(w, http.StatusRequestEntityTooLarge, "the file is larger than 20 MB")
			return
		}
		info, err := svc.GetHousehold(r.Context(), actor, struct{}{})
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "could not read the household")
			return
		}
		res, err := importer.Parse(r.Context(), data, info.Household.Currency)
		if err != nil {
			msg := "could not read the file"
			if errors.Is(err, importer.ErrUnknownFormat) {
				msg = strings.ToUpper(err.Error()[:1]) + err.Error()[1:]
			}
			writeProblem(w, http.StatusUnprocessableEntity, msg)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}
}

// --- export: CSV downloads -------------------------------------------------------

func exportCSV(svc *app.Service, kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorFrom(r.Context())
		if !ok {
			writeProblem(w, http.StatusUnauthorized, "sign in or send a bearer token")
			return
		}
		data, err := svc.Export(r.Context(), actor, struct{}{})
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "could not export")
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="exhale-%s-%s.csv"`, kind, data.ExportedAt.Format(time.DateOnly)))
		cw := csv.NewWriter(w)
		defer cw.Flush()
		date := func(d fmt.Stringer, zero bool) string {
			if zero {
				return ""
			}
			return d.String()
		}
		if kind == "charges" {
			_ = cw.Write([]string{"date", "subscription", "amount", "currency", "status", "charged amount", "charged currency", "scheduled date", "note"})
			for _, s := range data.Subscriptions {
				for _, c := range s.Charges {
					charged, chargedCur := "", ""
					if !c.ChargedAmount.IsZero() {
						charged, chargedCur = c.ChargedAmount.Decimal(), c.ChargedAmount.Currency
					}
					_ = cw.Write([]string{c.DueDate.String(), s.Name, c.Amount.Decimal(), c.Amount.Currency, string(c.Status), charged, chargedCur, c.OriginalDate.String(), c.Note})
				}
			}
			return
		}
		_ = cw.Write([]string{"name", "price", "currency", "cycle", "interval", "unit", "next charge", "status", "category", "payment method", "paid by",
			"monthly cost", "yearly cost", "started on", "trial ends", "notice days", "renewal", "end date", "cancel url", "account", "url", "notes"})
		methods := map[string]string{}
		for _, pm := range data.PaymentMethods {
			methods[pm.ID] = pm.Name
		}
		for _, s := range data.Subscriptions {
			rule := s.Schedule.Versions[len(s.Schedule.Versions)-1].Rule
			renewal := string(s.Renewal)
			if renewal == "" {
				renewal = string(domain.RenewalAuto)
			}
			_ = cw.Write([]string{s.Name, s.Price.Decimal(), s.Price.Currency, s.Cadence, strconv.Itoa(rule.Interval), string(rule.Unit),
				date(s.NextCharge, s.NextCharge.IsZero()), string(s.Status), s.CategoryName, methods[s.PaymentMethodID], s.PayerName,
				s.MonthlyCost.Decimal(), s.YearlyCost.Decimal(), date(s.StartedOn, s.StartedOn.IsZero()), date(s.TrialEndsOn, s.TrialEndsOn.IsZero()),
				strconv.Itoa(s.NoticeDays), renewal, date(s.Schedule.Until, s.Schedule.Until.IsZero()), s.CancelURL, s.Account, s.URL, s.Notes})
		}
	}
}

// --- calendar feed ---------------------------------------------------------------

// A feed link carries its own secret, because calendar apps cannot send a
// token. Only its hash is stored; a member has one link and can replace or
// remove it.
const (
	feedKeyPrefix   = "feed:"   // feed:<hash> → member id
	feedOwnerPrefix = "feedof:" // feedof:<member id> → hash|created
)

// FeedStatus says whether the member has a calendar link.
type FeedStatus struct {
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"createdAt,omitempty"`
	// Path is returned only when the link is created: Exhale keeps a hash.
	Path string `json:"path,omitempty" doc:"Path of the feed, shown once. Prefix it with the address of this Exhale."`
}

func feedStatus(ctx context.Context, st *store.Store, memberID string) (FeedStatus, string) {
	v, err := st.Q.GetSetting(ctx, feedOwnerPrefix+memberID)
	if err != nil {
		return FeedStatus{}, ""
	}
	hash, created, _ := strings.Cut(v, "|")
	return FeedStatus{Enabled: true, CreatedAt: created}, hash
}

func removeFeed(ctx context.Context, st *store.Store, memberID string) error {
	if _, hash := feedStatus(ctx, st, memberID); hash != "" {
		if err := st.Q.DeleteSetting(ctx, feedKeyPrefix+hash); err != nil {
			return err
		}
	}
	return st.Q.DeleteSetting(ctx, feedOwnerPrefix+memberID)
}

func registerData(api huma.API, mux *http.ServeMux, svc *app.Service) {
	mux.HandleFunc("POST /api/v1/import/parse", parseImport(svc))
	mux.HandleFunc("GET /api/v1/export/subscriptions.csv", exportCSV(svc, "subscriptions"))
	mux.HandleFunc("GET /api/v1/export/charges.csv", exportCSV(svc, "charges"))
	mux.HandleFunc("GET /feeds/calendar/{key}", calendarFeed(svc))

	member := func(ctx context.Context) (app.Actor, error) {
		a, ok := actorFrom(ctx)
		if !ok || a.Kind != app.ActorMember {
			return a, huma.Error403Forbidden("sign in to manage your calendar link")
		}
		return a, nil
	}
	op := func(id, method, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: "/api/v1/calendar/feed", Summary: summary, Tags: []string{"Data"}}
	}
	huma.Register(api, op("calendar_feed", http.MethodGet, "Calendar link status"), func(ctx context.Context, _ *struct{}) (*resp[FeedStatus], error) {
		a, err := member(ctx)
		if err != nil {
			return nil, err
		}
		s, _ := feedStatus(ctx, svc.Store(), a.MemberID)
		return &resp[FeedStatus]{Body: s}, nil
	})
	huma.Register(api, op("calendar_feed_create", http.MethodPost, "Create or replace the calendar link"), func(ctx context.Context, _ *struct{}) (*resp[FeedStatus], error) {
		a, err := member(ctx)
		if err != nil {
			return nil, err
		}
		st := svc.Store()
		if err := removeFeed(ctx, st, a.MemberID); err != nil {
			return nil, huma.Error500InternalServerError("could not replace the link", err)
		}
		key := auth.Random(24)
		created := svc.Now().UTC().Format(time.RFC3339)
		if err := st.Q.PutSetting(ctx, db.PutSettingParams{Key: feedKeyPrefix + auth.Hash(key), Value: a.MemberID}); err != nil {
			return nil, huma.Error500InternalServerError("could not create the link", err)
		}
		if err := st.Q.PutSetting(ctx, db.PutSettingParams{Key: feedOwnerPrefix + a.MemberID, Value: auth.Hash(key) + "|" + created}); err != nil {
			return nil, huma.Error500InternalServerError("could not create the link", err)
		}
		return &resp[FeedStatus]{Body: FeedStatus{Enabled: true, CreatedAt: created, Path: "/feeds/calendar/" + key + ".ics"}}, nil
	})
	huma.Register(api, op("calendar_feed_remove", http.MethodDelete, "Remove the calendar link"), func(ctx context.Context, _ *struct{}) (*resp[FeedStatus], error) {
		a, err := member(ctx)
		if err != nil {
			return nil, err
		}
		if err := removeFeed(ctx, svc.Store(), a.MemberID); err != nil {
			return nil, huma.Error500InternalServerError("could not remove the link", err)
		}
		return &resp[FeedStatus]{Body: FeedStatus{}}, nil
	})
}

func calendarFeed(svc *app.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimSuffix(r.PathValue("key"), ".ics")
		memberID, err := svc.Store().Q.GetSetting(r.Context(), feedKeyPrefix+auth.Hash(key))
		if err != nil || key == "" {
			http.NotFound(w, r)
			return
		}
		actor, err := svc.ActorForMember(r.Context(), memberID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		actor.Scope = app.ScopeRead
		info, err := svc.GetHousehold(r.Context(), actor, struct{}{})
		if err != nil {
			http.Error(w, "calendar unavailable", http.StatusInternalServerError)
			return
		}
		cal, err := svc.Calendar(r.Context(), actor, app.CalendarInput{From: info.Today.AddDays(-31).String(), To: info.Today.AddDays(400).String()})
		if err != nil {
			http.Error(w, "calendar unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		w.Header().Set("Cache-Control", "private, max-age=900")
		_, _ = io.WriteString(w, ICal(cal, info.Household.Name, svc.Now()))
	}
}

// ICal renders events as an iCalendar document (RFC 5545): all-day events,
// stable UIDs so calendar apps update instead of duplicating.
func ICal(cal app.CalendarView, name string, now time.Time) string {
	var b strings.Builder
	line := func(s string) {
		// Fold at 75 octets, continuing with a space, without cutting a rune.
		for len(s) > 75 {
			cut := 75
			for cut > 0 && s[cut]&0xC0 == 0x80 {
				cut--
			}
			b.WriteString(s[:cut] + "\r\n ")
			s = s[cut:]
		}
		b.WriteString(s + "\r\n")
	}
	esc := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`).Replace
	stamp := now.UTC().Format("20060102T150405Z")
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//Exhale//Subscriptions//EN")
	line("CALSCALE:GREGORIAN")
	line("METHOD:PUBLISH")
	line("X-WR-CALNAME:" + esc("Exhale · "+name))
	line("REFRESH-INTERVAL;VALUE=DURATION:PT6H")
	line("X-PUBLISHED-TTL:PT6H")
	for _, e := range cal.Events {
		if e.Status == string(domain.ChargeSkipped) {
			continue
		}
		id := e.ChargeID
		if id == "" {
			id = e.SubscriptionID + "-" + e.Date.String()
		}
		summary := e.Title
		if e.Kind != app.EventCancelBy {
			summary += " · " + e.Amount.String()
		}
		switch e.Status {
		case string(domain.ChargePaid):
			summary = "✓ " + summary
		case string(domain.ChargeFailed):
			summary = "✗ " + summary
		}
		line("BEGIN:VEVENT")
		line("UID:" + id + "-" + e.Kind + "@exhale")
		line("DTSTAMP:" + stamp)
		line("DTSTART;VALUE=DATE:" + strings.ReplaceAll(e.Date.String(), "-", ""))
		line("DTEND;VALUE=DATE:" + strings.ReplaceAll(e.Date.AddDays(1).String(), "-", ""))
		line("SUMMARY:" + esc(summary))
		line("CATEGORIES:" + esc(e.Kind))
		line("TRANSP:TRANSPARENT")
		line("END:VEVENT")
	}
	line("END:VCALENDAR")
	return b.String()
}
