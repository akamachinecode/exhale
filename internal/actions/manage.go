package actions

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/importer"
)

// Managing everything from chat (ADR 0019): adding a subscription step by
// step, editing it, payment methods, settings and the check-up. Buttons
// carry short routes; anything longer (a name, a price) is typed and kept
// in the chat's flow until the step is done.
const (
	actAdd      = "add"  // start the wizard
	actAddFreq  = "af"   // af|unit|n
	actAddMonth = "ad"   // ad|YYYY-MM       (calendar page)
	actAddDay   = "ap"   // ap|YYYY-MM-DD
	actAddPM    = "am"   // am|pm_id or -
	actAddCat   = "ac"   // ac|cat_id or -
	actAddTrial = "at"   // toggle "it is a free trial"
	actAddOK    = "aok"  // create it
	actAddDrop  = "ax"   // discard
	scrMethods  = "pms"  // payment methods
	scrMethod   = "pm"   // pm|pm_id
	actPMNew    = "pmn"  // ask the name of a new one
	actPMKind   = "pmk"  // pmk|kind         (kind of the new one)
	actPMEdit   = "pme"  // pme|pm_id|field  (name, last4, exp: typed)
	actPMDelete = "pmd"  // pmd|pm_id        (where do its subscriptions go?)
	actPMDelOK  = "pmdo" // pmdo|pm_id|to or -
	scrEdit     = "ed"   // ed|sub_id
	actEditAsk  = "ef"   // ef|sub_id|field  (typed)
	scrEditCat  = "ec"   // ec|sub_id
	actEditCat  = "ecs"  // ecs|sub_id|cat_id or -
	scrEditPM   = "em"   // em|sub_id
	actEditPM   = "ems"  // ems|sub_id|pm_id or -
	scrEditRem  = "er"   // er|sub_id
	actEditRem  = "ert"  // ert|sub_id|days  (toggle)
	actDelete   = "del"  // del|sub_id       (confirmation)
	actDeleteOK = "delok"
	actUndo     = "u" // u|aud_id
	scrSettings = "set"
	actSetAsk   = "sf"  // sf|field          (typed: time, payday, currency)
	actSetLang  = "sl"  // sl|en|es
	scrCheckup  = "chk" // the audit
)

const flowTTL = 30 * time.Minute

// Command starts a chat flow for a command that is not a screen (currently
// /add). ok is false for other commands.
func (e *Engine) Command(ctx context.Context, actor app.Actor, chat ChatRef, cmd string) (Reply, bool) {
	cmd, _, _ = strings.Cut(strings.TrimPrefix(cmd, "/"), "@")
	if cmd != "add" && cmd != "new" {
		return Reply{}, false
	}
	rep, err := e.startAdd(ctx, actor, chat)
	if err != nil {
		rep, _ = e.fail(err)
	}
	return rep, true
}

func (e *Engine) startAdd(ctx context.Context, actor app.Actor, chat ChatRef) (Reply, error) {
	_, locale := e.context(ctx, actor)
	if err := e.saveFlow(ctx, actor, chat, flowState{Kind: "add", Field: "name", Draft: map[string]string{}}); err != nil {
		return Reply{}, err
	}
	m := Message{Title: tr(locale, "➕ New subscription"), Lines: []string{tr(locale, "What is it called? Type the name, e.g. Netflix.")}}
	return Reply{Send: &m, Toast: tr(locale, "Type its name")}, nil
}

