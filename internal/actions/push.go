package actions

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/akamachinecode/exhale/internal/domain"
)

// Web Push (ADR 0026): a notification can carry a couple of buttons that
// run without opening the app. They are the message's own buttons, picked
// by verb, and run through Handle exactly like a press in chat.

// ErrGone is returned by Channel.Send when the destination no longer exists
// (a browser that dropped its push subscription). The channel has already
// removed it; Deliver skips it instead of retrying.
var ErrGone = errors.New("destination gone")

// PushVerbs are the button verbs a push notification may offer, in order.
// Browsers show at most two.
//
// HOOK(snooze): "Remind me tomorrow" goes here as the second verb once the
// snooze use case exists in the chat buttons; the notification then shows
// it with no other change (the label comes from the message's button).
var PushVerbs = []string{vPaid}

// maxPushButtons is what browsers show (Notification.maxActions is 2).
const maxPushButtons = 2

// ButtonVerb splits a button's data into its verb and charge id.
func ButtonVerb(data string) (verb, chargeID string) {
	p := strings.Split(data, "|")
	if len(p) < 2 {
		return p[0], ""
	}
	return p[0], p[1]
}

// PushButtons returns the buttons of a message that a push notification
// offers: the first button of each verb in PushVerbs, about a charge and
// with no extra arguments (a token covers one charge and one verb).
func PushButtons(m Message) []Button {
	var out []Button
	for _, verb := range PushVerbs {
		if b, ok := pushButton(m, verb); ok && len(out) < maxPushButtons {
			out = append(out, b)
		}
	}
	return out
}

func pushButton(m Message, verb string) (Button, bool) {
	for _, row := range m.Rows {
		for _, b := range row {
			if b.URL != "" || strings.Count(b.Data, "|") != 1 {
				continue
			}
			if v, charge := ButtonVerb(b.Data); v == verb && charge != "" {
				return b, true
			}
		}
	}
	return Button{}, false
}

// PushResult is what a notification shows after its button ran.
type PushResult struct {
	Title   string `json:"title,omitempty" doc:"the charge as it is now, e.g. \"✅ Netflix paid\""`
	Message string `json:"message" doc:"what happened, e.g. \"Marked as paid ✅\""`
}

// HandlePush runs a notification button for a member, through the same use
// case as the chat button with that verb. device is the channel link the
// notification went to.
func (e *Engine) HandlePush(ctx context.Context, memberID, device, verb, chargeID string) (PushResult, error) {
	if !slices.Contains(PushVerbs, verb) || chargeID == "" {
		return PushResult{}, fmt.Errorf("%w: unknown action", domain.ErrValidation)
	}
	actor, err := e.svc.ActorForMember(ctx, memberID)
	if err != nil {
		return PushResult{}, err
	}
	rep, err := e.Handle(ctx, actor, ChatRef{Channel: "webpush", ChatID: device}, data(verb, chargeID))
	if err != nil {
		return PushResult{}, err
	}
	out := PushResult{Message: rep.Toast}
	if rep.Edit != nil {
		out.Title = rep.Edit.Title
	}
	return out, nil
}
