package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendDiscordSummary_AllPass(t *testing.T) {
	var gotPayload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotPayload)
		w.WriteHeader(204)
	}))
	defer server.Close()

	results := []JobResult{
		{Name: "job1", ExitCode: 0, Elapsed: 1.5, Output: "ok"},
		{Name: "job2", ExitCode: 0, Elapsed: 2.0, Output: "done"},
	}
	SendDiscordSummary(results, server.URL)

	embeds := gotPayload["embeds"].([]interface{})
	embed := embeds[0].(map[string]interface{})
	color := int(embed["color"].(float64))
	if color != 0x00FF00 {
		t.Errorf("color = %x, want green (00FF00)", color)
	}
}

func TestSendDiscordSummary_MixedResults(t *testing.T) {
	var gotPayload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotPayload)
		w.WriteHeader(204)
	}))
	defer server.Close()

	results := []JobResult{
		{Name: "job1", ExitCode: 0, Elapsed: 1.5, Output: "ok"},
		{Name: "job2", ExitCode: 1, Elapsed: 2.0, Output: "error line"},
	}
	SendDiscordSummary(results, server.URL)

	embeds := gotPayload["embeds"].([]interface{})
	embed := embeds[0].(map[string]interface{})
	color := int(embed["color"].(float64))
	if color != 0xFF9900 {
		t.Errorf("color = %x, want yellow (FF9900)", color)
	}
}

func TestSendDiscordSummary_NoWebhook(t *testing.T) {
	results := []JobResult{{Name: "job1", ExitCode: 0, Elapsed: 1.0, Output: "ok"}}
	SendDiscordSummary(results, "")
}

func TestSendDiscordSummary_NoResults(t *testing.T) {
	SendDiscordSummary(nil, "https://example.com")
}

func TestSendNtfySummary_AllPass(t *testing.T) {
	var gotTitle, gotPriority, gotTags, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTitle = r.Header.Get("Title")
		gotPriority = r.Header.Get("Priority")
		gotTags = r.Header.Get("Tags")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(200)
	}))
	defer server.Close()

	results := []JobResult{
		{Name: "job1", ExitCode: 0, Elapsed: 1.5, Output: "ok"},
	}
	SendNtfySummary(results, server.URL, "", "", "")

	if gotTitle != "Dispatcher: 1 ok, 0 failed (2s)" {
		t.Errorf("title = %q", gotTitle)
	}
	if gotPriority != "default" {
		t.Errorf("priority = %q, want default", gotPriority)
	}
	if gotTags != "white_check_mark" {
		t.Errorf("tags = %q", gotTags)
	}
	if gotBody == "" {
		t.Error("body is empty")
	}
}

func TestSendNtfySummary_WithFailures(t *testing.T) {
	var gotPriority, gotTags string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPriority = r.Header.Get("Priority")
		gotTags = r.Header.Get("Tags")
		w.WriteHeader(200)
	}))
	defer server.Close()

	results := []JobResult{
		{Name: "job1", ExitCode: 1, Elapsed: 2.0, Output: "error"},
	}
	SendNtfySummary(results, server.URL, "", "", "")

	if gotPriority != "high" {
		t.Errorf("priority = %q, want high", gotPriority)
	}
	if gotTags != "x" {
		t.Errorf("tags = %q, want x", gotTags)
	}
}

func TestSendNtfySummary_WithTopic(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(200)
	}))
	defer server.Close()

	results := []JobResult{{Name: "job1", ExitCode: 0, Elapsed: 1.0, Output: "ok"}}
	SendNtfySummary(results, server.URL, "my-dispatch", "", "")

	if gotPath != "/my-dispatch" {
		t.Errorf("path = %q, want /my-dispatch", gotPath)
	}
}

func TestSendNtfySummary_WithToken(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer server.Close()

	results := []JobResult{{Name: "job1", ExitCode: 0, Elapsed: 1.0, Output: "ok"}}
	SendNtfySummary(results, server.URL, "", "tk_mytoken123", "")

	if gotAuth != "Bearer tk_mytoken123" {
		t.Errorf("auth = %q, want Bearer tk_mytoken123", gotAuth)
	}
}