// manage handles the routes of this file. ok is false for other routes.
func (e *Engine) manage(ctx context.Context, actor app.Actor, chat ChatRef, p []string) (rep Reply, live string, ok bool, err error) {
	arg := func(i int) string {
		if i < len(p) {
			return p[i]
		}
		return ""
	}
	today, locale := e.context(ctx, actor)
	edit := func(m Message, toast string) (Reply, string, bool, error) {
		return Reply{Edit: &m, Toast: toast}, "", true, nil
	}
	view := func(m Message, err error, r string) (Reply, string, bool, error) {
		if err != nil {
			return Reply{}, "", true, err
		}
		return Reply{Edit: &m}, r, true, nil
	}
	ask := func(f flowState, title, hint string) (Reply, string, bool, error) {
		if err := e.saveFlow(ctx, actor, chat, f); err != nil {
			return Reply{}, "", true, err
		}
		m := Message{Title: title, Lines: []string{hint}}
		return Reply{Send: &m, Toast: tr(locale, "Type your answer")}, "", true, nil
	}
	if rep, live, ok, err := e.answers(ctx, actor, p, today, locale); ok {
		return rep, live, ok, err
	}
	switch arg(1) {
	// --- add wizard
	case actAdd:
		rep, err := e.startAdd(ctx, actor, chat)
		return rep, "", true, err
	case actAddFreq, actAddMonth, actAddDay, actAddPM, actAddCat, actAddTrial, actAddOK, actAddDrop:
		rep, err := e.addStep(ctx, actor, chat, p, today, locale)
		return rep, "", true, err

	// --- payment methods
	case scrMethods:
		m, err := e.methodsScreen(ctx, actor)
		return view(m, err, route(scrMethods))
	case scrMethod:
		m, err := e.methodScreen(ctx, actor, arg(2), today)
		return view(m, err, route(scrMethod, arg(2)))
	case actPMNew:
		return ask(flowState{Kind: "pm", Field: "new"}, tr(locale, "💳 New payment method"), tr(locale, "Type its name, e.g. Revolut or Visa ••4421."))
	case actPMKind:
		f, found := e.flow(ctx, chat)
		if !found || f.Kind != "pm" || f.Draft["name"] == "" {
			return edit(Message{Title: tr(locale, "That took too long. Start again from 💳 Payment methods."), Rows: [][]Button{homeRow(locale)}}, "")
		}
		e.dropFlow(ctx, chat)
		name, last4 := splitLast4(f.Draft["name"])
		pm, err := e.svc.SavePaymentMethod(ctx, actor, app.SavePaymentMethodInput{Name: name, Kind: arg(2), Last4: last4})
		if err != nil {
			return Reply{}, "", true, err
		}
		m, err := e.methodScreen(ctx, actor, pm.ID, today)
		rep, _, _, err := view(m, err, "")
		rep.Toast = tr(locale, "Added 💳")
		return rep, "", true, err
	case actPMEdit:
		hint := map[string]string{"name": tr(locale, "Type the new name."), "last4": tr(locale, "Type the last four digits."), "exp": tr(locale, "Type when it expires, e.g. 08/27.")}[arg(3)]
		if hint == "" {
			return Reply{Toast: tr(locale, "Unknown field.")}, "", true, nil
		}
		return ask(flowState{Kind: "pm", Field: arg(3), Ref: arg(2)}, tr(locale, "💳 Edit payment method"), hint)
	case actPMDelete:
		m, err := e.methodDeleteScreen(ctx, actor, arg(2))
		return view(m, err, "")
	case actPMDelOK:
		to := arg(3)
		if to == "-" {
			to = ""
		}
		res, err := e.svc.DeletePaymentMethod(ctx, actor, app.DeletePaymentMethodInput{ID: arg(2), MoveTo: to})
		if err != nil {
			return Reply{}, "", true, err
		}
		m, err := e.methodsScreen(ctx, actor)
		rep, _, _, err := view(m, err, route(scrMethods))
		rep.Toast = tr(locale, "Deleted; %d %s moved", res.Moved, plural(locale, res.Moved, "subscription", "subscriptions"))
		return rep, route(scrMethods), true, err

	// --- edit a subscription
	case scrEdit:
		m, err := e.editScreen(ctx, actor, arg(2), locale)
		return view(m, err, "")
	case actEditAsk:
		hints := map[string]string{
			"name":    tr(locale, "Type the new name."),
			"price":   tr(locale, "Type the new price, e.g. 12.99. It applies from the next charge."),
			"notice":  tr(locale, "How many days before a renewal must it be cancelled? Type a number; 0 for none."),
			"link":    tr(locale, "Paste the link of the page where it is cancelled or managed."),
			"account": tr(locale, "Type the login or email it lives under."),
		}
		if hints[arg(3)] == "" {
			return Reply{Toast: tr(locale, "Unknown field.")}, "", true, nil
		}
		return ask(flowState{Kind: "edit", Field: arg(3), Ref: arg(2)}, tr(locale, "✏️ Edit"), hints[arg(3)])
	case scrEditCat, scrEditPM:
		m, err := e.pickScreen(ctx, actor, arg(1), arg(2))
		return view(m, err, "")
	case actEditCat, actEditPM:
		id := arg(3)
		if id == "-" {
			id = ""
		}
		in := app.UpdateSubscriptionInput{ID: arg(2)}
		if arg(1) == actEditCat {
			in.CategoryID = &id
		} else {
			in.PaymentMethodID = &id
		}
		if _, err := e.svc.UpdateSubscription(ctx, actor, in); err != nil {
			return Reply{}, "", true, err
		}
		m, err := e.editScreen(ctx, actor, arg(2), locale)
		rep, _, _, err := view(m, err, "")
		rep.Toast = tr(locale, "Saved")
		return rep, "", true, err
	case scrEditRem:
		m, err := e.remindersScreen(ctx, actor, arg(2))
		return view(m, err, "")
	case actEditRem:
		d, err := e.svc.GetSubscription(ctx, actor, app.GetSubscriptionInput{ID: arg(2)})
		if err != nil {
			return Reply{}, "", true, err
		}
		day, _ := strconv.Atoi(arg(3))
		days := slices.Clone(d.Subscription.Reminders.DaysBefore)
		if i := slices.Index(days, day); i >= 0 {
			days = slices.Delete(days, i, i+1)
		} else {
			days = append(days, day)
		}
		slices.SortFunc(days, func(a, b int) int { return b - a })
		if days == nil {
			days = []int{}
		}
		if _, err := e.svc.UpdateSubscription(ctx, actor, app.UpdateSubscriptionInput{ID: arg(2), ReminderDays: days}); err != nil {
			return Reply{}, "", true, err
		}
		m, err := e.remindersScreen(ctx, actor, arg(2))
		return view(m, err, "")
	case actDelete:
		d, err := e.svc.GetSubscription(ctx, actor, app.GetSubscriptionInput{ID: arg(2)})
		if err != nil {
			return Reply{}, "", true, err
		}
		return edit(Message{
			Title: tr(locale, "🗑 Delete %s?", d.Subscription.Name),
			Lines: []string{tr(locale, "It disappears with its whole history. To stop paying but keep the history, cancel it instead.")},
			Rows: [][]Button{
				{{Label: tr(locale, "🗑 Delete"), Data: route(actDeleteOK, arg(2))}, {Label: tr(locale, "↩︎ Keep it"), Data: route(scrSub, arg(2))}},
			},
		}, "")
	case actDeleteOK:
		res, err := e.svc.DeleteSubscription(ctx, actor, app.DeleteSubscriptionInput{ID: arg(2)})
		if err != nil {
			return Reply{}, "", true, err
		}
		return edit(Message{Title: tr(locale, "🗑 Deleted"), Rows: [][]Button{{{Label: tr(locale, "↩︎ Undo"), Data: route(actUndo, res.AuditID)}}, homeRow(locale)}}, "Deleted")
	case actUndo:
		res, err := e.svc.Undo(ctx, actor, app.UndoInput{AuditID: arg(2)})
		if err != nil {
			return Reply{}, "", true, err
		}
		if strings.HasPrefix(res.Restored, domain.PrefixSubscription+"_") {
			m, err := e.subScreen(ctx, actor, res.Restored, locale)
			rep, _, _, err := view(m, err, route(scrSub, res.Restored))
			rep.Toast = tr(locale, "Undone ↩︎")
			return rep, route(scrSub, res.Restored), true, err
		}
		m, err := e.homeScreen(ctx, actor, today, locale)
		return view(m, err, RouteHome)
	// --- settings and check-up
	case scrSettings:
		m, err := e.settingsScreen(ctx, actor)
		return view(m, err, route(scrSettings))
	case actSetAsk:
		hints := map[string]string{
			"time":     tr(locale, "At what time should reminders arrive? Type it like 09:00."),
			"payday":   tr(locale, "Which day of the month do you get paid? Type a number from 1 to 31, or 0 for none."),
			"currency": tr(locale, "In which currency do you want the totals? Type its code, e.g. EUR."),
		}
		if hints[arg(2)] == "" {
			return Reply{Toast: tr(locale, "Unknown setting.")}, "", true, nil
		}
		return ask(flowState{Kind: "set", Field: arg(2)}, tr(locale, "⚙️ Settings"), hints[arg(2)])
	case actSetLang:
		lang := arg(2)
		if _, err := e.svc.UpdateHousehold(ctx, actor, app.UpdateHouseholdInput{Locale: &lang}); err != nil {
			return Reply{}, "", true, err
		}
		m, err := e.settingsScreen(ctx, actor)
		return view(m, err, route(scrSettings))
	case scrCheckup:
		m, err := e.checkupScreen(ctx, actor, locale)
		return view(m, err, route(scrCheckup))
	}
	return Reply{}, "", false, nil
}

