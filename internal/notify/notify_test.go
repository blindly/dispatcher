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
