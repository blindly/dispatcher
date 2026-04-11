package main

import "strings"

type cronStatus int

const (
	cronAdded cronStatus = iota
	cronUpdated
	cronUnchanged
)

// buildCrontab returns the new crontab content and a status describing what
// changed. It replaces any existing dispatch line for projectDir with newLine,
// or appends newLine if none exists.
func buildCrontab(existing, newLine, projectDir string) (string, cronStatus) {
	trimmed := strings.TrimRight(existing, "\n")
	var lines []string
	if trimmed != "" {
		lines = strings.Split(trimmed, "\n")
	}

	var out []string
	firstMatch := false
	changed := false
	for _, line := range lines {
		if strings.Contains(line, "dispatch") && strings.Contains(line, projectDir) {
			if !firstMatch {
				firstMatch = true
				if line != newLine {
					changed = true
				}
				out = append(out, newLine)
			} else {
				// Subsequent duplicate: drop it, content has changed.
				changed = true
			}
		} else {
			out = append(out, line)
		}
	}

	if !firstMatch {
		out = append(out, newLine)
		return strings.Join(out, "\n") + "\n", cronAdded
	}
	content := strings.Join(out, "\n") + "\n"
	if changed {
		return content, cronUpdated
	}
	return content, cronUnchanged
}
