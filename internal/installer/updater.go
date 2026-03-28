package installer

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
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

// githubRelease holds the fields we care about from the GitHub releases API.
type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// CheckLatest queries the GitHub releases API and returns the latest version
// string (without leading "v") and the download URL for the current platform's
// tarball asset.  It returns an error if the request fails, the response is
// not HTTP 200, or no matching asset is found for the running OS/arch.
func CheckLatest(currentVersion string) (latestVersion, downloadURL string, err error) {
	client := &http.Client{Timeout: 15 * time.Second}

	req, err := http.NewRequest(http.MethodGet,
		"https://api.github.com/repos/joeldevz/neurox/releases/latest", nil)
	if err != nil {
		return "", "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "neurox-updater/"+currentVersion)

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("github API returned status %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", "", fmt.Errorf("decode response: %w", err)
	}

	// Strip leading "v" from the tag name.
	latestVersion = strings.TrimPrefix(release.TagName, "v")

	// Build the expected asset name for the current platform.
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	expectedName := fmt.Sprintf("neurox_%s_%s_%s.%s", latestVersion, runtime.GOOS, runtime.GOARCH, ext)

	for _, asset := range release.Assets {
		if asset.Name == expectedName {
			return latestVersion, asset.BrowserDownloadURL, nil
		}
	}

	return "", "", fmt.Errorf(
		"no release asset found for %s/%s (platform may not be supported)",
		runtime.GOOS, runtime.GOARCH,
	)
}

// DownloadAndReplace downloads the tarball at downloadURL, extracts the
// "neurox" binary from it, and atomically replaces the file at binaryPath.
// Temporary files are written next to the binary (same filesystem) to
// guarantee that os.Rename is atomic.  All temp files are removed in a defer,
// so they are cleaned up on both success and failure.
func DownloadAndReplace(downloadURL, binaryPath string) error {
	isZip := strings.HasSuffix(downloadURL, ".zip")

	var archivePath string
	if isZip {
		archivePath = binaryPath + ".tmp.zip"
	} else {
		archivePath = binaryPath + ".tmp.tar.gz"
	}
	tmpBin := binaryPath + ".tmp"
	oldBin := binaryPath + ".old"

	defer func() {
		os.Remove(archivePath)
		os.Remove(tmpBin)
		os.Remove(oldBin)
	}()

	if err := downloadFile(downloadURL, archivePath); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	if isZip {
		if err := extractBinaryFromZip(archivePath, tmpBin); err != nil {
			return fmt.Errorf("extract zip: %w", err)
		}
	} else {
		if err := extractBinary(archivePath, tmpBin); err != nil {
			return fmt.Errorf("extract tar.gz: %w", err)
		}
	}

	if err := os.Chmod(tmpBin, 0o755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	// On Windows the running binary is locked, so rename current → .old first.
	if runtime.GOOS == "windows" {
		_ = os.Remove(oldBin)
		if err := os.Rename(binaryPath, oldBin); err != nil {
			return fmt.Errorf("rename current binary: %w", err)
		}
	}

	if err := os.Rename(tmpBin, binaryPath); err != nil {
		return fmt.Errorf("replace binary: %w", err)
	}

	return nil
}

// downloadFile streams the HTTP response body of url into the local file at dst.
func downloadFile(url, dst string) error {
	client := &http.Client{Timeout: 5 * time.Minute} // generous timeout for binary download
	resp, err := client.Get(url)                     //nolint:noctx // stdlib client, timeout set above
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected HTTP status %d", resp.StatusCode)
	}

	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return nil
}

// extractBinary opens a .tar.gz file, walks its entries, finds the first entry
// whose base name starts with "neurox" (e.g. "neurox", "neurox_linux_amd64",
// "neurox.exe"), and writes it to dst.
func extractBinary(tarPath, dst string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)

	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		baseName := filepath.Base(hdr.Name)
		if !strings.HasPrefix(baseName, "neurox") {
			continue
		}

		out, err := os.Create(dst)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		out.Close()
		found = true
		break
	}

	if !found {
		return fmt.Errorf("neurox binary not found in tarball")
	}
	return nil
}

// extractBinaryFromZip opens a .zip file, finds the first entry whose base
// name starts with "neurox" (e.g. "neurox.exe", "neurox_windows_amd64.exe"),
// and writes it to dst.
func extractBinaryFromZip(zipPath, dst string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if !strings.HasPrefix(filepath.Base(f.Name), "neurox") {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open entry: %w", err)
		}
		defer rc.Close()

		out, err := os.Create(dst)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			return err
		}
		out.Close()
		return nil
	}

	return fmt.Errorf("neurox binary not found in zip")
}

// RunUpdate orchestrates the full self-update flow:
//  1. Resolves the path of the running binary.
//  2. Fetches the latest release from GitHub.
//  3. Optionally prompts the user for confirmation.
//  4. Downloads and atomically replaces the binary.
func RunUpdate(currentVersion string, skipConfirm bool) error {
	// Resolve the path of the currently running binary, following symlinks.
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine binary path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	// Fetch latest release metadata.
	latestVersion, downloadURL, err := CheckLatest(currentVersion)
	if err != nil {
		return fmt.Errorf("check latest: %w", err)
	}

	// Already on the latest version — nothing to do.
	if currentVersion == latestVersion {
		fmt.Printf("Already up to date (v%s).\n", currentVersion)
		return nil
	}

	fmt.Printf("Found v%s (current: v%s).\n", latestVersion, currentVersion)

	// Prompt for confirmation unless --yes/-y was passed.
	if !skipConfirm {
		fmt.Print("Update now? [y/N] ")
		var answer string
		fmt.Fscan(os.Stdin, &answer)
		if strings.ToLower(strings.TrimSpace(answer)) != "y" {
			fmt.Println("Update cancelled.")
			return nil
		}
	}

	fmt.Printf("Downloading neurox v%s...\n", latestVersion)
	if err := DownloadAndReplace(downloadURL, self); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	fmt.Printf("✓ neurox v%s installed. Restart your AI clients to pick up the new version.\n", latestVersion)
	return nil
}
