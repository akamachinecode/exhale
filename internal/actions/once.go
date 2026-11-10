package actions

import (
	"context"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// A member can have several destinations (Telegram, email, a webhook…).
// When one of them fails the outbox retries the whole item, so Deliver
// remembers which destinations already got it and skips them on the retry:
// a broken webhook must not turn into eight copies of the same email.
//
// The record is a row of the messages table keyed by the link and the
// outbox item, with no charge, so Sync never tries to re-edit it.

func deliveryRef(item app.OutboxItem) string { return "outbox:" + item.ID }

func (e *Engine) delivered(ctx context.Context, item app.OutboxItem, l db.ChannelLink) bool {
	if item.ID == "" {
		return false
	}
	_, err := e.svc.Store().Q.GetMessage(ctx, db.GetMessageParams{Channel: l.Channel, ChatID: l.ID, MessageID: deliveryRef(item)})
	return err == nil
}

func (e *Engine) markDelivered(ctx context.Context, item app.OutboxItem, l db.ChannelLink) {
	if item.ID == "" {
		return
	}
	_ = e.svc.Store().Q.SaveMessage(ctx, db.SaveMessageParams{
		ID: domain.NewID("msg"), Channel: l.Channel, ChatID: l.ID, MessageID: deliveryRef(item),
		MemberID: store.Ptr(item.MemberID), CreatedAt: store.FormatTime(time.Now()),
	})
}