// --- typed answers -----------------------------------------------------------------

func (e *Engine) flow(ctx context.Context, chat ChatRef) (flowState, bool) {
	row, err := e.svc.Store().Q.GetFlow(ctx, flowID(chat))
	if err != nil || parseTime(row.ExpiresAt).Before(time.Now()) {
		return flowState{}, false
	}
	return decodeFlow(row.State), true
}

func (e *Engine) dropFlow(ctx context.Context, chat ChatRef) {
	_ = e.svc.Store().Q.DeleteFlow(ctx, flowID(chat))
}

// flowText handles the typed answer of a flow started in this file.
func (e *Engine) flowText(ctx context.Context, actor app.Actor, chat ChatRef, f flowState, text string) (Reply, error) {
	today, locale := e.context(ctx, actor)
	send := func(m Message, err error) (Reply, error) {
		if err != nil {
			return e.fail(err)
		}
		return Reply{Send: &m}, nil
	}
	retry := func(why string) (Reply, error) {
		_ = e.saveFlow(ctx, actor, chat, f) // keep waiting for a valid answer
		m := Message{Title: "⚠️ " + why, Lines: []string{tr(locale, "Try again, or send /home to leave it.")}}
		return Reply{Send: &m}, nil
	}
	switch f.Kind {
	case "add":
		return e.addText(ctx, actor, chat, f, text, today, locale)
	case "pm":
		switch f.Field {
		case "new":
			if len(text) > 60 {
				return retry(tr(locale, "That name is too long."))
			}
			f.Draft = map[string]string{"name": text}
			if err := e.saveFlow(ctx, actor, chat, f); err != nil {
				return e.fail(err)
			}
			return Reply{Send: &Message{Title: "💳 " + text, Lines: []string{tr(locale, "What is it?")}, Rows: [][]Button{
				{{Label: tr(locale, "💳 Card"), Data: route(actPMKind, "card")}, {Label: tr(locale, "🏦 Bank account"), Data: route(actPMKind, "bank")}},
				{{Label: tr(locale, "PayPal"), Data: route(actPMKind, "paypal")}, {Label: tr(locale, "Something else"), Data: route(actPMKind, "other")}},
			}}}, nil
		default:
			info, err := e.svc.GetHousehold(ctx, actor, struct{}{})
			if err != nil {
				return e.fail(err)
			}
			i := slices.IndexFunc(info.PaymentMethods, func(pm domain.PaymentMethod) bool { return pm.ID == f.Ref })
			if i < 0 {
				return e.fail(fmt.Errorf("payment method %s: %w", f.Ref, domain.ErrNotFound))
			}
			pm := info.PaymentMethods[i]
			in := app.SavePaymentMethodInput{ID: pm.ID, Name: pm.Name, Kind: string(pm.Kind), Last4: pm.Last4, OwnerID: pm.OwnerID}
			if !pm.ExpiresOn.IsZero() {
				in.ExpiresOn = pm.ExpiresOn.String()
			}
			switch f.Field {
			case "name":
				in.Name = text
			case "last4":
				if len(text) != 4 || strings.Trim(text, "0123456789") != "" {
					return retry(tr(locale, "Four digits, e.g. 4421."))
				}
				in.Last4 = text
			case "exp":
				exp, ok := parseExpiry(text)
				if !ok {
					return retry(tr(locale, "Type it like 08/27."))
				}
				in.ExpiresOn = exp.String()
			}
			if _, err := e.svc.SavePaymentMethod(ctx, actor, in); err != nil {
				return e.fail(err)
			}
			return send(e.methodScreen(ctx, actor, pm.ID, today))
		}
	case "edit":
		in := app.UpdateSubscriptionInput{ID: f.Ref}
		switch f.Field {
		case "name":
			in.Name = &text
		case "price":
			amount, _ := importer.SplitPrice(text, "")
			in.Price = &amount
		case "notice":
			n, err := strconv.Atoi(text)
			if err != nil || n < 0 {
				return retry(tr(locale, "Type a number of days, e.g. 30."))
			}
			in.NoticeDays = &n
		case "link":
			link := text
			if link != "" && !strings.Contains(link, "://") {
				link = "https://" + link
			}
			in.CancelURL = &link
		case "account":
			in.Account = &text
		}
		if _, err := e.svc.UpdateSubscription(ctx, actor, in); err != nil {
			return e.fail(err)
		}
		return send(e.editScreen(ctx, actor, f.Ref, locale))
	case "set":
		in := app.UpdateHouseholdInput{}
		switch f.Field {
		case "time":
			in.ReminderTime = &text
		case "payday":
			n, err := strconv.Atoi(text)
			if err != nil {
				return retry(tr(locale, "Type a day of the month, e.g. 28."))
			}
			in.Payday = &n
		case "currency":
			in.Currency = &text
		}
		if _, err := e.svc.UpdateHousehold(ctx, actor, in); err != nil {
			return e.fail(err)
		}
		return send(e.settingsScreen(ctx, actor))
	case "paid":
		amount, cur := importer.SplitPrice(text, "")
		res, err := e.svc.SetChargeStatus(ctx, actor, app.SetChargeStatusInput{ChargeID: f.ChargeID, Status: string(domain.ChargePaid), ChargedAmount: amount, ChargedIn: cur})
		if err != nil {
			return e.fail(err)
		}
		m := chargeMessage("", res, today, locale)
		if res.AuditID != "" {
			m.Rows = append(m.Rows, []Button{{Label: tr(locale, "↩︎ Undo"), Data: data(vUndo, res.Charge.ID, res.AuditID)}})
		}
		if h := res.PriceChange; h != nil {
			m.Sections = append(m.Sections, Section{Heading: tr(locale, "New price?"), Lines: []string{
				tr(locale, "It took %s instead of %s (%s%%, %s a year).", h.Charged.Format(locale), h.Expected.Format(locale), h.Change, signed(h.YearlyDelta.Format(locale), h.YearlyDelta.Minor)),
			}})
			m.Rows = append([][]Button{{{Label: tr(locale, "Yes, from now on"), Data: data(vNewPrice, res.Charge.ID, h.Charged.Decimal())}, {Label: tr(locale, "Just this time"), Data: data(vBack, res.Charge.ID)}}}, m.Rows...)
		}
		return Reply{Send: &m}, nil
	}
	return Reply{}, nil
}

