package nvx

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
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

const maxExtractedArchiveBytes int64 = 2 << 30

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
	if err := os.MkdirAll(filepath.Dir(destPath), 0700); err != nil {
		return fmt.Errorf("failed to create directory for download: %w", err)
	}

	// Bounded on every step that can hang, and on the body only by whether it
	// is still moving. A single Client.Timeout of 60s used to cover the whole
	// request including the body, so a 25-60 MB archive failed below roughly
	// 4-8 Mbps while making steady progress. See download_stall.go.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	// #nosec G704 -- fetching a caller-supplied URL is this function's entire purpose; the URLs are built from the runtime release indexes, and what arrives is checksum-verified before use
	resp, err := newDownloadClient().Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %s", resp.Status)
	}

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer out.Close()

	totalBytes := resp.ContentLength
	pw := &progressWriter{
		total:      totalBytes,
		lastUpdate: time.Now(),
	}

	body := newStallReader(ctx, cancel, resp.Body, downloadStallTimeout)
	defer body.Stop()
	_, err = io.Copy(out, io.TeeReader(body, pw))
	if err != nil {
		return fmt.Errorf("failed to save download content: %w", err)
	}

	if stderrIsTerminal() {
		fmt.Fprint(os.Stderr, "\r\x1b[K") // Clear line
	}
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

// nodeShasumsURL is where the checksums for a release are published: on
// nodejs.org, or on the mirror the archive comes from.
func nodeShasumsURL(version string) string {
	return fmt.Sprintf("%s/%s/SHASUMS256.txt", nodeDistBase(), version)
}

// fetchExpectedShasum downloads a SHASUMS256.txt-style manifest (lines of
// "<sha256>  <filename>") and returns the entry for archiveFilename. A missing
// entry is an error: nothing is verified against a checksum nvx does not have.
func fetchExpectedShasum(shaUrl, archiveFilename string) (string, error) {
	tmpFile, err := os.CreateTemp("", "SHASUMS256-*.txt")
	if err != nil {
		return "", fmt.Errorf("failed to create secure temp file: %w", err)
	}
	shaTemp := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		return "", fmt.Errorf("failed to close checksum temp file: %w", err)
	}
	defer os.Remove(shaTemp)

	LogInfo("Verifying checksum for %s...", archiveFilename)
	if err := DownloadFile(shaUrl, shaTemp); err != nil {
		return "", fmt.Errorf("failed to download checksum file: %w", err)
	}
	content, err := os.ReadFile(shaTemp)
	if err != nil {
		return "", fmt.Errorf("failed to read checksum file: %w", err)
	}
	expectedSHA := findShasumEntry(string(content), archiveFilename)
	if expectedSHA == "" {
		return "", fmt.Errorf("checksum entry not found for %s in SHASUMS256.txt", archiveFilename)
	}
	return expectedSHA, nil
}

// verifyExpectedSHA256 checks archivePath against a known hex SHA-256 (e.g. one
// carried inline in a release index). Fail-closed on mismatch or empty expected.
func verifyExpectedSHA256(archivePath, expectedHex string) error {
	expectedHex = strings.TrimSpace(expectedHex)
	if expectedHex == "" {
		return fmt.Errorf("no expected checksum provided for %s", filepath.Base(archivePath))
	}
	LogInfo("Verifying checksum for %s...", filepath.Base(archivePath))
	computed, err := ComputeSHA256(archivePath)
	if err != nil {
		return fmt.Errorf("failed to compute SHA-256: %w", err)
	}
	if !strings.EqualFold(computed, expectedHex) {
		return fmt.Errorf("checksum verification failed! Expected: %s, Got: %s", expectedHex, computed)
	}
	LogSuccess("Checksum verified successfully.")
	return nil
}

