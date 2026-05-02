package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/blindly/dispatcher/internal/config"
)

func TestOpen_CreatesTable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var count int
	err = conn.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='cron_jobs'").Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected cron_jobs table, got count=%d", count)
	}
}

func TestEnsureJobs_RegistersNew(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	jobs := map[string]*config.JobConfig{
		"job_a": {Name: "job_a", Commands: []string{"echo a"}, IntervalSeconds: 300},
		"job_b": {Name: "job_b", Commands: []string{"echo b"}, IntervalSeconds: 600},
	}
	EnsureJobs(conn, jobs)

	rows, err := conn.Query("SELECT name FROM cron_jobs ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		names = append(names, name)
	}
	if len(names) != 2 || names[0] != "job_a" || names[1] != "job_b" {
		t.Errorf("got names=%v, want [job_a, job_b]", names)
	}
}

func TestGetDueJobs_ReturnsOverdue(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	past := NowUTC().Add(-1 * time.Hour).Format(time.RFC3339)
	future := NowUTC().Add(1 * time.Hour).Format(time.RFC3339)
	conn.Exec("INSERT INTO cron_jobs (name, next_run_at) VALUES (?, ?)", "overdue", past)
	conn.Exec("INSERT INTO cron_jobs (name, next_run_at) VALUES (?, ?)", "future", future)

	jobs := map[string]*config.JobConfig{
		"overdue": {Name: "overdue", Commands: []string{"echo"}, IntervalSeconds: 300},
		"future":  {Name: "future", Commands: []string{"echo"}, IntervalSeconds: 300},
	}
	due := GetDueJobs(conn, jobs, "America/New_York")

	found := false
	for _, name := range due {
		if name == "overdue" {
			found = true
		}
		if name == "future" {
			t.Error("future job should not be due")
		}
	}
	if !found {
		t.Error("overdue job should be due")
	}
}

func TestGetDueJobs_FiltersActiveHours(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	past := NowUTC().Add(-1 * time.Hour).Format(time.RFC3339)
	conn.Exec("INSERT INTO cron_jobs (name, next_run_at) VALUES (?, ?)", "narrow", past)

	// active_hours 3-4 AM — almost certainly not the current hour
	hours := [2]int{3, 4}
	jobs := map[string]*config.JobConfig{
		"narrow": {Name: "narrow", Commands: []string{"echo"}, IntervalSeconds: 300, ActiveHours: &hours},
	}
	due := GetDueJobs(conn, jobs, "America/New_York")
	for _, name := range due {
		if name == "narrow" {
			t.Error("narrow job should be filtered by active_hours")
		}
	}
}

func TestIsOnActiveDay_Nil(t *testing.T) {
	if !IsOnActiveDay(nil, "America/New_York") {
		t.Error("nil days should always be active")
	}
}

func TestIsOnActiveDay_Today(t *testing.T) {
	tz := "America/New_York"
	loc, _ := time.LoadLocation(tz)
	today := int(time.Now().In(loc).Weekday())

	var days [7]bool
	days[today] = true
	if !IsOnActiveDay(&days, tz) {
		t.Errorf("today (weekday=%d) should be active", today)
	}
}

func TestIsOnActiveDay_NotToday(t *testing.T) {
	tz := "America/New_York"
	loc, _ := time.LoadLocation(tz)
	today := int(time.Now().In(loc).Weekday())
	notToday := (today + 3) % 7

	var days [7]bool
	days[notToday] = true
	if IsOnActiveDay(&days, tz) {
		t.Errorf("only weekday=%d enabled, today=%d — should be filtered", notToday, today)
	}
}

