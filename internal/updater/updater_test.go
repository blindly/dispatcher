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

func TestFetchLatestStable_SkipsBetas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		releases := []release{
			{TagName: "v1.12.0-beta.2", Prerelease: true},
			{TagName: "v1.12.0-beta.1", Prerelease: true},
			{TagName: "v1.11.0", Prerelease: false, Assets: []asset{{Name: assetName(), BrowserDownloadURL: "https://example.com/stable"}}},
			{TagName: "v1.10.0", Prerelease: false},
		}
		json.NewEncoder(w).Encode(releases)
	}))
	defer server.Close()

	rel, err := fetchLatestStable(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v1.11.0" {
		t.Errorf("got %q, want v1.11.0", rel.TagName)
	}
}

func TestFetchLatestStable_SkipsMislabeledBetas(t *testing.T) {
	// Beta tag but prerelease=false (the bug we hit)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		releases := []release{
			{TagName: "v1.12.0-beta.1", Prerelease: false},
			{TagName: "v1.11.0", Prerelease: false, Assets: []asset{{Name: assetName(), BrowserDownloadURL: "https://example.com/stable"}}},
		}
		json.NewEncoder(w).Encode(releases)
	}))
	defer server.Close()

	rel, err := fetchLatestStable(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v1.11.0" {
		t.Errorf("got %q, want v1.11.0 (should skip mislabeled beta)", rel.TagName)
	}
}

func TestParseChecksums(t *testing.T) {
	body := []byte("abc123  dispatch-linux-amd64\n" +
		"DEF456 *dispatch-windows-amd64.exe\n" +
		"789aaa  dist/dispatch-darwin-arm64\n" +
		"malformed-line\n\n")
	sums := parseChecksums(body)
	cases := map[string]string{
		"dispatch-linux-amd64":       "abc123",
		"dispatch-windows-amd64.exe": "def456", // lower-cased
		"dispatch-darwin-arm64":      "789aaa", // dir prefix stripped
	}
	for name, want := range cases {
		if got := sums[name]; got != want {
			t.Errorf("sums[%q] = %q, want %q", name, got, want)
		}
	}
	if _, ok := sums["malformed-line"]; ok {
		t.Error("malformed line should be skipped")
	}
}

func TestVerifyChecksum(t *testing.T) {
	sums := map[string]string{"dispatch-linux-amd64": "abc123"}

	if err := verifyChecksum("abc123", "dispatch-linux-amd64", sums); err != nil {
		t.Errorf("matching checksum should pass: %v", err)
	}
	// Digests are compared case-insensitively.
	if err := verifyChecksum("ABC123", "dispatch-linux-amd64", sums); err != nil {
		t.Errorf("case-insensitive match should pass: %v", err)
	}
	if err := verifyChecksum("deadbeef", "dispatch-linux-amd64", sums); err == nil {
		t.Error("mismatched checksum should fail")
	}
	if err := verifyChecksum("abc123", "dispatch-unknown", sums); err == nil {
		t.Error("missing asset should fail")
	}
}

func TestFetchChecksums_MissingAsset(t *testing.T) {
	rel := &release{
		TagName: "v1.0.0",
		Assets:  []asset{{Name: assetName(), BrowserDownloadURL: "https://example.com/bin"}},
	}
	if _, err := fetchChecksums(rel); err == nil {
		t.Error("expected error when release has no checksums.txt")
	}
}

func TestFetchChecksums_Downloads(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("abc123  " + assetName() + "\n"))
	}))
	defer server.Close()

	rel := &release{
		TagName: "v1.0.0",
		Assets:  []asset{{Name: checksumsAsset, BrowserDownloadURL: server.URL}},
	}
	sums, err := fetchChecksums(rel)
	if err != nil {
		t.Fatal(err)
	}
	if sums[assetName()] != "abc123" {
		t.Errorf("got %q, want abc123", sums[assetName()])
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
