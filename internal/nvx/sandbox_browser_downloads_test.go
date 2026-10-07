package nvx

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A browser in a guest home that nvx is about to delete is named, with the
// command that installs it where the tool looks for it. A blocked download
// leaves only small records behind, and those say nothing.
func TestBrowserDownloadInTheGuestHomeIsNamed(t *testing.T) {
	var playwright string
	for _, c := range browserCaches(runtime.GOOS) {
		if c.tool == "Playwright" {
			// On Windows the folder is under the AppContainer package's own.
			playwright = filepath.FromSlash(strings.Replace(c.dir, "*", "nvx.sandbox.0123456789abcdef", 1))
		}
	}
	writeSized := func(path string, size int64) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, size); err != nil {
			t.Fatal(err)
		}
	}

	// A puppeteer download, and what Playwright leaves after a blocked one.
	home := tempDir(t)
	writeSized(filepath.Join(home, ".cache", "puppeteer", "chrome", "linux-154.0.8037.57", "chrome"), 2<<20)
	writeSized(filepath.Join(home, playwright, ".links", "0123abcd"), 64)
	out := captureStderrHere(t, func() { warnLostBrowserDownloads(home) })
	for _, want := range []string{
		"puppeteer downloaded a browser into the sandbox's home, which nvx deletes when the command ends.",
		"--no-sandbox npx puppeteer browsers install chrome",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the warning does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Playwright") {
		t.Errorf("a blocked Playwright download was reported as a browser:\n%s", out)
	}

	// A Playwright download, where this platform's sandbox puts it.
	home = tempDir(t)
	writeSized(filepath.Join(home, playwright, "chromium-1243", "chrome"), 2<<20)
	out = captureStderrHere(t, func() { warnLostBrowserDownloads(home) })
	if !strings.Contains(out, "Playwright downloaded a browser") || !strings.Contains(out, "--no-sandbox npx playwright install") {
		t.Errorf("a Playwright download was not named:\n%s", out)
	}

	if out := captureStderrHere(t, func() { warnLostBrowserDownloads(tempDir(t)) }); out != "" {
		t.Errorf("a guest home with no browser in it printed:\n%s", out)
	}
}
