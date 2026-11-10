package assistant

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akamachinecode/exhale/internal/app"
)

// TestScreens: the model chooses a screen and its records; Exhale fills
// every name and amount from the database, as the member who asked, and
// tells the model each problem with its call so it can fix it.
func TestScreens(t *testing.T) {
	w := newWorld(t)
	sam := w.member("Sam")
	netflix := w.sub(w.owner, app.CreateSubscriptionInput{Name: "Netflix", Price: "13.99", FirstCharge: "2026-11-12"})
	gym := w.sub(w.owner, app.CreateSubscriptionInput{Name: "Gym", Price: "39", FirstCharge: "2026-11-20", Interval: 3, Unit: "month"})
	diary := w.sub(sam, app.CreateSubscriptionInput{Name: "Diary", Price: "5", FirstCharge: "2026-11-15", Private: true})

	// A note that tries to change what is shown has no way in: the model's
	// arguments are ids and a kind, nothing else.
	calls := func(args ...map[string]any) func(Request) (Response, error) {
		return func(Request) (Response, error) {
			var cs []ToolCall
			for i, a := range args {
				raw, _ := json.Marshal(a)
				cs = append(cs, ToolCall{ID: "c" + string(rune('a'+i)), Name: showTool, Arguments: raw})
			}
			return Response{ToolCalls: cs, Stop: StopTools}, nil
		}
	}
	w.fake.Then(
		calls(
			map[string]any{"kind": "subscriptions", "subscriptionIds": []string{gym.ID, "sub_nothere", netflix.ID, gym.ID, diary.ID}},
			map[string]any{"kind": "subscriptions", "subscriptionIds": []string{gym.ID, netflix.ID}, "names": "FREE"},
			map[string]any{"kind": "subscriptions", "subscriptionIds": []string{gym.ID, netflix.ID}},
		),
		calls(
			map[string]any{"kind": "charges", "from": "2026-11-01", "to": "2026-11-30"},
			map[string]any{"kind": "total", "period": "year", "by": "subscription"},
			map[string]any{"kind": "total"},
		),
		Say("Here they are."),
	)
	turn, err := w.a.Ask(w.ctx, w.owner, "c", "show my subscriptions")
	if err != nil {
		t.Fatal(err)
	}
	results := func(n int) []ToolResult { m := w.fake.Requests[n].Messages; return m[len(m)-1].ToolResults }
	first := results(1)[0]
	for _, want := range []string{"nothing was shown", `subscriptionIds[1]: there is no subscription "sub_nothere"`, "subscriptionIds[3]: " + gym.ID + " is already in the list", `subscriptionIds[4]: there is no subscription "` + diary.ID + `"`} {
		if !first.IsError || !strings.Contains(first.Content, want) {
			t.Errorf("first call: want %q in\n%s", want, first.Content)
		}
	}
	if second := results(1)[1]; !second.IsError || !strings.Contains(second.Content, `unknown field "names"`) {
		t.Errorf("an unknown field must be refused: %+v", second)
	}
	if third := results(1)[2]; third.IsError || !strings.Contains(third.Content, "a list of 2 subscriptions (Gym, Netflix)") {
		t.Errorf("third call: %+v", third)
	}
	if len(turn.Screens) != 3 {
		t.Fatalf("screens: %d (the fourth is over the limit)", len(turn.Screens))
	}
	if over := results(2)[2]; !over.IsError || !strings.Contains(over.Content, "3 screens are already shown") {
		t.Errorf("over the limit: %+v", over)
	}
	s := turn.Screens[0]
	if s.Kind != ScreenSubscriptions || len(s.Subscriptions) != 2 || s.Subscriptions[0].Name != "Gym" || s.Subscriptions[0].Price.Format("en") != "€39.00" {
		t.Errorf("subscriptions screen: %+v", s)
	}
	c := turn.Screens[1].Charges
	if c == nil || c.From.String() != "2026-11-01" || len(c.Charges) != 2 || c.Charges[0].SubscriptionName != "Netflix" {
		t.Errorf("charges screen: %+v", c)
	}
	tot := turn.Screens[2].Total
	if tot == nil || tot.Period != "year" || tot.By != "subscription" || tot.Total.Format("en") != "€323.88" || len(tot.Rows) != 2 || tot.Rows[0].Label != "Netflix" || tot.Rows[0].SubscriptionID != netflix.ID {
		raw, _ := json.Marshal(tot)
		t.Errorf("total screen: %s", raw)
	}
	if turn.Text != "Here they are." {
		t.Errorf("text: %q", turn.Text)
	}
}