func TestGetDueJobs_FiltersActiveDays(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	past := NowUTC().Add(-1 * time.Hour).Format(time.RFC3339)
	conn.Exec("INSERT INTO cron_jobs (name, next_run_at) VALUES (?, ?)", "wrong_day", past)

	// Enable only the weekday 3 days from today — guaranteed not to be today.
	tz := "America/New_York"
	loc, _ := time.LoadLocation(tz)
	today := int(time.Now().In(loc).Weekday())
	notToday := (today + 3) % 7
	var days [7]bool
	days[notToday] = true

	jobs := map[string]*config.JobConfig{
		"wrong_day": {Name: "wrong_day", Commands: []string{"echo"}, IntervalSeconds: 300, ActiveDays: &days},
	}
	due := GetDueJobs(conn, jobs, tz)
	for _, name := range due {
		if name == "wrong_day" {
			t.Error("wrong_day should be filtered by active days")
		}
	}
}

func TestGetDueJobs_HoursAndDaysCombined(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	past := NowUTC().Add(-1 * time.Hour).Format(time.RFC3339)
	conn.Exec("INSERT INTO cron_jobs (name, next_run_at) VALUES (?, ?)", "right_day_wrong_hour", past)

	tz := "America/New_York"
	loc, _ := time.LoadLocation(tz)
	today := int(time.Now().In(loc).Weekday())
	var days [7]bool
	days[today] = true               // day matches
	hours := [2]int{3, 4}            // hour almost certainly does not

	jobs := map[string]*config.JobConfig{
		"right_day_wrong_hour": {
			Name: "right_day_wrong_hour", Commands: []string{"echo"}, IntervalSeconds: 300,
			ActiveHours: &hours, ActiveDays: &days,
		},
	}
	due := GetDueJobs(conn, jobs, tz)
	for _, name := range due {
		if name == "right_day_wrong_hour" {
			t.Error("hour filter should still apply when day matches")
		}
	}
}

func TestUpdateAfterRun(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	now := NowUTC().Format(time.RFC3339)
	conn.Exec("INSERT INTO cron_jobs (name, next_run_at) VALUES (?, ?)", "test", now)

	UpdateAfterRun(conn, "test", 300, 0, 1.5, "ok")

	var runCount int
	var status string
	conn.QueryRow("SELECT run_count, last_status FROM cron_jobs WHERE name = ?", "test").Scan(&runCount, &status)
	if runCount != 1 {
		t.Errorf("run_count = %d, want 1", runCount)
	}
	if status != "ok" {
		t.Errorf("status = %q, want ok", status)
	}

	// Verify job_runs history was also inserted
	var histCount int
	conn.QueryRow("SELECT COUNT(*) FROM job_runs WHERE name = ?", "test").Scan(&histCount)
	if histCount != 1 {
		t.Errorf("job_runs count = %d, want 1", histCount)
	}
}

func TestUpdateAfterRun_InterruptedDoesNotBumpFailCount(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	now := NowUTC().Format(time.RFC3339)
	conn.Exec("INSERT INTO cron_jobs (name, next_run_at) VALUES (?, ?)", "interrupt_test", now)

	// Simulate a Ctrl+C: rc=130 (SIGINT) but status="interrupted".
	// run_count should bump, fail_count should NOT.
	UpdateAfterRun(conn, "interrupt_test", 300, 130, 2.7, "interrupted")

	var runCount, failCount int
	var status string
	conn.QueryRow("SELECT run_count, fail_count, last_status FROM cron_jobs WHERE name = ?",
		"interrupt_test").Scan(&runCount, &failCount, &status)
	if runCount != 1 {
		t.Errorf("run_count = %d, want 1", runCount)
	}
	if failCount != 0 {
		t.Errorf("fail_count = %d, want 0 (interrupted runs must not count as failures)", failCount)
	}
	if status != "interrupted" {
		t.Errorf("status = %q, want %q", status, "interrupted")
	}

	// Sanity check: a regular failure with the same rc still bumps fail_count.
	UpdateAfterRun(conn, "interrupt_test", 300, 130, 2.7, "failed:130")
	conn.QueryRow("SELECT fail_count FROM cron_jobs WHERE name = ?", "interrupt_test").Scan(&failCount)
	if failCount != 1 {
		t.Errorf("fail_count after non-interrupted failure = %d, want 1", failCount)
	}
}

