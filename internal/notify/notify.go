package notify

import (
	"fmt"
	"os"
	"strings"
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
	// Separate output-mode jobs from summary jobs
	var summary []JobResult
	var outputJobs []JobResult

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
		SendDiscordSummary(summary, cfg.DiscordWebhook)
		SendNtfySummary(summary, cfg.NtfyURL, cfg.NtfyTopic, cfg.NtfyToken, cfg.NtfyPriority)
	}

	// Send individual output notifications
	for _, r := range outputJobs {
		sendOutputNotification(r, cfg)
	}
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
			return truncate("```\n"+strings.Join(tail, "\n")+"\n```", 300, "")
		}
		return ""
	}
	if len(nonEmpty) > 0 {
		return truncate(nonEmpty[len(nonEmpty)-1], 200, "")
	}
	return ""
}

func SendDiscordSummary(results []JobResult, webhookURL string) {
	if len(results) == 0 || webhookURL == "" {
		return
	}

	passed, failed, totalTime := Tally(results)

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

	description := truncate(strings.Join(lines, "\n"), discordMaxDescription, "\n...")

	color := 0x00FF00
	if failed > 0 && passed > 0 {
		color = 0xFF9900
	} else if failed > 0 {
		color = 0xFF0000
	}

	if err := postDiscordEmbed(webhookURL, summaryTitle(passed, failed, totalTime), description, color); err != nil {
		fmt.Printf("  Discord notification failed: %v\n", err)
	}
}

func SendNtfySummary(results []JobResult, ntfyURL string, topic string, token string, priority string) {
	url := ntfyEndpoint(ntfyURL, topic)
	if len(results) == 0 || url == "" {
		return
	}

	passed, failed, totalTime := Tally(results)

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

	if err := postNtfy(url, summaryTitle(passed, failed, totalTime), body, priority, tags, token); err != nil {
		fmt.Printf("  ntfy notification failed: %v\n", err)
	}
}

func sendOutputNotification(r JobResult, cfg NotifyConfig) {
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
		description := truncate(output, discordMaxDescription, "\n...")
		color := 0x4A9EFF // blue for output
		if r.ExitCode != 0 {
			color = 0xFF0000
		}
		if err := postDiscordEmbed(cfg.DiscordWebhook, title, "```\n"+description+"\n```", color); err != nil {
			fmt.Printf("  Discord output notification failed: %v\n", err)
			return
		}
	}

	// ntfy
	ntfyURL := ntfyEndpoint(cfg.NtfyURL, cfg.NtfyTopic)
	if ntfyURL == "" {
		return
	}

	priority, tags := "", ""
	if r.ExitCode != 0 {
		priority, tags = "high", "x"
	}
	if err := postNtfy(ntfyURL, title, output, priority, tags, cfg.NtfyToken); err != nil {
		fmt.Printf("  ntfy output notification failed: %v\n", err)
	}
}

func SendLiveNotification(message string, jobName string, cfg NotifyConfig) error {
	title := "Live Update"
	if jobName != "" {
		title = fmt.Sprintf("[%s] Live Update", jobName)
	}

	sent := false

	// Discord
	if cfg.DiscordWebhook != "" {
		if err := postDiscordEmbed(cfg.DiscordWebhook, title, message, 0x7289DA); err != nil {
			fmt.Fprintf(os.Stderr, "Discord live notification failed: %v\n", err)
		} else {
			sent = true
		}
	}

	// ntfy
	if ntfyURL := ntfyEndpoint(cfg.NtfyURL, cfg.NtfyTopic); ntfyURL != "" {
		if err := postNtfy(ntfyURL, title, message, "default", "speech_balloon", cfg.NtfyToken); err != nil {
			fmt.Fprintf(os.Stderr, "ntfy live notification failed: %v\n", err)
		} else {
			sent = true
		}
	}

	if !sent {
		return fmt.Errorf("no notification channels configured")
	}
	return nil
}