func signed(s string, minor int64) string {
	if minor > 0 {
		return "+" + s
	}
	return s
}

// splitLast4 reads "Visa ••4421" or "Visa 4421" as name and last digits.
func splitLast4(s string) (name, last4 string) {
	fields := strings.Fields(strings.NewReplacer("•", " ", "*", " ").Replace(s))
	if n := len(fields); n > 1 && len(fields[n-1]) == 4 && strings.Trim(fields[n-1], "0123456789") == "" {
		return strings.Join(fields[:n-1], " "), fields[n-1]
	}
	return strings.TrimSpace(s), ""
}

// parseExpiry reads "08/27", "8/2027" or "2027-08" as the last valid day.
func parseExpiry(s string) (engine.Date, bool) {
	s = strings.NewReplacer("-", "/", ".", "/", " ", "").Replace(s)
	a, b, ok := strings.Cut(s, "/")
	if !ok {
		return engine.Date{}, false
	}
	x, err1 := strconv.Atoi(a)
	y, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil {
		return engine.Date{}, false
	}
	month, year := x, y
	if x > 12 { // 2027/08
		month, year = y, x
	}
	if year < 100 {
		year += 2000
	}
	if month < 1 || month > 12 || year < 2000 || year > 2100 {
		return engine.Date{}, false
	}
	return engine.MustDate(year, time.Month(month), engine.DaysIn(year, time.Month(month))), true
}

// --- add wizard --------------------------------------------------------------------

func (e *Engine) addText(ctx context.Context, actor app.Actor, chat ChatRef, f flowState, text string, today engine.Date, locale string) (Reply, error) {
	switch f.Field {
	case "name":
		if text == "" || len(text) > 120 {
			_ = e.saveFlow(ctx, actor, chat, f)
			return Reply{Send: &Message{Title: tr(locale, "⚠️ Give it a short name.")}}, nil
		}
		f.Draft["name"], f.Field = text, "price"
		if err := e.saveFlow(ctx, actor, chat, f); err != nil {
			return e.fail(err)
		}
		return Reply{Send: &Message{Title: "➕ " + text, Lines: []string{tr(locale, "How much is each charge? Type it like 12.99, or 12.99 USD for another currency.")}}}, nil
	case "price":
		info, err := e.svc.GetHousehold(ctx, actor, struct{}{})
		if err != nil {
			return e.fail(err)
		}
		amount, cur := importer.SplitPrice(text, info.Household.Currency)
		if _, err := e.svc.CreateSubscription(ctx, actor, app.CreateSubscriptionInput{Name: f.Draft["name"], Price: amount, Currency: cur, FirstCharge: today.String(), DryRun: true}); err != nil {
			_ = e.saveFlow(ctx, actor, chat, f)
			return Reply{Send: &Message{Title: tr(locale, "⚠️ I didn't get that price."), Lines: []string{tr(locale, "Type it like 12.99, or 12.99 USD.")}}}, nil
		}
		f.Draft["price"], f.Draft["currency"], f.Field = amount, cur, ""
		if err := e.saveFlow(ctx, actor, chat, f); err != nil {
			return e.fail(err)
		}
		return Reply{Send: &Message{Title: "➕ " + f.Draft["name"], Lines: []string{tr(locale, "How often is it charged?")}, Rows: [][]Button{
			{{Label: tr(locale, "Every month"), Data: route(actAddFreq, "month", "1")}, {Label: tr(locale, "Every year"), Data: route(actAddFreq, "year", "1")}},
			{{Label: tr(locale, "Every week"), Data: route(actAddFreq, "week", "1")}, {Label: tr(locale, "Every 3 months"), Data: route(actAddFreq, "month", "3")}},
			{{Label: tr(locale, "Every 6 months"), Data: route(actAddFreq, "month", "6")}, {Label: tr(locale, "Every 2 years"), Data: route(actAddFreq, "year", "2")}},
			{{Label: tr(locale, "✖ Discard"), Data: route(actAddDrop)}},
		}}}, nil
	case "date":
		d := importer.AnyDate(text)
		if d == "" {
			_ = e.saveFlow(ctx, actor, chat, f)
			return Reply{Send: &Message{Title: tr(locale, "⚠️ I didn't get that date."), Lines: []string{tr(locale, "Type it like 2026-12-24 or 24/12/2026, or pick a day above.")}}}, nil
		}
		m, err := e.addAfterDate(ctx, actor, chat, f, d)
		if err != nil {
			return e.fail(err)
		}
		return Reply{Send: &m}, nil
	}
	return Reply{}, nil
}