func TestGetAnalytics(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	now := NowUTC().Format(time.RFC3339)
	conn.Exec("INSERT INTO cron_jobs (name, next_run_at) VALUES (?, ?)", "analytics_test", now)

	// Simulate 3 runs: 2 pass, 1 fail
	UpdateAfterRun(conn, "analytics_test", 300, 0, 1.0, "ok")
	UpdateAfterRun(conn, "analytics_test", 300, 0, 2.0, "ok")
	UpdateAfterRun(conn, "analytics_test", 300, 1, 3.0, "failed:1")

	results, err := GetAnalytics(conn)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	a := results[0]
	if a.TotalRuns != 3 {
		t.Errorf("total = %d, want 3", a.TotalRuns)
	}
	if a.PassCount != 2 {
		t.Errorf("pass = %d, want 2", a.PassCount)
	}
	if a.FailCount != 1 {
		t.Errorf("fail = %d, want 1", a.FailCount)
	}
	if a.SuccessRate < 66.6 || a.SuccessRate > 66.7 {
		t.Errorf("rate = %.1f, want ~66.7", a.SuccessRate)
	}
	if a.Last7dRuns != 3 {
		t.Errorf("last7d = %d, want 3", a.Last7dRuns)
	}
}

func TestGetAnalytics_Empty(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	results, err := GetAnalytics(conn)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestGetHistory(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	now := NowUTC().Format(time.RFC3339)
	conn.Exec("INSERT INTO cron_jobs (name, next_run_at) VALUES (?, ?)", "hist_test", now)

	UpdateAfterRun(conn, "hist_test", 300, 0, 1.0, "ok")
	UpdateAfterRun(conn, "hist_test", 300, 1, 2.0, "failed:1")
	UpdateAfterRun(conn, "hist_test", 300, 0, 1.5, "ok")

	entries, err := GetHistory(conn, "hist_test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	// Most recent first
	if entries[0].Status != "ok" {
		t.Errorf("first entry status = %q, want ok", entries[0].Status)
	}
	if entries[1].Status != "failed:1" {
		t.Errorf("second entry status = %q, want failed:1", entries[1].Status)
	}
}

func TestGetHistory_Empty(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	entries, err := GetHistory(conn, "nonexistent", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("got %d entries, want 0", len(entries))
	}
}

func TestMeta(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// GetMeta returns empty string for missing key
	if v := GetMeta(conn, "last_dispatch_at"); v != "" {
		t.Errorf("expected empty, got %q", v)
	}

	// SetMeta stores a value
	SetMeta(conn, "last_dispatch_at", "2026-04-04T12:00:00Z")
	if v := GetMeta(conn, "last_dispatch_at"); v != "2026-04-04T12:00:00Z" {
		t.Errorf("got %q, want 2026-04-04T12:00:00Z", v)
	}

	// SetMeta updates existing value
	SetMeta(conn, "last_dispatch_at", "2026-04-04T13:00:00Z")
	if v := GetMeta(conn, "last_dispatch_at"); v != "2026-04-04T13:00:00Z" {
		t.Errorf("got %q, want 2026-04-04T13:00:00Z", v)
	}
}

func TestPurgeHistory(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	now := NowUTC().Format(time.RFC3339)
	conn.Exec("INSERT INTO cron_jobs (name, next_run_at) VALUES (?, ?)", "purge_test", now)

	// Insert an old run (100 days ago)
	old := NowUTC().AddDate(0, 0, -100).Format(time.RFC3339)
	conn.Exec("INSERT INTO job_runs (name, run_at, status, exit_code, duration_s) VALUES (?, ?, ?, ?, ?)",
		"purge_test", old, "ok", 0, 1.0)

	// Insert a recent run
	UpdateAfterRun(conn, "purge_test", 300, 0, 1.0, "ok")

	// Purge older than 90 days
	deleted, err := PurgeHistory(conn, 90)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}

	// Recent run should still be there
	entries, _ := GetHistory(conn, "purge_test", 10)
	if len(entries) != 1 {
		t.Errorf("remaining = %d, want 1", len(entries))
	}
}
