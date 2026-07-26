package updater

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// repoAPI is a var (not a const) so tests can point it at a stub server.
var repoAPI = "https://api.github.com/repos/blindly/dispatcher/releases"

type release struct {
	TagName    string  `json:"tag_name"`
	Prerelease bool    `json:"prerelease"`
	Assets     []asset `json:"assets"`
}

type asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func assetName() string {
	name := fmt.Sprintf("dispatch-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func isPreleaseTag(tag string) bool {
	return strings.Contains(tag, "-beta") || strings.Contains(tag, "-alpha") || strings.Contains(tag, "-rc")
}

func fetchRelease(version string) (*release, error) {
	// Specific version requested — fetch directly
	if version != "" {
		return fetchReleaseByTag(repoAPI+"/tags/"+version, version)
	}

	// Fetch latest stable — try /latest first, fall back to listing if it returns a pre-release
	rel, err := fetchReleaseByTag(repoAPI+"/latest", "")
	if err != nil {
		return nil, err
	}
	if !isPreleaseTag(rel.TagName) {
		return rel, nil
	}

	// /latest returned a pre-release (not marked properly on GitHub) — scan the list
	return fetchLatestStable(repoAPI)
}

func fetchReleaseByTag(url, version string) (*release, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		if version != "" {
			return nil, fmt.Errorf("release %s not found", version)
		}
		return nil, fmt.Errorf("no releases found")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var rel release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("parsing release: %w", err)
	}
	return &rel, nil
}

func fetchLatestStable(apiURL string) (*release, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("fetching releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var releases []release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("parsing releases: %w", err)
	}

	for _, rel := range releases {
		if !rel.Prerelease && !isPreleaseTag(rel.TagName) {
			r := rel
			return &r, nil
		}
	}
	return nil, fmt.Errorf("no stable releases found")
}

func fetchLatestBeta(apiURL string) (*release, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("fetching releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var releases []release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("parsing releases: %w", err)
	}

	for _, rel := range releases {
		if rel.Prerelease {
			r := rel
			return &r, nil
		}
	}
	return nil, fmt.Errorf("no beta releases found")
}

func Update(currentVersion string, targetVersion string) error {
	var rel *release
	var err error

	if targetVersion == "beta" {
		rel, err = fetchLatestBeta(repoAPI)
	} else {
		rel, err = fetchRelease(targetVersion)
	}
	if err != nil {
		return err
	}

	if currentVersion == rel.TagName {
		fmt.Printf("Already up to date (%s)\n", currentVersion)
		return nil
	}

	want := assetName()
	var downloadURL string
	for _, a := range rel.Assets {
		if a.Name == want {
			downloadURL = a.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		return fmt.Errorf("no binary found for %s/%s in release %s", runtime.GOOS, runtime.GOARCH, rel.TagName)
	}

	fmt.Printf("Updating %s -> %s ...\n", currentVersion, rel.TagName)

	// Download to temp file
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("downloading: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable path: %w", err)
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(execPath), "dispatch-update-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		return fmt.Errorf("writing update: %w", err)
	}
	tmpFile.Close()

	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("setting permissions: %w", err)
	}

	// On Windows, can't overwrite a running binary — rename old one first
	oldPath := execPath + ".old"
	os.Remove(oldPath) // clean up any previous .old file
	if err := os.Rename(execPath, oldPath); err != nil {
		// Not on Windows or not locked — try direct replace
		if err := os.Rename(tmpPath, execPath); err != nil {
			return fmt.Errorf("replacing binary: %w", err)
		}
	} else {
		if err := os.Rename(tmpPath, execPath); err != nil {
			// Restore old binary
			os.Rename(oldPath, execPath)
			return fmt.Errorf("replacing binary: %w", err)
		}
		os.Remove(oldPath) // clean up
	}

	fmt.Printf("Updated to %s\n", rel.TagName)
	return nil
}
