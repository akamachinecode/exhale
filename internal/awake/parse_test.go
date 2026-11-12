package awake

import "testing"

func TestParsePmsetBatt(t *testing.T) {
	for name, tc := range map[string]struct {
		out  string
		want Power
	}{
		"laptop on battery": {"Now drawing from 'Battery Power'\n -InternalBattery-0 (id=4653155)\t82%; discharging; 5:12 remaining present: true\n",
			Power{Known: true, HasBattery: true, OnBattery: true, Percent: 82}},
		"laptop charging": {"Now drawing from 'AC Power'\n -InternalBattery-0 (id=4653155)\t100%; charged; 0:00 remaining present: true\n",
			Power{Known: true, HasBattery: true, Percent: 100}},
		"desktop":          {"Now drawing from 'AC Power'\n", Power{Known: true}},
		"desktop on a UPS": {"Now drawing from 'Battery Power'\n -UPS (id=1)\t90%; discharging\n", Power{Known: true}},
		"nothing":          {"", Power{}},
	} {
		if got := parsePmsetBatt(tc.out); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", name, got, tc.want)
		}
	}
}

func TestParsePmsetSleepDisabled(t *testing.T) {
	if !parsePmsetSleepDisabled("System-wide power settings:\n SleepDisabled\t\t1\nCurrently in use:\n") {
		t.Error("SleepDisabled 1 not seen")
	}
	if parsePmsetSleepDisabled("System-wide power settings:\n SleepDisabled\t\t0\n") {
		t.Error("SleepDisabled 0 taken for 1")
	}
}

func TestParseClamshell(t *testing.T) {
	if !parseClamshell(`  | "AppleClamshellState" = Yes`) || parseClamshell(`  | "AppleClamshellState" = No`) {
		t.Error("clamshell state misread")
	}
}

func TestParsePowercfgLid(t *testing.T) {
	out := `Power Scheme GUID: 381b4222-f694-41f0-9685-ff5bb260df2e  (Equilibrado)
  GUID del subgrupo: 4f971e89-eebd-4455-a8de-9e59040e7347  (Botones de inicio/apagado y tapa)
    GUID de configuración de energía: 5ca83367-6e45-459f-a27b-476b1d01c936  (Acción de cerrar la tapa)
      Índice de configuración posible: 000
      Nombre descriptivo de configuración posible: No hacer nada
      Índice de configuración posible: 001
      Nombre descriptivo de configuración posible: Suspender
    Índice de configuración de corriente alterna actual: 0x00000001
    Índice de configuración de corriente continua actual: 0x00000003
`
	ac, dc, err := parsePowercfgLid(out)
	if err != nil || ac != 1 || dc != 3 {
		t.Fatalf("got %d %d %v", ac, dc, err)
	}
	if _, _, err := parsePowercfgLid("nothing"); err == nil {
		t.Error("no error for an answer without the setting")
	}
}

func TestSudoersRule(t *testing.T) {
	want := "#501 ALL=(root) NOPASSWD: /usr/bin/pmset disablesleep 1, /usr/bin/pmset disablesleep 0"
	if got := sudoersRule(501); got != want {
		t.Errorf("got %q", got)
	}
}
