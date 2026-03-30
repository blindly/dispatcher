package updater

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"time"
)

const repoAPI = "https://api.github.com/repos/blindly/dispatcher/releases"

type release struct {
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

type asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func assetName() string {
	return fmt.Sprintf("dispatch-%s-%s", runtime.GOOS, runtime.GOARCH)
}

func fetchRelease(version string) (*release, error) {
	url := repoAPI + "/latest"
	if version != "" {
		url = repoAPI + "/tags/" + version
	}

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

func Update(currentVersion string, targetVersion string) error {
	rel, err := fetchRelease(targetVersion)
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

	tmpFile, err := os.CreateTemp("", "dispatch-update-*")
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

	// Atomic replace
	if err := os.Rename(tmpPath, execPath); err != nil {
		return fmt.Errorf("replacing binary: %w", err)
	}

	fmt.Printf("Updated to %s\n", rel.TagName)
	return nil
}
