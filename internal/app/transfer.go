package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// --- export ----------------------------------------------------------------------

// ExportedSubscription is a subscription with every charge it has had.
type ExportedSubscription struct {
	SubscriptionView
	Charges []domain.Charge `json:"charges"`
	// Files are its uploaded logo and documents, without their content:
	// the full export archive and the backups carry the content (ADR 0027).
	Files []Document `json:"files"`
}

// ExportData is everything the household keeps in Exhale, in one document.
type ExportData struct {
	Format         string                 `json:"format" jsonschema:"always exhale/1"`
	ExportedAt     time.Time              `json:"exportedAt"`
	Household      domain.Household       `json:"household"`
	Members        []domain.Member        `json:"members"`
	Categories     []domain.Category      `json:"categories"`
	PaymentMethods []domain.PaymentMethod `json:"paymentMethods"`
	Subscriptions  []ExportedSubscription `json:"subscriptions"`
}

// ExportFormat identifies Exhale's own export.
const ExportFormat = "exhale/1"

// Export returns the household's data: reference data, every subscription
// the actor can see and all its charges.
func (s *Service) Export(ctx context.Context, actor Actor, _ struct{}) (ExportData, error) {
	info, err := s.GetHousehold(ctx, actor, struct{}{})
	if err != nil {
		return ExportData{}, err
	}
	out := ExportData{Format: ExportFormat, ExportedAt: s.now().UTC(), Household: info.Household, Members: info.Members,
		Categories: info.Categories, PaymentMethods: info.PaymentMethods, Subscriptions: []ExportedSubscription{}}
	list, err := s.ListSubscriptions(ctx, actor, ListSubscriptionsInput{})
	if err != nil {
		return out, err
	}
	fileRows, err := s.st.Q.ListFilesForExport(ctx, actor.HouseholdID)
	if err != nil {
		return out, err
	}
	files := map[string][]Document{}
	for _, r := range fileRows {
		files[r.SubscriptionID] = append(files[r.SubscriptionID], documentFromRow(db.GetFileMetaRow(r)))
	}
	for _, v := range list.Subscriptions {
		rows, err := s.st.Q.ListChargesForSubscription(ctx, db.ListChargesForSubscriptionParams{SubscriptionID: v.ID, Limit: 5000})
		if err != nil {
			return out, err
		}
		e := ExportedSubscription{SubscriptionView: v, Charges: make([]domain.Charge, 0, len(rows)), Files: files[v.ID]}
		if e.Files == nil {
			e.Files = []Document{}
		}
		for _, r := range slices.Backward(rows) {
			e.Charges = append(e.Charges, store.ChargeFromRow(r))
		}
		out.Subscriptions = append(out.Subscriptions, e)
	}
	return out, nil
}

// --- import ----------------------------------------------------------------------

// ImportItem is one subscription to bring in. Categories, payment methods
// and payers are given by name, so a spreadsheet, another app's export or an
// AI agent reading a bank statement can fill it without knowing ids.
type ImportItem struct {
	Name          string `json:"name"`
	Price         string `json:"price" jsonschema:"decimal string, e.g. 12.99"`
	Currency      string `json:"currency,omitempty" jsonschema:"ISO 4217 code; default: the household's display currency"`
	Interval      int    `json:"interval,omitempty" jsonschema:"units between charges, default 1"`
	Unit          string `json:"unit,omitempty" jsonschema:"day, week, month (default) or year" enum:"day,week,month,year"`
	NextCharge    string `json:"nextCharge,omitempty" jsonschema:"next (or first) charge, YYYY-MM-DD. For an ended subscription: when it started"`
	StartedOn     string `json:"startedOn,omitempty" jsonschema:"when the member first subscribed, YYYY-MM-DD"`
	EndDate       string `json:"endDate,omitempty" jsonschema:"last day, for subscriptions that ended, YYYY-MM-DD"`
	Status        string `json:"status,omitempty" jsonschema:"active (default), paused or cancelled" enum:"active,paused,cancelled"`
	Category      string `json:"category,omitempty" jsonschema:"category name; created if it does not exist"`
	PaymentMethod string `json:"paymentMethod,omitempty" jsonschema:"payment method name; created if it does not exist"`
	Payer         string `json:"payer,omitempty" jsonschema:"member name; default: the caller"`
	URL           string `json:"url,omitempty"`
	Notes         string `json:"notes,omitempty"`
	CancelURL     string `json:"cancelUrl,omitempty"`
	Account       string `json:"account,omitempty"`
	Renewal       string `json:"renewal,omitempty" jsonschema:"auto (default) or manual" enum:"auto,manual"`
	NoticeDays    int    `json:"noticeDays,omitempty"`
	TrialEndsOn   string `json:"trialEndsOn,omitempty"`
	ReminderDays  []int  `json:"reminderDays,omitempty" jsonschema:"days before each charge to remind; default [3, 0]"`
	AutoConfirm   bool   `json:"autoConfirm,omitempty"`
	// Remark is a note from whoever prepared the item (a file parser or an
	// agent) for the person reviewing it. It is not saved.
	Remark string `json:"remark,omitempty" jsonschema:"note for the person reviewing the import; not saved"`
}

