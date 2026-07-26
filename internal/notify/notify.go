package notify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// checkResponse drains and closes resp, returning an error when the endpoint
// rejected the notification. Without this a 4xx/5xx from Discord or ntfy
// (bad webhook, revoked token, rate limit) looks exactly like a success.
func checkResponse(resp *http.Response, channel string) error {
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	detail := strings.TrimSpace(string(body))
	if detail == "" {
		return fmt.Errorf("%s returned HTTP %d", channel, resp.StatusCode)
	}
	return fmt.Errorf("%s returned HTTP %d: %s", channel, resp.StatusCode, detail)
}

// SendAll delivers summary and per-job output notifications. Delivery failures
// are returned joined together; they never abort the remaining sends.
func SendAll(results []JobResult, cfg NotifyConfig) error {
	// Separate output-mode jobs from summary jobs
	var summary []JobResult
	var outputJobs []JobResult
	var errs []error

	for _, r := range results {
		if r.Notify == "output" {
			outputJobs = append(outputJobs, r)
			continue
		}
		policy := cfg.On
		if r.Notify != "" {
			policy = r.Notify
		}
		if policy == "failure" && r.ExitCode == 0 {
			continue
		}
		summary = append(summary, r)
	}

	// Send summary notification for regular jobs
	if len(summary) > 0 {
		if err := SendDiscordSummary(summary, cfg.DiscordWebhook); err != nil {
			errs = append(errs, err)
		}
		if err := SendNtfySummary(summary, cfg.NtfyURL, cfg.NtfyTopic, cfg.NtfyToken, cfg.NtfyPriority); err != nil {
			errs = append(errs, err)
		}
	}

	// Send individual output notifications
	for _, r := range outputJobs {
		if err := sendOutputNotification(r, cfg); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type JobResult struct {
	Name     string
	ExitCode int
	Elapsed  float64
	Output   string
	Notify   string // per-job override: "always", "failure", or ""
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

func SendDiscordSummary(results []JobResult, webhookURL string) error {
	if len(results) == 0 || webhookURL == "" {
		return nil
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
		return fmt.Errorf("building discord payload: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("discord notification failed: %w", err)
	}
	return checkResponse(resp, "discord")
}

func SendNtfySummary(results []JobResult, ntfyURL string, topic string, token string, priority string) error {
	if len(results) == 0 || (ntfyURL == "" && topic == "") {
		return nil
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
		return fmt.Errorf("building ntfy request: %w", err)
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
		return fmt.Errorf("ntfy notification failed: %w", err)
	}
	return checkResponse(resp, "ntfy")
}

func sendOutputNotification(r JobResult, cfg NotifyConfig) error {
	var errs []error
	output := strings.TrimSpace(r.Output)
	if output == "" {
		output = "(no output)"
	}

	icon := "ok"
	if r.ExitCode != 0 {
		icon = "FAIL"
	}
	title := fmt.Sprintf("[%s] %s", icon, r.Name)

	// Discord
	if cfg.DiscordWebhook != "" {
		description := output
		if len(description) > 3900 {
			description = description[:3900] + "\n..."
		}
		color := 0x4A9EFF // blue for output
		if r.ExitCode != 0 {
			color = 0xFF0000
		}
		payload := map[string]interface{}{
			"embeds": []map[string]interface{}{
				{
					"title":       title,
					"color":       color,
					"description": "```\n" + description + "\n```",
					"timestamp":   time.Now().UTC().Format(time.RFC3339),
				},
			},
		}
		body, err := json.Marshal(payload)
		if err != nil {
			errs = append(errs, fmt.Errorf("building discord output payload for %s: %w", r.Name, err))
		} else {
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Post(cfg.DiscordWebhook, "application/json", bytes.NewReader(body))
			if err != nil {
				errs = append(errs, fmt.Errorf("discord output notification for %s failed: %w", r.Name, err))
			} else if err := checkResponse(resp, "discord"); err != nil {
				errs = append(errs, fmt.Errorf("output notification for %s: %w", r.Name, err))
			}
		}
	}

	// ntfy
	ntfyURL := cfg.NtfyURL
	if ntfyURL == "" && cfg.NtfyTopic == "" {
		return errors.Join(errs...)
	}
	if ntfyURL == "" {
		ntfyURL = "https://ntfy.sh"
	}
	if cfg.NtfyTopic != "" {
		ntfyURL = strings.TrimRight(ntfyURL, "/") + "/" + cfg.NtfyTopic
	}

	req, err := http.NewRequest("POST", ntfyURL, strings.NewReader(output))
	if err != nil {
		return errors.Join(append(errs, fmt.Errorf("building ntfy output request for %s: %w", r.Name, err))...)
	}
	req.Header.Set("Title", title)
	if r.ExitCode != 0 {
		req.Header.Set("Priority", "high")
		req.Header.Set("Tags", "x")
	}
	if cfg.NtfyToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.NtfyToken)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		errs = append(errs, fmt.Errorf("ntfy output notification for %s failed: %w", r.Name, err))
	} else if err := checkResponse(resp, "ntfy"); err != nil {
		errs = append(errs, fmt.Errorf("output notification for %s: %w", r.Name, err))
	}
	return errors.Join(errs...)
}

func SendLiveNotification(message string, jobName string, cfg NotifyConfig) error {
	title := "Live Update"
	if jobName != "" {
		title = fmt.Sprintf("[%s] Live Update", jobName)
	}

	sent := false
	var errs []error

	// Discord
	if cfg.DiscordWebhook != "" {
		payload := map[string]interface{}{
			"embeds": []map[string]interface{}{
				{
					"title":       title,
					"color":       0x7289DA,
					"description": message,
					"timestamp":   time.Now().UTC().Format(time.RFC3339),
				},
			},
		}
		body, err := json.Marshal(payload)
		if err != nil {
			errs = append(errs, fmt.Errorf("building discord payload: %w", err))
		} else {
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Post(cfg.DiscordWebhook, "application/json", bytes.NewReader(body))
			if err != nil {
				errs = append(errs, fmt.Errorf("discord live notification failed: %w", err))
			} else if err := checkResponse(resp, "discord"); err != nil {
				errs = append(errs, err)
			} else {
				sent = true
			}
		}
	}

	// ntfy
	ntfyURL := cfg.NtfyURL
	if ntfyURL == "" && cfg.NtfyTopic != "" {
		ntfyURL = "https://ntfy.sh"
	}
	if ntfyURL != "" {
		if cfg.NtfyTopic != "" {
			ntfyURL = strings.TrimRight(ntfyURL, "/") + "/" + cfg.NtfyTopic
		}
		req, err := http.NewRequest("POST", ntfyURL, strings.NewReader(message))
		if err != nil {
			errs = append(errs, fmt.Errorf("building ntfy request: %w", err))
		} else {
			req.Header.Set("Title", title)
			req.Header.Set("Priority", "default")
			req.Header.Set("Tags", "speech_balloon")
			if cfg.NtfyToken != "" {
				req.Header.Set("Authorization", "Bearer "+cfg.NtfyToken)
			}
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				errs = append(errs, fmt.Errorf("ntfy live notification failed: %w", err))
			} else if err := checkResponse(resp, "ntfy"); err != nil {
				errs = append(errs, err)
			} else {
				sent = true
			}
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	if !sent {
		return fmt.Errorf("no notification channels configured")
	}
	return nil
}
