package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// httpClient is used for short API calls. 30s is enough for a metadata request.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// downloadClient is used for binary downloads, which can be large and slower.
var downloadClient = &http.Client{Timeout: 5 * time.Minute}

// releaseInfo holds the GitHub API response fields we need.
type releaseInfo struct {
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

// asset holds one downloadable file from a release.
type asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// fetchLatestRelease queries the GitHub Releases API for the latest release.
func fetchLatestRelease() (*releaseInfo, error) {
	const url = "https://api.github.com/repos/LingNc/sshmenu/releases/latest"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "sshmenu-updater")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch release info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	var info releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &info, nil
}

// parseVersion parses a "vMAJOR.MINOR.PATCH" string into [3]int.
// The leading "v" is optional.
func parseVersion(v string) ([3]int, error) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return [3]int{}, fmt.Errorf("invalid version format: %q", v)
	}
	var result [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, fmt.Errorf("invalid version segment %q: %w", p, err)
		}
		result[i] = n
	}
	return result, nil
}

// needUpdate reports whether current version is older than latest.
// "dev" always counts as needing an update.
func needUpdate(current, latest string) bool {
	if current == "dev" {
		return true
	}
	cur, err := parseVersion(current)
	if err != nil {
		// Cannot parse current version; assume update needed.
		return true
	}
	lat, err := parseVersion(latest)
	if err != nil {
		return false // Cannot parse latest; skip update.
	}
	for i := 0; i < 3; i++ {
		if cur[i] < lat[i] {
			return true
		}
		if cur[i] > lat[i] {
			return false
		}
	}
	return false // Equal versions.
}

// assetURL finds the download URL for the current platform in the release assets.
func assetURL(assets []asset) (string, error) {
	var target string
	switch runtime.GOOS {
	case "linux":
		target = "sshmenu"
	case "windows":
		target = "sshmenu.exe"
	default:
		return "", fmt.Errorf("unsupported platform: %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	for _, a := range assets {
		if a.Name == target {
			return a.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("no asset found for %s/%s (looking for %q)", runtime.GOOS, runtime.GOARCH, target)
}

// downloadFile downloads url to a temporary file and returns its path.
// The caller is responsible for removing the temp file on error.
func downloadFile(url string) (string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "sshmenu-update-*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}

	n, err := io.Copy(tmp, resp.Body)
	tmp.Close() // Close before chmod/rename
	if err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("download: %w", err)
	}
	if n == 0 {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("downloaded file is empty")
	}

	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("chmod: %w", err)
	}

	return tmp.Name(), nil
}

// replaceBinary replaces the running binary at targetPath with the new file at tmpPath.
func replaceBinary(tmpPath, targetPath string) error {
	if runtime.GOOS == "windows" {
		return replaceBinaryWindows(tmpPath, targetPath)
	}
	return replaceBinaryUnix(tmpPath, targetPath)
}

// replaceBinaryUnix atomically replaces targetPath via os.Rename.
// Falls back to copy+delete when rename fails (e.g. cross-device).
func replaceBinaryUnix(tmpPath, targetPath string) error {
	backup := targetPath + ".backup"
	// Best-effort backup of the old binary.
	os.Rename(targetPath, backup)

	if err := os.Rename(tmpPath, targetPath); err != nil {
		// Cross-device or permission error — fall back to copy.
		if err := copyFile(tmpPath, targetPath); err != nil {
			os.Rename(backup, targetPath)
			return fmt.Errorf("replace binary: %w", err)
		}
		os.Remove(tmpPath)
	}
	os.Remove(backup)
	return nil
}

// copyFile copies a file from src to dst, preserving permissions.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// replaceBinaryWindows handles the Windows case where a running .exe cannot be
// directly overwritten. Strategy: rename old -> .old, move new into place, clean up.
func replaceBinaryWindows(tmpPath, targetPath string) error {
	oldPath := targetPath + ".old"

	// Remove leftover .old from a previous update.
	os.Remove(oldPath)

	// Rename the running binary out of the way.
	if err := os.Rename(targetPath, oldPath); err != nil {
		return fmt.Errorf("rename old binary: %w", err)
	}

	// Move new binary into place.
	if err := os.Rename(tmpPath, targetPath); err != nil {
		// Roll back.
		os.Rename(oldPath, targetPath)
		return fmt.Errorf("move new binary: %w", err)
	}

	// Best-effort cleanup of old binary (may fail if still in use).
	os.Remove(oldPath)
	return nil
}

// doUpdate checks for updates and, if a newer version is available,
// downloads and replaces the current binary.
func doUpdate() error {
	fmt.Printf("Current version: %s\n", version)

	// Get path of the running executable.
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	// Resolve symlinks to get the real path.
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	// Fetch latest release info from GitHub.
	info, err := fetchLatestRelease()
	if err != nil {
		return err
	}

	fmt.Printf("Latest version:  %s\n", info.TagName)

	if !needUpdate(version, info.TagName) {
		fmt.Println("Already at latest version.")
		return nil
	}

	// Find the download URL for this platform.
	url, err := assetURL(info.Assets)
	if err != nil {
		return err
	}
	fmt.Printf("Download: %s\n", url)

	// Ask for confirmation.
	fmt.Print("Update now? [y/N]: ")
	var answer string
	fmt.Scanln(&answer)
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer != "y" && answer != "yes" {
		fmt.Println("Update cancelled.")
		return nil
	}

	// Download new binary.
	fmt.Print("Downloading... ")
	tmpPath, err := downloadFile(url)
	if err != nil {
		return err
	}
	// Clean up temp file if replacement fails.
	defer os.Remove(tmpPath)

	stat, _ := os.Stat(tmpPath)
	if stat != nil {
		fmt.Printf("%.1f MB done\n", float64(stat.Size())/(1024*1024))
	}

	// Replace the running binary.
	if err := replaceBinary(tmpPath, exePath); err != nil {
		return err
	}

	fmt.Println("Update complete. Restart sshmenu to use the new version.")
	return nil
}
