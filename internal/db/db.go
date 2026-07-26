package db

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
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

// isDuplicateColumn reports whether err is SQLite's "duplicate column name"
// error, which an ALTER TABLE migration returns when it has already been applied.
func isDuplicateColumn(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate column name")
}

func applyMigration(db *sql.DB, stmt string) error {
	if _, err := db.Exec(stmt); err != nil && !isDuplicateColumn(err) {
		return fmt.Errorf("applying migration %q: %w", stmt, err)
	}
	return nil
}

func Open(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=10000"} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("setting %s: %w", pragma, err)
		}
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating schema: %w", err)
	}
	// Migrations: running_since column, dispatcher_meta table, force_next column
	for _, m := range []string{migration1, migration2, migration3} {
		if err := applyMigration(db, m); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}

func ClearAllRunning(db *sql.DB) error {
	if _, err := db.Exec("UPDATE cron_jobs SET running_since = NULL WHERE running_since IS NOT NULL"); err != nil {
		return fmt.Errorf("clearing running state: %w", err)
	}
	return nil
}

func ClearStaleRunning(db *sql.DB, jobs map[string]*config.JobConfig) error {
	now := NowUTC()
	for name, job := range jobs {
		var runningSince sql.NullString
		err := db.QueryRow("SELECT running_since FROM cron_jobs WHERE name = ?", name).Scan(&runningSince)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("reading running state for %s: %w", name, err)
		}
		if !runningSince.Valid {
			continue
		}

		started, err := time.Parse(time.RFC3339, runningSince.String)
		if err != nil {
			if err := ClearRunning(db, name); err != nil {
				return err
			}
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
			if _, err := db.Exec("UPDATE cron_jobs SET running_since = NULL, last_status = ? WHERE name = ?",
				"failed:stale", name); err != nil {
				return fmt.Errorf("marking %s stale: %w", name, err)
			}
			if _, err := db.Exec("INSERT INTO job_runs (name, run_at, status, exit_code, duration_s) VALUES (?, ?, ?, ?, ?)",
				name, runningSince.String, "failed:stale", -3, elapsed.Seconds()); err != nil {
				return fmt.Errorf("recording stale run for %s: %w", name, err)
			}
		} else if !job.Adhoc {
			// Non-adhoc but within timeout — still stale since we hold the lock
			if err := ClearRunning(db, name); err != nil {
				return err
			}
		}
	}
	return nil
}

func MarkRunning(db *sql.DB, name string) error {
	now := NowUTC().Format(time.RFC3339)
	if _, err := db.Exec("UPDATE cron_jobs SET running_since = ? WHERE name = ?", now, name); err != nil {
		return fmt.Errorf("marking %s running: %w", name, err)
	}
	return nil
}

func ClearRunning(db *sql.DB, name string) error {
	if _, err := db.Exec("UPDATE cron_jobs SET running_since = NULL WHERE name = ?", name); err != nil {
		return fmt.Errorf("clearing running state for %s: %w", name, err)
	}
	return nil
}

func NowUTC() time.Time {
	return time.Now().UTC()
}

func EnsureJobs(db *sql.DB, jobs map[string]*config.JobConfig) error {
	now := NowUTC().Format(time.RFC3339)
	for name := range jobs {
		if _, err := db.Exec("INSERT OR IGNORE INTO cron_jobs (name, next_run_at) VALUES (?, ?)", name, now); err != nil {
			return fmt.Errorf("registering job %s: %w", name, err)
		}
	}
	return nil
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
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		// Config validation rejects unknown timezones; stay defensive here and
		// fall back to UTC rather than computing against a nil location.
		fmt.Fprintf(os.Stderr, "Warning: unknown timezone %q, using UTC: %v\n", tzName, err)
		loc = time.UTC
	}

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