// addStep runs a wizard button.
func (e *Engine) addStep(ctx context.Context, actor app.Actor, chat ChatRef, p []string, today engine.Date, locale string) (Reply, error) {
	f, ok := e.flow(ctx, chat)
	if !ok || f.Kind != "add" {
		return Reply{Edit: &Message{Title: tr(locale, "That took too long. Send /add to start again.")}}, nil
	}
	arg := func(i int) string {
		if i < len(p) {
			return p[i]
		}
		return ""
	}
	save := func() error { return e.saveFlow(ctx, actor, chat, f) }
	pickDate := func(month engine.Date) Message {
		rows := monthGrid(month, engine.Date{}, today, locale, func(m string) string { return route(actAddMonth, m) }, func(d string) string { return route(actAddDay, d) })
		return Message{Title: "➕ " + f.Draft["name"], Lines: []string{tr(locale, "When is the next charge? Pick the day, or type a date.")}, Rows: append(rows, []Button{{Label: tr(locale, "✖ Discard"), Data: route(actAddDrop)}})}
	}
	switch arg(1) {
	case actAddFreq:
		f.Draft["unit"], f.Draft["interval"], f.Field = arg(2), arg(3), "date"
		if err := save(); err != nil {
			return e.fail(err)
		}
		m := pickDate(today)
		return Reply{Edit: &m}, nil
	case actAddMonth:
		month, err := engine.ParseDate(arg(2) + "-01")
		if err != nil {
			month = today
		}
		m := pickDate(month)
		return Reply{Edit: &m}, nil
	case actAddDay:
		m, err := e.addAfterDate(ctx, actor, chat, f, arg(2))
		if err != nil {
			return e.fail(err)
		}
		return Reply{Edit: &m}, nil
	case actAddPM:
		if f.Draft["pm"] = arg(2); arg(2) == "-" {
			f.Draft["pm"] = ""
		}
		f.Draft["pmDone"] = "1"
		if err := save(); err != nil {
			return e.fail(err)
		}
		m, err := e.addNext(ctx, actor, f, locale)
		if err != nil {
			return e.fail(err)
		}
		return Reply{Edit: &m}, nil
	case actAddCat:
		if f.Draft["cat"] = arg(2); arg(2) == "-" {
			f.Draft["cat"] = ""
		}
		f.Draft["catDone"] = "1"
		if err := save(); err != nil {
			return e.fail(err)
		}
		m, err := e.addNext(ctx, actor, f, locale)
		if err != nil {
			return e.fail(err)
		}
		return Reply{Edit: &m}, nil
	case actAddTrial:
		if f.Draft["trial"] == "" {
			f.Draft["trial"] = "1"
		} else {
			f.Draft["trial"] = ""
		}
		if err := save(); err != nil {
			return e.fail(err)
		}
		m, err := e.addNext(ctx, actor, f, locale)
		if err != nil {
			return e.fail(err)
		}
		return Reply{Edit: &m}, nil
	case actAddOK:
		d, err := e.svc.CreateSubscription(ctx, actor, addInput(f, false))
		if err != nil {
			return e.fail(err)
		}
		e.dropFlow(ctx, chat)
		m, err := e.subScreen(ctx, actor, d.Subscription.ID, locale)
		if err != nil {
			return e.fail(err)
		}
		m.Title = tr(locale, "✅ %s added", m.Title)
		return Reply{Edit: &m, Toast: tr(locale, "Added ✅")}, nil
	case actAddDrop:
		e.dropFlow(ctx, chat)
		return Reply{Edit: &Message{Title: tr(locale, "✖ Discarded"), Rows: [][]Button{homeRow(locale)}}}, nil
	}
	return Reply{Toast: tr(locale, "Unknown step.")}, nil
}

func addInput(f flowState, dry bool) app.CreateSubscriptionInput {
	n, _ := strconv.Atoi(f.Draft["interval"])
	in := app.CreateSubscriptionInput{Name: f.Draft["name"], Price: f.Draft["price"], Currency: f.Draft["currency"], Interval: n, Unit: f.Draft["unit"],
		FirstCharge: f.Draft["date"], PaymentMethodID: f.Draft["pm"], CategoryID: f.Draft["cat"], DryRun: dry}
	if f.Draft["trial"] != "" {
		in.TrialEndsOn = f.Draft["date"]
	}
	return in
}