// findShasumEntry extracts the expected hash for filename from checksum-file
// content. Accepted forms: sha256sum lines ("<hash>  <filename>", with optional
// "*" binary marker or "./" prefix), and, for per-asset sidecar files, a lone
// 64-char hash when the file contains exactly one entry.
func findShasumEntry(content, filename string) string {
	isHex64 := func(s string) bool {
		if len(s) != 64 {
			return false
		}
		for _, r := range s {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return false
			}
		}
		return true
	}
	var loneHash string
	entries := 0
	for _, line := range strings.Split(content, "\n") {
		parts := strings.Fields(strings.TrimSpace(line))
		if len(parts) == 0 {
			continue
		}
		entries++
		if len(parts) >= 2 && isHex64(parts[0]) {
			name := strings.TrimPrefix(parts[1], "*")
			if name == filename || strings.TrimPrefix(name, "./") == filename {
				return parts[0]
			}
		}
		if len(parts) == 1 && isHex64(parts[0]) {
			loneHash = parts[0]
		}
	}

	// A single-hash file (e.g. <asset>.sha256sum) unambiguously refers to the
	// asset it was fetched for.
	if entries == 1 && loneHash != "" {
		return loneHash
	}
	return ""
}

type progressWriter struct {
	total      int64
	downloaded int64
	lastUpdate time.Time
	// lastStep is the last milestone printed as a plain line, when stderr is not
	// a terminal.
	lastStep int64
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	pw.downloaded += int64(n)

	if !stderrIsTerminal() {
		pw.printPlainProgress()
		return n, nil
	}
	if time.Since(pw.lastUpdate) > 100*time.Millisecond || pw.downloaded == pw.total {
		pw.lastUpdate = time.Now()
		pw.printProgress()
	}
	return n, nil
}

// printPlainProgress is the progress for a log or a pipe: a bar redrawn with
// carriage returns arrives there as thousands of lines of control codes. One
// plain line per tenth of the download, or per 10 MB when the size is unknown.
func (pw *progressWriter) printPlainProgress() {
	mb := float64(pw.downloaded) / (1024 * 1024)
	if pw.total > 0 {
		step := pw.downloaded * 10 / pw.total
		if step <= pw.lastStep {
			return
		}
		pw.lastStep = step
		fmt.Fprintf(os.Stderr, "Downloading: %d%% (%.1f / %.1f MB)\n", step*10, mb, float64(pw.total)/(1024*1024))
		return
	}
	step := pw.downloaded / (10 * 1024 * 1024)
	if step <= pw.lastStep {
		return
	}
	pw.lastStep = step
	fmt.Fprintf(os.Stderr, "Downloading: %.1f MB\n", mb)
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
		fmt.Fprintf(os.Stderr, "\r%s [%s] %.1f%% (%.1f / %.1f MB)",
			paint("36", "📦 Downloading:"), bar, percent*100, mbDownloaded, mbTotal)
	} else {
		fmt.Fprintf(os.Stderr, "\r%s [%s] (%.1f MB)",
			paint("36", "📦 Downloading:"), bar, mbDownloaded)
	}
}

// ExtractZip extracts a zip file into destDir, stripping the top-level folder
// inside the zip (e.g. node-vX/, bun-<target>/).
func ExtractZip(zipPath, destDir string) error {
	return extractZip(zipPath, destDir, true)
}

func extractZip(zipPath, destDir string, strip bool) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open zip file: %w", err)
	}
	defer r.Close()
	return extractZipReader(&r.Reader, destDir, strip)
}

