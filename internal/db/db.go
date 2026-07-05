package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/blindly/dispatcher/internal/config"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS cron_jobs (
    name          TEXT PRIMARY KEY,
    last_run_at   TEXT,
    next_run_at   TEXT NOT NULL,
    last_status   TEXT,
    last_duration_s REAL,
    run_count     INTEGER DEFAULT 0,
    fail_count    INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS job_runs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    run_at      TEXT NOT NULL,
    status      TEXT NOT NULL,
    exit_code   INTEGER,
    duration_s  REAL
);

CREATE INDEX IF NOT EXISTS idx_job_runs_name_run_at ON job_runs(name, run_at);`

const migration1 = `ALTER TABLE cron_jobs ADD COLUMN running_since TEXT;`

const migration3 = `ALTER TABLE cron_jobs ADD COLUMN force_next INTEGER DEFAULT 0;`

const migration2 = `CREATE TABLE IF NOT EXISTS dispatcher_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);`

func Open(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA busy_timeout=10000")
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating schema: %w", err)
	}
	// Migration: add running_since column if missing
	db.Exec(migration1)
	// Migration: add dispatcher_meta table
	db.Exec(migration2)
	// Migration: add force_next column
	db.Exec(migration3)
	return db, nil
}

func ClearAllRunning(db *sql.DB) {
	db.Exec("UPDATE cron_jobs SET running_since = NULL WHERE running_since IS NOT NULL")
}

func ClearStaleRunning(db *sql.DB, jobs map[string]*config.JobConfig) {
	now := NowUTC()
	for name, job := range jobs {
		var runningSince sql.NullString
		db.QueryRow("SELECT running_since FROM cron_jobs WHERE name = ?", name).Scan(&runningSince)
		if !runningSince.Valid {
			continue
		}

		started, err := time.Parse(time.RFC3339, runningSince.String)
		if err != nil {
			db.Exec("UPDATE cron_jobs SET running_since = NULL WHERE name = ?", name)
			continue
		}

		elapsed := now.Sub(started)
		timeout := time.Duration(job.Timeout) * time.Second
		if timeout <= 0 {
			timeout = 600 * time.Second
		}

		if elapsed > timeout {
			// Stale — exceeded timeout, mark as failed
			fmt.Printf("  STALE %s — was running for %s (timeout %s), marking as failed\n",
				name, elapsed.Round(time.Second), timeout)
			db.Exec("UPDATE cron_jobs SET running_since = NULL, last_status = ? WHERE name = ?",
				"failed:stale", name)
			db.Exec("INSERT INTO job_runs (name, run_at, status, exit_code, duration_s) VALUES (?, ?, ?, ?, ?)",
				name, runningSince.String, "failed:stale", -3, elapsed.Seconds())
		} else if !job.Adhoc {
			// Non-adhoc but within timeout — still stale since we hold the lock
			db.Exec("UPDATE cron_jobs SET running_since = NULL WHERE name = ?", name)
		}
	}
}

func MarkRunning(db *sql.DB, name string) {
	now := NowUTC().Format(time.RFC3339)
	db.Exec("UPDATE cron_jobs SET running_since = ? WHERE name = ?", now, name)
}

func ClearRunning(db *sql.DB, name string) {
	db.Exec("UPDATE cron_jobs SET running_since = NULL WHERE name = ?", name)
}

func NowUTC() time.Time {
	return time.Now().UTC()
}

func EnsureJobs(db *sql.DB, jobs map[string]*config.JobConfig) {
	now := NowUTC().Format(time.RFC3339)
	for name := range jobs {
		db.Exec("INSERT OR IGNORE INTO cron_jobs (name, next_run_at) VALUES (?, ?)", name, now)
	}
}

func IsInActiveHours(hours *[2]int, tzName string) bool {
	if hours == nil {
		return true
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return true
	}
	nowHour := time.Now().In(loc).Hour()
	start, end := hours[0], hours[1]
	if start <= end {
		return nowHour >= start && nowHour < end
	}
	return nowHour >= start || nowHour < end
}

func IsOnActiveDay(days *[7]bool, tzName string) bool {
	if days == nil {
		return true
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return true
	}
	return days[int(time.Now().In(loc).Weekday())]
}

// computeNextAligned computes the next run time. When atMinute is nil it simply
// adds the interval to now. When atMinute is set it derives valid minutes from
// the interval, then snaps to the earliest valid minute after now — this prevents
// schedule drift. For multi-hour intervals (>=60m), it acts as a single anchor.
func computeNextAligned(now time.Time, intervalSec int, atMinute *int) time.Time {
	if atMinute == nil || intervalSec <= 0 {
		return now.Add(time.Duration(intervalSec) * time.Second)
	}

	// Derive the set of valid minutes from the interval.
	intervalMin := intervalSec / 60
	validMinutes := deriveValidMinutes(*atMinute, intervalMin)
	if validMinutes == nil {
		// Sub-minute interval — no alignment applies, fall back to plain addition.
		return now.Add(time.Duration(intervalSec) * time.Second)
	}

	// Find the earliest valid minute strictly after now within the current hour.
	var best time.Time
	for _, m := range validMinutes {
		c := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), m, 0, 0, now.Location())
		if !c.After(now) {
			continue
		}
		if best.IsZero() || c.Before(best) {
			best = c
		}
	}
	if best.IsZero() {
		// No valid minute left in the current hour; pick the earliest in the next hour.
		nextHour := now.Add(time.Hour).Truncate(time.Minute)
		earliest := 60
		for _, m := range validMinutes {
			if m < earliest {
				earliest = m
			}
		}
		best = time.Date(nextHour.Year(), nextHour.Month(), nextHour.Day(), nextHour.Hour(), earliest, 0, 0, nextHour.Location())
	}

	// For multi-hour intervals, add extra hours beyond the first aligned minute
	// so the minimum interval is still roughly respected.
	intervalHours := intervalSec / 3600
	if intervalHours > 1 {
		best = best.Add(time.Duration(intervalHours-1) * time.Hour)
	}

	return best
}

// deriveValidMinutes returns the set of minute-of-hour values that fall on the
// interval cadence. For sub-hour intervals (intervalMin > 0 && < 60) it cycles
// through the hour (e.g. 15m with anchor 0 → [0, 15, 30, 45]). For multi-hour
// intervals it returns just the anchor minute.
func deriveValidMinutes(anchor int, intervalMin int) []int {
	if intervalMin >= 60 {
		return []int{anchor}
	}
	if intervalMin <= 0 {
		return nil // sub-minute interval — no alignment
	}
	var out []int
	m := anchor % 60
	seen := make(map[int]bool)
	for {
		if !seen[m] {
			out = append(out, m)
			seen[m] = true
		} else {
			break
		}
		m = (m + intervalMin) % 60
	}
	return out
}

// ComputeNextRun returns the next time a job will actually execute, considering
// at_minute alignment, active_hours, and active days. It starts from the stored
// next_run_at (or now if not set/already past) and advances forward until all
// constraints are satisfied.
func ComputeNextRun(job *config.JobConfig, nextRunStr string, tzName string) time.Time {
	now := NowUTC()
	loc, _ := time.LoadLocation(tzName)

	// Parse next_run_at; fall back to now if missing or already past
	candidate := now
	if nextRunStr != "" {
		if t, err := time.Parse(time.RFC3339, nextRunStr); err == nil && t.After(now) {
			candidate = t
		}
	}

	// If no scheduling constraints, return as-is
	if job.ActiveHours == nil && job.ActiveDays == nil {
		return candidate
	}

	// Advance up to 8 days (covers a full week + 1 day) looking for a valid slot
	deadline := now.AddDate(0, 0, 8)
	for candidate.Before(deadline) {
		if !IsOnActiveDayAt(job.ActiveDays, candidate, loc) {
			// Skip to end of this day in the job's timezone
			localT := candidate.In(loc)
			nextDay := time.Date(localT.Year(), localT.Month(), localT.Day()+1, 0, 0, 0, 0, loc)
			candidate = nextDay.UTC()
			continue
		}
		if !IsInActiveHoursAt(job.ActiveHours, candidate, loc) {
			// If before active window, jump to start of window
			localT := candidate.In(loc)
			startHour := job.ActiveHours[0]
			// Check for overnight wrap: if end <= start and we're past end, jump to start of next day's window
			if job.ActiveHours[1] <= job.ActiveHours[0] && localT.Hour() >= job.ActiveHours[1] && localT.Hour() < job.ActiveHours[0] {
				// We're in the gap between end and start of a wrapping window — jump to start
				jump := time.Date(localT.Year(), localT.Month(), localT.Day(), startHour, 0, 0, 0, loc)
				if !jump.After(candidate) {
					jump = jump.AddDate(0, 0, 1)
				}
				candidate = jump.UTC()
				continue
			}
			jump := time.Date(localT.Year(), localT.Month(), localT.Day(), startHour, 0, 0, 0, loc)
			if !jump.After(candidate) {
				jump = jump.AddDate(0, 0, 1)
			}
			candidate = jump.UTC()
			continue
		}
		// All constraints satisfied
		return candidate
	}

	// Fallback: return the original candidate
	return candidate
}

// IsOnActiveDayAt checks if the given time falls on an active day.
func IsOnActiveDayAt(days *[7]bool, t time.Time, loc *time.Location) bool {
	if days == nil {
		return true
	}
	if loc != nil {
		return days[int(t.In(loc).Weekday())]
	}
	return days[int(t.Weekday())]
}

// IsInActiveHoursAt checks if the given time falls within active hours.
func IsInActiveHoursAt(hours *[2]int, t time.Time, loc *time.Location) bool {
	if hours == nil {
		return true
	}
	var hour int
	if loc != nil {
		hour = t.In(loc).Hour()
	} else {
		hour = t.Hour()
	}
	start, end := hours[0], hours[1]
	if start <= end {
		return hour >= start && hour < end
	}
	return hour >= start || hour < end
}

func GetDueJobs(db *sql.DB, jobs map[string]*config.JobConfig, tzName string) []string {
	now := NowUTC().Format(time.RFC3339)
	rows, err := db.Query("SELECT name, force_next FROM cron_jobs WHERE next_run_at <= ? ORDER BY next_run_at", now)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var due []string
	for rows.Next() {
		var name string
		var forceNext int
		rows.Scan(&name, &forceNext)
		job, ok := jobs[name]
		if !ok {
			continue
		}
		if job.Adhoc || job.Paused {
			continue
		}
		if forceNext == 0 && (!IsInActiveHours(job.ActiveHours, tzName) || !IsOnActiveDay(job.ActiveDays, tzName)) {
			continue
		}
		due = append(due, name)
	}
	return due
}

func UpdateAfterRun(db *sql.DB, name string, intervalSeconds int, rc int, elapsed float64, status string, atMinute *int) {
	now := NowUTC()
	nextRun := computeNextAligned(now, intervalSeconds, atMinute).Format(time.RFC3339)
	failInc := 0
	if rc != 0 && status != "interrupted" {
		failInc = 1
	}
	db.Exec(
		`UPDATE cron_jobs SET last_run_at = ?, next_run_at = ?, last_status = ?,
		 last_duration_s = ?, run_count = run_count + 1, fail_count = fail_count + ?,
		 force_next = 0
		 WHERE name = ?`,
		now.Format(time.RFC3339), nextRun, status, elapsed, failInc, name,
	)
	db.Exec(
		`INSERT INTO job_runs (name, run_at, status, exit_code, duration_s) VALUES (?, ?, ?, ?, ?)`,
		name, now.Format(time.RFC3339), status, rc, elapsed,
	)
}

type JobAnalytics struct {
	Name        string
	TotalRuns   int
	PassCount   int
	FailCount   int
	SuccessRate float64
	AvgDuration float64
	Last7dRuns  int
	Last7dPass  int
}

func GetAnalytics(db *sql.DB) ([]JobAnalytics, error) {
	sevenDaysAgo := NowUTC().AddDate(0, 0, -7).Format(time.RFC3339)

	rows, err := db.Query(`
		SELECT
			name,
			COUNT(*) as total,
			SUM(CASE WHEN exit_code = 0 THEN 1 ELSE 0 END) as pass,
			SUM(CASE WHEN exit_code != 0 THEN 1 ELSE 0 END) as fail,
			AVG(duration_s) as avg_dur
		FROM job_runs
		GROUP BY name
		ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	analytics := make(map[string]*JobAnalytics)
	var result []JobAnalytics

	for rows.Next() {
		var a JobAnalytics
		var avgDur sql.NullFloat64
		rows.Scan(&a.Name, &a.TotalRuns, &a.PassCount, &a.FailCount, &avgDur)
		if avgDur.Valid {
			a.AvgDuration = avgDur.Float64
		}
		if a.TotalRuns > 0 {
			a.SuccessRate = float64(a.PassCount) / float64(a.TotalRuns) * 100
		}
		analytics[a.Name] = &a
		result = append(result, a)
	}

	// Get last 7 days stats
	rows7d, err := db.Query(`
		SELECT
			name,
			COUNT(*) as total,
			SUM(CASE WHEN exit_code = 0 THEN 1 ELSE 0 END) as pass
		FROM job_runs
		WHERE run_at >= ?
		GROUP BY name
	`, sevenDaysAgo)
	if err != nil {
		return result, nil
	}
	defer rows7d.Close()

	for rows7d.Next() {
		var name string
		var total, pass int
		rows7d.Scan(&name, &total, &pass)
		if a, ok := analytics[name]; ok {
			a.Last7dRuns = total
			a.Last7dPass = pass
			// Update in result slice
			for i := range result {
				if result[i].Name == name {
					result[i].Last7dRuns = total
					result[i].Last7dPass = pass
				}
			}
		}
	}

	return result, nil
}

type HistoryEntry struct {
	RunAt    string
	Status   string
	ExitCode int
	Duration float64
}

func GetHistory(db *sql.DB, name string, limit int) ([]HistoryEntry, error) {
	rows, err := db.Query(
		`SELECT run_at, status, exit_code, duration_s FROM job_runs
		 WHERE name = ? ORDER BY run_at DESC LIMIT ?`,
		name, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		rows.Scan(&e.RunAt, &e.Status, &e.ExitCode, &e.Duration)
		entries = append(entries, e)
	}
	return entries, nil
}

func SetMeta(db *sql.DB, key, value string) {
	db.Exec("INSERT INTO dispatcher_meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = ?", key, value, value)
}

func GetMeta(db *sql.DB, key string) string {
	var value string
	err := db.QueryRow("SELECT value FROM dispatcher_meta WHERE key = ?", key).Scan(&value)
	if err != nil {
		return ""
	}
	return value
}

func PurgeHistory(db *sql.DB, retentionDays int) (int64, error) {
	cutoff := NowUTC().AddDate(0, 0, -retentionDays).Format(time.RFC3339)
	result, err := db.Exec("DELETE FROM job_runs WHERE run_at < ?", cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