func GetDueJobs(db *sql.DB, jobs map[string]*config.JobConfig, tzName string) ([]string, error) {
	now := NowUTC().Format(time.RFC3339)
	rows, err := db.Query("SELECT name, force_next FROM cron_jobs WHERE next_run_at <= ? ORDER BY next_run_at", now)
	if err != nil {
		return nil, fmt.Errorf("querying due jobs: %w", err)
	}
	defer rows.Close()

	var due []string
	for rows.Next() {
		var name string
		var forceNext int
		if err := rows.Scan(&name, &forceNext); err != nil {
			return nil, fmt.Errorf("scanning due jobs: %w", err)
		}
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading due jobs: %w", err)
	}
	return due, nil
}

func UpdateAfterRun(db *sql.DB, name string, intervalSeconds int, rc int, elapsed float64, status string, atMinute *int) error {
	now := NowUTC()
	nextRun := computeNextAligned(now, intervalSeconds, atMinute).Format(time.RFC3339)
	failInc := 0
	if rc != 0 && status != "interrupted" {
		failInc = 1
	}
	if _, err := db.Exec(
		`UPDATE cron_jobs SET last_run_at = ?, next_run_at = ?, last_status = ?,
		 last_duration_s = ?, run_count = run_count + 1, fail_count = fail_count + ?,
		 force_next = 0
		 WHERE name = ?`,
		now.Format(time.RFC3339), nextRun, status, elapsed, failInc, name,
	); err != nil {
		return fmt.Errorf("updating state for %s: %w", name, err)
	}
	if _, err := db.Exec(
		`INSERT INTO job_runs (name, run_at, status, exit_code, duration_s) VALUES (?, ?, ?, ?, ?)`,
		name, now.Format(time.RFC3339), status, rc, elapsed,
	); err != nil {
		return fmt.Errorf("recording run for %s: %w", name, err)
	}
	return nil
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
		if err := rows.Scan(&a.Name, &a.TotalRuns, &a.PassCount, &a.FailCount, &avgDur); err != nil {
			return nil, fmt.Errorf("scanning analytics: %w", err)
		}
		if avgDur.Valid {
			a.AvgDuration = avgDur.Float64
		}
		if a.TotalRuns > 0 {
			a.SuccessRate = float64(a.PassCount) / float64(a.TotalRuns) * 100
		}
		analytics[a.Name] = &a
		result = append(result, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading analytics: %w", err)
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
		return nil, fmt.Errorf("querying 7-day analytics: %w", err)
	}
	defer rows7d.Close()

	for rows7d.Next() {
		var name string
		var total, pass int
		if err := rows7d.Scan(&name, &total, &pass); err != nil {
			return nil, fmt.Errorf("scanning 7-day analytics: %w", err)
		}
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
	if err := rows7d.Err(); err != nil {
		return nil, fmt.Errorf("reading 7-day analytics: %w", err)
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
		if err := rows.Scan(&e.RunAt, &e.Status, &e.ExitCode, &e.Duration); err != nil {
			return nil, fmt.Errorf("scanning history: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading history: %w", err)
	}
	return entries, nil
}

func SetMeta(db *sql.DB, key, value string) error {
	if _, err := db.Exec("INSERT INTO dispatcher_meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = ?", key, value, value); err != nil {
		return fmt.Errorf("setting meta %s: %w", key, err)
	}
	return nil
}

// GetMeta returns the stored value for key. A missing key yields ("", nil);
// any other failure is returned so callers can distinguish it from "unset".
func GetMeta(db *sql.DB, key string) (string, error) {
	var value string
	err := db.QueryRow("SELECT value FROM dispatcher_meta WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading meta %s: %w", key, err)
	}
	return value, nil
}

func PurgeHistory(db *sql.DB, retentionDays int) (int64, error) {
	cutoff := NowUTC().AddDate(0, 0, -retentionDays).Format(time.RFC3339)
	result, err := db.Exec("DELETE FROM job_runs WHERE run_at < ?", cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
