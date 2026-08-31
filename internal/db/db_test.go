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
	hours := [2]int{180, 240}
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
	days[today] = true        // day matches
	hours := [2]int{180, 240} // hour almost certainly does not

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

	UpdateAfterRun(conn, "test", 300, 0, 1.5, "ok", nil)

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
	UpdateAfterRun(conn, "interrupt_test", 300, 130, 2.7, "interrupted", nil)

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
	UpdateAfterRun(conn, "interrupt_test", 300, 130, 2.7, "failed:130", nil)
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
	UpdateAfterRun(conn, "analytics_test", 300, 0, 1.0, "ok", nil)
	UpdateAfterRun(conn, "analytics_test", 300, 0, 2.0, "ok", nil)
	UpdateAfterRun(conn, "analytics_test", 300, 1, 3.0, "failed:1", nil)

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

	UpdateAfterRun(conn, "hist_test", 300, 0, 1.0, "ok", nil)
	UpdateAfterRun(conn, "hist_test", 300, 1, 2.0, "failed:1", nil)
	UpdateAfterRun(conn, "hist_test", 300, 0, 1.5, "ok", nil)

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
	UpdateAfterRun(conn, "purge_test", 300, 0, 1.0, "ok", nil)

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

func TestComputeNextAligned_NilAtMinute(t *testing.T) {
	now := time.Date(2026, 1, 1, 10, 5, 0, 0, time.UTC)
	next := computeNextAligned(now, 3600, nil)
	want := time.Date(2026, 1, 1, 11, 5, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %s, want %s", next, want)
	}
}

func TestComputeNextAligned_SnapsToMinute(t *testing.T) {
	// Job finishes at 10:05:00 with interval 1h and at_minute=0
	now := time.Date(2026, 1, 1, 10, 5, 0, 0, time.UTC)
	atMin := 0
	next := computeNextAligned(now, 3600, &atMin)
	// Should snap to 11:00:00
	want := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %s, want %s", next, want)
	}
}

func TestComputeNextAligned_CurrentMinuteAlreadyMatches(t *testing.T) {
	// Job finishes at 10:30:00 with interval 1h and at_minute=30
	// Candidate 10:30:00 is not after now (equal), so move to 11:30
	now := time.Date(2026, 1, 1, 10, 30, 0, 0, time.UTC)
	atMin := 30
	next := computeNextAligned(now, 3600, &atMin)
	want := time.Date(2026, 1, 1, 11, 30, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %s, want %s", next, want)
	}
}

func TestComputeNextAligned_JobRunsInSameMinute(t *testing.T) {
	// Job finishes at 10:00:01, at_minute=0, interval=1h
	// Candidate 10:00:00 is in the past, next candidate 11:00:00
	now := time.Date(2026, 1, 1, 10, 0, 1, 0, time.UTC)
	atMin := 0
	next := computeNextAligned(now, 3600, &atMin)
	want := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %s, want %s", next, want)
	}
}

func TestComputeNextAligned_FutureCandidateInSameHour(t *testing.T) {
	// Job finishes at 10:10, at_minute=30, interval=5m
	// 5m interval derives valid minutes: [30, 35, 40, 45, 50, 55, 5, 10, 15, 20, 25]
	// First one after 10:10 in current hour is 10:15
	now := time.Date(2026, 1, 1, 10, 10, 0, 0, time.UTC)
	atMin := 30
	next := computeNextAligned(now, 300, &atMin)
	want := time.Date(2026, 1, 1, 10, 15, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %s, want %s", next, want)
	}
}

func TestComputeNextAligned_MultiHourInterval(t *testing.T) {
	// Job finishes at 10:05, at_minute=0, interval=2h
	// Candidate 11:00:00, + 1 extra hour = 12:00:00
	now := time.Date(2026, 1, 1, 10, 5, 0, 0, time.UTC)
	atMin := 0
	next := computeNextAligned(now, 7200, &atMin)
	want := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %s, want %s", next, want)
	}
}

func TestComputeNextAligned_FifteenMinuteIntervalPicksNext(t *testing.T) {
	// interval=15m, at_minute=0 → derived: [0, 15, 30, 45]
	// Job finishes at 10:20, next in current hour is 30
	now := time.Date(2026, 1, 1, 10, 20, 0, 0, time.UTC)
	atMin := 0
	next := computeNextAligned(now, 900, &atMin)
	want := time.Date(2026, 1, 1, 10, 30, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %s, want %s", next, want)
	}
}

