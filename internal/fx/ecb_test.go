package fx

import (
	"context"
	"os"
	"testing"

	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

func TestParseAndSave(t *testing.T) {
	f, err := os.Open("testdata/eurofxref-daily.xml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rates, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) < 25 {
		t.Fatalf("got %d rates", len(rates))
	}
	ctx := context.Background()
	st, _ := store.OpenMemory(ctx)
	defer st.Close()
	if err := Save(ctx, st, rates); err != nil {
		t.Fatal(err)
	}
	r, err := st.Q.FxRateOnOrBefore(ctx, db.FxRateOnOrBeforeParams{Currency: "USD", Day: "9999-12-31"})
	if err != nil || r.Rate == "" {
		t.Fatalf("USD rate missing: %v", err)
	}
}
