package actions

import "strings"

// Texts a channel adapter says outside any screen: linking a chat, joining
// from an invite and the list of commands. They live here so every channel
// says the same thing, in the member's language.

// Language picks the language of a message for someone Exhale does not know
// yet (an unlinked chat) from the code the chat platform reports, e.g.
// "es-ES".
func Language(code string) string {
	if strings.HasPrefix(strings.ToLower(code), "es") {
		return "es"
	}
	return "en"
}

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