func (e *Engine) addAfterDate(ctx context.Context, actor app.Actor, chat ChatRef, f flowState, date string) (Message, error) {
	if _, err := engine.ParseDate(date); err != nil {
		return Message{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	f.Draft["date"], f.Field = date, ""
	if err := e.saveFlow(ctx, actor, chat, f); err != nil {
		return Message{}, err
	}
	_, locale := e.context(ctx, actor)
	return e.addNext(ctx, actor, f, locale)
}

// addNext asks the next optional question, or shows the summary to confirm.
func (e *Engine) addNext(ctx context.Context, actor app.Actor, f flowState, locale string) (Message, error) {
	info, err := e.svc.GetHousehold(ctx, actor, struct{}{})
	if err != nil {
		return Message{}, err
	}
	title := "➕ " + f.Draft["name"]
	if f.Draft["pmDone"] == "" && len(info.PaymentMethods) > 0 {
		m := Message{Title: title, Lines: []string{tr(locale, "Which payment method pays it?")}}
		for _, pm := range info.PaymentMethods {
			m.Rows = append(m.Rows, []Button{{Label: pm.Label(), Data: route(actAddPM, pm.ID)}})
		}
		m.Rows = append(m.Rows, []Button{{Label: tr(locale, "Skip"), Data: route(actAddPM, "-")}, {Label: tr(locale, "✖ Discard"), Data: route(actAddDrop)}})
		return m, nil
	}
	if f.Draft["catDone"] == "" && len(info.Categories) > 0 {
		m := Message{Title: title, Lines: []string{tr(locale, "Which category?")}}
		row := []Button{}
		for _, c := range info.Categories {
			row = append(row, Button{Label: c.Name, Data: route(actAddCat, c.ID)})
			if len(row) == 2 {
				m.Rows = append(m.Rows, row)
				row = []Button{}
			}
		}
		if len(row) > 0 {
			m.Rows = append(m.Rows, row)
		}
		m.Rows = append(m.Rows, []Button{{Label: tr(locale, "Skip"), Data: route(actAddCat, "-")}, {Label: tr(locale, "✖ Discard"), Data: route(actAddDrop)}})
		return m, nil
	}
	d, err := e.svc.CreateSubscription(ctx, actor, addInput(f, true))
	if err != nil {
		return Message{}, err
	}
	s := d.Subscription
	m := Message{Title: title, Lines: []string{fmt.Sprintf("💶 %s · %s", s.Price.Format(locale), cadenceText(s.Cadence, locale))}}
	if f.Draft["trial"] != "" {
		m.Lines = append(m.Lines, tr(locale, "⏳ Free trial until %s, then it charges", longDate(s.NextCharge, locale)))
	} else {
		m.Lines = append(m.Lines, tr(locale, "📅 Next charge %s", longDate(s.NextCharge, locale)))
	}
	if s.PaymentMethod != "" {
		m.Lines = append(m.Lines, "💳 "+s.PaymentMethod)
	}
	if s.CategoryName != "" {
		m.Lines = append(m.Lines, "🏷 "+s.CategoryName)
	}
	dates := []string{}
	for i, c := range d.Upcoming {
		if i == 3 {
			break
		}
		dates = append(dates, shortDate(c.DueDate, locale))
	}
	if len(dates) > 0 {
		m.Sections = append(m.Sections, Section{Heading: tr(locale, "Then"), Lines: []string{strings.Join(dates, " · ")}})
	}
	trial := tr(locale, "⏳ It's a free trial")
	if f.Draft["trial"] != "" {
		trial = tr(locale, "⏳ Not a free trial")
	}
	m.Rows = [][]Button{
		{{Label: tr(locale, "✅ Add it"), Data: route(actAddOK)}},
		{{Label: trial, Data: route(actAddTrial)}, {Label: tr(locale, "✖ Discard"), Data: route(actAddDrop)}},
	}
	return m, nil
}

// --- payment methods -----------------------------------------------------------------

func (e *Engine) methodUse(ctx context.Context, actor app.Actor) (map[string][]app.SubscriptionView, error) {
	list, err := e.svc.ListSubscriptions(ctx, actor, app.ListSubscriptionsInput{Status: string(domain.StatusActive)})
	if err != nil {
		return nil, err
	}
	use := map[string][]app.SubscriptionView{}
	for _, s := range list.Subscriptions {
		if s.PaymentMethodID != "" {
			use[s.PaymentMethodID] = append(use[s.PaymentMethodID], s)
		}
	}
	return use, nil
}

func (e *Engine) methodsScreen(ctx context.Context, actor app.Actor) (Message, error) {
	_, locale := e.context(ctx, actor)
	info, err := e.svc.GetHousehold(ctx, actor, struct{}{})
	if err != nil {
		return Message{}, err
	}
	use, err := e.methodUse(ctx, actor)
	if err != nil {
		return Message{}, err
	}
	m := Message{Title: tr(locale, "💳 Payment methods (%d)", len(info.PaymentMethods))}
	if len(info.PaymentMethods) == 0 {
		m.Lines = []string{tr(locale, "None yet. Add the cards and accounts that pay your subscriptions.")}
	} else {
		m.Lines = []string{tr(locale, "Tap one to edit it or move its subscriptions.")}
	}
	for _, pm := range info.PaymentMethods {
		label := fmt.Sprintf("%s · %d", pm.Label(), len(use[pm.ID]))
		if !pm.ExpiresOn.IsZero() && info.Today.DaysUntil(pm.ExpiresOn) <= 45 {
			label = "⚠️ " + label
		}
		m.Rows = append(m.Rows, []Button{{Label: label, Data: route(scrMethod, pm.ID)}})
	}
	m.Rows = append(m.Rows, []Button{{Label: tr(locale, "➕ Add a payment method"), Data: route(actPMNew)}}, homeRow(locale))
	return m, nil
}

func (e *Engine) method(ctx context.Context, actor app.Actor, id string) (domain.PaymentMethod, []domain.PaymentMethod, error) {
	info, err := e.svc.GetHousehold(ctx, actor, struct{}{})
	if err != nil {
		return domain.PaymentMethod{}, nil, err
	}
	i := slices.IndexFunc(info.PaymentMethods, func(pm domain.PaymentMethod) bool { return pm.ID == id })
	if i < 0 {
		return domain.PaymentMethod{}, nil, fmt.Errorf("payment method %s: %w", id, domain.ErrNotFound)
	}
	return info.PaymentMethods[i], slices.Delete(slices.Clone(info.PaymentMethods), i, i+1), nil
}

func (e *Engine) methodScreen(ctx context.Context, actor app.Actor, id string, today engine.Date) (Message, error) {
	_, locale := e.context(ctx, actor)
	pm, others, err := e.method(ctx, actor, id)
	if err != nil {
		return Message{}, err
	}
	use, err := e.methodUse(ctx, actor)
	if err != nil {
		return Message{}, err
	}
	kind := map[domain.PaymentMethodKind]string{domain.KindCard: tr(locale, "Card"), domain.KindBank: tr(locale, "Bank account"), domain.KindPayPal: "PayPal", domain.KindOther: tr(locale, "Other")}[pm.Kind]
	m := Message{Title: "💳 " + pm.Label(), Lines: []string{kind}}
	if !pm.ExpiresOn.IsZero() {
		line := tr(locale, "Expires %s", pm.ExpiresOn.At(12, 0, time.UTC).Format("01/06"))
		if left := today.DaysUntil(pm.ExpiresOn); left <= 45 && left >= 0 {
			line = tr(locale, "⚠️ Expires in %d days (%s)", left, pm.ExpiresOn.At(12, 0, time.UTC).Format("01/06"))
		} else if left < 0 {
			line = tr(locale, "⚠️ Expired %s", pm.ExpiresOn.At(12, 0, time.UTC).Format("01/06"))
		}
		m.Lines = append(m.Lines, line)
	}
	if subs := use[pm.ID]; len(subs) > 0 {
		s := Section{Heading: tr(locale, "Pays %d %s", len(subs), plural(locale, len(subs), "subscription", "subscriptions"))}
		for i, sub := range subs {
			if i == maxListed {
				break
			}
			s.Lines = append(s.Lines, sub.Name)
		}
		m.Sections = append(m.Sections, s)
	} else {
		m.Lines = append(m.Lines, tr(locale, "No subscription uses it."))
	}
	m.Rows = [][]Button{
		{{Label: tr(locale, "✏️ Name"), Data: route(actPMEdit, id, "name")}, {Label: tr(locale, "🔢 Last 4 digits"), Data: route(actPMEdit, id, "last4")}},
		{{Label: tr(locale, "📅 Expiry"), Data: route(actPMEdit, id, "exp")}, {Label: tr(locale, "🗑 Delete"), Data: route(actPMDelete, id)}},
		{{Label: tr(locale, "‹ Payment methods"), Data: route(scrMethods)}, {Label: tr(locale, "🏠 Home"), Data: RouteHome}},
	}
	if row := methodMoveRow(pm, len(use[pm.ID]), len(others), locale); row != nil {
		m.Rows = append([][]Button{row}, m.Rows...)
	}
	return m, nil
}

func (e *Engine) methodDeleteScreen(ctx context.Context, actor app.Actor, id string) (Message, error) {
	_, locale := e.context(ctx, actor)
	pm, others, err := e.method(ctx, actor, id)
	if err != nil {
		return Message{}, err
	}
	use, err := e.methodUse(ctx, actor)
	if err != nil {
		return Message{}, err
	}
	n := len(use[id])
	m := Message{Title: tr(locale, "🗑 Delete %s?", pm.Label())}
	if n == 0 {
		m.Rows = [][]Button{{{Label: tr(locale, "🗑 Delete"), Data: route(actPMDelOK, id, "-")}, {Label: tr(locale, "↩︎ Keep it"), Data: route(scrMethod, id)}}}
		return m, nil
	}
	m.Lines = []string{tr(locale, "It pays %d %s. Which payment method takes over?", n, plural(locale, n, "subscription", "subscriptions"))}
	for _, o := range others {
		m.Rows = append(m.Rows, []Button{{Label: "→ " + o.Label(), Data: route(actPMDelOK, id, o.ID)}})
	}
	m.Rows = append(m.Rows, []Button{{Label: tr(locale, "None: leave them without one"), Data: route(actPMDelOK, id, "-")}}, []Button{{Label: tr(locale, "↩︎ Keep it"), Data: route(scrMethod, id)}})
	return m, nil
}

// --- edit a subscription -------------------------------------------------------------

func (e *Engine) editScreen(ctx context.Context, actor app.Actor, id, locale string) (Message, error) {
	d, err := e.svc.GetSubscription(ctx, actor, app.GetSubscriptionInput{ID: id})
	if err != nil {
		return Message{}, err
	}
	s := d.Subscription
	or := func(v, none string) string {
		if v == "" {
			return none
		}
		return v
	}
	none := tr(locale, "none")
	notice := none
	if s.NoticeDays > 0 {
		notice = tr(locale, "%d days", s.NoticeDays)
	}
	m := Message{Title: "✏️ " + s.Name, Sections: []Section{{Table: [][]string{
		{tr(locale, "Price"), s.Price.Format(locale) + " " + cadenceText(s.Cadence, locale)},
		{tr(locale, "Category"), or(s.CategoryName, none)},
		{tr(locale, "Paid with"), or(s.PaymentMethod, none)},
		{tr(locale, "Reminders"), reminderText(s.Reminders.DaysBefore, locale)},
		{tr(locale, "Notice"), notice},
		{tr(locale, "Account"), or(s.Account, none)},
	}}}}
	m.Rows = [][]Button{
		{{Label: tr(locale, "✏️ Name"), Data: route(actEditAsk, id, "name")}, {Label: tr(locale, "💶 Price"), Data: route(actEditAsk, id, "price")}},
		{{Label: tr(locale, "🏷 Category"), Data: route(scrEditCat, id)}, {Label: tr(locale, "💳 Payment method"), Data: route(scrEditPM, id)}},
		{{Label: tr(locale, "🔔 Reminders"), Data: route(scrEditRem, id)}, {Label: tr(locale, "⏰ Notice period"), Data: route(actEditAsk, id, "notice")}},
		{{Label: tr(locale, "🔗 Cancel link"), Data: route(actEditAsk, id, "link")}, {Label: tr(locale, "🔑 Account"), Data: route(actEditAsk, id, "account")}},
		{{Label: tr(locale, "🗑 Delete"), Data: route(actDelete, id)}},
		{{Label: "‹ " + truncate(s.Name, 20), Data: route(scrSub, id)}, {Label: tr(locale, "🏠 Home"), Data: RouteHome}},
	}
	return m, nil
}

func reminderText(days []int, locale string) string {
	if len(days) == 0 {
		return tr(locale, "off")
	}
	parts := make([]string, len(days))
	for i, d := range days {
		switch d {
		case 0:
			parts[i] = tr(locale, "same day")
		case 1:
			parts[i] = tr(locale, "1 day before")
		default:
			parts[i] = tr(locale, "%d days before", d)
		}
	}
	return strings.Join(parts, ", ")
}

func (e *Engine) remindersScreen(ctx context.Context, actor app.Actor, id string) (Message, error) {
	_, locale := e.context(ctx, actor)
	d, err := e.svc.GetSubscription(ctx, actor, app.GetSubscriptionInput{ID: id})
	if err != nil {
		return Message{}, err
	}
	days := d.Subscription.Reminders.DaysBefore
	m := Message{Title: tr(locale, "🔔 Reminders for %s", d.Subscription.Name), Lines: []string{tr(locale, "Now: %s. Tap to switch each one on or off.", reminderText(days, locale))}}
	row := []Button{}
	for _, opt := range []int{0, 1, 3, 7, 14, 30} {
		label := reminderText([]int{opt}, locale)
		if slices.Contains(days, opt) {
			label = "✅ " + label
		}
		row = append(row, Button{Label: label, Data: route(actEditRem, id, strconv.Itoa(opt))})
		if len(row) == 2 {
			m.Rows = append(m.Rows, row)
			row = []Button{}
		}
	}
	m.Rows = append(m.Rows, []Button{{Label: tr(locale, "‹ Back"), Data: route(scrEdit, id)}})
	return m, nil
}

// pickScreen lists categories or payment methods to choose for a subscription.
func (e *Engine) pickScreen(ctx context.Context, actor app.Actor, what, id string) (Message, error) {
	_, locale := e.context(ctx, actor)
	info, err := e.svc.GetHousehold(ctx, actor, struct{}{})
	if err != nil {
		return Message{}, err
	}
	d, err := e.svc.GetSubscription(ctx, actor, app.GetSubscriptionInput{ID: id})
	if err != nil {
		return Message{}, err
	}
	m := Message{Title: tr(locale, "🏷 Category for %s", d.Subscription.Name)}
	set := actEditCat
	type opt struct{ id, label string }
	opts := []opt{}
	current := d.Subscription.CategoryID
	if what == scrEditPM {
		m.Title, set, current = tr(locale, "💳 Payment method for %s", d.Subscription.Name), actEditPM, d.Subscription.PaymentMethodID
		for _, pm := range info.PaymentMethods {
			opts = append(opts, opt{pm.ID, pm.Label()})
		}
	} else {
		for _, c := range info.Categories {
			opts = append(opts, opt{c.ID, c.Name})
		}
	}
	row := []Button{}
	for _, o := range opts {
		label := o.label
		if o.id == current {
			label = "● " + label
		}
		row = append(row, Button{Label: label, Data: route(set, id, o.id)})
		if len(row) == 2 {
			m.Rows = append(m.Rows, row)
			row = []Button{}
		}
	}
	if len(row) > 0 {
		m.Rows = append(m.Rows, row)
	}
	m.Rows = append(m.Rows, []Button{{Label: tr(locale, "None"), Data: route(set, id, "-")}, {Label: tr(locale, "‹ Back"), Data: route(scrEdit, id)}})
	return m, nil
}

// --- settings and check-up -----------------------------------------------------------

func (e *Engine) settingsScreen(ctx context.Context, actor app.Actor) (Message, error) {
	_, locale := e.context(ctx, actor)
	info, err := e.svc.GetHousehold(ctx, actor, struct{}{})
	if err != nil {
		return Message{}, err
	}
	h := info.Household
	payday := tr(locale, "not set")
	if h.Payday > 0 {
		payday = tr(locale, "day %d", h.Payday)
	}
	lang := map[string]string{"en": "English", "es": "Español"}[h.Locale]
	m := Message{Title: tr(locale, "⚙️ Settings"), Sections: []Section{{Table: [][]string{
		{tr(locale, "Reminders at"), fmt.Sprintf("%02d:%02d", h.ReminderHour, h.ReminderMinute)},
		{tr(locale, "Time zone"), h.Timezone},
		{tr(locale, "Totals in"), h.Currency},
		{tr(locale, "Payday"), payday},
		{tr(locale, "Language"), lang},
		{tr(locale, "Ask again"), repeatText(h.ConfirmTimes, h.ConfirmEveryDays, locale)},
	}}}, Lines: []string{tr(locale, "They apply to everyone you share Exhale with.")}}
	other, otherLabel := "es", "Español"
	if h.Locale == "es" {
		other, otherLabel = "en", "English"
	}
	m.Rows = [][]Button{
		{{Label: tr(locale, "🕘 Reminder time"), Data: route(actSetAsk, "time")}, {Label: tr(locale, "💰 Payday"), Data: route(actSetAsk, "payday")}},
		{{Label: tr(locale, "💱 Currency"), Data: route(actSetAsk, "currency")}, {Label: "🌐 " + otherLabel, Data: route(actSetLang, other)}},
		{{Label: tr(locale, "💳 Payment methods"), Data: route(scrMethods)}, {Label: tr(locale, "🔁 Ask again"), Data: route(scrRepeat)}},
		homeRow(locale),
	}
	return m, nil
}

func (e *Engine) checkupScreen(ctx context.Context, actor app.Actor, locale string) (Message, error) {
	a, err := e.svc.Audit(ctx, actor, struct{}{})
	if err != nil {
		return Message{}, err
	}
	m := Message{Title: tr(locale, "🩺 Check-up")}
	if a.Findings == 0 {
		m.Lines = []string{tr(locale, "Nothing to look at. Breathe out.")}
		m.Rows = [][]Button{homeRow(locale)}
		return m, nil
	}
	section := func(heading string, lines []string) {
		if len(lines) > 0 {
			m.Sections = append(m.Sections, Section{Heading: heading, Lines: lines})
		}
	}
	var lines []string
	for _, c := range a.TrialsEnding {
		lines = append(lines, tr(locale, "%s: %s, then %s", c.SubscriptionName, shortDate(c.DueDate, locale), c.Amount.Format(locale)))
		m.Rows = append(m.Rows, []Button{{Label: "⏳ " + c.SubscriptionName, Data: route(scrCharge, c.ID)}})
	}
	section(tr(locale, "Free trials about to charge"), lines)
	lines = nil
	for _, c := range a.CancelDeadlines {
		lines = append(lines, tr(locale, "%s: cancel by %s", c.SubscriptionName, shortDate(c.CancelBy, locale)))
		m.Rows = append(m.Rows, []Button{{Label: "⏰ " + c.SubscriptionName, Data: route(scrCharge, c.ID)}})
	}
	section(tr(locale, "Last days to cancel"), lines)
	lines = nil
	for _, c := range a.Expiring {
		lines = append(lines, fmt.Sprintf("%s: %s", c.SubscriptionName, shortDate(c.DueDate, locale)))
	}
	section(tr(locale, "About to expire"), lines)
	lines = nil
	for _, c := range a.BigCharges {
		lines = append(lines, tr(locale, "%s: %s on %s", c.SubscriptionName, c.Amount.Format(locale), shortDate(c.DueDate, locale)))
	}
	section(tr(locale, "Big charges this month"), lines)
	lines = nil
	for _, c := range a.CardsExpiring {
		names := make([]string, len(c.Subscriptions))
		for i, s := range c.Subscriptions {
			names[i] = s.Name
		}
		line := tr(locale, "%s expires %s", c.PaymentMethod.Label(), shortDate(c.PaymentMethod.ExpiresOn, locale))
		if len(names) > 0 {
			line += ": " + strings.Join(names, ", ")
		}
		lines = append(lines, line)
		m.Rows = append(m.Rows, []Button{{Label: "💳 " + c.PaymentMethod.Label(), Data: route(scrMethod, c.PaymentMethod.ID)}})
	}
	section(tr(locale, "Cards about to expire"), lines)
	lines = nil
	for _, p := range a.PriceIncreases {
		lines = append(lines, tr(locale, "%s: %s → %s (%s%%, +%s a year)", p.Subscription.Name, p.From.Format(locale), p.To.Format(locale), p.Change, p.YearlyDelta.Format(locale)))
	}
	section(tr(locale, "Prices that went up"), lines)
	lines = nil
	for _, g := range a.Duplicates {
		names := make([]string, len(g.Subscriptions))
		for i, s := range g.Subscriptions {
			names[i] = s.Name
			if s.Payer != "" {
				names[i] += " (" + s.Payer + ")"
			}
		}
		lines = append(lines, strings.Join(names, tr(locale, " and ")))
	}
	section(tr(locale, "Paid twice?"), lines)
	lines = nil
	for _, u := range a.NotUsed {
		lines = append(lines, tr(locale, "%s: %s a year", u.Subscription.Name, u.Yearly.Format(locale)))
		m.Rows = append(m.Rows, []Button{{Label: "🤔 " + u.Subscription.Name, Data: route(scrSub, u.Subscription.ID)}})
	}
	section(tr(locale, "You said you don't use"), lines)
	if n := len(a.Unconfirmed); n > 0 {
		m.Rows = append(m.Rows, []Button{{Label: tr(locale, "⚠️ %d %s to confirm", n, plural(locale, n, "charge", "charges")), Data: route(scrAttn)}})
	}
	if len(m.Rows) > maxListed {
		m.Rows = m.Rows[:maxListed]
	}
	m.Rows = append(m.Rows, homeRow(locale))
	return m, nil
}
