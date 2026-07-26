package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCronToOnCalendar_Every5Minutes(t *testing.T) {
	got, err := cronToOnCalendar("*/5 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	if got != "*-*-* 00:00/5" {
		t.Errorf("got %q, want *-*-* 00:00/5", got)
	}
}

func TestCronToOnCalendar_Hourly(t *testing.T) {
	got, err := cronToOnCalendar("0 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	if got != "*-*-* 00:00" {
		t.Errorf("got %q, want *-*-* 00:00", got)
	}
}

func TestCronToOnCalendar_Daily(t *testing.T) {
	got, err := cronToOnCalendar("0 0 * * *")
	if err != nil {
		t.Fatal(err)
	}
	if got != "*-*-* 00:00" {
		t.Errorf("got %q, want *-*-* 00:00", got)
	}
}

func TestCronToOnCalendar_Daily2AM(t *testing.T) {
	got, err := cronToOnCalendar("30 2 * * *")
	if err != nil {
		t.Fatal(err)
	}
	if got != "*-*-* 02:30" {
		t.Errorf("got %q, want *-*-* 02:30", got)
	}
}

func TestCronToOnCalendar_Invalid(t *testing.T) {
	_, err := cronToOnCalendar("bad")
	if err == nil {
		t.Error("expected error for invalid cron")
	}
}

func TestUnitNameFromDir(t *testing.T) {
	tests := []struct {
		dir  string
		want string
	}{
		{"/home/user/my-project", "dispatch-my-project"},
		{"/home/user/my project", "dispatch-my-project"},
		{"/home/user/", "dispatch-user"},
	}
	for _, tt := range tests {
		got := unitNameFromDir(tt.dir)
		if got != tt.want {
			t.Errorf("unitNameFromDir(%q) = %q, want %q", tt.dir, got, tt.want)
		}
	}
}

func TestCronToOnCalendar_DayOfMonth(t *testing.T) {
	got, err := cronToOnCalendar("5 3 15 * *")
	if err != nil {
		t.Fatal(err)
	}
	if got != "*-*-15 03:05" {
		t.Errorf("got %q, want *-*-15 03:05", got)
	}
}

func TestCronToOnCalendar_Month(t *testing.T) {
	got, err := cronToOnCalendar("0 4 * 6 *")
	if err != nil {
		t.Fatal(err)
	}
	if got != "*-6-* 04:00" {
		t.Errorf("got %q, want *-6-* 04:00", got)
	}
}

func TestCronToOnCalendar_WildcardMinute(t *testing.T) {
	got, err := cronToOnCalendar("* * * * *")
	if err != nil {
		t.Fatal(err)
	}
	if got != "*-*-* 00:00" {
		t.Errorf("got %q, want *-*-* 00:00", got)
	}
}

func TestCronToOnCalendar_EveryNHours(t *testing.T) {
	got, err := cronToOnCalendar("30 */6 * * *")
	if err != nil {
		t.Fatal(err)
	}
	if got != "*-*-* 00/6:30" {
		t.Errorf("got %q, want *-*-* 00/6:30", got)
	}
}

func TestCronToOnCalendar_SteppedFromBase(t *testing.T) {
	got, err := cronToOnCalendar("5/10 2/3 * * *")
	if err != nil {
		t.Fatal(err)
	}
	if got != "*-*-* 02/3:05/10" {
		t.Errorf("got %q, want *-*-* 02/3:05/10", got)
	}
}

// Day-of-week is not translatable to a simple OnCalendar value; the
// conversion falls through to the time-only form rather than failing.
func TestCronToOnCalendar_DayOfWeekIgnored(t *testing.T) {
	got, err := cronToOnCalendar("0 9 * * 1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "*-*-* 09:00" {
		t.Errorf("got %q, want *-*-* 09:00", got)
	}
}

func TestPadHourMinute(t *testing.T) {
	if got := padHour("*"); got != "*" {
		t.Errorf("padHour(*) = %q, want *", got)
	}
	if got := padHour("7"); got != "07" {
		t.Errorf("padHour(7) = %q, want 07", got)
	}
	if got := padHour("*/2"); got != "*/2" {
		t.Errorf("padHour(*/2) = %q, want */2 passed through", got)
	}
	if got := padMinute("*"); got != "0" {
		t.Errorf("padMinute(*) = %q, want 0", got)
	}
	if got := padMinute("7"); got != "07" {
		t.Errorf("padMinute(7) = %q, want 07", got)
	}
	if got := padMinute("*/5"); got != "*/5" {
		t.Errorf("padMinute(*/5) = %q, want */5 passed through", got)
	}
}

func TestPadInt(t *testing.T) {
	if got := padInt("3"); got != "03" {
		t.Errorf("got %q, want 03", got)
	}
	if got := padInt("notanumber"); got != "notanumber" {
		t.Errorf("non-numeric input should pass through, got %q", got)
	}
}

func TestUnitNameFromDir_FallsBackWhenEmpty(t *testing.T) {
	if got := unitNameFromDir("///"); got != "dispatch-dispatch" {
		t.Errorf("got %q, want dispatch-dispatch", got)
	}
}

func TestSystemdUnitDirAndControlPath(t *testing.T) {
	sc := systemdControlPath()
	if len(sc) == 0 || sc[0] != "systemctl" {
		t.Fatalf("systemdControlPath() = %v, want it to start with systemctl", sc)
	}
	dir := systemdUnitDir()
	if isUserSystemd() {
		if len(sc) != 2 || sc[1] != "--user" {
			t.Errorf("non-root should use systemctl --user, got %v", sc)
		}
		if !strings.HasSuffix(dir, filepath.Join(".config", "systemd", "user")) {
			t.Errorf("non-root unit dir = %q, want a user unit dir", dir)
		}
	} else {
		if len(sc) != 1 {
			t.Errorf("root should use plain systemctl, got %v", sc)
		}
		if dir != "/etc/systemd/system" {
			t.Errorf("root unit dir = %q, want /etc/systemd/system", dir)
		}
	}
}

func TestIsSystemdInstalled_NotInstalled(t *testing.T) {
	installed, _ := isSystemdInstalled(t.TempDir())
	if installed {
		t.Error("a scratch directory should never have a timer installed")
	}
}
