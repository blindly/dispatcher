package updater

import (
	"crypto/sha256"
	"encoding/hex"
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

const repoAPI = "https://api.github.com/repos/blindly/dispatcher/releases"

// checksumsAsset is the release asset that lists the SHA-256 of every binary.
const checksumsAsset = "checksums.txt"

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

// parseChecksums parses the `sha256sum`-style output found in checksums.txt,
// mapping each file's base name to its lower-case hex SHA-256 digest. Lines it
// can't understand are skipped.
func parseChecksums(body []byte) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		sum := strings.ToLower(fields[0])
		// The filename is the last field; strip any leading "*" binary marker
		// and any directory prefix so we match on the bare asset name.
		name := filepath.Base(strings.TrimPrefix(fields[len(fields)-1], "*"))
		out[name] = sum
	}
	return out
}

// fetchChecksums downloads and parses the release's checksums.txt asset.
func fetchChecksums(rel *release) (map[string]string, error) {
	var url string
	for _, a := range rel.Assets {
		if a.Name == checksumsAsset {
			url = a.BrowserDownloadURL
			break
		}
	}
	if url == "" {
		return nil, fmt.Errorf("release %s has no %s; refusing to update because the download cannot be verified", rel.TagName, checksumsAsset)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching checksums: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fetching checksums returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading checksums: %w", err)
	}
	sums := parseChecksums(body)
	if len(sums) == 0 {
		return nil, fmt.Errorf("%s is empty or malformed", checksumsAsset)
	}
	return sums, nil
}

// verifyChecksum reports an error unless the downloaded file's digest matches
// the expected SHA-256 recorded for assetName in checksums.txt.
func verifyChecksum(gotSum, assetName string, sums map[string]string) error {
	want, ok := sums[assetName]
	if !ok {
		return fmt.Errorf("no checksum listed for %s", assetName)
	}
	if !strings.EqualFold(gotSum, want) {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", assetName, want, gotSum)
	}
	return nil
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

	// Fetch the signed manifest of checksums before downloading the binary.
	// We refuse to install anything we can't verify against it.
	sums, err := fetchChecksums(rel)
	if err != nil {
		return err
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

	// Hash the bytes as they stream to disk so we can verify integrity.
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmpFile, hasher), resp.Body); err != nil {
		tmpFile.Close()
		return fmt.Errorf("writing update: %w", err)
	}
	tmpFile.Close()

	gotSum := hex.EncodeToString(hasher.Sum(nil))
	if err := verifyChecksum(gotSum, want, sums); err != nil {
		return fmt.Errorf("integrity check failed: %w", err)
	}

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
