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
	return db, nil
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

func GetDueJobs(db *sql.DB, jobs map[string]*config.JobConfig, tzName string) []string {
	now := NowUTC().Format(time.RFC3339)
	rows, err := db.Query("SELECT name FROM cron_jobs WHERE next_run_at <= ? ORDER BY next_run_at", now)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var due []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		job, ok := jobs[name]
		if !ok {
			continue
		}
		if !IsInActiveHours(job.ActiveHours, tzName) {
			continue
		}
		due = append(due, name)
	}
	return due
}

func UpdateAfterRun(db *sql.DB, name string, intervalSeconds int, rc int, elapsed float64, status string) {
	now := NowUTC()
	nextRun := now.Add(time.Duration(intervalSeconds) * time.Second).Format(time.RFC3339)
	failInc := 0
	if rc != 0 {
		failInc = 1
	}
	db.Exec(
		`UPDATE cron_jobs SET last_run_at = ?, next_run_at = ?, last_status = ?,
		 last_duration_s = ?, run_count = run_count + 1, fail_count = fail_count + ?
		 WHERE name = ?`,
		now.Format(time.RFC3339), nextRun, status, elapsed, failInc, name,
	)
}
