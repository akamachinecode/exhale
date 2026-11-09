// Package domain (piece ⑧) holds the entities and business rules of Exhale.
// It is pure Go: no database, no HTTP, no channels.
package domain

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
)

// ID prefixes make identifiers self-describing for humans and AI agents
// ("sub_…" is a subscription, "chg_…" a charge).
const (
	PrefixHousehold     = "hh"
	PrefixMember        = "mem"
	PrefixSubscription  = "sub"
	PrefixCharge        = "chg"
	PrefixPaymentMethod = "pm"
	PrefixCategory      = "cat"
	PrefixEvent         = "evt"
	PrefixFlow          = "flw"
	PrefixOutbox        = "out"
)

var idEncoding = base32.NewEncoding("0123456789abcdefghjkmnpqrstvwxyz").WithPadding(base32.NoPadding)

// NewID returns a random, URL-safe identifier such as "sub_7k3m9q2x4w8z1c5v".
func NewID(prefix string) string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + idEncoding.EncodeToString(b[:])
}

// HasPrefix reports whether id carries the given prefix.
func HasPrefix(id, prefix string) bool { return strings.HasPrefix(id, prefix+"_") }
