package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Notify-only destinations (an email address, an ntfy topic, a webhook
// URL) live in channel_links like chat accounts, but the member types them
// in the web instead of proving them with a one-time code.

// MaxDestinations is how many destinations of one channel a member can
// have.
const MaxDestinations = 5

// AddDestination links a destination to a member. The caller validates
// externalID for its channel first. A destination belongs to one member:
// adding someone else's is refused, never moved.
func AddDestination(ctx context.Context, st *store.Store, memberID, channel, externalID string) (LinkInfo, error) {
	if owner, err := st.Q.MemberForChannel(ctx, db.MemberForChannelParams{Channel: channel, ExternalID: externalID}); err == nil && owner != memberID {
		return LinkInfo{}, fmt.Errorf("%w: someone else in this Exhale already uses that destination", domain.ErrConflict)
	}
	links, err := ListLinks(ctx, st, memberID)
	if err != nil {
		return LinkInfo{}, err
	}
	n := 0
	for _, l := range links {
		if l.Channel != channel {
			continue
		}
		if l.ExternalID == externalID {
			return l, nil // already there
		}
		n++
	}
	if n >= MaxDestinations {
		return LinkInfo{}, fmt.Errorf("%w: %d destinations of this kind is the limit; remove one first", domain.ErrValidation, MaxDestinations)
	}
	l := LinkInfo{ID: domain.NewID("lnk"), Channel: channel, ExternalID: externalID, CreatedAt: store.FormatTime(time.Now())}
	err = st.Q.LinkChannel(ctx, db.LinkChannelParams{ID: l.ID, MemberID: memberID, Channel: channel, ExternalID: externalID, CreatedAt: l.CreatedAt})
	return l, err
}

// Link returns one of a member's links.
func Link(ctx context.Context, st *store.Store, memberID, id string) (LinkInfo, error) {
	links, err := ListLinks(ctx, st, memberID)
	if err != nil {
		return LinkInfo{}, err
	}
	for _, l := range links {
		if l.ID == id {
			return l, nil
		}
	}
	return LinkInfo{}, fmt.Errorf("%w: link %s", domain.ErrNotFound, id)
}
