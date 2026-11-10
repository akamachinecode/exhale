package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/toolschema"
)

// Generated screens (ADR 0019 §4, the minimal version). Instead of listing
// records in a sentence, the model can show a screen from a fixed
// vocabulary: a list of subscriptions, a table of charges, or a total with
// its breakdown. It chooses the kind and which records, through one tool
// with a fixed schema; Exhale reads every name, date and amount from the
// database, as the member who asked. Nothing the model writes reaches the
// screen, so the injection rules of ADR 0018 hold: a note cannot change a
// figure, and the screen's buttons only open records.
//
// The arguments are validated and every problem is told to the model in
// one readable line, so it can fix the call.

// Screen kinds.
const (
	ScreenSubscriptions = "subscriptions"
	ScreenCharges       = "charges"
	ScreenTotal         = "total"
)

const (
	showTool          = "show_screen"
	maxScreens        = 3  // per answer
	maxScreenSubs     = 20 // subscriptions on one screen
	maxScreenCharges  = 50 // charges on one screen
	maxScreenDays     = 366
	defaultScreenDays = 30
)

// Screen is a view the assistant chose to show. Only the field of its kind
// is set.
type Screen struct {
	Kind          string                 `json:"kind" enum:"subscriptions,charges,total"`
	Subscriptions []app.SubscriptionView `json:"subscriptions,omitempty" doc:"kind subscriptions: in the order the model chose"`
	Charges       *app.ChargeList        `json:"charges,omitempty" doc:"kind charges: the period and its charges"`
	Total         *app.Breakdown         `json:"total,omitempty" doc:"kind total"`
	More          int                    `json:"more,omitempty" doc:"records left out because the screen is full"`
}

var showSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "kind": {"type": "string", "enum": ["subscriptions", "charges", "total"], "description": "subscriptions: a list of subscriptions with price and next charge. charges: a table of charges in a period. total: what the active subscriptions cost per month or year, with a breakdown."},
    "subscriptionIds": {"type": "array", "items": {"type": "string"}, "maxItems": 20, "description": "Which subscriptions, by id, in the order to show them. Required for subscriptions. For charges and total: only these (empty for all)."},
    "from": {"type": "string", "description": "charges: first day, YYYY-MM-DD (default today)"},
    "to": {"type": "string", "description": "charges: last day, YYYY-MM-DD (default 30 days after from)"},
    "period": {"type": "string", "enum": ["month", "year"], "description": "total: per month (default) or per year"},
    "by": {"type": "string", "enum": ["category", "payment_method", "subscription"], "description": "total: how to break it down (default category)"}
  },
  "required": ["kind"],
  "additionalProperties": false
}`)

const showDescription = "Show the person a screen instead of listing records in text: a list of subscriptions, a table of charges in a period, or a total with a breakdown. Exhale fills in every name, date and amount from the database; you only choose the kind and which records. After it is shown, answer in one short sentence and do not repeat the figures."

type showArgs struct {
	Kind            string   `json:"kind"`
	SubscriptionIDs []string `json:"subscriptionIds"`
	From            string   `json:"from"`
	To              string   `json:"to"`
	Period          string   `json:"period"`
	By              string   `json:"by"`
}

// show builds the screen the model asked for, or returns every problem
// with its arguments.
func (a *Assistant) show(ctx context.Context, st *turnState, raw json.RawMessage) (Screen, []string) {
	var in showArgs
	if len(raw) > 0 && string(raw) != "null" {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			return Screen{}, []string{"the arguments do not fit the schema (" + truncate(strings.TrimPrefix(err.Error(), "json: "), 120) + ")"}
		}
	}
	var problems []string
	if len(st.screens) >= maxScreens {
		return Screen{}, []string{fmt.Sprintf("%d screens are already shown in this answer; that is the most", maxScreens)}
	}
	list, err := a.svc.ListSubscriptions(ctx, st.agent, app.ListSubscriptionsInput{})
	if err != nil {
		return Screen{}, []string{"the subscriptions could not be read"}
	}
	visible := map[string]app.SubscriptionView{}
	for _, s := range list.Subscriptions {
		visible[s.ID] = s
	}
	var subs []app.SubscriptionView
	seen := map[string]bool{}
	if len(in.SubscriptionIDs) > maxScreenSubs {
		problems = append(problems, fmt.Sprintf("subscriptionIds: at most %d", maxScreenSubs))
	}
	for i, id := range in.SubscriptionIDs {
		s, ok := visible[id]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("subscriptionIds[%d]: there is no subscription %q this person can see; take ids from the briefing or list_subscriptions", i, truncate(id, 40)))
		case seen[id]:
			problems = append(problems, fmt.Sprintf("subscriptionIds[%d]: %s is already in the list", i, id))
		default:
			seen[id] = true
			subs = append(subs, s)
		}
	}
	screen := Screen{Kind: in.Kind}
	switch in.Kind {
	case ScreenSubscriptions:
		if len(in.SubscriptionIDs) == 0 {
			problems = append(problems, "subscriptionIds: say which subscriptions to show")
		}
		if len(problems) > 0 {
			return Screen{}, problems
		}
		screen.Subscriptions = subs
	case ScreenCharges:
		today, _ := a.today(ctx, st.agent)
		from, to := today, engine.Date{}
		if in.From != "" {
			if from, err = engine.ParseDate(in.From); err != nil {
				problems = append(problems, fmt.Sprintf("from: %q is not a date like 2026-01-31", in.From))
			}
		}
		to = from.AddDays(defaultScreenDays)
		if in.To != "" {
			if to, err = engine.ParseDate(in.To); err != nil {
				problems = append(problems, fmt.Sprintf("to: %q is not a date like 2026-01-31", in.To))
			}
		}
		if len(problems) == 0 && to.Before(from) {
			problems = append(problems, "to: the last day is before the first")
		}
		if len(problems) == 0 && from.AddDays(maxScreenDays).Before(to) {
			problems = append(problems, fmt.Sprintf("from, to: show at most %d days", maxScreenDays))
		}
		if len(problems) > 0 {
			return Screen{}, problems
		}
		charges, err := a.svc.ListCharges(ctx, st.agent, app.ListChargesInput{From: from.String(), To: to.String()})
		if err != nil {
			return Screen{}, []string{toolschema.ErrorText(err)}
		}
		kept := []app.ChargeView{}
		for _, c := range charges.Charges {
			if len(seen) > 0 && !seen[c.SubscriptionID] {
				continue
			}
			if len(kept) == maxScreenCharges {
				screen.More++
				continue
			}
			kept = append(kept, c)
		}
		charges.Charges = kept
		screen.Charges = &charges
	case ScreenTotal:
		if in.Period != "" && in.Period != "month" && in.Period != "year" {
			problems = append(problems, fmt.Sprintf("period: %q is not month or year", in.Period))
		}
		by := in.By
		if by == "" {
			by = "category"
		}
		if by != "category" && by != "payment_method" && by != "subscription" {
			problems = append(problems, fmt.Sprintf("by: %q is not category, payment_method or subscription", in.By))
		}
		if len(problems) > 0 {
			return Screen{}, problems
		}
		b, err := a.svc.SpendBreakdown(ctx, st.agent, in.Period, by, in.SubscriptionIDs)
		if err != nil {
			return Screen{}, []string{toolschema.ErrorText(err)}
		}
		screen.Total = &b
	case "":
		return Screen{}, append(problems, "kind: say which screen: subscriptions, charges or total")
	default:
		return Screen{}, append(problems, fmt.Sprintf("kind: %q is not subscriptions, charges or total", in.Kind))
	}
	return screen, nil
}

// shown tells the model what the screen holds, so its sentence can refer to
// it without repeating it.
func shown(s Screen) string {
	var b strings.Builder
	b.WriteString("SHOWN to the person: ")
	switch s.Kind {
	case ScreenSubscriptions:
		names := make([]string, len(s.Subscriptions))
		for i, v := range s.Subscriptions {
			names[i] = v.Name
		}
		fmt.Fprintf(&b, "a list of %d subscriptions (%s).", len(names), strings.Join(names, ", "))
	case ScreenCharges:
		fmt.Fprintf(&b, "a table of %d charges from %s to %s.", len(s.Charges.Charges), s.Charges.From, s.Charges.To)
		if s.More > 0 {
			fmt.Fprintf(&b, " %d more did not fit: suggest a shorter period.", s.More)
		}
	case ScreenTotal:
		fmt.Fprintf(&b, "a total per %s of %s, by %s, in %d rows.", s.Total.Period, s.Total.Total.Format("en"), s.Total.By, len(s.Total.Rows))
	}
	b.WriteString(" Do not repeat these figures; answer in one short sentence.")
	return b.String()
}
