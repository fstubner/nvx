package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// GetArch returns the Node.js architecture suffix for downloads (x64, x86, arm64)
func GetArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "386":
		return "x86"
	case "arm64":
		return "arm64"
	default:
		return "x64"
	}
}

// DownloadFile downloads a URL to a local filepath, displaying a progress bar to stderr
func DownloadFile(url, destPath string) error {
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory for download: %w", err)
	}

	client := &http.Client{
		Timeout: 60 * time.Second,
	}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %s", resp.Status)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer out.Close()

	totalBytes := resp.ContentLength
	pw := &progressWriter{
		total:      totalBytes,
		lastUpdate: time.Now(),
	}

	_, err = io.Copy(out, io.TeeReader(resp.Body, pw))
	if err != nil {
		return fmt.Errorf("failed to save download content: %w", err)
	}

	fmt.Fprint(os.Stderr, "\r\x1b[K") // Clear line
	return nil
}

// ComputeSHA256 computes the SHA-256 hash of a local file
func ComputeSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyNodeChecksum downloads the SHASUMS256.txt for the given Node version,
// finds the expected SHA-256 for the archive filename, and verifies the downloaded file's hash.
func VerifyNodeChecksum(version, archivePath, archiveFilename string) error {
	// SHASUMS256.txt is at https://nodejs.org/dist/<version>/SHASUMS256.txt
	shaUrl := fmt.Sprintf("https://nodejs.org/dist/%s/SHASUMS256.txt", version)
	
	// Create a secure temp file for checksums
	tmpFile, err := os.CreateTemp("", "SHASUMS256-*.txt")
	if err != nil {
		return fmt.Errorf("failed to create secure temp file: %w", err)
	}
	shaTemp := tmpFile.Name()
	tmpFile.Close() // Close immediately since DownloadFile will open/overwrite it
	defer os.Remove(shaTemp)

	LogInfo("Verifying checksum for %s...", archiveFilename)
	err = DownloadFile(shaUrl, shaTemp)
	if err != nil {
		return fmt.Errorf("failed to download checksum file: %w", err)
	}

	content, err := os.ReadFile(shaTemp)
	if err != nil {
		return fmt.Errorf("failed to read checksum file: %w", err)
	}

	expectedSHA := ""
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			// Parts[0] is checksum, Parts[1] is filename
			if parts[1] == archiveFilename {
				expectedSHA = parts[0]
				break
			}
		}
	}

	if expectedSHA == "" {
		return fmt.Errorf("checksum entry not found for %s in SHASUMS256.txt", archiveFilename)
	}

	// Compute checksum of downloaded file
	computedSHA, err := ComputeSHA256(archivePath)
	if err != nil {
		return fmt.Errorf("failed to compute SHA-256: %w", err)
	}

	if !strings.EqualFold(computedSHA, expectedSHA) {
		return fmt.Errorf("checksum verification failed! Expected: %s, Got: %s", expectedSHA, computedSHA)
	}

	LogSuccess("Checksum verified successfully.")
	return nil
}


type progressWriter struct {
	total      int64
	downloaded int64
	lastUpdate time.Time
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	pw.downloaded += int64(n)

	if time.Since(pw.lastUpdate) > 100*time.Millisecond || pw.downloaded == pw.total {
		pw.lastUpdate = time.Now()
		pw.printProgress()
	}
	return n, nil
}

func (pw *progressWriter) printProgress() {
	width := 30
	var percent float64
	if pw.total > 0 {
		percent = float64(pw.downloaded) / float64(pw.total)
	} else {
		percent = 0
	}
	completed := int(percent * float64(width))
	if completed > width {
		completed = width
	}

	bar := ""
	for i := 0; i < completed; i++ {
		bar += "█"
	}
	for i := completed; i < width; i++ {
		bar += "░"
	}

	mbDownloaded := float64(pw.downloaded) / (1024 * 1024)
	if pw.total > 0 {
		mbTotal := float64(pw.total) / (1024 * 1024)
		fmt.Fprintf(os.Stderr, "\r\x1b[36m📦 Downloading:\x1b[0m [%s] %.1f%% (%.1f / %.1f MB)",
			bar, percent*100, mbDownloaded, mbTotal)
	} else {
		fmt.Fprintf(os.Stderr, "\r\x1b[36m📦 Downloading:\x1b[0m [%s] (%.1f MB)",
			bar, mbDownloaded)
	}
}

