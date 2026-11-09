package domain

import (
	"testing"
	"time"
)

func TestChargeTransitionsAlwaysUndoable(t *testing.T) {
	all := []ChargeStatus{ChargePending, ChargePaid, ChargeFailed, ChargeSkipped}
	for _, from := range all {
		for _, to := range all {
			if CanTransition(from, to) && to != ChargePending && !CanTransition(to, ChargePending) {
				t.Errorf("%s -> %s cannot be undone", from, to)
			}
		}
	}
	c := Charge{Status: ChargePending}
	if err := c.Transition(ChargePaid, time.Now()); err != nil || c.ResolvedAt == nil {
		t.Fatalf("pending -> paid: %v", err)
	}
	if err := c.Transition(ChargeSkipped, time.Now()); err == nil {
		t.Fatal("paid -> skipped must go through pending")
	}
}

func TestDefaultScopes(t *testing.T) {
	if DefaultScope(FieldDate) != ScopeThisCharge || DefaultScope(FieldAmount) != ScopeThisAndAfter || DefaultScope(FieldPaymentMethod) != ScopeThisAndAfter {
		t.Fatal("unexpected default scopes")
	}
}

func TestIDs(t *testing.T) {
	a, b := NewID(PrefixSubscription), NewID(PrefixSubscription)
	if a == b || !HasPrefix(a, PrefixSubscription) || len(a) != len("sub_")+16 {
		t.Fatalf("ids %q %q", a, b)
	}
}
