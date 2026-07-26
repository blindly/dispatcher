package display

import (
	"bytes"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
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

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	os.Stdout = orig
	w.Close()
	out := <-done
	r.Close()
	return out
}

// useTimezone sets the display timezone and restores it afterwards.
func useTimezone(t *testing.T, tzName string) {
	t.Helper()
	orig := displayLoc
	t.Cleanup(func() { displayLoc = orig })
	SetTimezone(tzName)
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestFormatInterval_Seconds(t *testing.T) {
	if got := FormatInterval(45); got != "45s" {
		t.Errorf("got %q, want 45s", got)
	}
}

func TestSetTimezone_Valid(t *testing.T) {
	useTimezone(t, "America/New_York")
	if displayLoc == nil || displayLoc.String() != "America/New_York" {
		t.Fatalf("displayLoc = %v, want America/New_York", displayLoc)
	}
	if got := FormatDt("2025-01-15T10:30:00Z"); got != "2025-01-15 05:30" {
		t.Errorf("FormatDt = %q, want 2025-01-15 05:30", got)
	}
	if got := FormatTimestamp(time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)); got != "2025-01-15 05:30:00" {
		t.Errorf("FormatTimestamp = %q, want 2025-01-15 05:30:00", got)
	}
}

func TestSetTimezone_InvalidKeepsPrevious(t *testing.T) {
	useTimezone(t, "UTC")
	SetTimezone("Not/AZone")
	if displayLoc == nil || displayLoc.String() != "UTC" {
		t.Errorf("displayLoc = %v, want UTC to be kept", displayLoc)
	}
}

func TestFormatTimestamp_NoTimezone(t *testing.T) {
	orig := displayLoc
	displayLoc = nil
	t.Cleanup(func() { displayLoc = orig })

	if got := FormatTimestamp(time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)); got != "2025-01-15 10:30:00" {
		t.Errorf("got %q, want 2025-01-15 10:30:00", got)
	}
}

func TestFormatDt_Unparseable(t *testing.T) {
	if got := FormatDt("2025-01-15 10:30:00 not-rfc3339"); got != "2025-01-15 10:30:00" {
		t.Errorf("long non-RFC3339 value should be truncated to 19 chars, got %q", got)
	}
	if got := FormatDt("garbage"); got != "garbage" {
		t.Errorf("short non-RFC3339 value should pass through, got %q", got)
	}
}

func TestFormatTimeAgo(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		iso  string
		want string
	}{
		{now.Add(-2 * time.Second).Format(time.RFC3339), "just now"},
		{now.Add(-30 * time.Second).Format(time.RFC3339), "30s ago"},
		{now.Add(-5 * time.Minute).Format(time.RFC3339), "5m ago"},
		{now.Add(-3 * time.Hour).Format(time.RFC3339), "3h ago"},
		{now.AddDate(0, 0, -2).Format(time.RFC3339), "2d ago"},
		{"not-a-time", "unknown"},
	}
	for _, tt := range tests {
		if got := formatTimeAgo(now, tt.iso); got != tt.want {
			t.Errorf("formatTimeAgo(%q) = %q, want %q", tt.iso, got, tt.want)
		}
	}
}

func TestPrintPauseBanner(t *testing.T) {
	out := captureStdout(t, func() { PrintPauseBanner("Dispatcher paused until 15:04") })
	if !strings.Contains(out, "Dispatcher paused until 15:04") {
		t.Errorf("banner missing message: %q", out)
	}

	quiet := captureStdout(t, func() { PrintPauseBanner("") })
	if quiet != "" {
		t.Errorf("empty message should print nothing, got %q", quiet)
	}
}

func TestPrintQuickStatus_RunningAndFailed(t *testing.T) {
	conn := openTestDB(t)
	jobs := map[string]*config.JobConfig{
		"running_job": {Name: "running_job", Commands: []string{"echo"}, IntervalSeconds: 300},
		"failed_job":  {Name: "failed_job", Commands: []string{"echo"}, IntervalSeconds: 300},
		"paused_job":  {Name: "paused_job", Commands: []string{"echo"}, IntervalSeconds: 300, Paused: true},
	}
	db.EnsureJobs(conn, jobs)
	db.SetMeta(conn, "last_dispatch_at", db.NowUTC().Add(-90*time.Second).Format(time.RFC3339))
	db.MarkRunning(conn, "running_job")
	db.UpdateAfterRun(conn, "failed_job", 300, 1, 2.5, "failed: exit 1", nil)

	out := captureStdout(t, func() {
		PrintQuickStatus(conn, jobs, "UTC", t.TempDir(), false, "systemd", "active")
	})

	for _, want := range []string{"Last dispatch: 1m ago", "running_job running", "2 jobs", "1 failed: failed_job", "Systemd: active"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "paused_job") {
		t.Errorf("paused job should be hidden without -a:\n%s", out)
	}

	all := captureStdout(t, func() {
		PrintQuickStatus(conn, jobs, "UTC", t.TempDir(), true, "cron", "disabled")
	})
	if !strings.Contains(all, "3 jobs") {
		t.Errorf("showAll should count paused jobs:\n%s", all)
	}
	if !strings.Contains(all, "Cron: disabled") {
		t.Errorf("cron scheduler label missing:\n%s", all)
	}
}

func TestPrintQuickStatus_DueJobs(t *testing.T) {
	conn := openTestDB(t)
	jobs := map[string]*config.JobConfig{
		"due":   {Name: "due", Commands: []string{"echo"}, IntervalSeconds: 300},
		"adhoc": {Name: "adhoc", Commands: []string{"echo"}, Adhoc: true},
	}
	db.EnsureJobs(conn, jobs)

	out := captureStdout(t, func() {
		PrintQuickStatus(conn, jobs, "UTC", t.TempDir(), false, "cron", "disabled")
	})
	if !strings.Contains(out, "1 due") {
		t.Errorf("adhoc jobs are never due, expected exactly 1 due:\n%s", out)
	}
	if !strings.Contains(out, "Last job run: never") {
		t.Errorf("output missing never-run marker:\n%s", out)
	}
}