// ImportInput brings in several subscriptions at once.
type ImportInput struct {
	Items           []ImportItem `json:"items"`
	AllowDuplicates bool         `json:"allowDuplicates,omitempty" jsonschema:"also create items that match an existing subscription (same name, price and currency)"`
	DryRun          bool         `json:"dryRun,omitempty" jsonschema:"preview: nothing is saved. Show the result to the user, then call again without it"`
}

// ImportOutcome is what happened (or would happen) to one item.
type ImportOutcome struct {
	Name    string `json:"name"`
	Action  string `json:"action" jsonschema:"create, duplicate (skipped) or error (skipped)" enum:"create,duplicate,error"`
	ID      string `json:"id,omitempty" jsonschema:"new subscription id; not saved in a dry run"`
	Message string `json:"message,omitempty"`
}

// ImportResult summarises an import.
type ImportResult struct {
	Created           int             `json:"created"`
	Duplicates        int             `json:"duplicates"`
	Errors            int             `json:"errors"`
	Items             []ImportOutcome `json:"items"`
	NewCategories     []string        `json:"newCategories" jsonschema:"categories created along the way"`
	NewPaymentMethods []string        `json:"newPaymentMethods" jsonschema:"payment methods created along the way"`
	DryRun            bool            `json:"dryRun,omitempty"`
}

// MaxImportItems bounds one import call.
const MaxImportItems = 500

// Import creates many subscriptions in one step. Items that fail validation
// or duplicate an existing subscription are skipped and reported; the rest
// are created. With DryRun it is a review list: nothing is saved.
func (s *Service) Import(ctx context.Context, actor Actor, in ImportInput) (ImportResult, error) {
	out := ImportResult{Items: []ImportOutcome{}, NewCategories: []string{}, NewPaymentMethods: []string{}, DryRun: in.DryRun}
	if len(in.Items) == 0 || len(in.Items) > MaxImportItems {
		return out, fmt.Errorf("%w: pass between 1 and %d items", domain.ErrValidation, MaxImportItems)
	}
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		h, err := s.household(ctx, q, actor.HouseholdID)
		if err != nil {
			return err
		}
		cats, methods, members := map[string]string{}, map[string]string{}, map[string]string{}
		for _, r := range mustList(q.ListCategories(ctx, h.ID)) {
			cats[strings.ToLower(r.Name)] = r.ID
		}
		for _, r := range mustList(q.ListPaymentMethods(ctx, h.ID)) {
			methods[strings.ToLower(r.Name)] = r.ID
		}
		for _, r := range mustList(q.ListMembers(ctx, h.ID)) {
			members[strings.ToLower(r.Name)] = r.ID
		}
		seen := map[string]bool{}
		for _, r := range mustList(q.ListSubscriptions(ctx, h.ID)) {
			seen[importKey(r.Name, r.PriceMinor, r.Currency)] = true
		}
		for _, it := range in.Items {
			o := ImportOutcome{Name: strings.TrimSpace(it.Name), Action: "create"}
			ci, err := it.createInput(h, members)
			var sub domain.Subscription
			if err == nil {
				sub, err = s.newSubscription(ctx, q, actor, ci)
			}
			if err != nil {
				o.Action, o.Message = "error", strings.TrimPrefix(err.Error(), domain.ErrValidation.Error()+": ")
				out.Errors++
				out.Items = append(out.Items, o)
				continue
			}
			key := importKey(sub.Name, sub.Price.Minor, sub.Price.Currency)
			if seen[key] && !in.AllowDuplicates {
				o.Action, o.Message = "duplicate", "already in Exhale with the same price"
				out.Duplicates++
				out.Items = append(out.Items, o)
				continue
			}
			seen[key] = true
			if name := strings.TrimSpace(it.Category); name != "" {
				id, ok := cats[strings.ToLower(name)]
				if !ok {
					id = domain.NewID(domain.PrefixCategory)
					if err := q.CreateCategory(ctx, db.CreateCategoryParams{ID: id, HouseholdID: h.ID, Name: name}); err != nil {
						return err
					}
					if _, err := c.record(q, "category.create", "category", id, nil, domain.Category{ID: id, HouseholdID: h.ID, Name: name}); err != nil {
						return err
					}
					cats[strings.ToLower(name)] = id
					out.NewCategories = append(out.NewCategories, name)
				}
				sub.CategoryID = id
			}
			if name := strings.TrimSpace(it.PaymentMethod); name != "" {
				id, ok := methods[strings.ToLower(name)]
				if !ok {
					pm := domain.PaymentMethod{ID: domain.NewID(domain.PrefixPaymentMethod), HouseholdID: h.ID, Name: name, Kind: guessKind(name)}
					if err := q.CreatePaymentMethod(ctx, db.CreatePaymentMethodParams{ID: pm.ID, HouseholdID: h.ID, Name: pm.Name, Kind: string(pm.Kind)}); err != nil {
						return err
					}
					if _, err := c.record(q, "payment_method.save", "payment_method", pm.ID, nil, pm); err != nil {
						return err
					}
					id = pm.ID
					methods[strings.ToLower(name)] = id
					out.NewPaymentMethods = append(out.NewPaymentMethods, name)
				}
				sub.PaymentMethodID = id
			}
			if err := s.persistNew(ctx, q, c, actor, &sub); err != nil {
				if errors.Is(err, domain.ErrValidation) {
					o.Action, o.Message = "error", strings.TrimPrefix(err.Error(), domain.ErrValidation.Error()+": ")
					out.Errors++
					out.Items = append(out.Items, o)
					continue
				}
				return err
			}
			o.ID = sub.ID
			out.Created++
			out.Items = append(out.Items, o)
		}
		c.emit(bus.Event{Type: bus.CategorySaved, HouseholdID: h.ID})
		return nil
	})
	return out, err
}

