package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blindly/dispatcher/internal/config"
	"github.com/blindly/dispatcher/internal/db"
)

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	os.Stdout = orig
	w.Close()
	out := <-done
	r.Close()
	return out
}

func testInfoConfig() *config.DispatcherConfig {
	return &config.DispatcherConfig{
		Timezone:     "America/New_York",
		Scheduler:    "cron",
		Schedule:     "*/5 * * * *",
		Retention:    90,
		PauseTimeout: 3600,
		Jobs: map[string]*config.JobConfig{
			"nightly": {Name: "nightly", Commands: []string{"echo"}, IntervalSeconds: 86400, Description: "Nightly backup"},
			"manual":  {Name: "manual", Commands: []string{"echo"}, Adhoc: true},
			"stopped": {Name: "stopped", Commands: []string{"echo"}, IntervalSeconds: 300, Paused: true},
		},
	}
}

func TestPrintInfo(t *testing.T) {
	dir := t.TempDir()
	dispDir := ensureDispatcherDir(dir)
	conn, err := db.Open(filepath.Join(dispDir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	cfg := testInfoConfig()
	db.EnsureJobs(conn, cfg.Jobs)
	db.SetMeta(conn, "last_dispatch_at", "2026-04-07T14:30:00Z")

	out := captureStdout(t, func() {
		printInfo(cfg, filepath.Join(dir, "Dispatcher.yaml"), dir, dispDir, conn)
	})

	wants := []string{
		"Version:       " + version,
		"Timezone:      America/New_York",
		"Schedule:      */5 * * * *",
		"Retention:     90d",
		"Pause timeout: 1h",
		"Type:          cron",
		"Paused:        no",
		"Last dispatch: 2026-04-07 14:30",
		"Database:      " + filepath.Join(dispDir, "data.db"),
		"Jobs: 1 scheduled, 1 adhoc, 1 paused",
		"nightly",
		"Nightly backup",
		"Mode:          always",
		"Discord:       not configured",
		"Ntfy:          not configured",
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintInfo_PausedAndNotifiers(t *testing.T) {
	dir := t.TempDir()
	dispDir := ensureDispatcherDir(dir)
	conn, err := db.Open(filepath.Join(dispDir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	cfg := testInfoConfig()
	cfg.Notify.On = "failure"
	cfg.Notify.Discord.Webhook = "https://discord.example/hook"
	cfg.Notify.Ntfy.Topic = "my-topic"
	writePauseFile(dispDir, time.Now().UTC().Add(time.Hour), "deploying")

	out := captureStdout(t, func() {
		printInfo(cfg, filepath.Join(dir, "Dispatcher.yaml"), dir, dispDir, conn)
	})

	wants := []string{
		"Paused:        yes",
		"— deploying",
		"Last dispatch: never",
		"Mode:          failure",
		"Discord:       configured",
		"Ntfy:          configured (https://ntfy.sh/my-topic)",
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintInfo_SystemdScheduler(t *testing.T) {
	dir := t.TempDir()
	dispDir := ensureDispatcherDir(dir)
	conn, err := db.Open(filepath.Join(dispDir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	cfg := testInfoConfig()
	cfg.Scheduler = "systemd"

	out := captureStdout(t, func() {
		printInfo(cfg, filepath.Join(dir, "Dispatcher.yaml"), dir, dispDir, conn)
	})

	if !strings.Contains(out, "Scheduler:     systemd") || !strings.Contains(out, "Type:          systemd") {
		t.Errorf("systemd scheduler not reported:\n%s", out)
	}
	// No timer is installed for a scratch directory.
	if !strings.Contains(out, "Status:        disabled") {
		t.Errorf("expected the timer to be reported as disabled:\n%s", out)
	}
}

func TestFormatJobInfo(t *testing.T) {
	scheduled := formatJobInfo("nightly", &config.JobConfig{IntervalSeconds: 86400, Description: "Nightly backup"}, 10)
	if !strings.HasPrefix(scheduled, "nightly   ") {
		t.Errorf("name should be padded to nameWidth, got %q", scheduled)
	}
	if !strings.Contains(scheduled, "1d") || !strings.HasSuffix(scheduled, "Nightly backup") {
		t.Errorf("got %q, want interval and description", scheduled)
	}

	adhoc := formatJobInfo("manual", &config.JobConfig{}, 10)
	if strings.TrimSpace(adhoc) != "manual" {
		t.Errorf("adhoc job without a description should render just its name, got %q", adhoc)
	}
}