// ExtractZip extracts a zip file into destDir, stripping the top-level folder inside the zip
func ExtractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open zip file: %w", err)
	}
	defer r.Close()

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination folder: %w", err)
	}

	fmt.Fprint(os.Stderr, "🚚 Extracting files... ")
	startTime := time.Now()

	for _, f := range r.File {
		parts := strings.Split(f.Name, "/")
		if len(parts) <= 1 {
			continue
		}
		strippedPath := filepath.Join(parts[1:]...)
		if strippedPath == "" {
			continue
		}

		fpath := filepath.Join(destDir, strippedPath)

		if !strings.HasPrefix(fpath, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path in zip: %s", fpath)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, f.Mode())
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
			return fmt.Errorf("failed to create subdirectory: %w", err)
		}

		src, err := f.Open()
		if err != nil {
			return fmt.Errorf("failed to open zip member %s: %w", f.Name, err)
		}

		dst, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			src.Close()
			return fmt.Errorf("failed to create file %s: %w", fpath, err)
		}

		_, err = io.Copy(dst, src)
		src.Close()
		dst.Close()
		if err != nil {
			return fmt.Errorf("failed to extract file contents for %s: %w", fpath, err)
		}
	}

	fmt.Fprintf(os.Stderr, "done in %s\n", time.Since(startTime).Round(time.Millisecond))
	return nil
}

// ExtractTarGz extracts a tar.gz archive into destDir, stripping the top-level folder
func ExtractTarGz(tarPath, destDir string) error {
	file, err := os.Open(tarPath)
	if err != nil {
		return fmt.Errorf("failed to open tar.gz file: %w", err)
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination folder: %w", err)
	}

	fmt.Fprint(os.Stderr, "🚚 Extracting files... ")
	startTime := time.Now()

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar archive: %w", err)
		}

		parts := strings.Split(header.Name, "/")
		if len(parts) <= 1 {
			continue // skip root folder itself
		}
		strippedPath := filepath.Join(parts[1:]...)
		if strippedPath == "" {
			continue
		}

		fpath := filepath.Join(destDir, strippedPath)

		if !strings.HasPrefix(fpath, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path in tar archive: %s", fpath)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(fpath, os.FileMode(header.Mode)); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
				return fmt.Errorf("failed to create subdirectory: %w", err)
			}
			outFile, err := os.OpenFile(fpath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("failed to open destination file %s: %w", fpath, err)
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				outFile.Close()
				return fmt.Errorf("failed to extract file contents for %s: %w", fpath, err)
			}
			outFile.Close()
		case tar.TypeSymlink:
			// Verify that the symlink target is safe and does not escape destDir
			linkTarget := header.Linkname
			if filepath.IsAbs(linkTarget) {
				return fmt.Errorf("illegal absolute symlink target in tar archive: %s -> %s", fpath, linkTarget)
			}
			resolvedTarget := filepath.Join(filepath.Dir(fpath), linkTarget)
			cleanDest := filepath.Clean(destDir)
			cleanTarget := filepath.Clean(resolvedTarget)
			if cleanTarget != cleanDest && !strings.HasPrefix(cleanTarget, cleanDest+string(os.PathSeparator)) {
				return fmt.Errorf("illegal symlink target outside destination: %s -> %s (resolved: %s)", fpath, linkTarget, cleanTarget)
			}

			// Remove existing symlink/file if it exists
			if err := os.Remove(fpath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("failed to remove existing symlink target %s: %w", fpath, err)
			}
			if err := os.Symlink(header.Linkname, fpath); err != nil {
				return fmt.Errorf("failed to create symlink %s -> %s: %w", fpath, header.Linkname, err)
			}
		}

	}

	fmt.Fprintf(os.Stderr, "done in %s\n", time.Since(startTime).Round(time.Millisecond))
	return nil
}
