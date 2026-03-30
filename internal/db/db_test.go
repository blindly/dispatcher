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
		"job_a": {Name: "job_a", Command: "echo a", IntervalSeconds: 300},
		"job_b": {Name: "job_b", Command: "echo b", IntervalSeconds: 600},
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
		"overdue": {Name: "overdue", Command: "echo", IntervalSeconds: 300},
		"future":  {Name: "future", Command: "echo", IntervalSeconds: 300},
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
		"narrow": {Name: "narrow", Command: "echo", IntervalSeconds: 300, ActiveHours: &hours},
	}
	due := GetDueJobs(conn, jobs, "America/New_York")
	for _, name := range due {
		if name == "narrow" {
			t.Error("narrow job should be filtered by active_hours")
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
}