func importKey(name string, minor int64, currency string) string {
	return fmt.Sprintf("%s|%d|%s", strings.ToLower(strings.TrimSpace(name)), minor, currency)
}

// guessKind picks a payment method kind from its name.
func guessKind(name string) domain.PaymentMethodKind {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "paypal"):
		return domain.KindPayPal
	case strings.Contains(n, "direct debit") || strings.Contains(n, "sepa") || strings.Contains(n, "bank") || strings.Contains(n, "transfer") || strings.Contains(n, "iban"):
		return domain.KindBank
	case strings.Contains(n, "cash") || strings.Contains(n, "crypto") || strings.Contains(n, "bitcoin"):
		return domain.KindOther
	}
	return domain.KindCard
}

func (it ImportItem) createInput(h domain.Household, members map[string]string) (CreateSubscriptionInput, error) {
	ci := CreateSubscriptionInput{
		Name: it.Name, Price: strings.ReplaceAll(strings.TrimSpace(it.Price), ",", "."), Currency: strings.ToUpper(strings.TrimSpace(it.Currency)),
		Interval: it.Interval, Unit: it.Unit, FirstCharge: it.NextCharge, EndDate: it.EndDate, Status: it.Status,
		URL: normalizeURL(it.URL), Notes: it.Notes, AutoConfirm: it.AutoConfirm, ReminderDays: it.ReminderDays,
		TermsInput: TermsInput{StartedOn: it.StartedOn, TrialEndsOn: it.TrialEndsOn, NoticeDays: it.NoticeDays, Renewal: it.Renewal, CancelURL: normalizeURL(it.CancelURL), Account: it.Account},
	}
	if ci.Currency == "" {
		ci.Currency = h.Currency
	}
	if ci.Unit == "" {
		ci.Unit = "month"
	}
	if ci.FirstCharge == "" && ci.TrialEndsOn == "" {
		if ci.StartedOn == "" {
			return ci, fmt.Errorf("%w: a next charge date is needed", domain.ErrValidation)
		}
		ci.FirstCharge = ci.StartedOn
	}
	if name := strings.TrimSpace(it.Payer); name != "" {
		id, ok := members[strings.ToLower(name)]
		if !ok {
			return ci, fmt.Errorf("%w: nobody called %q shares this Exhale", domain.ErrValidation, name)
		}
		ci.PayerID = id
	}
	return ci, nil
}

// normalizeURL adds https:// to bare hosts such as "netflix.com/account".
func normalizeURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" || strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	return "https://" + u
}
