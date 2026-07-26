package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/blindly/dispatcher/internal/config"
)

// cronToOnCalendar converts a cron expression to a systemd OnCalendar value.
// Supports common patterns: */N, fixed minute/hour, and wildcards.
func cronToOnCalendar(schedule string) (string, error) {
	parts := strings.Fields(schedule)
	if len(parts) != 5 {
		return "", fmt.Errorf("invalid cron expression: %q (expected 5 fields)", schedule)
	}
	minute, hour, dom, month, dow := parts[0], parts[1], parts[2], parts[3], parts[4]

	if dow != "*" {
		// systemd doesn't easily combine DoW with time; skip with warning at enable time
	}
	if dom != "*" {
		return fmt.Sprintf("*-*-%s %s:%s", dom, padHour(hour), padMinute(minute)), nil
	}
	if month != "*" {
		return fmt.Sprintf("*-%s-* %s:%s", month, padHour(hour), padMinute(minute)), nil
	}
	// Format as *-*-* HH:MM where HH:MM handles */N patterns
	hourStr := systemdHour(hour)
	minuteStr := systemdMinute(minute)
	return fmt.Sprintf("*-*-* %s:%s", hourStr, minuteStr), nil
}

// padHour zero-pads a fixed hour value or passes through special forms like "*/2".
func padHour(h string) string {
	if h == "*" {
		return "*"
	}
	n, err := strconv.Atoi(h)
	if err == nil {
		return fmt.Sprintf("%02d", n)
	}
	// Pass through as-is (e.g. */2)
	return h
}

// padMinute zero-pads a fixed minute value or passes through special forms like "*/5".
func padMinute(m string) string {
	if m == "*" {
		return "0"
	}
	n, err := strconv.Atoi(m)
	if err == nil {
		return fmt.Sprintf("%02d", n)
	}
	// Pass through as-is (e.g. */5)
	return m
}

// systemdHour formats a cron hour field for systemd.
func systemdHour(h string) string {
	if h == "*" {
		return "00"
	}
	if idx := strings.Index(h, "/"); idx >= 0 {
		base := h[:idx]
		step := h[idx+1:]
		if base == "*" {
			return "00/" + step
		}
		return fmt.Sprintf("%s/%s", padInt(base), step)
	}
	return padHour(h)
}

// systemdMinute formats a cron minute field for systemd.
func systemdMinute(m string) string {
	if m == "*" {
		return "00"
	}
	if idx := strings.Index(m, "/"); idx >= 0 {
		base := m[:idx]
		step := m[idx+1:]
		if base == "*" {
			return "00/" + step
		}
		return fmt.Sprintf("%s/%s", padInt(base), step)
	}
	return padMinute(m)
}

// padInt zero-pads a number string.
func padInt(s string) string {
	n, err := strconv.Atoi(s)
	if err == nil {
		return fmt.Sprintf("%02d", n)
	}
	return s
}

// unitNameFromDir creates a safe systemd unit name from a project directory path.
func unitNameFromDir(projectDir string) string {
	// Use last directory component, sanitize for systemd unit naming.
	base := filepath.Base(projectDir)
	re := regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	sanitized := re.ReplaceAllString(base, "-")
	sanitized = strings.Trim(sanitized, "-")
	if sanitized == "" {
		sanitized = "dispatch"
	}
	return "dispatch-" + sanitized
}

// isUserSystemd returns true if we should install to user-level systemd (no root).
func isUserSystemd() bool {
	return os.Geteuid() != 0
}

// systemdControlPath returns the systemctl command prefix.
func systemdControlPath() []string {
	if isUserSystemd() {
		return []string{"systemctl", "--user"}
	}
	return []string{"systemctl"}
}

// systemdUnitDir returns the directory where unit files should be installed.
func systemdUnitDir() string {
	if isUserSystemd() {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "/root"
		}
		return filepath.Join(home, ".config", "systemd", "user")
	}
	return "/etc/systemd/system"
}

