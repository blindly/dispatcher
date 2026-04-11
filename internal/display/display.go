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
	case seconds >= 60:
		return fmt.Sprintf("%dm", seconds/60)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}

var displayLoc *time.Location

func SetTimezone(tzName string) {
	loc, err := time.LoadLocation(tzName)
	if err == nil {
		displayLoc = loc
	}
}

func FormatTimestamp(t time.Time) string {
	if displayLoc != nil {
		t = t.In(displayLoc)
	}
	return t.Format("2006-01-02 15:04:05")
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
	if displayLoc != nil {
		t = t.In(displayLoc)
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

func formatTimeAgo(now time.Time, iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return "unknown"
	}
	ago := now.Sub(t)
	if ago < 5*time.Second {
		return "just now"
	} else if ago < time.Minute {
		return fmt.Sprintf("%ds ago", int(ago.Seconds()))
	} else if ago < time.Hour {
		return fmt.Sprintf("%dm ago", int(ago.Minutes()))
	} else if ago < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(ago.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(ago.Hours()/24))
}

func PrintPauseBanner(pauseMsg string) {
	if pauseMsg != "" {
		fmt.Printf("⏸  %s\n", pauseMsg)
	}
}

func PrintQuickStatus(conn *sql.DB, jobs map[string]*config.JobConfig, tzName string, projectDir string, showAll bool) {
	now := db.NowUTC()

	var totalJobs, dueCount, runningCount, failedCount int
	var lastRunAt sql.NullString
	var runningStart, runningName string
	var failedNames []string

	rows, err := conn.Query("SELECT name, next_run_at, last_run_at, last_status, running_since, force_next FROM cron_jobs ORDER BY last_run_at DESC")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer rows.Close()

	first := true
	for rows.Next() {
		var name, nextRun string
		var forceNext int
		var lr, lastStatus, runningSince sql.NullString
		rows.Scan(&name, &nextRun, &lr, &lastStatus, &runningSince, &forceNext)

		job, ok := jobs[name]
		if !ok {
			continue
		}

		if !showAll && job.Paused {
			continue
		}

		totalJobs++

		if first && lr.Valid {
			lastRunAt = lr
			first = false
		}

		if runningSince.Valid {
			runningCount++
			if runningStart == "" || runningSince.String < runningStart {
				runningStart = runningSince.String
				runningName = name
			}
		}

		if lastStatus.Valid && strings.HasPrefix(lastStatus.String, "failed") {
			failedCount++
			failedNames = append(failedNames, name)
		}

		if !job.Adhoc && !job.Paused && nextRun <= now.Format(time.RFC3339) {
			if forceNext == 1 || db.IsInActiveHours(job.ActiveHours, tzName) {
				dueCount++
			}
		}
	}

	lastJobRunStr := "never"
	if lastRunAt.Valid {
		lastJobRunStr = formatTimeAgo(now, lastRunAt.String)
	}

	lastDispatchStr := "never"
	if v := db.GetMeta(conn, "last_dispatch_at"); v != "" {
		lastDispatchStr = formatTimeAgo(now, v)
	}

	cronStatus := "disabled"
	if installed, schedule := IsCronInstalled(projectDir); installed {
		cronStatus = "enabled (" + schedule + ")"
	}

	statusLine := fmt.Sprintf("Last dispatch: %s", lastDispatchStr)
	if runningCount > 0 {
		runningInfo := fmt.Sprintf("%d running", runningCount)
		if runningStart != "" {
			if t, err := time.Parse(time.RFC3339, runningStart); err == nil {
				ago := now.Sub(t)
				elapsed := fmt.Sprintf("%dm", int(ago.Minutes()))
				if ago < time.Minute {
					elapsed = fmt.Sprintf("%ds", int(ago.Seconds()))
				}
				if runningCount == 1 {
					runningInfo = fmt.Sprintf("%s running (%s)", runningName, elapsed)
				} else {
					runningInfo = fmt.Sprintf("%d running (started %s ago)", runningCount, elapsed)
				}
			}
		}
		statusLine += " | " + runningInfo
	} else {
		statusLine += fmt.Sprintf(" | Last job run: %s", lastJobRunStr)
	}
	statusLine += fmt.Sprintf(" | %d jobs | %d due", totalJobs, dueCount)
	if failedCount > 0 {
		if failedCount <= 3 {
			statusLine += fmt.Sprintf(" | %d failed: %s", failedCount, strings.Join(failedNames, ", "))
		} else {
			statusLine += fmt.Sprintf(" | %d failed", failedCount)
		}
	}
	fmt.Println(statusLine)
	fmt.Printf("Cron: %s\n", cronStatus)
}

type jobRow struct {
	name         string
	lastRun      sql.NullString
	nextRun      sql.NullString
	status       sql.NullString
	duration     sql.NullFloat64
	runCount     int
	failCount    int
	runningSince sql.NullString
	forceNext    int
}

func formatRunning(runningSince sql.NullString, now time.Time) string {
	if !runningSince.Valid {
		return ""
	}
	t, err := time.Parse(time.RFC3339, runningSince.String)
	if err != nil {
		return "RUNNING"
	}
	elapsed := now.Sub(t)
	if elapsed < time.Minute {
		return fmt.Sprintf("RUNNING (%ds)", int(elapsed.Seconds()))
	}
	return fmt.Sprintf("RUNNING (%dm)", int(elapsed.Minutes()))
}

func PrintStatus(conn *sql.DB, jobs map[string]*config.JobConfig, tzName string, showAll bool) {
	now := db.NowUTC()

	rows, err := conn.Query("SELECT name, last_run_at, next_run_at, last_status, last_duration_s, run_count, fail_count, running_since, force_next FROM cron_jobs ORDER BY next_run_at")
	if err != nil {
		fmt.Printf("Error querying jobs: %v\n", err)
		return
	}
	defer rows.Close()

	var scheduled, adhoc []jobRow
	for rows.Next() {
		var r jobRow
		rows.Scan(&r.name, &r.lastRun, &r.nextRun, &r.status, &r.duration, &r.runCount, &r.failCount, &r.runningSince, &r.forceNext)
		job, ok := jobs[r.name]
		if !ok {
			continue
		}
		if !showAll && job.Paused {
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
		fmt.Printf("%-30s  %8s  %10s  %-19s  %15s  %-19s  %4s  %5s  %5s\n",
			"Name", "Interval", "Active", "Last Run", "Status", "Next Run", "Due", "Runs", "Fails")
		fmt.Println(strings.Repeat("-", 145))

		for _, r := range scheduled {
			job := jobs[r.name]
			interval := FormatInterval(job.IntervalSeconds)
			lr := "-"
			if r.lastRun.Valid {
				lr = FormatDt(r.lastRun.String)
			}
			nr := FormatDt(r.nextRun.String)
			st := "-"
			if job.Paused {
				st = "paused"
			} else if running := formatRunning(r.runningSince, now); running != "" {
				st = running
			} else if r.status.Valid {
				st = r.status.String
			}
			isDue := ""
			if !job.Paused && r.nextRun.Valid && r.nextRun.String <= now.Format(time.RFC3339) {
				isDue = "YES"
			}
			active := "always"
			if job.ActiveHours != nil {
				active = fmt.Sprintf("%02d-%02d", job.ActiveHours[0], job.ActiveHours[1])
				if r.forceNext == 0 && !db.IsInActiveHours(job.ActiveHours, tzName) {
					isDue = ""
				}
			}
			fmt.Printf("%-30s  %8s  %10s  %-19s  %15s  %-19s  %4s  %5d  %5d\n",
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
			if running := formatRunning(r.runningSince, now); running != "" {
				st = running
			} else if r.status.Valid {
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

func PrintHistory(conn *sql.DB, name string, limit int) {
	entries, err := db.GetHistory(conn, name, limit)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	if len(entries) == 0 {
		fmt.Printf("No history for %s\n", name)
		return
	}

	fmt.Printf("\n%s — last %d runs\n", name, len(entries))
	fmt.Printf("%-19s  %12s  %6s  %10s\n", "Run At", "Status", "Exit", "Duration")
	fmt.Println(strings.Repeat("-", 55))

	for _, e := range entries {
		runAt := FormatDt(e.RunAt)
		fmt.Printf("%-19s  %12s  %6d  %9.1fs\n", runAt, e.Status, e.ExitCode, e.Duration)
	}
	fmt.Println()
}
