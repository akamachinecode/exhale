// Package merchant reduces the description a bank writes for a payment to
// the words that identify who was paid, so "NETFLIX.COM 866-579-7172 CA",
// "Netflix.com Amsterdam" and a subscription called "Netflix" compare
// equal. The importer uses it to group transactions; the app uses it to
// match a statement with the charges it expects.
package merchant

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// noise are words of a bank description that say nothing about who was
// paid.
var noise = map[string]bool{
	"card": true, "payment": true, "to": true, "pos": true, "purchase": true, "direct": true, "debit": true, "dd": true, "sepa": true,
	"compra": true, "pago": true, "en": true, "recibo": true, "tarjeta": true, "com": true, "www": true, "inc": true, "ltd": true, "llc": true,
	"gmbh": true, "the": true, "bill": true, "subscription": true, "suscripcion": true, "online": true, "internet": true, "paypal": true,
	"sq": true, "tst": true, "recurring": true, "adeudo": true, "domiciliacion": true, "visa": true, "mastercard": true, "ref": true,
	"help": true, "http": true, "https": true, "net": true, "org": true, "app": true, "pay": true, "via": true, "from": true,
}

// Key reduces a bank description (or a subscription name) to at most two
// lowercase words that identify the merchant. It is "" when nothing is
// left.
func Key(text string) string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var kept []string
	for _, w := range words {
		if len(w) < 3 || noise[w] || strings.IndexFunc(w, unicode.IsDigit) >= 0 {
			continue
		}
		kept = append(kept, w)
		if len(kept) == 2 {
			break
		}
	}
	return strings.Join(kept, " ")
}

// Name writes a key as a name: "netflix" becomes "Netflix".
func Name(key string) string {
	words := strings.Fields(key)
	for i, w := range words {
		r, n := utf8.DecodeRuneInString(w)
		words[i] = string(unicode.ToUpper(r)) + w[n:]
	}
	return strings.Join(words, " ")
}

// Same reports whether two keys name the same merchant: the same first
// word ("spotify" and "spotify premium"), or one written together is the
// start of the other ("disney" and "disneyplus"), with at least four
// letters in common so short words do not match by chance.
func Same(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	fa, _, _ := strings.Cut(a, " ")
	fb, _, _ := strings.Cut(b, " ")
	if fa == fb {
		return true
	}
	ja, jb := strings.ReplaceAll(a, " ", ""), strings.ReplaceAll(b, " ", "")
	if len(ja) > len(jb) {
		ja, jb = jb, ja
	}
	return len(ja) >= 4 && strings.HasPrefix(jb, ja)
}
