package display

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/blindly/dispatcher/internal/config"
	"github.com/blindly/dispatcher/internal/db"
)

func TestFormatDurationHuman(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{-1 * time.Minute, "overdue"},
		{30 * time.Second, "now"},
		{5 * time.Minute, "in 5m"},
		{2 * time.Hour, "in 2h"},
		{3 * 24 * time.Hour, "in 3d"},
	}
	for _, tt := range tests {
		got := FormatDurationHuman(tt.d)
		if got != tt.want {
			t.Errorf("FormatDurationHuman(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestFormatInterval_Minutes(t *testing.T) {
	if got := FormatInterval(300); got != "5m" {
		t.Errorf("got %q, want 5m", got)
	}
}

func TestFormatInterval_Hours(t *testing.T) {
	if got := FormatInterval(7200); got != "2h" {
		t.Errorf("got %q, want 2h", got)
	}
}

func TestFormatInterval_Days(t *testing.T) {
	if got := FormatInterval(86400); got != "1d" {
		t.Errorf("got %q, want 1d", got)
	}
}

func TestFormatInterval_Weeks(t *testing.T) {
	if got := FormatInterval(604800); got != "1w" {
		t.Errorf("got %q, want 1w", got)
	}
}

func TestFormatDt_Valid(t *testing.T) {
	got := FormatDt("2025-01-15T10:30:00Z")
	if got != "2025-01-15 10:30" {
		t.Errorf("got %q", got)
	}
}

func TestFormatDt_Empty(t *testing.T) {
	if got := FormatDt(""); got != "-" {
		t.Errorf("got %q, want -", got)
	}
}

func TestFormatActive_Default(t *testing.T) {
	if got := formatActive(nil, nil); got != "always" {
		t.Errorf("got %q, want always", got)
	}
}

func TestFormatActive_HoursOnly(t *testing.T) {
	hours := [2]int{9, 17}
	if got := formatActive(nil, &hours); got != "09-17" {
		t.Errorf("got %q, want 09-17", got)
	}
}

func TestFormatActive_Weekdays(t *testing.T) {
	days := [7]bool{false, true, true, true, true, true, false}
	if got := formatActive(&days, nil); got != "M-F" {
		t.Errorf("got %q, want M-F", got)
	}
}

func TestFormatActive_Weekends(t *testing.T) {
	days := [7]bool{true, false, false, false, false, false, true}
	if got := formatActive(&days, nil); got != "S-S" {
		t.Errorf("got %q, want S-S", got)
	}
}

func TestFormatActive_ArbitraryDays(t *testing.T) {
	days := [7]bool{false, true, false, true, false, true, false} // Mon, Wed, Fri
	if got := formatActive(&days, nil); got != "Mo,We,Fr" {
		t.Errorf("got %q, want Mo,We,Fr", got)
	}
}

func TestFormatActive_Combined(t *testing.T) {
	days := [7]bool{false, true, true, true, true, true, false}
	hours := [2]int{9, 17}
	if got := formatActive(&days, &hours); got != "M-F 09-17" {
		t.Errorf("got %q, want M-F 09-17", got)
	}
}

func TestPrintQuickStatus(t *testing.T) {
	// Just verify it doesn't panic with an empty DB
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	jobs := map[string]*config.JobConfig{
		"j1": {Name: "j1", Commands: []string{"echo"}, IntervalSeconds: 300},
	}
	db.EnsureJobs(conn, jobs)

	// Should print without error
	PrintQuickStatus(conn, jobs, "America/New_York", t.TempDir(), false, "cron", "disabled")
}

func TestShortStatus(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	paused := &config.JobConfig{Name: "x", Paused: true, IntervalSeconds: 300}
	if got := shortStatus(jobRow{}, paused, now); got != "paused" {
		t.Errorf("paused = %q", got)
	}

	runningRecent := jobRow{runningSince: sql.NullString{String: now.Add(-30 * time.Second).Format(time.RFC3339), Valid: true}}
	job := &config.JobConfig{Name: "x", IntervalSeconds: 300}
	if got := shortStatus(runningRecent, job, now); got != "running 30s" {
		t.Errorf("running recent = %q", got)
	}

	runningLong := jobRow{runningSince: sql.NullString{String: now.Add(-5 * time.Minute).Format(time.RFC3339), Valid: true}}
	if got := shortStatus(runningLong, job, now); got != "running 5m" {
		t.Errorf("running long = %q", got)
	}

	failed := jobRow{status: sql.NullString{String: "failed: exit 1", Valid: true}}
	if got := shortStatus(failed, job, now); got != "failed" {
		t.Errorf("failed = %q", got)
	}

	passed := jobRow{status: sql.NullString{String: "passed in 12s", Valid: true}}
	if got := shortStatus(passed, job, now); got != "ok" {
		t.Errorf("passed = %q", got)
	}

	inted := jobRow{status: sql.NullString{String: "interrupted", Valid: true}}
	if got := shortStatus(inted, job, now); got != "interrupt" {
		t.Errorf("interrupted = %q", got)
	}

	empty := jobRow{}
	if got := shortStatus(empty, job, now); got != "-" {
		t.Errorf("empty = %q", got)
	}
}

func TestFormatTimeShort(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	if got := formatTimeShort("", now); got != "-" {
		t.Errorf("empty = %q", got)
	}

	today := now.Add(-2 * time.Hour).Format(time.RFC3339)
	if got := formatTimeShort(today, now); got != "10:00" {
		t.Errorf("today = %q", got)
	}

	oldDays := now.AddDate(0, 0, -3).Format(time.RFC3339)
	got := formatTimeShort(oldDays, now)
	if len(got) != 11 || got[2] != '-' {
		t.Errorf("older date should be MM-DD HH:MM, got %q", got)
	}
}

func TestPrintStatus_NoJobs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	PrintStatus(conn, map[string]*config.JobConfig{}, "UTC", false)
	// Expect: "\nNo jobs configured.\n\n"
}

func TestPrintStatus_Scheduled(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	jobs := map[string]*config.JobConfig{
		"backup": {Name: "backup", Commands: []string{"echo"}, IntervalSeconds: 86400},
	}
	db.EnsureJobs(conn, jobs)

	PrintStatus(conn, jobs, "UTC", false)
}
