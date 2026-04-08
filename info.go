package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blindly/dispatcher/internal/config"
	"github.com/blindly/dispatcher/internal/db"
	"github.com/blindly/dispatcher/internal/display"
)

func printInfo(cfg *config.DispatcherConfig, configPath, configDir, dispDir string, conn *sql.DB) {
	absConfig, _ := filepath.Abs(configPath)

	// Version
	fmt.Println("Dispatcher")
	fmt.Printf("  Version:       %s\n", version)
	fmt.Printf("  Config:        %s\n", absConfig)
	fmt.Printf("  Data dir:      %s\n", dispDir)
	fmt.Printf("  Timezone:      %s\n", cfg.Timezone)
	fmt.Printf("  Schedule:      %s\n", cfg.Schedule)
	fmt.Printf("  Retention:     %dd\n", cfg.Retention)
	fmt.Printf("  Pause timeout: %s\n", display.FormatInterval(cfg.PauseTimeout))
	fmt.Println()

	// Cron
	fmt.Println("Cron")
	if installed, entry := display.IsCronInstalled(configDir); installed {
		fmt.Println("  Status:        installed")
		fmt.Printf("  Entry:         %s\n", entry)
	} else {
		fmt.Println("  Status:        not installed")
	}
	fmt.Println()

	// State
	fmt.Println("State")
	pauseInfo := readPauseFile(dispDir)
	if pauseInfo != nil {
		pauseStr := "yes"
		if pauseInfo.ExpiresAt != "" {
			pauseStr += fmt.Sprintf(" (until %s)", pauseInfo.ExpiresAt)
		} else {
			pauseStr += " (no expiry)"
		}
		if pauseInfo.Reason != "" {
			pauseStr += fmt.Sprintf(" — %s", pauseInfo.Reason)
		}
		fmt.Printf("  Paused:        %s\n", pauseStr)
	} else {
		fmt.Println("  Paused:        no")
	}

	lastDispatch := db.GetMeta(conn, "last_dispatch_at")
	if lastDispatch != "" {
		fmt.Printf("  Last dispatch: %s\n", display.FormatDt(lastDispatch))
	} else {
		fmt.Println("  Last dispatch: never")
	}

	dbPath := filepath.Join(dispDir, "data.db")
	if info, err := os.Stat(dbPath); err == nil {
		size := info.Size()
		sizeStr := fmt.Sprintf("%d B", size)
		if size >= 1024*1024 {
			sizeStr = fmt.Sprintf("%.1f MB", float64(size)/1024/1024)
		} else if size >= 1024 {
			sizeStr = fmt.Sprintf("%.1f KB", float64(size)/1024)
		}
		fmt.Printf("  Database:      %s (%s)\n", dbPath, sizeStr)
	}
	fmt.Println()

	// Jobs
	var scheduled, adhoc, paused []string
	for name, job := range cfg.Jobs {
		if job.Paused {
			paused = append(paused, name)
		} else if job.Adhoc {
			adhoc = append(adhoc, name)
		} else {
			scheduled = append(scheduled, name)
		}
	}
	sort.Strings(scheduled)
	sort.Strings(adhoc)
	sort.Strings(paused)

	fmt.Printf("Jobs: %d scheduled, %d adhoc, %d paused\n", len(scheduled), len(adhoc), len(paused))

	if len(scheduled) > 0 {
		fmt.Println("  Scheduled:")
		for _, name := range scheduled {
			fmt.Printf("    %s\n", formatJobInfo(name, cfg.Jobs[name]))
		}
	}
	if len(adhoc) > 0 {
		fmt.Println("  Adhoc:")
		for _, name := range adhoc {
			fmt.Printf("    %s\n", formatJobInfo(name, cfg.Jobs[name]))
		}
	}
	if len(paused) > 0 {
		fmt.Println("  Paused:")
		for _, name := range paused {
			fmt.Printf("    %s  (paused in config)\n", formatJobInfo(name, cfg.Jobs[name]))
		}
	}
	fmt.Println()

	// Notifications
	fmt.Println("Notifications")
	notifyOn := cfg.Notify.On
	if notifyOn == "" {
		notifyOn = "always"
	}
	fmt.Printf("  Mode:          %s\n", notifyOn)

	if cfg.Notify.Discord.Webhook != "" {
		fmt.Println("  Discord:       configured")
	} else {
		fmt.Println("  Discord:       not configured")
	}

	if cfg.Notify.Ntfy.Topic != "" {
		url := cfg.Notify.Ntfy.URL
		if url == "" {
			url = "https://ntfy.sh"
		}
		fmt.Printf("  Ntfy:          configured (%s/%s)\n", url, cfg.Notify.Ntfy.Topic)
	} else {
		fmt.Println("  Ntfy:          not configured")
	}
}

func formatJobInfo(name string, job *config.JobConfig) string {
	parts := []string{fmt.Sprintf("%-20s", name)}

	if job.IntervalSeconds > 0 {
		parts = append(parts, fmt.Sprintf("%-4s", display.FormatInterval(job.IntervalSeconds)))
	}

	if job.ActiveHours != nil {
		parts = append(parts, fmt.Sprintf("active: %02d-%02d", job.ActiveHours[0], job.ActiveHours[1]))
	} else {
		parts = append(parts, "active: always")
	}

	if job.Timeout != 600 {
		parts = append(parts, fmt.Sprintf("timeout: %s", display.FormatInterval(job.Timeout)))
	}

	if job.Retries != 2 {
		parts = append(parts, fmt.Sprintf("retries: %d", job.Retries))
	}

	if job.DependsOn != "" {
		parts = append(parts, fmt.Sprintf("depends: %s", job.DependsOn))
	}

	if job.Description != "" {
		parts = append(parts, fmt.Sprintf("(%s)", job.Description))
	}

	return strings.Join(parts, "   ")
}
