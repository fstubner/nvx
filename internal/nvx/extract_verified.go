package nvx

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// extractVerifiedArchive verifies an archive against its expected SHA-256 and
// extracts it, and the bytes it extracts are the bytes it verified.
//
// Both installers downloaded to a fixed path under ~/.nvx/downloads, hashed the
// file at that path, and then extracted from that path: two opens, and whatever
// the file held at the second one is what was installed. A same-user process
// that changes the file between the two -- another nvx installing the same
// version into the same fixed name is the ordinary way that happens -- got its
// bytes installed under the first one's verified checksum. Measured: with the
// file rewritten between the two steps, the rewritten content was extracted.
//
// The archive is read once, into memory. That is 25-60 MB for a runtime
// archive, held for the duration of an install, and it removes the window
// entirely rather than narrowing it: a single open handle would still see an
// in-place rewrite, and a checksum over a stream during extraction cannot undo
// files already written. Verifying the bytes and then extracting those same
// bytes has nothing to race.
func extractVerifiedArchive(archivePath, expectedSHA, destDir string, zipArchive bool) error {
	data, err := os.ReadFile(archivePath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", filepath.Base(archivePath), err)
	}
	if err := verifyBytesSHA256(data, expectedSHA, filepath.Base(archivePath)); err != nil {
		return err
	}
	if archiveVerifyHook != nil {
		archiveVerifyHook()
	}
	if zipArchive {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return fmt.Errorf("failed to open zip archive: %w", err)
		}
		return extractZipReader(zr, destDir, true)
	}
	return extractTarGzReader(bytes.NewReader(data), destDir)
}

// verifyBytesSHA256 is verifyExpectedSHA256 over bytes already in hand.
func verifyBytesSHA256(data []byte, expectedHex, name string) error {
	expectedHex = strings.TrimSpace(expectedHex)
	if expectedHex == "" {
		return fmt.Errorf("no expected checksum provided for %s", name)
	}
	sum := sha256.Sum256(data)
	computed := hex.EncodeToString(sum[:])
	if !strings.EqualFold(computed, expectedHex) {
		return fmt.Errorf("checksum verification failed! Expected: %s, Got: %s", expectedHex, computed)
	}
	LogSuccess("Checksum verified successfully.")
	return nil
}

// archiveVerifyHook, when set, runs between verification and extraction. Test
// only: it is how a change to the file on disk in that window is simulated,
// and with the bytes already in hand it must make no difference.
var archiveVerifyHook func()
