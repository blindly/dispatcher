package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	httpTimeout = 10 * time.Second
	// discordMaxDescription is Discord's practical embed description limit.
	discordMaxDescription = 3900
	defaultNtfyURL        = "https://ntfy.sh"
)

// truncate shortens s to max runes-worth of bytes, appending suffix when it had to cut.
func truncate(s string, max int, suffix string) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + suffix
}

// Tally counts successes, failures and total runtime across results.
func Tally(results []JobResult) (passed, failed int, totalTime float64) {
	for _, r := range results {
		if r.ExitCode == 0 {
			passed++
		} else {
			failed++
		}
		totalTime += r.Elapsed
	}
	return
}

// summaryTitle is the shared headline for Discord and ntfy batch notifications.
func summaryTitle(passed, failed int, totalTime float64) string {
	return fmt.Sprintf("Dispatcher: %d ok, %d failed (%.0fs)", passed, failed, totalTime)
}

// postDiscordEmbed sends a single embed to a Discord webhook.
func postDiscordEmbed(webhookURL, title, description string, color int) error {
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
		return err
	}

	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ntfyEndpoint resolves the publish URL for a base URL + topic pair.
// It returns "" when neither is configured.
func ntfyEndpoint(baseURL, topic string) string {
	if baseURL == "" && topic == "" {
		return ""
	}
	if baseURL == "" {
		baseURL = defaultNtfyURL
	}
	if topic != "" {
		return strings.TrimRight(baseURL, "/") + "/" + topic
	}
	return baseURL
}

// postNtfy publishes a message to an ntfy endpoint. Empty priority, tags or
// token leave the corresponding header unset.
func postNtfy(url, title, body, priority, tags, token string) error {
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", title)
	if priority != "" {
		req.Header.Set("Priority", priority)
	}
	if tags != "" {
		req.Header.Set("Tags", tags)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
