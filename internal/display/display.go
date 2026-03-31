package display

import (
	"database/sql"
	"fmt"
	"os/exec"
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

// IsCronInstalled checks if a dispatch crontab entry exists for the given project directory.
func IsCronInstalled(projectDir string) (bool, string) {
	out, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		return false, ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "dispatch") && strings.Contains(line, projectDir) {
			return true, strings.TrimSpace(line)
		}
	}
	return false, ""
}

func PrintQuickStatus(conn *sql.DB, jobs map[string]*config.JobConfig, tzName string, projectDir string) {
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

	cronStatus := "not installed"
	if installed, schedule := IsCronInstalled(projectDir); installed {
		cronStatus = "installed (" + schedule + ")"
	}

	fmt.Printf("Last run: %s | %d jobs | %d due | %d total runs | %d failures\n",
		lastRunStr, totalJobs, dueCount, totalRuns, totalFails)
	fmt.Printf("Cron: %s\n", cronStatus)
}

type jobRow struct {
	name      string
	lastRun   sql.NullString
	nextRun   sql.NullString
	status    sql.NullString
	duration  sql.NullFloat64
	runCount  int
	failCount int
}

func PrintStatus(conn *sql.DB, jobs map[string]*config.JobConfig, tzName string) {
	now := db.NowUTC()

	rows, err := conn.Query("SELECT name, last_run_at, next_run_at, last_status, last_duration_s, run_count, fail_count FROM cron_jobs ORDER BY next_run_at")
	if err != nil {
		fmt.Printf("Error querying jobs: %v\n", err)
		return
	}
	defer rows.Close()

	var scheduled, adhoc []jobRow
	for rows.Next() {
		var r jobRow
		rows.Scan(&r.name, &r.lastRun, &r.nextRun, &r.status, &r.duration, &r.runCount, &r.failCount)
		job, ok := jobs[r.name]
		if !ok {
			continue
		}
		if job.Adhoc {
			adhoc = append(adhoc, r)
		} else {
			scheduled = append(scheduled, r)
		}
	}

	if len(scheduled) > 0 {
		fmt.Printf("\nScheduled Jobs\n")
		fmt.Printf("%-30s  %8s  %10s  %-19s  %10s  %-19s  %4s  %5s  %5s\n",
			"Name", "Interval", "Active", "Last Run", "Status", "Next Run", "Due", "Runs", "Fails")
		fmt.Println(strings.Repeat("-", 140))

		for _, r := range scheduled {
			job := jobs[r.name]
			interval := FormatInterval(job.IntervalSeconds)
			lr := "-"
			if r.lastRun.Valid {
				lr = FormatDt(r.lastRun.String)
			}
			nr := FormatDt(r.nextRun.String)
			st := "-"
			if r.status.Valid {
				st = r.status.String
			}
			isDue := ""
			if r.nextRun.Valid && r.nextRun.String <= now.Format(time.RFC3339) {
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
				r.name, interval, active, lr, st, nr, isDue, r.runCount, r.failCount)
		}
	}

	if len(adhoc) > 0 {
		fmt.Printf("\nAdhoc Jobs\n")
		fmt.Printf("%-30s  %-19s  %10s  %5s  %5s\n",
			"Name", "Last Run", "Status", "Runs", "Fails")
		fmt.Println(strings.Repeat("-", 80))

		for _, r := range adhoc {
			lr := "-"
			if r.lastRun.Valid {
				lr = FormatDt(r.lastRun.String)
			}
			st := "-"
			if r.status.Valid {
				st = r.status.String
			}
			fmt.Printf("%-30s  %-19s  %10s  %5d  %5d\n",
				r.name, lr, st, r.runCount, r.failCount)
		}
	}

	if len(scheduled) == 0 && len(adhoc) == 0 {
		fmt.Println("\nNo jobs configured.")
	}
	fmt.Println()
}

func PrintAnalytics(conn *sql.DB) {
	analytics, err := db.GetAnalytics(conn)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	if len(analytics) == 0 {
		fmt.Println("No run history yet.")
		return
	}

	fmt.Printf("\n%-30s  %6s  %6s  %6s  %7s  %10s  %10s\n",
		"Job", "Runs", "Pass", "Fail", "Rate", "Avg Time", "Last 7d")
	fmt.Println(strings.Repeat("-", 100))

	var totalRuns, totalPass, totalFail int
	var bestName, worstName string
	bestRate, worstRate := -1.0, 101.0

	for _, a := range analytics {
		last7d := fmt.Sprintf("%d/%d", a.Last7dPass, a.Last7dRuns)
		if a.Last7dRuns == 0 {
			last7d = "-"
		}
		fmt.Printf("%-30s  %6d  %6d  %6d  %6.1f%%  %9.1fs  %10s\n",
			a.Name, a.TotalRuns, a.PassCount, a.FailCount,
			a.SuccessRate, a.AvgDuration, last7d)

		totalRuns += a.TotalRuns
		totalPass += a.PassCount
		totalFail += a.FailCount

		if a.SuccessRate > bestRate {
			bestRate = a.SuccessRate
			bestName = a.Name
		}
		if a.SuccessRate < worstRate {
			worstRate = a.SuccessRate
			worstName = a.Name
		}
	}

	overallRate := 0.0
	if totalRuns > 0 {
		overallRate = float64(totalPass) / float64(totalRuns) * 100
	}

	fmt.Println()
	fmt.Printf("Overall: %d runs, %.1f%% success rate, %d jobs\n", totalRuns, overallRate, len(analytics))
	if bestName != "" {
		fmt.Printf("Most reliable: %s (%.1f%%)\n", bestName, bestRate)
	}
	if worstName != "" && worstName != bestName {
		fmt.Printf("Least reliable: %s (%.1f%%)\n", worstName, worstRate)
	}
	fmt.Println()
}
