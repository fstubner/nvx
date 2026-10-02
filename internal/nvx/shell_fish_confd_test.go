package nvx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installerFishConfD reads the text install.sh's setup_fish writes, by parsing
// its printf lines. The installer is the other half of this pair, so the test
// reads the script itself instead of a copy of it.
func installerFishConfD(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatalf("cannot read install.sh from the package directory: %v", err)
	}
	script := strings.ReplaceAll(string(raw), "\r\n", "\n")

	marker := ""
	body := ""
	inFish := false
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "MARKER_LINE='") {
			marker = strings.TrimSuffix(strings.TrimPrefix(trimmed, "MARKER_LINE='"), "'")
		}
		if strings.HasPrefix(trimmed, "setup_fish()") {
			inFish = true
			continue
		}
		if !inFish {
			continue
		}
		if strings.HasPrefix(trimmed, `} >> "$FISH_CONF"`) {
			break
		}
		const printfPrefix = `printf '%s\n' `
		if !strings.HasPrefix(trimmed, printfPrefix) {
			continue
		}
		arg := strings.TrimPrefix(trimmed, printfPrefix)
		if arg == `"$MARKER_LINE"` {
			body += marker + "\n"
			continue
		}
		if len(arg) < 2 || arg[0] != '\'' || arg[len(arg)-1] != '\'' {
			t.Fatalf("setup_fish has a printf argument this test does not parse: %s", trimmed)
		}
		body += arg[1:len(arg)-1] + "\n"
	}
	if marker == "" || body == "" {
		t.Fatalf("did not find setup_fish's output in install.sh (marker %q, body %q)", marker, body)
	}
	return body
}

// doctor --fix wrote only the `nvx env` line to the fish conf.d file. Fish then
// ran it before ~/.nvx/bin was on PATH. install.sh writes the PATH block as
// well, so the two must produce the same file. If either changes alone, a
// machine set up by one and repaired by the other holds a different integration
// than the one that was tested.
func TestFishConfDMatchesInstaller(t *testing.T) {
	if got, want := fishConfDContent(), installerFishConfD(t); got != want {
		t.Errorf("doctor's fish conf.d text differs from install.sh's setup_fish.\ndoctor:\n%s\ninstall.sh:\n%s", got, want)
	}
}

// The detection that stops doctor adding a second copy must read both the old
// doctor form (the line alone) and the installer form.
func TestFishConfDIsRecognisedInBothForms(t *testing.T) {
	dir := tempDir(t)
	for name, body := range map[string]string{
		"installer": fishConfDContent(),
		"line only": "\n# nvx shell integration (runtime switching on cd)\nnvx env --shell=fish | source\n",
	} {
		file := filepath.Join(dir, strings.ReplaceAll(name, " ", "-"))
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if !profileLoadsIntegration(file) {
			t.Errorf("the %s form of the fish file was not recognised as loading nvx:\n%s", name, body)
		}
	}
}

// Wired through doctor itself, with HOME and XDG_CONFIG_HOME in a temp tree and
// SHELL naming fish. The write runs for real: that is the point.
func TestDoctorFixWritesTheFishConfDFileOnce(t *testing.T) {
	allowRealProfileWrite(t)

	home := tempDir(t)
	xdg := filepath.Join(tempDir(t), "xdg")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("SHELL", "/usr/bin/fish")
	t.Setenv("MSYSTEM", "")
	t.Setenv("NVX_SHELL_INTEGRATION", "")
	withParentShell(t, "") // on Windows a real parent would answer before SHELL

	restorePath := repairPersistentPath
	repairPersistentPath = func(string, bool) (bool, error) { return false, nil }
	t.Cleanup(func() { repairPersistentPath = restorePath })

	runDoctor(tempDir(t), true)

	file := filepath.Join(xdg, "fish", "conf.d", "nvx.fish")
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("doctor --fix wrote no fish file at %s: %v", file, err)
	}
	if string(body) != fishConfDContent() {
		t.Errorf("doctor --fix wrote:\n%s\nwant what install.sh writes:\n%s", body, fishConfDContent())
	}

	runDoctor(tempDir(t), true)
	again, _ := os.ReadFile(file)
	if string(again) != string(body) {
		t.Errorf("a second doctor --fix changed the file:\n%s", again)
	}
}
