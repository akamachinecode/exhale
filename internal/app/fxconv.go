package app

import (
	"context"
	"math/big"

	"github.com/akamachinecode/exhale/internal/money"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// converter turns amounts into the household's main currency with the ECB
// rate of a given day (or the latest one before it).
type converter struct {
	q     *db.Queries
	main  string
	day   string
	cache map[string]rateInfo
}

type rateInfo struct {
	rate string // units of main per 1 unit of the currency
	day  string
	ok   bool
}

func (s *Service) converter(main, day string) *converter {
	return &converter{q: s.st.Q, main: main, day: day, cache: map[string]rateInfo{}}
}

// perEUR returns units of cur per 1 EUR.
func (c *converter) perEUR(ctx context.Context, cur string) (*big.Rat, string, bool) {
	if cur == "EUR" {
		return big.NewRat(1, 1), c.day, true
	}
	r, err := c.q.FxRateOnOrBefore(ctx, db.FxRateOnOrBeforeParams{Currency: cur, Day: c.day})
	if err != nil {
		return nil, "", false
	}
	v, ok := new(big.Rat).SetString(r.Rate)
	return v, r.Day, ok
}

func (c *converter) rate(ctx context.Context, cur string) rateInfo {
	if ri, ok := c.cache[cur]; ok {
		return ri
	}
	ri := rateInfo{}
	if cur == c.main {
		ri = rateInfo{rate: "1", day: c.day, ok: true}
	} else if from, d1, ok1 := c.perEUR(ctx, cur); ok1 {
		if to, d2, ok2 := c.perEUR(ctx, c.main); ok2 {
			r := new(big.Rat).Quo(to, from)
			day := d1
			if d2 < day {
				day = d2
			}
			ri = rateInfo{rate: r.FloatString(10), day: day, ok: true}
		}
	}
	c.cache[cur] = ri
	return ri
}

// toMain converts m; ok is false when no rate is known for its currency.
func (c *converter) toMain(ctx context.Context, m money.Money) (money.Money, bool) {
	if m.Currency == c.main {
		return m, true
	}
	ri := c.rate(ctx, m.Currency)
	if !ri.ok {
		return money.Money{}, false
	}
	out, err := m.Convert(c.main, ri.rate)
	return out, err == nil
}

func (c *converter) describe() (rates map[string]string, day string) {
	rates = map[string]string{}
	for cur, ri := range c.cache {
		if ri.ok && cur != c.main {
			rates[cur] = ri.rate
			if day == "" || ri.day < day {
				day = ri.day
			}
		}
	}
	return rates, day
}