// extractZipReader is extractZip over an already-open archive, so that the
// bytes which were checksum-verified can be the bytes extracted; see
// extractVerifiedArchive.
func extractZipReader(r *zip.Reader, destDir string, strip bool) error {
	if err := os.MkdirAll(destDir, 0700); err != nil {
		return fmt.Errorf("failed to create destination folder: %w", err)
	}

	fmt.Fprint(os.Stderr, "🚚 Extracting files... ")
	startTime := time.Now()
	remaining := maxExtractedArchiveBytes

	for _, f := range r.File {
		fpath, skip, err := safeArchiveTargetStrip(destDir, f.Name, strip)
		if err != nil {
			return fmt.Errorf("illegal file path in zip: %w", err)
		}
		if skip {
			continue
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(fpath, f.Mode()&0770); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", fpath, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fpath), 0700); err != nil {
			return fmt.Errorf("failed to create subdirectory: %w", err)
		}

		src, err := f.Open()
		if err != nil {
			return fmt.Errorf("failed to open zip member %s: %w", f.Name, err)
		}

		dst, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode()&0770)
		if err != nil {
			_ = src.Close()
			return fmt.Errorf("failed to create file %s: %w", fpath, err)
		}

		copyErr := copyArchiveFile(dst, src, &remaining, f.Name)
		srcErr := src.Close()
		dstErr := dst.Close()
		if copyErr != nil {
			return copyErr
		}
		if srcErr != nil {
			return fmt.Errorf("failed to close zip member %s: %w", f.Name, srcErr)
		}
		if dstErr != nil {
			return fmt.Errorf("failed to close destination file %s: %w", fpath, dstErr)
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
	return extractTarGzReader(file, destDir)
}

// extractTarGzReader is ExtractTarGz over an open stream, so that the bytes
// which were checksum-verified can be the bytes extracted; see
// extractVerifiedArchive.
func extractTarGzReader(file io.Reader, destDir string) error {
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)

	if err := os.MkdirAll(destDir, 0700); err != nil {
		return fmt.Errorf("failed to create destination folder: %w", err)
	}
	// Every entry below is placed by REAL path, resolved through whatever
	// symlinks earlier entries created. See extract_within.go.
	realDest, err := filepath.EvalSymlinks(destDir)
	if err != nil {
		return fmt.Errorf("failed to resolve destination directory: %w", err)
	}

	fmt.Fprint(os.Stderr, "🚚 Extracting files... ")
	startTime := time.Now()
	remaining := maxExtractedArchiveBytes

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar archive: %w", err)
		}

		fpath, skip, err := safeArchiveTarget(destDir, header.Name)
		if err != nil {
			return fmt.Errorf("illegal file path in tar archive: %w", err)
		}
		if skip {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			target, err := extractTargetWithin(realDest, destDir, fpath)
			if err != nil {
				return err
			}
			// #nosec G115 -- header.Mode is attacker-controlled, but &0770 bounds the result: setuid, setgid, sticky and world bits cannot survive the mask however the conversion wraps
			if err := os.MkdirAll(target, os.FileMode(header.Mode)&0770); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
		case tar.TypeReg:
			target, err := extractTargetWithin(realDest, destDir, fpath)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return fmt.Errorf("failed to create subdirectory: %w", err)
			}
			// Never write through a symlink an earlier entry left at this name.
			if fi, lerr := os.Lstat(target); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
				if err := os.Remove(target); err != nil {
					return fmt.Errorf("failed to replace symlink %s with a file: %w", target, err)
				}
			}
			// #nosec G115 -- same as above: &0770 bounds an attacker-controlled tar mode, so no setuid/setgid bit can reach the created file
			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode)&0770)
			if err != nil {
				return fmt.Errorf("failed to open destination file %s: %w", fpath, err)
			}
			copyErr := copyArchiveFile(outFile, tarReader, &remaining, header.Name)
			closeErr := outFile.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return fmt.Errorf("failed to close destination file %s: %w", fpath, closeErr)
			}
		case tar.TypeSymlink:
			// Verify that the symlink target is safe and does not escape destDir
			linkTarget := header.Linkname
			// filepath.IsAbs is platform-specific: on Windows it is FALSE for a
			// POSIX-rooted path like "/etc/passwd", because an absolute Windows path
			// needs a drive letter. filepath.Join below then folds the leading slash
			// away, so the containment check that follows also passes, and the
			// symlink is created -- resolving to \etc\passwd on the current drive,
			// outside destDir. Tar archives carry POSIX paths regardless of the host,
			// so rooted targets are rejected explicitly rather than via IsAbs alone.
			if filepath.IsAbs(linkTarget) ||
				strings.HasPrefix(linkTarget, "/") ||
				strings.HasPrefix(linkTarget, `\`) ||
				strings.Contains(linkTarget, ":") {
				return fmt.Errorf("illegal absolute symlink target in tar archive: %s -> %s", fpath, linkTarget)
			}
			// Where the link itself lands, by real path, and where its target
			// really points from there. Both walks follow any symlink earlier
			// entries created and fail the moment a step leaves the destination.
			// The old check resolved the target lexically against the entry's own
			// path, which a chain of individually-inside links defeated.
			target, err := extractTargetWithin(realDest, destDir, fpath)
			if err != nil {
				return err
			}
			if _, err := resolveWithin(realDest, filepath.Dir(target), linkTarget); err != nil {
				return fmt.Errorf("illegal symlink target outside destination: %s -> %s: %w", fpath, linkTarget, err)
			}

			// Ensure the parent directory exists — tar entries do not always list
			// a directory before the symlinks inside it.
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return fmt.Errorf("failed to create subdirectory for symlink %s: %w", fpath, err)
			}
			// Remove existing symlink/file if it exists
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("failed to remove existing symlink target %s: %w", fpath, err)
			}
			if err := os.Symlink(header.Linkname, target); err != nil {
				return fmt.Errorf("failed to create symlink %s -> %s: %w", fpath, header.Linkname, err)
			}
		}

	}

	fmt.Fprintf(os.Stderr, "done in %s\n", time.Since(startTime).Round(time.Millisecond))
	return nil
}

func safeArchiveTarget(destDir, archiveName string) (string, bool, error) {
	return safeArchiveTargetStrip(destDir, archiveName, true)
}

// safeArchiveTargetStrip resolves an archive member to a path under destDir,
// rejecting traversal. When strip is true the leading path segment is dropped
// (flattening wrapper folders like node-vX/); when false, members are placed
// as-is (for archives whose files sit at the root).
func safeArchiveTargetStrip(destDir, archiveName string, strip bool) (string, bool, error) {
	normalized := strings.ReplaceAll(archiveName, "\\", "/")
	parts := strings.Split(normalized, "/")
	if strip {
		if len(parts) <= 1 {
			return "", true, nil
		}
		parts = parts[1:]
	}

	strippedParts := parts
	for _, part := range strippedParts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", false, fmt.Errorf("%s", archiveName)
		}
		if strings.Contains(part, ":") {
			return "", false, fmt.Errorf("%s", archiveName)
		}
	}

	strippedPath := filepath.Join(strippedParts...)
	if strippedPath == "" || strippedPath == "." || filepath.IsAbs(strippedPath) {
		return "", true, nil
	}

	cleanDest, err := filepath.Abs(destDir)
	if err != nil {
		return "", false, fmt.Errorf("resolve destination: %w", err)
	}
	cleanDest = filepath.Clean(cleanDest)
	cleanTarget := filepath.Clean(filepath.Join(cleanDest, strippedPath))
	if cleanTarget != cleanDest && !strings.HasPrefix(cleanTarget, cleanDest+string(os.PathSeparator)) {
		return "", false, fmt.Errorf("%s", archiveName)
	}
	return cleanTarget, false, nil
}

func copyArchiveFile(dst io.Writer, src io.Reader, remaining *int64, name string) error {
	if *remaining <= 0 {
		return fmt.Errorf("archive extraction limit exceeded before %s", name)
	}
	before := *remaining
	limited := &io.LimitedReader{R: src, N: before + 1}
	n, err := io.Copy(dst, limited)
	if n > before {
		*remaining = 0
		return fmt.Errorf("archive extraction limit exceeded while extracting %s", name)
	}
	*remaining -= n
	if err != nil {
		return fmt.Errorf("failed to extract file contents for %s: %w", name, err)
	}
	return nil
}
