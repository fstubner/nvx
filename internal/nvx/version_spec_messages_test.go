package nvx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `nvx install --lts` is what nvm and fnm users type, and it was answered with
// `prerelease and build metadata are not supported in "--lts"`.
func TestTheLtsFlagMeansTheNewestLts(t *testing.T) {
	for _, tc := range []struct{ arg, want string }{
		{"--lts", "lts"},
		{"--lts=iron", "lts/iron"},
		{"lts", "lts"},
		{"22", "22"},
	} {
		provider, version := parseRuntimeSpec(tc.arg)
		if provider.Name() != "node" || version != tc.want {
			t.Errorf("parseRuntimeSpec(%q) = %s, %q; want node, %q", tc.arg, provider.Name(), version, tc.want)
		}
	}
}

// fnm writes `lts-latest` in .node-version files. It was unreadable.
func TestLtsLatestIsTheNewestLtsToo(t *testing.T) {
	releases := []Release{
		{Version: "v24.1.0", Lts: false},
		{Version: "v22.11.0", Lts: "Jod"},
		{Version: "v20.18.0", Lts: "Iron"},
	}
	got, err := ResolveVersion("lts-latest", releases)
	if err != nil || got.Version != "v22.11.0" {
		t.Errorf("lts-latest resolved to %v, %v; want v22.11.0", got.Version, err)
	}

	nvxHome := tempDir(t)
	dir := filepath.Join(nvxHome, "versions", "node", "v22.11.0")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ltsMarkerName), []byte("Jod\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if v, err := resolveLocalVersion(NodeProvider{}, "lts-latest", nvxHome); err != nil || v != "v22.11.0" {
		t.Errorf("lts-latest against what is installed gave %q, %v; want v22.11.0", v, err)
	}
}

// `lts/-1` counts back from the newest LTS line. nvx does not follow that, and
// the message used to tell the reader to run `nvx install lts/-1`, which then
// failed in the same way.
func TestLtsOffsetsSayTheyAreNotReadAndDoNotOfferAnInstall(t *testing.T) {
	releases := []Release{{Version: "v22.11.0", Lts: "Jod"}, {Version: "v20.18.0", Lts: "Iron"}}
	nvxHome := tempDir(t)
	if err := os.MkdirAll(filepath.Join(nvxHome, "versions", "node", "v22.11.0"), 0o700); err != nil {
		t.Fatal(err)
	}

	_, remote := ResolveVersion("lts/-1", releases)
	_, local := resolveLocalVersion(NodeProvider{}, "lts/-1", nvxHome)
	for name, err := range map[string]error{"the release list": remote, "what is installed": local} {
		if err == nil {
			t.Errorf("lts/-1 resolved against %s", name)
			continue
		}
		if !strings.Contains(err.Error(), "does not read") || strings.Contains(err.Error(), "nvx install lts/-1") {
			t.Errorf("against %s the message is %q", name, err)
		}
		// Callers show an unreadable expression as such and never offer to download it.
		if !isUnsupportedRange(err) {
			t.Errorf("against %s the failure would be treated as a missing version and offered as an install", name)
		}
	}
	if ltsOffsetError("lts/iron") != nil || ltsOffsetError("lts/*") != nil || ltsOffsetError("22") != nil {
		t.Error("a codename, lts/* or a number was reported as an offset")
	}
}

// `deno@1` is a runtime nvx does not manage.
func TestAnUnmanagedRuntimeIsNamedAsSuch(t *testing.T) {
	for spec, want := range map[string]string{
		"deno@1":   "deno",
		"python@3": "python",
		"bun@1.2":  "",
		"node@22":  "",
		"Node@22":  "",
		"22":       "",
		"lts/iron": "",
		"bun":      "",
	} {
		if got := unknownRuntimeIn(spec); got != want {
			t.Errorf("unknownRuntimeIn(%q) = %q, want %q", spec, got, want)
		}
	}
	if got := runtimeNamesForMessage(); got != "Node.js and Bun" {
		t.Errorf("the runtimes nvx manages are listed as %q", got)
	}
}

// Through the command itself, since the check exits.
func TestInstallingAnUnmanagedRuntimeNamesIt(t *testing.T) {
	if spec := os.Getenv("NVX_TEST_INSTALL_SPEC"); spec != "" {
		os.Args = []string{"nvx", "install", spec}
		Main()
		os.Exit(0) // not reached: the spec is refused before anything is downloaded
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestInstallingAnUnmanagedRuntimeNamesIt$")
	cmd.Env = append(os.Environ(), "NVX_TEST_INSTALL_SPEC=deno@1", "NVX_HOME="+tempDir(t))
	out, err := cmd.CombinedOutput()
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("nvx install deno@1 should exit 1, got %v\n%s", err, out)
	}
	for _, want := range []string{`no runtime called "deno"`, "Node.js and Bun"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("the message does not contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "not a version number") {
		t.Errorf("deno@1 was still reported as a bad version number:\n%s", out)
	}
}

// `nvx use --lts` reaches the version resolution as the version it stands for,
// rather than being dropped as an unknown flag, which left "Please specify a
// version to use".
func TestUseTakesTheLtsFlagAsTheVersion(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--lts"}, "--lts"},
		{[]string{"--lts=iron", "--shell=bash"}, "--lts=iron"},
		{[]string{"--shell", "bash", "--lts"}, "--lts"},
		{[]string{"22", "--shell=bash"}, "22"},
		{[]string{"--shell=bash"}, ""},
		{[]string{"--bogus"}, ""},
	} {
		if got := useVersionArg(tc.args); got != tc.want {
			t.Errorf("useVersionArg(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

// `lts/-1` is refused before anything is looked up or fetched. After the lookup
// it offered to download on a home with nothing installed, and offline it
// reported the failed fetch instead.
func TestAnLtsOffsetIsRefusedBeforeAnythingIsFetched(t *testing.T) {
	if verb := os.Getenv("NVX_TEST_OFFSET_VERB"); verb != "" {
		os.Args = []string{"nvx", verb, "lts/-1"}
		Main()
		os.Exit(0) // not reached: the spec is refused first
	}
	for _, verb := range []string{"install", "use", "default"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestAnLtsOffsetIsRefusedBeforeAnythingIsFetched$")
		// Nothing listens on the mirror, so a fetch would fail loudly, and nothing
		// is installed, so `use` would otherwise offer to download.
		cmd.Env = append(os.Environ(), "NVX_TEST_OFFSET_VERB="+verb, "NVX_HOME="+tempDir(t), "NVX_NODE_MIRROR=http://127.0.0.1:1")
		out, err := cmd.CombinedOutput()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 1 {
			t.Errorf("nvx %s lts/-1 should exit 1, got %v\n%s", verb, err, out)
			continue
		}
		if !strings.Contains(string(out), "does not read") || strings.Contains(string(out), "fetch") || strings.Contains(string(out), "download") {
			t.Errorf("nvx %s lts/-1 said:\n%s", verb, out)
		}
	}
}
