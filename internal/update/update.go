package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
)

const (
	owner = "tripledoublev"
	repo  = "v100"
)

// Release represents a GitHub release.
type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

// Asset represents a GitHub release asset.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// CheckLatest checks for the latest release on GitHub.
func CheckLatest(ctx context.Context) (Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return Release{}, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("github api returned status %s", resp.Status)
	}

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return Release{}, err
	}

	return release, nil
}

// DownloadAsset downloads the specified asset to a temporary file.
func DownloadAsset(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned status %s", resp.Status)
	}

	tmpFile, err := os.CreateTemp("", "v100-update-*")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return "", err
	}

	return tmpFile.Name(), nil
}

// ApplyUpdate replaces the current executable with the new one.
func ApplyUpdate(newPath string) error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	// For safer replacement, especially on Windows:
	// 1. Move the current executable to a temporary "old" name.
	// 2. Move the new executable to the original name.
	// 3. (Optional) Schedule the old executable for deletion if needed.

	oldPath := exePath + ".old"
	_ = os.Remove(oldPath) // ignore error if it doesn't exist

	if err := os.Rename(exePath, oldPath); err != nil {
		return fmt.Errorf("could not move current executable: %w", err)
	}

	if err := os.Rename(newPath, exePath); err != nil {
		// Try to rollback if possible
		_ = os.Rename(oldPath, exePath)
		return fmt.Errorf("could not install new executable: %w", err)
	}

	// Set executable permissions on Unix-like systems
	if runtime.GOOS != "windows" {
		if err := os.Chmod(exePath, 0755); err != nil {
			return fmt.Errorf("could not set executable permissions: %w", err)
		}
	}

	return nil
}

// TargetAsset returns the expected asset name for the current platform.
func TargetAsset() string {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("v100-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)
}

// IsNewer compares the current version with a new tag name.
func IsNewer(current, latest string) bool {
	if current == "dev" {
		return true // assume latest is newer than dev for testing
	}
	current = strings.TrimPrefix(current, "v")
	latest = strings.TrimPrefix(latest, "v")
	return latest != current && latest > current // naive semver comparison for now
}