func TestSendNtfySummary_NoURL(t *testing.T) {
	// Should not panic
	results := []JobResult{{Name: "job1", ExitCode: 0, Elapsed: 1.0, Output: "ok"}}
	SendNtfySummary(results, "", "", "", "")
}

func TestSendLiveDiscord_WithJob(t *testing.T) {
	var gotPayload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotPayload)
		w.WriteHeader(204)
	}))
	defer server.Close()

	cfg := NotifyConfig{DiscordWebhook: server.URL}
	SendLiveNotification("Backup 50% complete", "db-backup", cfg)

	embeds := gotPayload["embeds"].([]interface{})
	embed := embeds[0].(map[string]interface{})

	title := embed["title"].(string)
	if title != "[db-backup] Live Update" {
		t.Errorf("title = %q, want [db-backup] Live Update", title)
	}

	color := int(embed["color"].(float64))
	if color != 0x7289DA {
		t.Errorf("color = %x, want 7289DA", color)
	}

	desc := embed["description"].(string)
	if desc != "Backup 50% complete" {
		t.Errorf("description = %q, want message text", desc)
	}
}

func TestSendLiveDiscord_NoJob(t *testing.T) {
	var gotPayload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotPayload)
		w.WriteHeader(204)
	}))
	defer server.Close()

	cfg := NotifyConfig{DiscordWebhook: server.URL}
	SendLiveNotification("Something happened", "", cfg)

	embeds := gotPayload["embeds"].([]interface{})
	embed := embeds[0].(map[string]interface{})

	title := embed["title"].(string)
	if title != "Live Update" {
		t.Errorf("title = %q, want Live Update", title)
	}
}

func TestSendLiveNtfy_WithJob(t *testing.T) {
	var gotTitle, gotTags, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTitle = r.Header.Get("Title")
		gotTags = r.Header.Get("Tags")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(200)
	}))
	defer server.Close()

	cfg := NotifyConfig{NtfyURL: server.URL}
	err := SendLiveNotification("Step 3 done", "db-backup", cfg)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if gotTitle != "[db-backup] Live Update" {
		t.Errorf("title = %q, want [db-backup] Live Update", gotTitle)
	}
	if gotTags != "speech_balloon" {
		t.Errorf("tags = %q, want speech_balloon", gotTags)
	}
	if gotBody != "Step 3 done" {
		t.Errorf("body = %q, want message text", gotBody)
	}
}

func TestSendLiveNotification_NoChannels(t *testing.T) {
	cfg := NotifyConfig{}
	err := SendLiveNotification("hello", "test", cfg)
	if err == nil {
		t.Error("expected error when no channels configured")
	}
}

func TestSendOutputNotification_EmptyOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("should not send notification when output is empty")
	}))
	defer server.Close()

	cfg := NotifyConfig{
		DiscordWebhook: server.URL,
	}
	result := JobResult{
		Name:     "test-job",
		ExitCode: 0,
		Output:   "",
		Notify:   "output",
	}
	sendOutputNotification(result, cfg)
}

func TestSendOutputNotification_WithOutput(t *testing.T) {
	var gotPayload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotPayload)
	}))
	defer server.Close()

	cfg := NotifyConfig{
		DiscordWebhook: server.URL,
	}
	result := JobResult{
		Name:     "test-job",
		ExitCode: 0,
		Output:   "some output",
		Notify:   "output",
	}
	sendOutputNotification(result, cfg)

	if gotPayload == nil {
		t.Fatal("notification should be sent when output is present")
	}
	embeds := gotPayload["embeds"].([]interface{})
	embed := embeds[0].(map[string]interface{})
	description := embed["description"].(string)
	if description != "```\nsome output\n```" {
		t.Errorf("description = %q, want output in code block", description)
	}
}