func TestComputeNextAligned_FifteenMinuteIntervalWrapsToNextHour(t *testing.T) {
	// interval=15m, at_minute=0 → derived: [0, 15, 30, 45]
	// Job finishes at 10:50, no target left in current hour, earliest next = 11:00
	now := time.Date(2026, 1, 1, 10, 50, 0, 0, time.UTC)
	atMin := 0
	next := computeNextAligned(now, 900, &atMin)
	want := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %s, want %s", next, want)
	}
}

func TestComputeNextAligned_TenMinuteIntervalWithAnchor5(t *testing.T) {
	// interval=10m, at_minute=5 → derived: [5, 15, 25, 35, 45, 55]
	// Job finishes at 10:20, next in current hour is 25
	now := time.Date(2026, 1, 1, 10, 20, 0, 0, time.UTC)
	atMin := 5
	next := computeNextAligned(now, 600, &atMin)
	want := time.Date(2026, 1, 1, 10, 25, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %s, want %s", next, want)
	}
}

func TestComputeNextAligned_TenMinuteIntervalWrapWithAnchor5(t *testing.T) {
	// interval=10m, at_minute=5 → derived: [5, 15, 25, 35, 45, 55]
	// Job finishes at 10:56, no target left, earliest next hour = 11:05
	now := time.Date(2026, 1, 1, 10, 56, 0, 0, time.UTC)
	atMin := 5
	next := computeNextAligned(now, 600, &atMin)
	want := time.Date(2026, 1, 1, 11, 5, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %s, want %s", next, want)
	}
}

func TestComputeNextAligned_FifteenMinuteIntervalPicksClosest(t *testing.T) {
	// interval=15m, at_minute=0 → derived: [0, 15, 30, 45]
	// Job finishes at 10:02, next target is 15
	now := time.Date(2026, 1, 1, 10, 2, 0, 0, time.UTC)
	atMin := 0
	next := computeNextAligned(now, 900, &atMin)
	want := time.Date(2026, 1, 1, 10, 15, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %s, want %s", next, want)
	}
}

func TestComputeNextAligned_SubMinuteIntervalNoAlignment(t *testing.T) {
	// interval=30s → intervalMin=0, no alignment applied
	now := time.Date(2026, 1, 1, 10, 5, 0, 0, time.UTC)
	atMin := 0
	next := computeNextAligned(now, 30, &atMin)
	want := time.Date(2026, 1, 1, 10, 5, 30, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("got %s, want %s", next, want)
	}
}

func TestDeriveValidMinutes(t *testing.T) {
	// 15m from anchor 0
	got := deriveValidMinutes(0, 15)
	if len(got) != 4 || got[0] != 0 || got[1] != 15 || got[2] != 30 || got[3] != 45 {
		t.Errorf("got %v, want [0 15 30 45]", got)
	}

	// 10m from anchor 5
	got = deriveValidMinutes(5, 10)
	if len(got) != 6 || got[0] != 5 || got[1] != 15 || got[2] != 25 {
		t.Errorf("got %v, want [5 15 25 35 45 55]", got)
	}

	// 20m from anchor 10
	got = deriveValidMinutes(10, 20)
	if len(got) != 3 || got[0] != 10 || got[1] != 30 || got[2] != 50 {
		t.Errorf("got %v, want [10 30 50]", got)
	}

	// multi-hour (intervalMin >= 60) → just anchor
	got = deriveValidMinutes(30, 60)
	if len(got) != 1 || got[0] != 30 {
		t.Errorf("got %v, want [30]", got)
	}

	// sub-minute (intervalMin == 0) → nil
	got = deriveValidMinutes(0, 0)
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestUpdateAfterRun_AtMinuteAligns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	now := NowUTC().Format(time.RFC3339)
	conn.Exec("INSERT INTO cron_jobs (name, next_run_at) VALUES (?, ?)", "aligned", now)

	atMin := 30
	UpdateAfterRun(conn, "aligned", 3600, 0, 1.5, "ok", &atMin)

	var nextRunStr string
	conn.QueryRow("SELECT next_run_at FROM cron_jobs WHERE name = ?", "aligned").Scan(&nextRunStr)
	nextRun, _ := time.Parse(time.RFC3339, nextRunStr)
	if nextRun.Minute() != 30 {
		t.Errorf("next_run minute = %d, want 30", nextRun.Minute())
	}
}

