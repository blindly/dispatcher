package updater

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
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

// stubAPI points repoAPI at a test server for the duration of the test.
func stubAPI(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	old := repoAPI
	repoAPI = server.URL
	t.Cleanup(func() {
		repoAPI = old
		server.Close()
	})
	return server
}

func TestIsPreleaseTag(t *testing.T) {
	tests := []struct {
		tag  string
		want bool
	}{
		{"v1.0.0", false},
		{"v1.0.0-beta.1", true},
		{"v1.0.0-alpha", true},
		{"v1.0.0-rc1", true},
		{"", false},
	}
	for _, tt := range tests {
		if got := isPreleaseTag(tt.tag); got != tt.want {
			t.Errorf("isPreleaseTag(%q) = %v, want %v", tt.tag, got, tt.want)
		}
	}
}

func TestFetchReleaseByTag_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(release{TagName: "v1.2.3"})
	}))
	defer server.Close()

	rel, err := fetchReleaseByTag(server.URL, "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v1.2.3" {
		t.Errorf("got %q, want v1.2.3", rel.TagName)
	}
}

func TestFetchReleaseByTag_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer server.Close()

	_, err := fetchReleaseByTag(server.URL, "v9.9.9")
	if err == nil || !strings.Contains(err.Error(), "v9.9.9 not found") {
		t.Errorf("got %v, want release-not-found error naming the version", err)
	}

	_, err = fetchReleaseByTag(server.URL, "")
	if err == nil || !strings.Contains(err.Error(), "no releases found") {
		t.Errorf("got %v, want no-releases error", err)
	}
}

func TestFetchReleaseByTag_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer server.Close()

	_, err := fetchReleaseByTag(server.URL, "")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("got %v, want error mentioning status 500", err)
	}
}

func TestFetchReleaseByTag_BadJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer server.Close()

	_, err := fetchReleaseByTag(server.URL, "")
	if err == nil || !strings.Contains(err.Error(), "parsing release") {
		t.Errorf("got %v, want parse error", err)
	}
}

func TestFetchReleaseByTag_Unreachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := server.URL
	server.Close()

	if _, err := fetchReleaseByTag(url, ""); err == nil {
		t.Error("expected error when the server is unreachable")
	}
}

func TestFetchRelease_SpecificVersion(t *testing.T) {
	stubAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tags/v1.2.3" {
			t.Errorf("requested %q, want /tags/v1.2.3", r.URL.Path)
		}
		json.NewEncoder(w).Encode(release{TagName: "v1.2.3"})
	})

	rel, err := fetchRelease("v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v1.2.3" {
		t.Errorf("got %q, want v1.2.3", rel.TagName)
	}
}

func TestFetchRelease_LatestStable(t *testing.T) {
	stubAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/latest" {
			t.Errorf("requested %q, want /latest", r.URL.Path)
		}
		json.NewEncoder(w).Encode(release{TagName: "v1.11.0"})
	})

	rel, err := fetchRelease("")
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v1.11.0" {
		t.Errorf("got %q, want v1.11.0", rel.TagName)
	}
}

// GitHub's /latest sometimes returns a pre-release that wasn't flagged as one;
// fetchRelease must then scan the full release list for a stable tag.
func TestFetchRelease_LatestPrereleaseFallsBackToList(t *testing.T) {
	stubAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest" {
			json.NewEncoder(w).Encode(release{TagName: "v1.12.0-beta.1"})
			return
		}
		json.NewEncoder(w).Encode([]release{
			{TagName: "v1.12.0-beta.1", Prerelease: true},
			{TagName: "v1.11.0"},
		})
	})

	rel, err := fetchRelease("")
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v1.11.0" {
		t.Errorf("got %q, want v1.11.0", rel.TagName)
	}
}

func TestFetchRelease_LatestError(t *testing.T) {
	stubAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	})

	if _, err := fetchRelease(""); err == nil {
		t.Error("expected error when the API is failing")
	}
}

func TestFetchLatestStable_Errors(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer failing.Close()
	if _, err := fetchLatestStable(failing.URL); err == nil {
		t.Error("expected error for HTTP 500")
	}

	badJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{"))
	}))
	defer badJSON.Close()
	if _, err := fetchLatestStable(badJSON.URL); err == nil {
		t.Error("expected error for malformed JSON")
	}

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]release{})
	}))
	defer empty.Close()
	if _, err := fetchLatestStable(empty.URL); err == nil {
		t.Error("expected error when there are no stable releases")
	}
}

func TestFetchLatestBeta_Errors(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer failing.Close()
	if _, err := fetchLatestBeta(failing.URL); err == nil {
		t.Error("expected error for HTTP 500")
	}

	badJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{"))
	}))
	defer badJSON.Close()
	if _, err := fetchLatestBeta(badJSON.URL); err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestUpdate_AlreadyUpToDate(t *testing.T) {
	stubAPI(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(release{
			TagName: "v1.11.0",
			Assets:  []asset{{Name: assetName(), BrowserDownloadURL: "http://127.0.0.1:0/should-not-be-fetched"}},
		})
	})

	if err := Update("v1.11.0", ""); err != nil {
		t.Errorf("expected no error when already current, got %v", err)
	}
}

func TestUpdate_NoAssetForPlatform(t *testing.T) {
	stubAPI(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(release{
			TagName: "v1.12.0",
			Assets:  []asset{{Name: "dispatch-plan9-mips", BrowserDownloadURL: "http://example.invalid/bin"}},
		})
	})

	err := Update("v1.11.0", "")
	if err == nil || !strings.Contains(err.Error(), "no binary found") {
		t.Errorf("got %v, want no-binary-found error", err)
	}
}

func TestUpdate_BetaTargetUsesPrereleases(t *testing.T) {
	stubAPI(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]release{
			{TagName: "v1.11.0"},
			{TagName: "v1.12.0-beta.1", Prerelease: true, Assets: []asset{{Name: "dispatch-plan9-mips"}}},
		})
	})

	// The beta release carries no asset for this platform, so Update stops
	// before touching the running binary — but the error proves it resolved
	// the beta tag rather than the stable one.
	err := Update("v1.11.0", "beta")
	if err == nil || !strings.Contains(err.Error(), "v1.12.0-beta.1") {
		t.Errorf("got %v, want error naming the beta release", err)
	}
}

func TestUpdate_DownloadFails(t *testing.T) {
	var server *httptest.Server
	server = stubAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/download" {
			w.WriteHeader(500)
			return
		}
		json.NewEncoder(w).Encode(release{
			TagName: "v1.12.0",
			Assets:  []asset{{Name: assetName(), BrowserDownloadURL: server.URL + "/download"}},
		})
	})

	err := Update("v1.11.0", "")
	if err == nil || !strings.Contains(err.Error(), "download returned 500") {
		t.Errorf("got %v, want download failure error", err)
	}
}

func TestUpdate_FetchError(t *testing.T) {
	stubAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	})

	if err := Update("v1.11.0", ""); err == nil {
		t.Error("expected error when the release lookup fails")
	}
}
