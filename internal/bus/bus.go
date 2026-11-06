// Package bus (piece ⑫) fans out domain events in memory: the web receives
// them over SSE, chat adapters re-edit messages already sent, and the
// scheduler re-plans reminders.
package bus

import (
	"sync"
	"time"
)

// Event types.
const (
	SubscriptionCreated = "subscription.created"
	SubscriptionUpdated = "subscription.updated"
	SubscriptionDeleted = "subscription.deleted"
	ChargeUpdated       = "charge.updated"
	PaymentMethodSaved  = "payment_method.saved"
	CategorySaved       = "category.saved"
	HouseholdUpdated    = "household.updated"
)

// Event describes something that changed.
type Event struct {
	Type           string    `json:"type"`
	HouseholdID    string    `json:"householdId"`
	SubscriptionID string    `json:"subscriptionId,omitempty"`
	ChargeID       string    `json:"chargeId,omitempty"`
	EntityID       string    `json:"entityId,omitempty"`
	ActorLabel     string    `json:"actor,omitempty"`
	AuditID        string    `json:"auditId,omitempty"`
	At             time.Time `json:"at"`
}

// Bus is a non-blocking in-memory pub/sub. A slow subscriber loses events
// (its channel is buffered); consumers must refetch on gaps, as SSE clients
// do on reconnect (ADR 0013).
type Bus struct {
	mu   sync.RWMutex
	subs map[int]chan Event
	next int
}

// New returns an empty bus.
func New() *Bus { return &Bus{subs: map[int]chan Event{}} }

// Publish delivers e to every subscriber without blocking.
func (b *Bus) Publish(e Event) {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default: // subscriber is too slow; it will resync
		}
	}
}

// Subscribe returns a channel of events and a function to stop receiving.
func (b *Bus) Subscribe(buffer int) (<-chan Event, func()) {
	ch := make(chan Event, buffer)
	b.mu.Lock()
	id := b.next
	b.next++
	b.subs[id] = ch
	b.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, id)
			b.mu.Unlock()
			close(ch)
		})
	}
}