func TestComputeNextRun_NoConstraints(t *testing.T) {
	job := &config.JobConfig{Name: "test", Commands: []string{"echo"}, IntervalSeconds: 300}
	// Future next_run_at should be returned as-is
	tomorrow := NowUTC().Add(24 * time.Hour).Truncate(24 * time.Hour)
	want := tomorrow.Add(10 * time.Hour)
	nextRun := want.Format(time.RFC3339)
	result := ComputeNextRun(job, nextRun, "UTC")
	if !result.Equal(want) {
		t.Errorf("got %s, want %s", result, want)
	}
}

func TestComputeNextRun_PastNextRunFallsBackToNow(t *testing.T) {
	job := &config.JobConfig{Name: "test", Commands: []string{"echo"}, IntervalSeconds: 300}
	past := "2020-01-01T00:00:00Z"
	result := ComputeNextRun(job, past, "UTC")
	now := NowUTC()
	// Should be approximately now (within a few seconds)
	diff := result.Sub(now)
	if diff < -time.Second || diff > 2*time.Second {
		t.Errorf("expected ~now, got %s (diff=%s)", result, diff)
	}
}

func TestComputeNextRun_EmptyNextRun(t *testing.T) {
	job := &config.JobConfig{Name: "test", Commands: []string{"echo"}, IntervalSeconds: 300}
	result := ComputeNextRun(job, "", "UTC")
	now := NowUTC()
	diff := result.Sub(now)
	if diff < -time.Second || diff > 2*time.Second {
		t.Errorf("expected ~now, got %s (diff=%s)", result, diff)
	}
}

func TestComputeNextRun_ActiveHoursWithinWindow(t *testing.T) {
	hours := [2]int{540, 1020}
	job := &config.JobConfig{Name: "test", Commands: []string{"echo"}, IntervalSeconds: 3600, ActiveHours: &hours}
	// Use tomorrow at 10:00 UTC — within the 9-17 UTC window
	tomorrow := NowUTC().Add(24 * time.Hour).Truncate(24 * time.Hour)
	want := tomorrow.Add(10 * time.Hour)
	nextRun := want.Format(time.RFC3339)
	result := ComputeNextRun(job, nextRun, "UTC")
	if !result.Equal(want) {
		t.Errorf("got %s, want %s", result, want)
	}
}

func TestComputeNextRun_ActiveHoursBeforeWindow(t *testing.T) {
	hours := [2]int{540, 1020}
	job := &config.JobConfig{Name: "test", Commands: []string{"echo"}, IntervalSeconds: 3600, ActiveHours: &hours}
	// Tomorrow 08:00 UTC is before the 9-17 window — should jump to 09:00
	tomorrow := NowUTC().Add(24 * time.Hour).Truncate(24 * time.Hour)
	nextRun := tomorrow.Add(8 * time.Hour)
	want := tomorrow.Add(9 * time.Hour)
	result := ComputeNextRun(job, nextRun.Format(time.RFC3339), "UTC")
	if !result.Equal(want) {
		t.Errorf("got %s, want %s", result, want)
	}
}

func TestComputeNextRun_ActiveHoursAfterWindow(t *testing.T) {
	hours := [2]int{540, 1020}
	job := &config.JobConfig{Name: "test", Commands: []string{"echo"}, IntervalSeconds: 3600, ActiveHours: &hours}
	// Tomorrow 18:00 UTC is after the 9-17 window — should jump to next day 09:00
	tomorrow := NowUTC().Add(24 * time.Hour).Truncate(24 * time.Hour)
	nextRun := tomorrow.Add(18 * time.Hour)
	want := tomorrow.Add(24*time.Hour + 9*time.Hour)
	result := ComputeNextRun(job, nextRun.Format(time.RFC3339), "UTC")
	if !result.Equal(want) {
		t.Errorf("got %s, want %s", result, want)
	}
}