// enableSystemd creates and enables a systemd timer + service for the dispatcher.
func enableSystemd(cfg *config.DispatcherConfig, configDir string) {
	dispatchPath, err := os.Executable()
	if err != nil {
		dispatchPath = "dispatch"
	}

	unitName := unitNameFromDir(configDir)
	onCal, err := cronToOnCalendar(cfg.Schedule)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to convert schedule: %v\n", err)
		fmt.Fprintln(os.Stderr, "Falling back to cron enable...")
		enableCron(cfg.Schedule, configDir)
		return
	}

	unitDir := systemdUnitDir()
	if err := os.MkdirAll(unitDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create unit directory %s: %v\n", unitDir, err)
		os.Exit(1)
	}

	serviceUnit := fmt.Sprintf(`[Unit]
Description=Dispatcher job runner (project: %s)
After=network.target

[Service]
Type=oneshot
WorkingDirectory=%s
ExecStart=%s
Environment=HOME=%s
StandardOutput=append:%s/.dispatcher/logs/dispatcher.log
StandardError=append:%s/.dispatcher/logs/dispatcher.log

[Install]
WantedBy=multi-user.target
`, unitName, configDir, dispatchPath, os.Getenv("HOME"), configDir, configDir)

	timerUnit := fmt.Sprintf(`[Unit]
Description=Dispatcher timer (project: %s)

[Timer]
OnCalendar=%s
Persistent=true

[Install]
WantedBy=timers.target
`, unitName, onCal)

	svcFile := filepath.Join(unitDir, unitName+".service")
	timerFile := filepath.Join(unitDir, unitName+".timer")

	if err := os.WriteFile(svcFile, []byte(serviceUnit), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write service unit: %v\n", err)
		return
	}
	if err := os.WriteFile(timerFile, []byte(timerUnit), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write timer unit: %v\n", err)
		return
	}

	sc := systemdControlPath()

	// Reload systemd daemon
	reloadCmd := append([]string{}, sc...)
	reloadCmd = append(reloadCmd, "daemon-reload")
	if err := exec.Command(reloadCmd[0], reloadCmd[1:]...).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: daemon-reload failed: %v\n", err)
	}

	// Enable and start the timer
	enableCmd := append([]string{}, sc...)
	enableCmd = append(enableCmd, "enable", "--now", unitName+".timer")
	if err := exec.Command(enableCmd[0], enableCmd[1:]...).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to enable timer: %v\n", err)
		return
	}

	mode := "system"
	if isUserSystemd() {
		mode = "user"
	}
	fmt.Printf("Systemd timer enabled (%s): %s.timer (OnCalendar=%s)\n", mode, unitName, onCal)
}

// disableSystemd stops and removes the systemd timer + service.
func disableSystemd(configDir string) {
	unitName := unitNameFromDir(configDir)
	unitDir := systemdUnitDir()
	sc := systemdControlPath()

	timerPath := filepath.Join(unitDir, unitName+".timer")
	_, timerErr := os.Stat(timerPath)
	timerInstalled := timerErr == nil

	// Stop and disable the timer
	stopCmd := append([]string{}, sc...)
	stopCmd = append(stopCmd, "disable", "--now", unitName+".timer")
	if err := exec.Command(stopCmd[0], stopCmd[1:]...).Run(); err != nil && timerInstalled {
		// Only noteworthy when the unit is actually installed — otherwise
		// systemctl is just telling us there is nothing to disable.
		fmt.Fprintf(os.Stderr, "Warning: could not disable %s.timer: %v\n", unitName, err)
	}

	// Remove unit files
	for _, f := range []string{unitName + ".service", unitName + ".timer"} {
		path := filepath.Join(unitDir, f)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Failed to remove %s: %v\n", path, err)
			os.Exit(1)
		}
	}

	// Reload daemon
	reloadCmd := append([]string{}, sc...)
	reloadCmd = append(reloadCmd, "daemon-reload")
	if err := exec.Command(reloadCmd[0], reloadCmd[1:]...).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: daemon-reload failed: %v\n", err)
	}

	fmt.Printf("Systemd timer disabled: %s.timer\n", unitName)
}

// isSystemdInstalled checks if a dispatch systemd timer exists for the given project directory.
func isSystemdInstalled(configDir string) (bool, string) {
	unitName := unitNameFromDir(configDir)
	sc := systemdControlPath()

	// Check if the timer is active
	cmd := exec.Command(sc[0], append(sc[1:], "is-active", unitName+".timer")...)
	out, err := cmd.Output()
	if err != nil {
		return false, ""
	}
	status := strings.TrimSpace(string(out))
	if status != "active" {
		return false, status
	}

	// Get the OnCalendar value for display
	cmd = exec.Command(sc[0], append(sc[1:], "show", "--property=OnCalendar", "--value", unitName+".timer")...)
	out, err = cmd.Output()
	if err != nil {
		return true, status
	}
	onCal := strings.TrimSpace(strings.ReplaceAll(string(out), "\n", "; "))
	if onCal == "" || onCal == "n/a" {
		return true, status
	}
	return true, "active (" + onCal + ")"
}
