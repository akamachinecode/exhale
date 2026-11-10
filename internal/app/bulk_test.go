package app

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/akamachinecode/exhale/internal/domain"
)

func (f *fixture) get(t *testing.T, id string) SubscriptionView {
	t.Helper()
	d, err := f.svc.GetSubscription(context.Background(), f.owner, GetSubscriptionInput{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	return d.Subscription
}

func TestTagsAreCleanedAndSearchable(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	d := f.create(t, CreateSubscriptionInput{Name: "Figma", Price: "15", Currency: "EUR", FirstCharge: "2026-11-20", Notes: "design seat",
		Account: "alex@studio.com", ExtrasInput: ExtrasInput{Tags: []string{" work ", "Work", "client  A", ""}}})
	if got := d.Subscription.Tags; !slices.Equal(got, []string{"work", "client A"}) {
		t.Fatalf("tags = %q", got)
	}
	f.create(t, CreateSubscriptionInput{Name: "Netflix", Price: "19.99", Currency: "EUR", FirstCharge: "2026-11-20"})
	if _, err := f.svc.CreateSubscription(ctx, f.owner, CreateSubscriptionInput{Name: "X", Price: "1", Currency: "EUR", FirstCharge: "2026-11-20",
		ExtrasInput: ExtrasInput{Tags: []string{"a,b"}}}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("a comma in a tag must fail, got %v", err)
	}
	for q, want := range map[string]int{"WORK": 1, "studio": 1, "design seat": 1, "netflix": 1, "client a": 1, "nothing": 0} {
		l, _ := f.svc.ListSubscriptions(ctx, f.owner, ListSubscriptionsInput{Query: q})
		if len(l.Subscriptions) != want {
			t.Errorf("query %q: %d results, want %d", q, len(l.Subscriptions), want)
		}
	}
	l, _ := f.svc.ListSubscriptions(ctx, f.owner, ListSubscriptionsInput{Tag: "Client a"})
	if len(l.Subscriptions) != 1 || l.Subscriptions[0].Name != "Figma" {
		t.Fatalf("tag filter = %+v", l.Subscriptions)
	}
	info, _ := f.svc.GetHousehold(ctx, f.owner, struct{}{})
	if len(info.Tags) != 2 || info.Tags[0].Name != "client A" || info.Tags[1].Subscriptions != 1 {
		t.Fatalf("household tags = %+v", info.Tags)
	}
	// Update replaces them; an empty list clears them.
	if _, err := f.svc.UpdateSubscription(ctx, f.owner, UpdateSubscriptionInput{ID: d.Subscription.ID, ExtrasUpdate: ExtrasUpdate{Tags: []string{}}}); err != nil {
		t.Fatal(err)
	}
	if got := f.get(t, d.Subscription.ID).Tags; len(got) != 0 {
		t.Fatalf("tags after clearing = %q", got)
	}
}

func TestBulkUpdatePreviewsAppliesAndUndoesAsOne(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cat, err := f.svc.CreateCategory(ctx, f.owner, CreateCategoryInput{Name: "Work"})
	if err != nil {
		t.Fatal(err)
	}
	a := f.create(t, CreateSubscriptionInput{Name: "Figma", Price: "15", Currency: "EUR", FirstCharge: "2026-11-20", ExtrasInput: ExtrasInput{Tags: []string{"old"}}}).Subscription
	b := f.create(t, CreateSubscriptionInput{Name: "Notion", Price: "10", Currency: "EUR", FirstCharge: "2026-11-21", CategoryID: cat.ID}).Subscription
	in := BulkUpdateInput{IDs: []string{a.ID, b.ID, a.ID}, CategoryID: &cat.ID, AddTags: []string{"work"}, RemoveTags: []string{"OLD"}, DryRun: true}
	prev, err := f.svc.BulkUpdate(ctx, f.owner, in)
	if err != nil {
		t.Fatal(err)
	}
	if prev.Changed != 2 || len(prev.Items) != 2 || len(prev.AuditIDs) != 0 {
		t.Fatalf("preview = %+v", prev)
	}
	if ch := prev.Items[0].Changes; len(ch) != 2 || ch[0] != (FieldChange{Field: "category", To: "Work"}) || ch[1] != (FieldChange{Field: "tags", From: "old", To: "work"}) {
		t.Fatalf("changes for Figma = %+v", ch)
	}
	if ch := prev.Items[1].Changes; len(ch) != 1 || ch[0].Field != "tags" {
		t.Fatalf("Notion already has the category, only tags change: %+v", ch)
	}
	if f.get(t, a.ID).CategoryID != "" {
		t.Fatal("a dry run must not save")
	}
	in.DryRun = false
	res, err := f.svc.BulkUpdate(ctx, f.owner, in)
	if err != nil || len(res.AuditIDs) != 2 {
		t.Fatalf("apply = %+v, %v", res, err)
	}
	if got := f.get(t, a.ID); got.CategoryID != cat.ID || !slices.Equal(got.Tags, []string{"work"}) {
		t.Fatalf("after = %+v", got)
	}
	u, err := f.svc.Undo(ctx, f.owner, UndoInput{AuditIDs: res.AuditIDs})
	if err != nil || u.Undone != 2 {
		t.Fatalf("undo = %+v, %v", u, err)
	}
	if got := f.get(t, a.ID); got.CategoryID != "" || !slices.Equal(got.Tags, []string{"old"}) {
		t.Fatalf("after undo = %+v", got)
	}
	if got := f.get(t, b.ID); len(got.Tags) != 0 {
		t.Fatalf("Notion after undo = %+v", got.Tags)
	}
	if _, err := f.svc.Undo(ctx, f.owner, UndoInput{AuditIDs: res.AuditIDs}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("undoing twice must fail, got %v", err)
	}
}

func TestBulkStatusAndDeleteAreAllOrNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.create(t, CreateSubscriptionInput{Name: "Figma", Price: "15", Currency: "EUR", FirstCharge: "2026-11-20"}).Subscription
	b := f.create(t, CreateSubscriptionInput{Name: "Notion", Price: "10", Currency: "EUR", FirstCharge: "2026-11-21", Status: "paused"}).Subscription

	res, err := f.svc.BulkUpdate(ctx, f.owner, BulkUpdateInput{IDs: []string{a.ID, b.ID}, Action: "pause"})
	if err != nil || res.Changed != 1 || len(res.Items[1].Changes) != 0 {
		t.Fatalf("pause = %+v, %v", res, err)
	}
	if f.get(t, a.ID).Status != domain.StatusPaused {
		t.Fatal("Figma must be paused")
	}
	// One unknown id fails the whole change.
	if _, err := f.svc.BulkUpdate(ctx, f.owner, BulkUpdateInput{IDs: []string{a.ID, "sub_nope"}, Action: "delete"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown id = %v", err)
	}
	if f.get(t, a.ID).Name == "" {
		t.Fatal("nothing may be deleted when one id fails")
	}
	del, err := f.svc.BulkUpdate(ctx, f.owner, BulkUpdateInput{IDs: []string{a.ID, b.ID}, Action: "delete"})
	if err != nil || del.Changed != 2 {
		t.Fatalf("delete = %+v, %v", del, err)
	}
	if l, _ := f.svc.ListSubscriptions(ctx, f.owner, ListSubscriptionsInput{}); len(l.Subscriptions) != 0 {
		t.Fatalf("left after delete: %d", len(l.Subscriptions))
	}
	if _, err := f.svc.Undo(ctx, f.owner, UndoInput{AuditIDs: del.AuditIDs}); err != nil {
		t.Fatal(err)
	}
	if l, _ := f.svc.ListSubscriptions(ctx, f.owner, ListSubscriptionsInput{}); len(l.Subscriptions) != 2 {
		t.Fatalf("restored: %d", len(l.Subscriptions))
	}
	if _, err := f.svc.BulkUpdate(ctx, f.owner, BulkUpdateInput{IDs: []string{a.ID}, Action: "delete", AddTags: []string{"x"}}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("fields with delete = %v", err)
	}
	if _, err := f.svc.BulkUpdate(ctx, f.owner, BulkUpdateInput{IDs: []string{a.ID}}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("update with nothing to change = %v", err)
	}
}

func TestRenameAndDeleteATagEverywhere(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.create(t, CreateSubscriptionInput{Name: "Figma", Price: "15", Currency: "EUR", FirstCharge: "2026-11-20", ExtrasInput: ExtrasInput{Tags: []string{"Job", "design"}}}).Subscription
	b := f.create(t, CreateSubscriptionInput{Name: "Slack", Price: "8", Currency: "EUR", FirstCharge: "2026-11-20", ExtrasInput: ExtrasInput{Tags: []string{"job", "work"}}}).Subscription
	res, err := f.svc.RenameTag(ctx, f.owner, RenameTagInput{Tag: "JOB", To: "work"})
	if err != nil || res.Changed != 2 {
		t.Fatalf("rename = %+v, %v", res, err)
	}
	if got := f.get(t, a.ID).Tags; !slices.Equal(got, []string{"work", "design"}) {
		t.Fatalf("Figma tags = %q", got)
	}
	if got := f.get(t, b.ID).Tags; !slices.Equal(got, []string{"work"}) {
		t.Fatalf("Slack tags merge = %q", got)
	}
	del, err := f.svc.DeleteTag(ctx, f.owner, DeleteTagInput{Tag: "Work"})
	if err != nil || del.Changed != 2 {
		t.Fatalf("delete = %+v, %v", del, err)
	}
	if got := f.get(t, b.ID).Tags; len(got) != 0 {
		t.Fatalf("Slack tags after delete = %q", got)
	}
	if _, err := f.svc.Undo(ctx, f.owner, UndoInput{AuditIDs: del.AuditIDs}); err != nil {
		t.Fatal(err)
	}
	if got := f.get(t, a.ID).Tags; !slices.Equal(got, []string{"work", "design"}) {
		t.Fatalf("Figma tags after undo = %q", got)
	}
	if _, err := f.svc.DeleteTag(ctx, f.owner, DeleteTagInput{Tag: "missing"}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("unknown tag = %v", err)
	}
}

func TestOtherPriceShowsTheYearlySaving(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	monthly := f.create(t, CreateSubscriptionInput{Name: "Spotify", Price: "10.99", Currency: "EUR", FirstCharge: "2026-11-20",
		ExtrasInput: ExtrasInput{AltPrice: "109,00"}}).Subscription
	p := monthly.PlanSwitch
	if p == nil || p.Current.String() != "131.88 EUR" || p.Other.String() != "109.00 EUR" || p.Saving.String() != "22.88 EUR" {
		t.Fatalf("plan switch = %+v", p)
	}
	// The yearly plan is already the cheaper one: a negative saving, no finding.
	yearly := f.create(t, CreateSubscriptionInput{Name: "1Password", Price: "36", Currency: "EUR", Unit: "year", FirstCharge: "2026-11-20",
		ExtrasInput: ExtrasInput{AltPrice: "3.99", AltUnit: "month"}}).Subscription
	if yearly.PlanSwitch == nil || yearly.PlanSwitch.Saving.Minor != -1188 {
		t.Fatalf("yearly plan switch = %+v", yearly.PlanSwitch)
	}
	plain := f.create(t, CreateSubscriptionInput{Name: "Netflix", Price: "19.99", Currency: "EUR", FirstCharge: "2026-11-20"}).Subscription
	if plain.PlanSwitch != nil {
		t.Fatal("no other price, no hint: never guess")
	}
	a, err := f.svc.Audit(ctx, f.owner, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.CheaperPlans) != 1 || a.CheaperPlans[0].Subscription.ID != monthly.ID || a.CheaperPlans[0].Other.Unit != "year" {
		t.Fatalf("cheaper plans = %+v", a.CheaperPlans)
	}
	// Removing it removes the hint.
	empty := ""
	d, err := f.svc.UpdateSubscription(ctx, f.owner, UpdateSubscriptionInput{ID: monthly.ID, ExtrasUpdate: ExtrasUpdate{AltPrice: &empty}})
	if err != nil || d.Subscription.PlanSwitch != nil || d.Subscription.AltPrice != nil {
		t.Fatalf("after removing = %+v, %v", d.Subscription.AltPrice, err)
	}
	unit := "week"
	if _, err := f.svc.UpdateSubscription(ctx, f.owner, UpdateSubscriptionInput{ID: yearly.ID, ExtrasUpdate: ExtrasUpdate{AltUnit: &unit}}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("a weekly other price must fail, got %v", err)
	}
}
