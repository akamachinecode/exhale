package actions

import (
	"strings"

	"github.com/akamachinecode/exhale/internal/i18n"
)

// Texts a channel adapter says outside any screen: linking a chat, joining
// from an invite and the list of commands. They live here so every channel
// says the same thing, in the member's language.

// Language picks the language of a message for someone Exhale does not know
// yet (an unlinked chat) from the code the chat platform reports, e.g.
// "es-ES": a registered language, else English.
func Language(code string) string { return i18n.Match(code) }

// HelpText lists the commands.
func HelpText(locale string) string {
	var b strings.Builder
	b.WriteString(tr(locale, "Commands:"))
	for _, c := range Commands {
		b.WriteString("\n/" + c.Command + " — " + tr(locale, c.Description))
	}
	return b.String()
}

// CommandDescription returns the description of a command in a language.
func CommandDescription(locale, description string) string { return tr(locale, description) }

// LinkedText confirms that a chat was linked.
func LinkedText(locale string) string {
	return tr(locale, "✅ Linked. Reminders arrive here, and the message below is Exhale itself: tap around.")
}

// CodeExpiredText explains that a link code no longer works.
func CodeExpiredText(locale string) string {
	return tr(locale, "That code is not valid anymore: codes work once and for 10 minutes. In Exhale, press “Link Telegram” again and scan the new QR code (or send the new /start message).")
}

// PrivateBotText is what an unlinked chat hears.
func PrivateBotText(locale string) string {
	return tr(locale, "Hi! This is a private Exhale bot. To link this chat, open Exhale, press “Link Telegram” and scan the QR code.")
}

// AlreadyLinkedText says the chat already belongs to a member.
func AlreadyLinkedText(locale string) string {
	return tr(locale, "This chat is already linked to Exhale.")
}

// InviteExpiredText says an invite cannot be used.
func InviteExpiredText(locale string) string {
	return tr(locale, "This invite has expired or was already used. Ask for a new one.")
}

// WelcomeText greets someone who joined from an invite.
func WelcomeText(locale, household, name string) string {
	return tr(locale, "👋 Welcome to %s, %s. Your reminders arrive here, and the message below is Exhale itself: tap around.", household, name)
}

// FailedText is the generic "it did not work" answer.
func FailedText(locale string) string {
	return tr(locale, "Something went wrong. Try again in a moment.")
}

// OpenFailedText says the app could not be opened.
func OpenFailedText(locale string) string { return tr(locale, "Something went wrong opening Exhale.") }

// ChangedByText is the footer of a message someone else changed.
func ChangedByText(locale, who string) string { return tr(locale, "Changed by %s", who) }

// Slack says the same things in its own words (codes are sent as a message
// or after /exhale, and the app also lives in the Home tab).

// SlackNotLinkedText is what an unlinked Slack account hears.
func SlackNotLinkedText(locale string) string {
	return tr(locale, "This Slack account is not linked to Exhale yet. In Exhale, open Settings → Slack, press “Link Slack” and send me the code it shows.")
}

// SlackBadCodeText explains that a link code no longer works.
func SlackBadCodeText(locale string) string {
	return tr(locale, "That code is not valid anymore: codes work once and for 10 minutes. In Exhale, open Settings → Slack and press “Link Slack” again.")
}

// SlackLinkedText confirms that a Slack account was linked.
func SlackLinkedText(locale string) string {
	return tr(locale, "✅ Linked. Reminders arrive here, and the message below is Exhale itself: tap around. The Home tab has it too.")
}

// SlackAlreadyLinkedText says the account already belongs to a member.
func SlackAlreadyLinkedText(locale string) string {
	return tr(locale, "This Slack account is already linked to Exhale.")
}

// SlackTypeLinkText is the hint under the Home tab of an unlinked account.
func SlackTypeLinkText(locale string) string {
	return tr(locale, "You can also type: /exhale link CODE")
}

// SlackLinkFirstText answers a button pressed by someone not linked yet.
func SlackLinkFirstText(locale string) string {
	return tr(locale, "Link your account first.") + " " + SlackNotLinkedText(locale)
}

// SlackOpenInDMText says where the app was opened.
func SlackOpenInDMText(locale string) string {
	return tr(locale, "Exhale is open in our direct messages.")
}

// SlackHelpText lists what can be typed in Slack.
func SlackHelpText(locale string) string {
	var b strings.Builder
	b.WriteString(tr(locale, "Type one of these, here or after /exhale anywhere:"))
	for _, c := range Commands {
		b.WriteString("\n• " + c.Command + " — " + tr(locale, c.Description))
	}
	return b.String()
}

// SlackChannelHello is the note posted in the team channel when it is set.
func SlackChannelHello(locale string) Message {
	return Message{Title: tr(locale, "🔔 Exhale posts renewals here"), Lines: []string{
		tr(locale, "Reminders of shared subscriptions show up in this channel. Anyone who linked their Slack account in Exhale can act on them; private subscriptions stay private."),
	}}
}