func TestComputeNextRun_ActiveHoursOvernight(t *testing.T) {
	hours := [2]int{1320, 360}
	job := &config.JobConfig{Name: "test", Commands: []string{"echo"}, IntervalSeconds: 3600, ActiveHours: &hours}
	// Tomorrow 03:00 UTC is within the 22-6 overnight window
	tomorrow := NowUTC().Add(24 * time.Hour).Truncate(24 * time.Hour)
	want := tomorrow.Add(3 * time.Hour)
	result := ComputeNextRun(job, want.Format(time.RFC3339), "UTC")
	if !result.Equal(want) {
		t.Errorf("got %s, want %s", result, want)
	}
}

func TestComputeNextRun_ActiveHoursOvernightGap(t *testing.T) {
	hours := [2]int{1320, 360}
	job := &config.JobConfig{Name: "test", Commands: []string{"echo"}, IntervalSeconds: 3600, ActiveHours: &hours}
	// Tomorrow 12:00 UTC is in the gap — should jump to 22:00 same day
	tomorrow := NowUTC().Add(24 * time.Hour).Truncate(24 * time.Hour)
	nextRun := tomorrow.Add(12 * time.Hour)
	want := tomorrow.Add(22 * time.Hour)
	result := ComputeNextRun(job, nextRun.Format(time.RFC3339), "UTC")
	if !result.Equal(want) {
		t.Errorf("got %s, want %s", result, want)
	}
}

func TestComputeNextRun_ActiveDaysFilter(t *testing.T) {
	// Weekdays only
	weekdays := [7]bool{false, true, true, true, true, true, false}
	job := &config.JobConfig{Name: "test", Commands: []string{"echo"}, IntervalSeconds: 3600, ActiveDays: &weekdays}
	// Find the next Saturday, then expect it to skip to Monday
	now := NowUTC()
	daysUntilSat := (time.Saturday - now.Weekday()) % 7
	if daysUntilSat == 0 {
		daysUntilSat = 7
	}
	saturday := now.Add(time.Duration(daysUntilSat) * 24 * time.Hour).Truncate(24 * time.Hour)
	nextRun := saturday.Add(10 * time.Hour)
	want := saturday.Add(2 * 24 * time.Hour) // Monday 00:00
	result := ComputeNextRun(job, nextRun.Format(time.RFC3339), "UTC")
	if !result.Equal(want) {
		t.Errorf("got %s, want %s", result, want)
	}
}

func TestComputeNextRun_ActiveDaysAndHoursCombined(t *testing.T) {
	hours := [2]int{540, 1020}
	weekdays := [7]bool{false, true, true, true, true, true, false}
	job := &config.JobConfig{Name: "test", Commands: []string{"echo"}, IntervalSeconds: 3600, ActiveHours: &hours, ActiveDays: &weekdays}
	// Find next Friday at 18:00 — after hours on a weekday, should jump to Monday 09:00
	now := NowUTC()
	daysUntilFri := (time.Friday - now.Weekday()) % 7
	if daysUntilFri == 0 {
		daysUntilFri = 7
	}
	friday := now.Add(time.Duration(daysUntilFri) * 24 * time.Hour).Truncate(24 * time.Hour)
	nextRun := friday.Add(18 * time.Hour)
	want := friday.Add(3*24*time.Hour + 9*time.Hour) // Monday 09:00
	result := ComputeNextRun(job, nextRun.Format(time.RFC3339), "UTC")
	if !result.Equal(want) {
		t.Errorf("got %s, want %s", result, want)
	}
}

func TestIsOnActiveDayAt_Nil(t *testing.T) {
	loc, _ := time.LoadLocation("UTC")
	if !IsOnActiveDayAt(nil, time.Now(), loc) {
		t.Error("nil days should always be active")
	}
}

func TestIsInActiveHoursAt_Nil(t *testing.T) {
	loc, _ := time.LoadLocation("UTC")
	if !IsInActiveHoursAt(nil, time.Now(), loc) {
		t.Error("nil hours should always be active")
	}
}