func TestPrintStatus_AdhocAndTruncation(t *testing.T) {
	conn := openTestDB(t)
	longName := "a-very-long-job-name-that-overflows-the-column"
	jobs := map[string]*config.JobConfig{
		longName:   {Name: longName, Commands: []string{"echo"}, IntervalSeconds: 3600},
		"manual":   {Name: "manual", Commands: []string{"echo"}, Adhoc: true},
		"paused_j": {Name: "paused_j", Commands: []string{"echo"}, IntervalSeconds: 300, Paused: true},
	}
	db.EnsureJobs(conn, jobs)
	db.UpdateAfterRun(conn, longName, 3600, 0, 1.0, "passed", nil)

	out := captureStdout(t, func() { PrintStatus(conn, jobs, "UTC", false) })

	if !strings.Contains(out, "Scheduled Jobs") || !strings.Contains(out, "Adhoc Jobs") {
		t.Errorf("both sections should render:\n%s", out)
	}
	if !strings.Contains(out, "…") {
		t.Errorf("long job name should be truncated with an ellipsis:\n%s", out)
	}
	if !strings.Contains(out, "adhoc") {
		t.Errorf("adhoc jobs should show 'adhoc' instead of an interval:\n%s", out)
	}
	if strings.Contains(out, "paused_j") {
		t.Errorf("paused job should be hidden without showAll:\n%s", out)
	}

	all := captureStdout(t, func() { PrintStatus(conn, jobs, "UTC", true) })
	if !strings.Contains(all, "paused_j") || !strings.Contains(all, "paused") {
		t.Errorf("showAll should list paused jobs:\n%s", all)
	}
}

func TestPrintNextRuns(t *testing.T) {
	conn := openTestDB(t)
	jobs := map[string]*config.JobConfig{
		"hourly":  {Name: "hourly", Commands: []string{"echo"}, IntervalSeconds: 3600},
		"daily":   {Name: "daily", Commands: []string{"echo"}, IntervalSeconds: 86400, DependsOn: "hourly"},
		"manual":  {Name: "manual", Commands: []string{"echo"}, Adhoc: true},
		"stopped": {Name: "stopped", Commands: []string{"echo"}, IntervalSeconds: 300, Paused: true},
	}
	db.EnsureJobs(conn, jobs)

	out := captureStdout(t, func() { PrintNextRuns(conn, jobs, "UTC") })

	if !strings.Contains(out, "hourly") || !strings.Contains(out, "daily") {
		t.Errorf("scheduled jobs missing:\n%s", out)
	}
	if strings.Contains(out, "manual") || strings.Contains(out, "stopped") {
		t.Errorf("adhoc and paused jobs should be excluded:\n%s", out)
	}
	if !strings.Contains(out, "Depends On") || !strings.Contains(out, "hourly") {
		t.Errorf("depends_on column missing:\n%s", out)
	}
}

func TestPrintNextRuns_NoScheduledJobs(t *testing.T) {
	conn := openTestDB(t)
	jobs := map[string]*config.JobConfig{
		"manual": {Name: "manual", Commands: []string{"echo"}, Adhoc: true},
	}
	db.EnsureJobs(conn, jobs)

	out := captureStdout(t, func() { PrintNextRuns(conn, jobs, "UTC") })
	if !strings.Contains(out, "No scheduled jobs.") {
		t.Errorf("got %q, want the empty-state message", out)
	}
}

func TestPrintAnalytics(t *testing.T) {
	conn := openTestDB(t)
	jobs := map[string]*config.JobConfig{
		"good": {Name: "good", Commands: []string{"echo"}, IntervalSeconds: 300},
		"bad":  {Name: "bad", Commands: []string{"echo"}, IntervalSeconds: 300},
	}
	db.EnsureJobs(conn, jobs)
	db.UpdateAfterRun(conn, "good", 300, 0, 1.0, "passed", nil)
	db.UpdateAfterRun(conn, "good", 300, 0, 3.0, "passed", nil)
	db.UpdateAfterRun(conn, "bad", 300, 1, 2.0, "failed: exit 1", nil)

	out := captureStdout(t, func() { PrintAnalytics(conn) })

	for _, want := range []string{"good", "bad", "Overall: 3 runs", "Most reliable: good (100.0%)", "Least reliable: bad (0.0%)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintAnalytics_NoHistory(t *testing.T) {
	conn := openTestDB(t)
	out := captureStdout(t, func() { PrintAnalytics(conn) })
	if !strings.Contains(out, "No run history yet.") {
		t.Errorf("got %q, want the empty-state message", out)
	}
}

func TestPrintHistory(t *testing.T) {
	conn := openTestDB(t)
	jobs := map[string]*config.JobConfig{
		"job": {Name: "job", Commands: []string{"echo"}, IntervalSeconds: 300},
	}
	db.EnsureJobs(conn, jobs)
	db.UpdateAfterRun(conn, "job", 300, 2, 4.5, "failed: exit 2", nil)

	out := captureStdout(t, func() { PrintHistory(conn, "job", 20) })

	for _, want := range []string{"job — last 1 runs", "failed: exit 2", "4.5s"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintHistory_Empty(t *testing.T) {
	conn := openTestDB(t)
	out := captureStdout(t, func() { PrintHistory(conn, "nope", 20) })
	if !strings.Contains(out, "No history for nope") {
		t.Errorf("got %q, want the empty-state message", out)
	}
}
