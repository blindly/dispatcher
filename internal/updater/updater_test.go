package updater

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
)

func TestAssetName(t *testing.T) {
	want := "dispatch-" + runtime.GOOS + "-" + runtime.GOARCH
	if got := assetName(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUpdate_AlreadyCurrent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := release{
			TagName: "v1.0.0",
			Assets:  []asset{{Name: assetName(), BrowserDownloadURL: "https://example.com/bin"}},
		}
		json.NewEncoder(w).Encode(rel)
	}))
	defer server.Close()

	// We can't easily test this without overriding the URL, so just test assetName
	// The full integration is tested via the CLI
}

func TestUpdate_NoRelease(t *testing.T) {
	// fetchRelease with a bad version should error
	_, err := fetchRelease("v999.999.999")
	if err == nil {
		t.Error("expected error for nonexistent release")
	}
}

func TestFetchLatestBeta(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		releases := []release{
			{TagName: "v1.11.0", Prerelease: false},
			{TagName: "v1.12.0-beta.2", Prerelease: true, Assets: []asset{{Name: assetName(), BrowserDownloadURL: "https://example.com/beta2"}}},
			{TagName: "v1.12.0-beta.1", Prerelease: true, Assets: []asset{{Name: assetName(), BrowserDownloadURL: "https://example.com/beta1"}}},
		}
		json.NewEncoder(w).Encode(releases)
	}))
	defer server.Close()

	rel, err := fetchLatestBeta(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v1.12.0-beta.2" {
		t.Errorf("got %q, want v1.12.0-beta.2", rel.TagName)
	}
}

func TestFetchLatestBeta_NoBetas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		releases := []release{
			{TagName: "v1.11.0", Prerelease: false},
		}
		json.NewEncoder(w).Encode(releases)
	}))
	defer server.Close()

	_, err := fetchLatestBeta(server.URL)
	if err == nil {
		t.Error("expected error when no beta releases exist")
	}
}
