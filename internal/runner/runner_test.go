package runner

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/blindly/dispatcher/internal/config"
	"github.com/blindly/dispatcher/internal/db"
)

func TestRunOnce_Success(t *testing.T) {
	job := &config.JobConfig{
		Name:    "echo_test",
		Command: "echo hello",
	}
	rc, output := RunOnce(job)
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(output, "hello") {
		t.Errorf("output = %q, want to contain 'hello'", output)
	}
}

func TestRunOnce_Failure(t *testing.T) {
	cmd := "false"
	if runtime.GOOS == "windows" {
		cmd = "cmd /c exit 1"
	}
	job := &config.JobConfig{
		Name:    "fail_test",
		Command: cmd,
	}
	rc, _ := RunOnce(job)
	if rc == 0 {
		t.Error("expected non-zero exit code")
	}
}

func TestRunJob_UpdatesDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	job := &config.JobConfig{
		Name:            "test_echo",
		Command:         "echo hello",
		IntervalSeconds: 300,
		Retries:         0,
		RetryDelay:      1,
	}
	jobs := map[string]*config.JobConfig{"test_echo": job}
	db.EnsureJobs(conn, jobs)

	rc, _, output := RunJob(conn, job)
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.Contains(output, "hello") {
		t.Errorf("output = %q, want to contain 'hello'", output)
	}

	var runCount int
	var status string
	conn.QueryRow("SELECT run_count, last_status FROM cron_jobs WHERE name = ?", "test_echo").Scan(&runCount, &status)
	if runCount != 1 {
		t.Errorf("run_count = %d, want 1", runCount)
	}
	if status != "ok" {
		t.Errorf("status = %q, want ok", status)
	}
}

func TestResolveOrder_RespectsDependencies(t *testing.T) {
	jobs := map[string]*config.JobConfig{
		"fetch": {Name: "fetch", Command: "echo", IntervalSeconds: 300},
		"scan":  {Name: "scan", Command: "echo", IntervalSeconds: 300, DependsOn: "fetch"},
		"other": {Name: "other", Command: "echo", IntervalSeconds: 300},
	}
	due := []string{"scan", "fetch", "other"}
	ordered := ResolveOrder(due, jobs)

	fetchIdx, scanIdx := -1, -1
	for i, name := range ordered {
		if name == "fetch" {
			fetchIdx = i
		}
		if name == "scan" {
			scanIdx = i
		}
	}
	if fetchIdx >= scanIdx {
		t.Errorf("fetch (idx=%d) should come before scan (idx=%d)", fetchIdx, scanIdx)
	}
	if len(ordered) != 3 {
		t.Errorf("got %d items, want 3", len(ordered))
	}
}

func TestResolveOrder_DepNotDue(t *testing.T) {
	jobs := map[string]*config.JobConfig{
		"fetch": {Name: "fetch", Command: "echo", IntervalSeconds: 300},
		"scan":  {Name: "scan", Command: "echo", IntervalSeconds: 300, DependsOn: "fetch"},
	}
	due := []string{"scan"}
	ordered := ResolveOrder(due, jobs)
	if len(ordered) != 1 || ordered[0] != "scan" {
		t.Errorf("got %v, want [scan]", ordered)
	}
}
