package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/importer"
	"github.com/akamachinecode/exhale/internal/merchant"
)

// Import limits. A file is read in memory and never written anywhere.
const (
	// MaxImageSize is the largest picture sent to a model.
	MaxImageSize = 5 << 20
	// MaxPDFSize is the largest PDF sent to a model.
	MaxPDFSize = 20 << 20
	// MaxTextSize is the largest text sent to a model. A longer file is
	// refused instead of being cut, so nothing is silently left out.
	MaxTextSize = 200_000
	// maxExtracted bounds how many charges one document can propose.
	maxExtracted = 100
	// maxReportedPayments bounds the payments a model reports for matching.
	// The prompt asks for at most 300, which keeps the answer within its
	// token budget.
	maxReportedPayments = 300
	extractToken        = 16_000
	// extractTimeout bounds reading one document.
	extractTimeout = 150 * time.Second
)

// ErrNeedsAssistant is returned for files only a model can read while the
// assistant is off.
var ErrNeedsAssistant = domain.Invalid("reading a PDF, a picture or free text needs the built-in assistant. An owner can turn it on in Settings; a spreadsheet saved as CSV works without it")

// ErrScanNeedsAssistant is returned for a PDF without text (a scan) while
// the assistant is off.
var ErrScanNeedsAssistant = domain.Invalid("this PDF has no text (it is probably a scan): reading it needs the built-in assistant. An owner can turn it on in Settings; or export the statement as CSV from your bank")

// ErrPDFUnreadable is returned for a PDF Exhale cannot open, while the
// assistant is off.
var ErrPDFUnreadable = domain.Invalid("Exhale could not open this PDF (it may be damaged or protected with a password). Export the statement as CSV from your bank, or ask an owner to turn on the built-in assistant in Settings")

// ErrPDFNeedsAssistant is returned for a PDF with text in which Exhale
// found no transactions, while the assistant is off.
var ErrPDFNeedsAssistant = domain.Invalid("Exhale found no list of transactions in this PDF. Export the statement as CSV from your bank, or ask an owner to turn on the built-in assistant in Settings to read it")

const reportTool = "report_recurring_charges"

var reportSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "items": {
      "type": "array",
      "description": "one entry per recurring charge; empty if the document has none",
      "items": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "description": "the service as a person would call it"},
          "price": {"type": "string", "description": "the most recent amount as a positive decimal, e.g. 12.99"},
          "currency": {"type": "string", "description": "ISO 4217 code, if the document shows it"},
          "interval": {"type": "integer", "description": "units between charges, usually 1"},
          "unit": {"type": "string", "enum": ["day", "week", "month", "year"]},
          "nextCharge": {"type": "string", "description": "next expected charge, YYYY-MM-DD"},
          "paymentMethod": {"type": "string", "description": "card or account, only if the document shows it"},
          "remark": {"type": "string", "description": "one sentence: what this is based on"}
        },
        "required": ["name", "price", "remark"]
      }
    },
    "notes": {"type": "array", "items": {"type": "string"}, "description": "things the person should know about the document as a whole, e.g. the period it covers"},
    "transactions": {
      "type": "array",
      "description": "every outgoing payment the document lists, recurring or not, at most the 300 most recent; empty if it lists none",
      "items": {
        "type": "object",
        "properties": {
          "date": {"type": "string", "description": "YYYY-MM-DD"},
          "description": {"type": "string", "description": "the text as the document prints it"},
          "amount": {"type": "string", "description": "what left the account, as a positive decimal, e.g. 12.99"},
          "currency": {"type": "string", "description": "ISO 4217 code, if the document shows it"}
        },
        "required": ["date", "description", "amount"]
      }
    }
  },
  "required": ["items"]
}`)

type reported struct {
	Items []struct {
		Name          string `json:"name"`
		Price         any    `json:"price"`
		Currency      string `json:"currency"`
		Interval      any    `json:"interval"`
		Unit          string `json:"unit"`
		NextCharge    string `json:"nextCharge"`
		PaymentMethod string `json:"paymentMethod"`
		Remark        string `json:"remark"`
	} `json:"items"`
	Notes        []string `json:"notes"`
	Transactions []struct {
		Date        string `json:"date"`
		Description string `json:"description"`
		Amount      any    `json:"amount"`
		Currency    string `json:"currency"`
	} `json:"transactions"`
}

// ReadImport turns an uploaded file or pasted text into an import review
// list. Files Exhale understands by itself (a Wallos database, an export, a
// spreadsheet, a bank export with transactions) never reach a model, except
// that the merchant names of a bank export are tidied when the assistant is
// on. A PDF, a picture or free text is read by the model, which only
// proposes: the person ticks what to import.
func (a *Assistant) ReadImport(ctx context.Context, actor app.Actor, data []byte) (importer.Result, error) {
	info, err := a.svc.GetHousehold(ctx, actor, struct{}{})
	if err != nil {
		return importer.Result{}, err
	}
	cur, today := info.Household.Currency, info.Today
	kind, media := importer.Sniff(data)
	switch kind {
	case importer.FilePDF, importer.FileImage:
		limit, what := MaxImageSize, domain.Word("picture")
		if kind == importer.FilePDF {
			limit, what = MaxPDFSize, "PDF"
		}
		if len(data) > limit {
			return importer.Result{}, domain.Invalid("the %s is larger than %d MB", what, limit>>20)
		}
		source := "statement"
		if kind == importer.FileImage {
			source = "picture"
		}
		if kind == importer.FilePDF {
			// A PDF with text is read by Exhale, like a CSV; the model is
			// only needed for a scan or a layout Exhale cannot follow.
			text, terr := importer.PDFText(data)
			if terr == nil {
				if res, ok := importer.TextTransactions(text, cur, today, source); ok {
					if a.finishStatement(ctx, actor, &res) {
						return res, nil
					}
				}
			}
			if !a.Enabled(ctx) {
				switch {
				case errors.Is(terr, importer.ErrNoText):
					return importer.Result{}, ErrScanNeedsAssistant
				case terr != nil:
					return importer.Result{}, ErrPDFUnreadable
				}
				return importer.Result{}, ErrPDFNeedsAssistant
			}
		}
		res, err := a.extract(ctx, actor, source, cur, today, "Report the recurring charges in the attached document.", []Attachment{{MediaType: media, Data: data}})
		if err == nil {
			a.matchStatement(ctx, actor, &res)
		}
		return res, err
	case importer.FileSQLite, importer.FileJSON:
		return importer.Parse(ctx, data, cur)
	case importer.FileUnknown:
		return importer.Result{}, importer.ErrUnknownFormat
	case importer.FileText:
	}
	if res, shaped := importer.Transactions(data, cur, today); shaped {
		if a.finishStatement(ctx, actor, &res) {
			return res, nil
		}
		if !a.Enabled(ctx) {
			// A list with a name, a date and an amount may still be a list
			// of subscriptions.
			if sheet, err := importer.Parse(ctx, data, cur); err == nil {
				return sheet, nil
			}
			return importer.Result{}, domain.Invalid("no charge repeats in these transactions. A subscription shows after two charges from the same merchant: export a longer period")
		}
		return a.extractText(ctx, actor, cur, today, data)
	}
	res, err := importer.Parse(ctx, data, cur)
	if err == nil || !errors.Is(err, importer.ErrUnknownFormat) {
		return res, err
	}
	// Lines copied from a statement: a date, a description, an amount.
	if res, ok := importer.TextTransactions(string(data), cur, today, "text"); ok && a.finishStatement(ctx, actor, &res) {
		return res, nil
	}
	if !a.Enabled(ctx) {
		return importer.Result{}, ErrNeedsAssistant
	}
	res, err = a.extractText(ctx, actor, cur, today, data)
	if err == nil {
		a.matchStatement(ctx, actor, &res)
	}
	return res, err
}

// finishStatement completes a statement Exhale read by itself: its
// payments are matched with the expected charges, and the merchant names
// are tidied when the assistant is on. It reports false when the statement
// gave nothing to review (no recurring charge, no expected charge found).
func (a *Assistant) finishStatement(ctx context.Context, actor app.Actor, res *importer.Result) bool {
	a.matchStatement(ctx, actor, res)
	if len(res.Items) == 0 && (res.Match == nil || len(res.Match.Matches) == 0) {
		return false
	}
	if len(res.Items) > 0 {
		a.tidyNames(ctx, actor, res)
	}
	return true
}

// matchStatement compares the payments of a statement with the charges
// Exhale expects (app.MatchStatement). A recurring charge whose merchant
// is a subscription that matched is not offered as a new one.
func (a *Assistant) matchStatement(ctx context.Context, actor app.Actor, res *importer.Result) {
	if len(res.Transactions) == 0 {
		return
	}
	m, err := a.svc.MatchStatement(ctx, actor, app.MatchStatementInput{Transactions: res.Transactions})
	if err != nil {
		a.log.Info("import: the statement was not matched", "err", err)
		return
	}
	res.Match = &m
	known := 0
	res.Items = slices.DeleteFunc(res.Items, func(it app.ImportItem) bool {
		for _, mt := range m.Matches {
			if merchant.Same(merchant.Key(it.Name), merchant.Key(mt.Charge.SubscriptionName)) {
				known++
				return true
			}
		}
		return false
	})
	if known > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("%d of the recurring charges are subscriptions Exhale already has: they are in the charges found below, not in the new ones.", known))
	}
}

func (a *Assistant) extractText(ctx context.Context, actor app.Actor, cur string, today engine.Date, data []byte) (importer.Result, error) {
	if len(data) > MaxTextSize {
		return importer.Result{}, domain.Invalid("the text is longer than the model reads in one go (%d KB). Import a shorter period", MaxTextSize/1000)
	}
	return a.extract(ctx, actor, "text", cur, today, "Report the recurring charges in this document.\n\n<document>\n"+dataText(string(data))+"\n</document>", nil)
}

// extract asks the model for the recurring charges of a document. The model
// has one tool with a fixed schema and none of Exhale's: whatever the
// document says, the only thing that can come back is a list to review.
func (a *Assistant) extract(ctx context.Context, actor app.Actor, source, cur string, today engine.Date, text string, files []Attachment) (importer.Result, error) {
	p, err := a.provider(ctx)
	if err != nil {
		if IsKind(err, KindNotConfigured) {
			return importer.Result{}, ErrNeedsAssistant
		}
		return importer.Result{}, err
	}
	if !a.allow(actor.MemberID) {
		return importer.Result{}, errKind(KindBusy, "")
	}
	ctx, cancel := context.WithTimeout(ctx, extractTimeout)
	defer cancel()
	system := extractPrompt + fmt.Sprintf("\n\nToday is %s. Amounts without a currency are in %s.", today, cur)
	res, err := p.Complete(ctx, Request{
		System:    system,
		Messages:  []Message{{Role: RoleUser, Text: text, Attachments: files}},
		Tools:     []Tool{{Name: reportTool, Description: "Report the recurring charges found in the document, for the person to review.", Schema: reportSchema}},
		MaxTokens: extractToken,
	})
	a.record(ctx, actor, usageRecord{kind: "import", surface: "import", provider: p.Name(), model: res.Model, rounds: 1, usage: res.Usage, failed: err != nil})
	if err != nil {
		a.log.Warn("assistant: reading a document failed", "provider", p.Name(), "source", source, "err", err)
		return importer.Result{}, err
	}
	var rep reported
	found := false
	for _, c := range res.ToolCalls {
		if c.Name == reportTool && json.Unmarshal(c.Arguments, &rep) == nil {
			found = true
			break
		}
	}
	if !found {
		// A model without tool support may answer with the JSON as text.
		if i, j := strings.Index(res.Text, "{"), strings.LastIndex(res.Text, "}"); i >= 0 && j > i {
			found = json.Unmarshal([]byte(res.Text[i:j+1]), &rep) == nil
		}
	}
	if !found {
		return importer.Result{}, errKind(KindNoAnswer, "the model did not report a list")
	}
	out := importer.Result{Source: source, Items: []app.ImportItem{}, Notes: []string{}, Model: res.Model}
	if out.Model == "" {
		out.Model = p.Name()
	}
	for _, r := range rep.Items {
		if len(out.Items) == maxExtracted {
			out.Notes = append(out.Notes, fmt.Sprintf("Only the first %d charges are listed.", maxExtracted))
			break
		}
		it, ok := cleanItem(r.Name, anyString(r.Price), r.Currency, anyInt(r.Interval), r.Unit, r.NextCharge, r.PaymentMethod, r.Remark, cur)
		if ok {
			out.Items = append(out.Items, it)
		}
	}
	for i, n := range rep.Notes {
		if i == 5 {
			break
		}
		if n = oneLine(n, 300); n != "" {
			out.Notes = append(out.Notes, n)
		}
	}
	for _, t := range rep.Transactions {
		if len(out.Transactions) == maxReportedPayments {
			break
		}
		if l, ok := cleanPayment(t.Date, t.Description, anyString(t.Amount), t.Currency, cur); ok {
			out.Transactions = append(out.Transactions, l)
		}
	}
	out.Notes = append(out.Notes, "Read by a model: check every line before importing. Nothing is saved until you choose.")
	a.log.Info("assistant read a document", "provider", p.Name(), "source", source, "items", len(out.Items), "input_tokens", res.Usage.Input, "output_tokens", res.Usage.Output)
	return out, nil
}

// cleanItem checks what a model reported before it reaches the review
// list: the text is bounded and single-line, the cycle is one Exhale has,
// dates are dates. What does not hold is dropped, not guessed.
func cleanItem(name, price, currency string, interval int, unit, next, method, remark, defaultCurrency string) (app.ImportItem, bool) {
	it := app.ImportItem{Name: oneLine(name, 120), PaymentMethod: oneLine(method, 60), Remark: oneLine(remark, 300)}
	if it.Name == "" {
		return it, false
	}
	it.Price, it.Currency = importer.SplitPrice(strings.TrimPrefix(strings.TrimSpace(price), "-"), defaultCurrency)
	if c := strings.ToUpper(strings.TrimSpace(currency)); len(c) == 3 {
		it.Currency = c
	}
	if _, err := strconv.ParseFloat(it.Price, 64); err != nil {
		return it, false
	}
	switch unit = strings.ToLower(strings.TrimSpace(unit)); unit {
	case "day", "week", "month", "year":
		it.Unit = unit
	default:
		it.Unit = "month"
		it.Remark = strings.TrimSpace(it.Remark + " (cycle not given: set to monthly)")
	}
	it.Interval = min(max(interval, 1), 120)
	it.NextCharge = importer.AnyDate(next)
	if it.NextCharge == "" {
		it.Remark = strings.TrimSpace(it.Remark + " (no date: add the next charge)")
	}
	return it, true
}

// cleanPayment checks a payment a model reported before it is matched: a
// real date, a positive amount, a bounded single line of text.
func cleanPayment(date, description, amount, currency, defaultCurrency string) (app.StatementLine, bool) {
	l := app.StatementLine{Date: importer.AnyDate(date), Description: oneLine(description, 200)}
	l.Amount, l.Currency = importer.SplitPrice(strings.TrimPrefix(strings.TrimSpace(amount), "-"), defaultCurrency)
	if c := strings.ToUpper(strings.TrimSpace(currency)); len(c) == 3 {
		l.Currency = c
	}
	if f, err := strconv.ParseFloat(l.Amount, 64); err != nil || f <= 0 || l.Date == "" || l.Description == "" || len(l.Currency) != 3 {
		return l, false
	}
	return l, true
}

func oneLine(s string, n int) string {
	return truncate(strings.Join(strings.Fields(s), " "), n)
}

func anyString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return ""
}

func anyInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(t))
		return n
	}
	return 0
}

const namesTool = "report_names"

var namesSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "names": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {"id": {"type": "integer"}, "name": {"type": "string"}},
        "required": ["id", "name"]
      }
    }
  },
  "required": ["names"]
}`)

