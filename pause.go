package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type PauseInfo struct {
	ExpiresAt string `json:"expires_at"`
	Reason    string `json:"reason"`
	PausedAt  string `json:"paused_at"`
}

func writePauseFile(dir string, expiresAt time.Time, reason string) error {
	info := PauseInfo{
		ExpiresAt: expiresAt.Format(time.RFC3339),
		Reason:    reason,
		PausedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding pause file: %w", err)
	}
	path := filepath.Join(dir, "paused")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

func readPauseFile(dir string) *PauseInfo {
	path := filepath.Join(dir, "paused")
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			// The pause file exists but can't be read — stay paused rather
			// than dispatching jobs the user may have deliberately stopped.
			fmt.Fprintf(os.Stderr, "Warning: cannot read %s: %v\n", path, err)
			return &PauseInfo{Reason: "pause file unreadable"}
		}
		return nil
	}

	var info PauseInfo
	if err := json.Unmarshal(data, &info); err != nil {
		// Empty or unparseable file (manual touch) — paused with no expiry
		return &PauseInfo{}
	}
	return &info
}

func removePauseFile(dir string) error {
	path := filepath.Join(dir, "paused")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	return nil
}

func FormatDuration(d time.Duration) string {
	if d >= 24*time.Hour {
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	if d >= time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func checkPause(dir string) (paused bool, msg string) {
	info := readPauseFile(dir)
	if info == nil {
		return false, ""
	}

	// No expiry (manual touch or empty file)
	if info.ExpiresAt == "" {
		msg = "Dispatcher paused (no expiry — run 'dispatch resume' to unpause)"
		if info.Reason != "" {
			msg = fmt.Sprintf("Dispatcher paused — %s (no expiry — run 'dispatch resume' to unpause)", info.Reason)
		}
		return true, msg
	}

	expiresAt, err := time.Parse(time.RFC3339, info.ExpiresAt)
	if err != nil {
		return true, "Dispatcher paused (invalid expiry — run 'dispatch resume' to unpause)"
	}

	if time.Now().UTC().After(expiresAt) {
		if err := removePauseFile(dir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: pause expired but %v\n", err)
		}
		return false, ""
	}

	timeLeft := time.Until(expiresAt).Round(time.Minute)
	msg = fmt.Sprintf("Dispatcher paused until %s (%s remaining)", expiresAt.Local().Format("15:04"), timeLeft)
	if info.Reason != "" {
		msg = fmt.Sprintf("Dispatcher paused until %s — %s (%s remaining)", expiresAt.Local().Format("15:04"), info.Reason, timeLeft)
	}
	return true, msg
}
