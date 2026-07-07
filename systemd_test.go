package main

import "testing"

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
