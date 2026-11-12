package auth

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/store"
)

func TestEnsureDesktopToken(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := app.New(st, bus.New(), nil, nil)
	owner, err := svc.Bootstrap(ctx, app.BootstrapInput{HouseholdName: "Home", OwnerName: "Alex", Currency: "EUR", Timezone: "UTC", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), DesktopTokenFile)
	read := func() string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	if err := EnsureDesktopToken(ctx, st, owner.MemberID, path); err != nil {
		t.Fatal(err)
	}
	first := read()
	if a, err := ResolveToken(ctx, st, first); err != nil || a.MemberID != owner.MemberID || !a.CanWrite() {
		t.Fatalf("token does not work: %+v %v", a, err)
	}
	// Windows has no Unix modes: the file lives in the person's own profile.
	if fi, _ := os.Stat(path); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	// Kept while it works.
	if err := EnsureDesktopToken(ctx, st, owner.MemberID, path); err != nil || read() != first {
		t.Fatalf("replaced a working token: %v", err)
	}
	// Revoked from the web: replaced, and only one stays.
	toks, _ := ListTokens(ctx, st, owner.MemberID)
	if err := RevokeToken(ctx, st, owner.MemberID, toks[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDesktopToken(ctx, st, owner.MemberID, path); err != nil || read() == first {
		t.Fatalf("revoked token kept: %v", err)
	}
	if toks, _ := ListTokens(ctx, st, owner.MemberID); len(toks) != 1 {
		t.Fatalf("%d tokens", len(toks))
	}
}