func TestIsInActiveHoursAt_NormalRange(t *testing.T) {
	loc, _ := time.LoadLocation("UTC")
	hours := [2]int{540, 1020}
	t09 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	t12 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	t17 := time.Date(2026, 1, 1, 17, 0, 0, 0, time.UTC)
	t08 := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
	if !IsInActiveHoursAt(&hours, t09, loc) {
		t.Error("9:00 should be in 9-17")
	}
	if !IsInActiveHoursAt(&hours, t12, loc) {
		t.Error("12:00 should be in 9-17")
	}
	if IsInActiveHoursAt(&hours, t17, loc) {
		t.Error("17:00 should not be in 9-17 (exclusive end)")
	}
	if IsInActiveHoursAt(&hours, t08, loc) {
		t.Error("8:00 should not be in 9-17")
	}
}

func TestIsInActiveHoursAt_OvernightRange(t *testing.T) {
	loc, _ := time.LoadLocation("UTC")
	hours := [2]int{1320, 360}
	t22 := time.Date(2026, 1, 1, 22, 0, 0, 0, time.UTC)
	t03 := time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC)
	t10 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	t06 := time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC)
	if !IsInActiveHoursAt(&hours, t22, loc) {
		t.Error("22:00 should be in 22-6")
	}
	if !IsInActiveHoursAt(&hours, t03, loc) {
		t.Error("03:00 should be in 22-6")
	}
	if IsInActiveHoursAt(&hours, t10, loc) {
		t.Error("10:00 should not be in 22-6")
	}
	if IsInActiveHoursAt(&hours, t06, loc) {
		t.Error("06:00 should not be in 22-6 (exclusive end)")
	}
}

func TestIsInActiveHoursAt_MinuteBoundary(t *testing.T) {
	loc, _ := time.LoadLocation("UTC")
	hours := [2]int{571, 960} // 09:31–16:00
	before := time.Date(2026, 1, 1, 9, 30, 59, 0, time.UTC)
	first := time.Date(2026, 1, 1, 9, 31, 0, 0, time.UTC)
	last := time.Date(2026, 1, 1, 15, 59, 59, 0, time.UTC)
	after := time.Date(2026, 1, 1, 16, 0, 0, 0, time.UTC)
	if IsInActiveHoursAt(&hours, before, loc) {
		t.Error("9:30:59 should be before a 9:31 window start")
	}
	if !IsInActiveHoursAt(&hours, first, loc) {
		t.Error("9:31:00 should be in a 9:31–16:00 window")
	}
	if !IsInActiveHoursAt(&hours, last, loc) {
		t.Error("15:59:59 should be in a 9:31–16:00 window")
	}
	if IsInActiveHoursAt(&hours, after, loc) {
		t.Error("16:00:00 should be outside a 9:31–16:00 window (exclusive end)")
	}
}

func TestComputeNextRun_ActiveHoursMinuteStartJump(t *testing.T) {
	hours := [2]int{571, 960} // 09:31–16:00
	job := &config.JobConfig{Name: "test", Commands: []string{"echo"}, IntervalSeconds: 60, ActiveHours: &hours}
	// Tomorrow 08:00 UTC is before the 9:31 window start — should jump to 09:31
	tomorrow := NowUTC().Add(24 * time.Hour).Truncate(24 * time.Hour)
	nextRun := tomorrow.Add(8 * time.Hour)
	want := tomorrow.Add(571 * time.Minute)
	result := ComputeNextRun(job, nextRun.Format(time.RFC3339), "UTC")
	if !result.Equal(want) {
		t.Errorf("got %s, want %s", result, want)
	}
}

func TestComputeNextRun_ActiveHoursOvernightGapMinutes(t *testing.T) {
	hours := [2]int{1350, 360} // 22:30–06:00
	job := &config.JobConfig{Name: "test", Commands: []string{"echo"}, IntervalSeconds: 3600, ActiveHours: &hours}
	// Tomorrow 12:00 UTC is in the gap — should jump to 22:30 same day
	tomorrow := NowUTC().Add(24 * time.Hour).Truncate(24 * time.Hour)
	nextRun := tomorrow.Add(12 * time.Hour)
	want := tomorrow.Add(1350 * time.Minute)
	result := ComputeNextRun(job, nextRun.Format(time.RFC3339), "UTC")
	if !result.Equal(want) {
		t.Errorf("got %s, want %s", result, want)
	}
}
