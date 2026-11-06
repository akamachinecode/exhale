// Package scheduler (piece ⑪) is Exhale's clock. It plans reminders, sleeps
// until the next one is due and delivers it, with retries and catch-up after
// downtime. No cron: the next wake-up comes from the outbox itself (ADR 0003).
package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/bus"
)

// Deliverer sends one outbox item (the action layer implements it).
type Deliverer interface {
	Deliver(ctx context.Context, item app.OutboxItem) error
}

// Scheduler is a supervised service.
type Scheduler struct {
	svc     *app.Service
	log     *slog.Logger
	deliver Deliverer
	// MaxSleep bounds every sleep so that clock jumps and new days are noticed.
	MaxSleep time.Duration
}

// New returns a scheduler. deliver may be nil (reminders are planned but
// not sent, e.g. when no channel is configured).
func New(svc *app.Service, log *slog.Logger, deliver Deliverer) *Scheduler {
	return &Scheduler{svc: svc, log: log, deliver: deliver, MaxSleep: time.Minute}
}

func (s *Scheduler) String() string { return "scheduler" }

// Serve runs until ctx is cancelled.
func (s *Scheduler) Serve(ctx context.Context) error {
	events, stop := s.svc.Bus().Subscribe(64)
	defer stop()
	lastPlan := time.Time{}
	for {
		now := s.svc.Now()
		if now.Sub(lastPlan) > time.Hour {
			if err := s.svc.Plan(ctx); err != nil {
				s.log.Error("planning reminders failed", "err", err)
			}
			lastPlan = now
		}
		s.Tick(ctx)
		wait := s.MaxSleep
		if next, ok := s.svc.NextOutboxDue(ctx); ok && s.deliver != nil {
			if d := next.Sub(s.svc.Now()); d < wait {
				wait = max(d, 0)
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case e := <-events:
			timer.Stop()
			if e.Type == bus.SubscriptionCreated || e.Type == bus.SubscriptionUpdated || e.Type == bus.ChargeUpdated {
				lastPlan = time.Time{} // re-plan right away
			}
		case <-timer.C:
		}
	}
}

// Tick delivers every due outbox item once.
func (s *Scheduler) Tick(ctx context.Context) {
	items, err := s.svc.DueOutbox(ctx, 50)
	if err != nil {
		s.log.Error("reading outbox failed", "err", err)
		return
	}
	for _, it := range items {
		if s.deliver == nil {
			continue
		}
		if err := s.deliver.Deliver(ctx, it); err != nil {
			s.log.Warn("delivery failed, will retry", "kind", it.Kind, "charge", it.ChargeID, "attempt", it.Attempts+1, "err", err)
			_ = s.svc.MarkFailed(ctx, it, err)
			continue
		}
		if err := s.svc.MarkDelivered(ctx, it.ID); err != nil {
			s.log.Error("marking delivery failed", "err", err)
		}
	}
}
