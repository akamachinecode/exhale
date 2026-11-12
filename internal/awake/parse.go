package awake

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The systems' own tools answer in text; these read it. They are kept apart
// from the files of each system so they are tested everywhere.

var (
	pmsetPercent     = regexp.MustCompile(`(\d{1,3})%`)
	pmsetSleepOff    = regexp.MustCompile(`(?m)^\s*SleepDisabled\s+1\b`)
	clamshellClosed  = regexp.MustCompile(`"AppleClamshellState"\s*=\s*Yes`)
	powercfgIndex    = regexp.MustCompile(`0x[0-9a-fA-F]{8}`)
	powercfgSettings = regexp.MustCompile(`(?i)LIDACTION|5ca83367-6e45-459f-a27b-476b1d01c936`)
)

// parsePmsetBatt reads `pmset -g batt`:
//
//	Now drawing from 'Battery Power'
//	 -InternalBattery-0 (id=4653155)	82%; discharging; 5:12 remaining present: true
//
// A Mac without a battery shows only the first line ('AC Power').
func parsePmsetBatt(out string) Power {
	p := Power{Known: strings.Contains(out, "drawing from")}
	if !p.Known {
		return p
	}
	p.OnBattery = strings.Contains(out, "'Battery Power'")
	if strings.Contains(out, "InternalBattery") {
		p.HasBattery = true
		if m := pmsetPercent.FindStringSubmatch(out); m != nil {
			p.Percent, _ = strconv.Atoi(m[1])
		}
	} else {
		p.OnBattery = false // a UPS reports 'Battery Power' too; a desktop has no battery of its own
	}
	return p
}

// parsePmsetSleepDisabled reads `pmset -g`: SleepDisabled 1 means lid sleep
// is off.
func parsePmsetSleepDisabled(out string) bool { return pmsetSleepOff.MatchString(out) }

// parseClamshell reads `ioreg -r -k AppleClamshellState -d 1`.
func parseClamshell(out string) bool { return clamshellClosed.MatchString(out) }

// parsePowercfgLid reads `powercfg /query SCHEME_CURRENT SUB_BUTTONS
// LIDACTION`. Its labels are in the system's language; the two values that
// follow the setting, AC then DC, are not:
//
//	Power Setting GUID: 5ca83367-... (Lid close action)
//	  ...
//	  Possible Setting Index: 000 ...
//	Current AC Power Setting Index: 0x00000001
//	Current DC Power Setting Index: 0x00000001
func parsePowercfgLid(out string) (ac, dc int, err error) {
	loc := powercfgSettings.FindStringIndex(out)
	if loc == nil {
		return 0, 0, fmt.Errorf("powercfg: no lid action in %q", truncate(out, 200))
	}
	vals := powercfgIndex.FindAllString(out[loc[1]:], -1)
	if len(vals) < 2 {
		return 0, 0, fmt.Errorf("powercfg: no current values in %q", truncate(out, 200))
	}
	// The last two are the current ones: the possible values come first
	// as plain numbers, not 0x...
	a, err1 := strconv.ParseInt(vals[len(vals)-2][2:], 16, 32)
	d, err2 := strconv.ParseInt(vals[len(vals)-1][2:], 16, 32)
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("powercfg: unreadable values %v", vals)
	}
	return int(a), int(d), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// sudoersRule allows the user with uid to turn lid sleep off and on, and
// nothing else. A uid, not a name: names can hold characters sudoers or a
// shell would read (an email as the short name, on some managed Macs).
func sudoersRule(uid int) string {
	return fmt.Sprintf("#%d ALL=(root) NOPASSWD: /usr/bin/pmset disablesleep 1, /usr/bin/pmset disablesleep 0", uid)
}
