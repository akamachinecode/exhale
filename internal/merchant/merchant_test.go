package merchant

import "testing"

func TestKey(t *testing.T) {
	for in, want := range map[string]string{
		"NETFLIX.COM 866-579-7172 CA":      "netflix",
		"Netflix.com Amsterdam":            "netflix amsterdam",
		"CARD PAYMENT TO SPOTIFY P1A2B3C4": "spotify",
		"Compra en tarjeta DISNEYPLUS":     "disneyplus",
		"12345":                            "",
	} {
		if got := Key(in); got != want {
			t.Errorf("Key(%q) = %q, want %q", in, got, want)
		}
	}
	if Name("spotify premium") != "Spotify Premium" {
		t.Error(Name("spotify premium"))
	}
}

func TestSame(t *testing.T) {
	yes := [][2]string{{"netflix", "netflix amsterdam"}, {Key("Disney+"), Key("DISNEYPLUS")}, {Key("Spotify Premium"), Key("SPOTIFY P123")}}
	no := [][2]string{{"netflix", "spotify"}, {"gym", "gymshark"}, {"", "netflix"}, {Key("Apple One"), Key("Applebees")}}
	for _, p := range yes {
		if !Same(p[0], p[1]) {
			t.Errorf("Same(%q, %q) = false", p[0], p[1])
		}
	}
	for _, p := range no {
		if Same(p[0], p[1]) {
			t.Errorf("Same(%q, %q) = true", p[0], p[1])
		}
	}
}