// tidyNames asks the model for readable names of the merchants a bank
// export listed. Only the descriptions are sent: no amounts, no dates. It
// is a nicety, so any failure leaves the names Exhale worked out.
func (a *Assistant) tidyNames(ctx context.Context, actor app.Actor, res *importer.Result) {
	p, err := a.provider(ctx)
	if err != nil || !a.allow(actor.MemberID) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	var b strings.Builder
	for i, it := range res.Items {
		raw := it.Name
		if _, text, ok := strings.Cut(it.Remark, " · “"); ok {
			raw = strings.TrimSuffix(text, "”")
		}
		fmt.Fprintf(&b, "%d: %s\n", i, dataText(raw))
	}
	out, err := p.Complete(ctx, Request{
		System:    namesPrompt,
		Messages:  []Message{{Role: RoleUser, Text: b.String()}},
		Tools:     []Tool{{Name: namesTool, Description: "Report the readable name of each merchant.", Schema: namesSchema}},
		MaxTokens: 4096,
	})
	a.record(ctx, actor, usageRecord{kind: "import", surface: "import", provider: p.Name(), model: out.Model, rounds: 1, usage: out.Usage, failed: err != nil})
	if err != nil {
		a.log.Info("assistant: merchant names were not tidied", "err", err)
		return
	}
	var rep struct {
		Names []struct {
			ID   any    `json:"id"`
			Name string `json:"name"`
		} `json:"names"`
	}
	for _, c := range out.ToolCalls {
		if c.Name == namesTool {
			_ = json.Unmarshal(c.Arguments, &rep)
		}
	}
	changed := false
	for _, n := range rep.Names {
		name := oneLine(n.Name, 60)
		if n.ID == nil {
			continue
		}
		if i := anyInt(n.ID); i >= 0 && i < len(res.Items) && name != "" && res.Items[i].Name != name {
			res.Items[i].Name, changed = name, true
		}
	}
	if changed {
		res.Model = out.Model
		if res.Model == "" {
			res.Model = p.Name()
		}
		res.Notes = append(res.Notes, "The names were tidied by a model from the bank's descriptions; the amounts and dates were read by Exhale.")
	}
}
