package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// recorder captures every request a notifier makes, so tests can assert on
// which channels fired and what they sent.
type recorder struct {
	mu       sync.Mutex
	requests []recordedRequest
}

type recordedRequest struct {
	Path   string
	Header http.Header
	Body   string
}

func newRecorder(t *testing.T) (*recorder, string) {
	t.Helper()
	rec := &recorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.requests = append(rec.requests, recordedRequest{Path: r.URL.Path, Header: r.Header.Clone(), Body: string(body)})
		rec.mu.Unlock()
		w.WriteHeader(204)
	}))
	t.Cleanup(server.Close)
	return rec, server.URL
}

func (r *recorder) all() []recordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedRequest(nil), r.requests...)
}

func (r *recorder) withPath(path string) []recordedRequest {
	var out []recordedRequest
	for _, req := range r.all() {
		if req.Path == path {
			out = append(out, req)
		}
	}
	return out
}

func TestSendAll_FailureOnlyPolicy(t *testing.T) {
	rec, url := newRecorder(t)
	cfg := NotifyConfig{On: "failure", DiscordWebhook: url + "/discord", NtfyURL: url, NtfyTopic: "topic"}

	SendAll([]JobResult{
		{Name: "ok_job", ExitCode: 0, Elapsed: 1},
		{Name: "bad_job", ExitCode: 1, Elapsed: 2},
	}, cfg)

	discord := rec.withPath("/discord")
	if len(discord) != 1 {
		t.Fatalf("got %d Discord requests, want 1", len(discord))
	}
	if !strings.Contains(discord[0].Body, "bad_job") || strings.Contains(discord[0].Body, "ok_job") {
		t.Errorf("summary should only contain the failed job: %s", discord[0].Body)
	}
	if len(rec.withPath("/topic")) != 1 {
		t.Errorf("expected one ntfy request, got %d", len(rec.withPath("/topic")))
	}
}

func TestSendAll_PerJobOverrideBeatsGlobalPolicy(t *testing.T) {
	rec, url := newRecorder(t)
	cfg := NotifyConfig{On: "failure", DiscordWebhook: url + "/discord"}

	SendAll([]JobResult{{Name: "chatty", ExitCode: 0, Elapsed: 1, Notify: "always"}}, cfg)

	discord := rec.withPath("/discord")
	if len(discord) != 1 {
		t.Fatalf("got %d Discord requests, want 1", len(discord))
	}
	if !strings.Contains(discord[0].Body, "chatty") {
		t.Errorf("summary missing the job: %s", discord[0].Body)
	}
}

func TestSendAll_NothingToSend(t *testing.T) {
	rec, url := newRecorder(t)
	cfg := NotifyConfig{On: "failure", DiscordWebhook: url + "/discord", NtfyURL: url, NtfyTopic: "topic"}

	SendAll([]JobResult{{Name: "ok_job", ExitCode: 0, Elapsed: 1}}, cfg)

	if got := rec.all(); len(got) != 0 {
		t.Errorf("expected no notifications for a passing job under 'failure', got %d", len(got))
	}
}

func TestSendAll_OutputJobsSentIndividually(t *testing.T) {
	rec, url := newRecorder(t)
	cfg := NotifyConfig{On: "always", DiscordWebhook: url + "/discord", NtfyURL: url, NtfyTopic: "topic", NtfyToken: "tok"}

	SendAll([]JobResult{
		{Name: "report", ExitCode: 0, Elapsed: 1, Output: "line one\nline two", Notify: "output"},
		{Name: "regular", ExitCode: 0, Elapsed: 1, Output: "ok"},
	}, cfg)

	discord := rec.withPath("/discord")
	if len(discord) != 2 {
		t.Fatalf("got %d Discord requests, want 2 (summary + output)", len(discord))
	}
	var sawOutput bool
	for _, req := range discord {
		if strings.Contains(req.Body, "line two") {
			sawOutput = true
		}
	}
	if !sawOutput {
		t.Errorf("output notification should include the full job output: %v", discord)
	}

	ntfy := rec.withPath("/topic")
	if len(ntfy) != 2 {
		t.Fatalf("got %d ntfy requests, want 2", len(ntfy))
	}
	for _, req := range ntfy {
		if req.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing ntfy auth header: %v", req.Header)
		}
	}
}

func TestSendOutputNotification_Failure(t *testing.T) {
	rec, url := newRecorder(t)
	cfg := NotifyConfig{DiscordWebhook: url + "/discord", NtfyURL: url, NtfyTopic: "topic"}

	sendOutputNotification(JobResult{Name: "broken", ExitCode: 3, Output: "boom"}, cfg)

	discord := rec.withPath("/discord")
	if len(discord) != 1 {
		t.Fatalf("got %d Discord requests, want 1", len(discord))
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(discord[0].Body), &payload); err != nil {
		t.Fatal(err)
	}
	embed := payload["embeds"].([]any)[0].(map[string]any)
	if got := int(embed["color"].(float64)); got != 0xFF0000 {
		t.Errorf("color = %x, want red for a failed job", got)
	}
	if title := embed["title"].(string); title != "[FAIL] broken" {
		t.Errorf("title = %q, want [FAIL] broken", title)
	}

	ntfy := rec.withPath("/topic")
	if len(ntfy) != 1 {
		t.Fatalf("got %d ntfy requests, want 1", len(ntfy))
	}
	if ntfy[0].Header.Get("Priority") != "high" || ntfy[0].Header.Get("Tags") != "x" {
		t.Errorf("failed job should raise ntfy priority: %v", ntfy[0].Header)
	}
	if ntfy[0].Body != "boom" {
		t.Errorf("ntfy body = %q, want boom", ntfy[0].Body)
	}
}

func TestSendOutputNotification_EmptyOutputAndNoNtfy(t *testing.T) {
	rec, url := newRecorder(t)
	cfg := NotifyConfig{DiscordWebhook: url + "/discord"}

	sendOutputNotification(JobResult{Name: "quiet", ExitCode: 0, Output: "   "}, cfg)

	discord := rec.withPath("/discord")
	if len(discord) != 1 {
		t.Fatalf("got %d Discord requests, want 1", len(discord))
	}
	if !strings.Contains(discord[0].Body, "(no output)") {
		t.Errorf("blank output should render as (no output): %s", discord[0].Body)
	}
	if len(rec.all()) != 1 {
		t.Errorf("ntfy should be skipped when unconfigured, got %d requests", len(rec.all()))
	}
}
