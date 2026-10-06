package nvx

import (
	"encoding/json"
	"os"
	"time"
)

// writeWindowsSetupState writes the record an older `nvx setup` left in the nvx
// home. Nothing in nvx writes it any more, so tests that need the record to
// exist write it here.
func writeWindowsSetupState(nvxHome string, s windowsSetupState) error {
	if s.SetupAt == "" {
		s.SetupAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := os.MkdirAll(nvxHome, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(windowsSetupMarkerPath(nvxHome), append(data, '\n'), 0600)
}
