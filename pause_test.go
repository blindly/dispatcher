package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWritePauseFile(t *testing.T) {
	dir := t.TempDir()
	reason := "deploying auth changes"
	expiresAt := time.Date(2026, 4, 7, 15, 30, 0, 0, time.UTC)

	writePauseFile(dir, expiresAt, reason)

	data, err := os.ReadFile(filepath.Join(dir, "paused"))
	if err != nil {
		t.Fatal("pause file not created")
	}

	var info PauseInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if info.Reason != reason {
		t.Errorf("reason = %q, want %q", info.Reason, reason)
	}
	if info.ExpiresAt != "2026-04-07T15:30:00Z" {
		t.Errorf("expires_at = %q", info.ExpiresAt)
	}
	if info.PausedAt == "" {
		t.Error("paused_at should be set")
	}
}

func TestReadPauseFile_NotPaused(t *testing.T) {
	dir := t.TempDir()
	info := readPauseFile(dir)
	if info != nil {
		t.Error("expected nil when no pause file")
	}
}

func TestReadPauseFile_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	data := `{"expires_at":"2026-04-07T15:30:00Z","reason":"test","paused_at":"2026-04-07T14:30:00Z"}`
	os.WriteFile(filepath.Join(dir, "paused"), []byte(data), 0644)

	info := readPauseFile(dir)
	if info == nil {
		t.Fatal("expected pause info")
	}
	if info.Reason != "test" {
		t.Errorf("reason = %q", info.Reason)
	}
	if info.ExpiresAt != "2026-04-07T15:30:00Z" {
		t.Errorf("expires_at = %q", info.ExpiresAt)
	}
}

func TestReadPauseFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "paused"), []byte(""), 0644)

	info := readPauseFile(dir)
	if info == nil {
		t.Fatal("expected pause info for empty file (manual touch)")
	}
	if info.ExpiresAt != "" {
		t.Errorf("expires_at should be empty for manual touch, got %q", info.ExpiresAt)
	}
}

func TestRemovePauseFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "paused"), []byte("{}"), 0644)

	removePauseFile(dir)

	if _, err := os.Stat(filepath.Join(dir, "paused")); !os.IsNotExist(err) {
		t.Error("pause file should be removed")
	}
}

func TestRemovePauseFile_NoFile(t *testing.T) {
	dir := t.TempDir()
	// Should not panic
	removePauseFile(dir)
}

func TestCheckPause_NotPaused(t *testing.T) {
	dir := t.TempDir()
	paused, _ := checkPause(dir)
	if paused {
		t.Error("should not be paused when no file exists")
	}
}

func TestCheckPause_ActivePause(t *testing.T) {
	dir := t.TempDir()
	future := time.Now().UTC().Add(1 * time.Hour)
	writePauseFile(dir, future, "working on something")

	paused, msg := checkPause(dir)
	if !paused {
		t.Error("should be paused")
	}
	if msg == "" {
		t.Error("should have a message")
	}
}

func TestCheckPause_ExpiredPause(t *testing.T) {
	dir := t.TempDir()
	past := time.Now().UTC().Add(-1 * time.Hour)
	writePauseFile(dir, past, "old pause")

	paused, _ := checkPause(dir)
	if paused {
		t.Error("should not be paused when expired")
	}

	// File should be cleaned up
	if _, err := os.Stat(filepath.Join(dir, "paused")); !os.IsNotExist(err) {
		t.Error("expired pause file should be auto-removed")
	}
}

func TestCheckPause_ManualTouch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "paused"), []byte(""), 0644)

	paused, msg := checkPause(dir)
	if !paused {
		t.Error("manual touch should be paused with no expiry")
	}
	if msg == "" {
		t.Error("should have a message")
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Minute, "30m"},
		{90 * time.Minute, "1h"},
		{3 * time.Hour, "3h"},
		{48 * time.Hour, "2d"},
		{0, "0m"},
	}
	for _, tt := range tests {
		if got := FormatDuration(tt.d); got != tt.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestCheckPause_InvalidExpiry(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "paused"), []byte(`{"expires_at":"tomorrow"}`), 0644)

	paused, msg := checkPause(dir)
	if !paused {
		t.Error("an unparseable expiry should keep the dispatcher paused")
	}
	if !strings.Contains(msg, "invalid expiry") {
		t.Errorf("msg = %q, want it to mention the invalid expiry", msg)
	}
}

func TestCheckPause_ManualTouchWithReason(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "paused"), []byte(`{"reason":"maintenance"}`), 0644)

	paused, msg := checkPause(dir)
	if !paused {
		t.Fatal("should be paused")
	}
	if !strings.Contains(msg, "maintenance") || !strings.Contains(msg, "no expiry") {
		t.Errorf("msg = %q, want reason and no-expiry note", msg)
	}
}
