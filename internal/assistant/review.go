package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/importer"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Review is a file read from a chat and waiting for the person to decide:
// the chat shows it with a button per line, and the web shows the same list
// (Settings → Data → Import) when it is too long for a chat. Each member
// has at most one; a new file replaces it. The file itself is not kept,
// only what was read from it.
type Review struct {
	ID      string          `json:"id" doc:"changes when another file replaces the review"`
	Channel string          `json:"channel" doc:"the chat it came from, e.g. telegram"`
	Result  importer.Result `json:"result"`
	// Outcomes is the dry run of importing Result.Items.
	Outcomes []app.ImportOutcome `json:"outcomes"`
	// Off are the lines the person unticked in the chat.
	Off       map[int]bool `json:"off,omitempty"`
	CreatedAt time.Time    `json:"createdAt"`
}

// ReviewTTL is how long a review waits.
const ReviewTTL = 24 * time.Hour

// ErrNoReview is returned when the member has no review waiting.
var ErrNoReview = fmt.Errorf("%w: no import is waiting for review", domain.ErrNotFound)

func reviewKey(memberID string) string { return "imr_" + memberID }

// NewReviewID returns a short id: chat buttons carry it.
func NewReviewID() string { return auth.Random(6) }

// SaveReview keeps a review for the member, replacing any other.
func (a *Assistant) SaveReview(ctx context.Context, actor app.Actor, r Review) error {
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return a.svc.Store().Q.SaveFlow(ctx, db.SaveFlowParams{
		ID: reviewKey(actor.MemberID), MemberID: actor.MemberID, State: string(b),
		ExpiresAt: store.FormatTime(r.CreatedAt.Add(ReviewTTL)), CreatedAt: store.FormatTime(r.CreatedAt),
	})
}

// LoadReview returns the member's review, if one is waiting.
func (a *Assistant) LoadReview(ctx context.Context, actor app.Actor) (Review, error) {
	row, err := a.svc.Store().Q.GetFlow(ctx, reviewKey(actor.MemberID))
	if err != nil || row.MemberID != actor.MemberID || store.ParseTime(row.ExpiresAt).Before(time.Now()) {
		return Review{}, ErrNoReview
	}
	var r Review
	if err := json.Unmarshal([]byte(row.State), &r); err != nil {
		return Review{}, errors.Join(ErrNoReview, err)
	}
	return r, nil
}

// DropReview forgets the member's review.
func (a *Assistant) DropReview(ctx context.Context, actor app.Actor) error {
	return a.svc.Store().Q.DeleteFlow(ctx, reviewKey(actor.MemberID))
}
