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
	defer func() { _ = resp.Body.Close() }() // ignore close error

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
	defer func() { _ = resp.Body.Close() }() // ignore close error

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned status %s", resp.Status)
	}

	tmpFile, err := os.CreateTemp("", "v100-update-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = tmpFile.Close() }() // ignore close error

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

	// Try os.Rename first (fast), fall back to copy+delete if cross-device
	if err := os.Rename(newPath, exePath); err != nil {
		// Cross-device link error: fall back to copy + delete
		if copyErr := copyFile(newPath, exePath); copyErr != nil {
			// Try to rollback if possible
			_ = os.Rename(oldPath, exePath)
			return fmt.Errorf("could not install new executable: %w", err)
		}
		_ = os.Remove(newPath) // clean up temp file
	}

	// Set executable permissions on Unix-like systems
	if runtime.GOOS != "windows" {
		if err := os.Chmod(exePath, 0755); err != nil {
			return fmt.Errorf("could not set executable permissions: %w", err)
		}
	}

	return nil
}

// copyFile copies src to dst, preserving permissions.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = srcFile.Close() }() // ignore close error

	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer func() { _ = dstFile.Close() }() // ignore close error

	_, err = io.Copy(dstFile, srcFile)
	return err
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
