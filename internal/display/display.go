package display

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/blindly/dispatcher/internal/config"
	"github.com/blindly/dispatcher/internal/db"
)

func FormatInterval(seconds int) string {
	switch {
	case seconds >= 604800:
		return fmt.Sprintf("%dw", seconds/604800)
	case seconds >= 86400:
		return fmt.Sprintf("%dd", seconds/86400)
	case seconds >= 3600:
		return fmt.Sprintf("%dh", seconds/3600)
	default:
		return fmt.Sprintf("%dm", seconds/60)
	}
}

func FormatDt(iso string) string {
	if iso == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		if len(iso) >= 19 {
			return iso[:19]
		}
		return iso
	}
	return t.Format("2006-01-02 15:04")
}

func PrintQuickStatus(conn *sql.DB, jobs map[string]*config.JobConfig, tzName string) {
	now := db.NowUTC()

	var totalJobs, dueCount, totalRuns, totalFails int
	var lastRunAt sql.NullString

	totalJobs = len(jobs)

	rows, err := conn.Query("SELECT name, next_run_at, last_run_at, run_count, fail_count FROM cron_jobs ORDER BY last_run_at DESC")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer rows.Close()

	first := true
	for rows.Next() {
		var name, nextRun string
		var lr sql.NullString
		var runs, fails int
		rows.Scan(&name, &nextRun, &lr, &runs, &fails)

		job, ok := jobs[name]
		if !ok {
			continue
		}

		if first && lr.Valid {
			lastRunAt = lr
			first = false
		}

		totalRuns += runs
		totalFails += fails

		if nextRun <= now.Format(time.RFC3339) {
			if db.IsInActiveHours(job.ActiveHours, tzName) {
				dueCount++
			}
		}
	}

	lastRunStr := "never"
	if lastRunAt.Valid {
		t, err := time.Parse(time.RFC3339, lastRunAt.String)
		if err == nil {
			ago := now.Sub(t)
			if ago < time.Minute {
				lastRunStr = fmt.Sprintf("%ds ago", int(ago.Seconds()))
			} else if ago < time.Hour {
				lastRunStr = fmt.Sprintf("%dm ago", int(ago.Minutes()))
			} else if ago < 24*time.Hour {
				lastRunStr = fmt.Sprintf("%dh ago", int(ago.Hours()))
			} else {
				lastRunStr = fmt.Sprintf("%dd ago", int(ago.Hours()/24))
			}
		}
	}

	fmt.Printf("Last run: %s | %d jobs | %d due | %d total runs | %d failures\n",
		lastRunStr, totalJobs, dueCount, totalRuns, totalFails)
}

func PrintStatus(conn *sql.DB, jobs map[string]*config.JobConfig, tzName string) {
	now := db.NowUTC()

	rows, err := conn.Query("SELECT name, last_run_at, next_run_at, last_status, last_duration_s, run_count, fail_count FROM cron_jobs ORDER BY next_run_at")
	if err != nil {
		fmt.Printf("Error querying jobs: %v\n", err)
		return
	}
	defer rows.Close()

	fmt.Printf("\n%-30s  %8s  %10s  %-19s  %10s  %-19s  %4s  %5s  %5s\n",
		"Name", "Interval", "Active", "Last Run", "Status", "Next Run", "Due", "Runs", "Fails")
	fmt.Println(strings.Repeat("-", 140))

	for rows.Next() {
		var name string
		var lastRun, nextRun, status sql.NullString
		var duration sql.NullFloat64
		var runCount, failCount int

		rows.Scan(&name, &lastRun, &nextRun, &status, &duration, &runCount, &failCount)

		job, ok := jobs[name]
		if !ok {
			continue
		}

		interval := FormatInterval(job.IntervalSeconds)
		lr := "-"
		if lastRun.Valid {
			lr = FormatDt(lastRun.String)
		}
		nr := FormatDt(nextRun.String)
		st := "-"
		if status.Valid {
			st = status.String
		}

		isDue := ""
		if nextRun.Valid && nextRun.String <= now.Format(time.RFC3339) {
			isDue = "YES"
		}

		active := "always"
		if job.ActiveHours != nil {
			active = fmt.Sprintf("%02d-%02d", job.ActiveHours[0], job.ActiveHours[1])
			if !db.IsInActiveHours(job.ActiveHours, tzName) {
				isDue = ""
			}
		}

		fmt.Printf("%-30s  %8s  %10s  %-19s  %10s  %-19s  %4s  %5d  %5d\n",
			name, interval, active, lr, st, nr, isDue, runCount, failCount)
	}
	fmt.Println()
}
