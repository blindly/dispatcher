package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type NotifyConfig struct {
	On             string // "always" or "failure"
	DiscordWebhook string
	NtfyURL        string
	NtfyTopic      string
	NtfyToken      string
	NtfyPriority   string
}

func SendAll(results []JobResult, cfg NotifyConfig) {
	if cfg.On == "failure" {
		hasFail := false
		for _, r := range results {
			if r.ExitCode != 0 {
				hasFail = true
				break
			}
		}
		if !hasFail {
			return
		}
	}
	SendDiscordSummary(results, cfg.DiscordWebhook)
	SendNtfySummary(results, cfg.NtfyURL, cfg.NtfyTopic, cfg.NtfyToken, cfg.NtfyPriority)
}

type JobResult struct {
	Name     string
	ExitCode int
	Elapsed  float64
	Output   string
}

func extractSummary(rc int, output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var nonEmpty []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, strings.TrimSpace(l))
		}
	}
	if rc != 0 {
		tail := nonEmpty
		if len(tail) > 2 {
			tail = tail[len(tail)-2:]
		}
		if len(tail) > 0 {
			s := "```\n" + strings.Join(tail, "\n") + "\n```"
			if len(s) > 300 {
				s = s[:300]
			}
			return s
		}
		return ""
	}
	if len(nonEmpty) > 0 {
		s := nonEmpty[len(nonEmpty)-1]
		if len(s) > 200 {
			s = s[:200]
		}
		return s
	}
	return ""
}

func SendDiscordSummary(results []JobResult, webhookURL string) {
	if len(results) == 0 || webhookURL == "" {
		return
	}

	passed := 0
	failed := 0
	totalTime := 0.0
	for _, r := range results {
		if r.ExitCode == 0 {
			passed++
		} else {
			failed++
		}
		totalTime += r.Elapsed
	}

	var lines []string
	for _, r := range results {
		icon := "\u2705"
		if r.ExitCode != 0 {
			icon = "\u274c"
		}
		line := fmt.Sprintf("%s **%s** (%.1fs)", icon, r.Name, r.Elapsed)
		summary := extractSummary(r.ExitCode, r.Output)
		if summary != "" {
			line += "\n" + summary
		}
		lines = append(lines, line)
	}

	description := strings.Join(lines, "\n")
	if len(description) > 3900 {
		description = description[:3900] + "\n..."
	}

	color := 0x00FF00
	if failed > 0 && passed > 0 {
		color = 0xFF9900
	} else if failed > 0 {
		color = 0xFF0000
	}

	title := fmt.Sprintf("Dispatcher: %d ok, %d failed (%.0fs)", passed, failed, totalTime)

	payload := map[string]interface{}{
		"embeds": []map[string]interface{}{
			{
				"title":       title,
				"color":       color,
				"description": description,
				"timestamp":   time.Now().UTC().Format(time.RFC3339),
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("  Discord notification marshal failed: %v\n", err)
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Printf("  Discord notification failed: %v\n", err)
		return
	}
	resp.Body.Close()
}

func SendNtfySummary(results []JobResult, ntfyURL string, topic string, token string, priority string) {
	if len(results) == 0 || (ntfyURL == "" && topic == "") {
		return
	}

	// Build the target URL
	url := ntfyURL
	if url == "" {
		url = "https://ntfy.sh"
	}
	if topic != "" {
		url = strings.TrimRight(url, "/") + "/" + topic
	}

	passed := 0
	failed := 0
	totalTime := 0.0
	for _, r := range results {
		if r.ExitCode == 0 {
			passed++
		} else {
			failed++
		}
		totalTime += r.Elapsed
	}

	title := fmt.Sprintf("Dispatcher: %d ok, %d failed (%.0fs)", passed, failed, totalTime)

	var lines []string
	for _, r := range results {
		icon := "ok"
		if r.ExitCode != 0 {
			icon = "FAIL"
		}
		line := fmt.Sprintf("[%s] %s (%.1fs)", icon, r.Name, r.Elapsed)
		lines = append(lines, line)
	}
	body := strings.Join(lines, "\n")

	if priority == "" {
		if failed > 0 {
			priority = "high"
		} else {
			priority = "default"
		}
	}

	tags := "white_check_mark"
	if failed > 0 && passed > 0 {
		tags = "warning"
	} else if failed > 0 {
		tags = "x"
	}

	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		fmt.Printf("  ntfy notification failed: %v\n", err)
		return
	}
	req.Header.Set("Title", title)
	req.Header.Set("Priority", priority)
	req.Header.Set("Tags", tags)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("  ntfy notification failed: %v\n", err)
		return
	}
	resp.Body.Close()
}
