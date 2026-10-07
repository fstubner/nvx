package nvx

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// yarn turns dependency scripts off only through the settings it reads, and the
// install-script check is skipped only for those.
//
// Until 2026-10-07 a project .npmrc with ignore-scripts=true skipped the check
// for yarn as it does for npm. Measured that day in a container, with a
// dependency whose postinstall writes a marker, yarn 1.22.22, 2.4.3 and 3.8.7
// ran the postinstall under that .npmrc. A repository could ship the file to
// silence the prompt while yarn ran the scripts.
func TestYarnScriptsAreOffOnlyWhereYarnTurnsThemOff(t *testing.T) {
	const berryRC = "yarnPath: .yarn/releases/yarn-4.18.1.cjs\n"
	cases := []struct {
		name     string
		manifest string // package.json; a bare project when empty
		npmrc    string
		yarnrc   string
		subRC    string // .yarnrc.yml in sub/, where the command runs
		env      map[string]string
		direct   bool // run outside the sandbox, where the environment reaches yarn
		args     []string
		by       string // the check_skipped record's by; "" when the check must ask
	}{
		{name: "project .npmrc", npmrc: "ignore-scripts=true\n"},
		{name: "npm_config_ignore_scripts", env: map[string]string{"npm_config_ignore_scripts": "true"}, direct: true},
		{name: "enableScripts under yarn 1", yarnrc: "enableScripts: false\n"},
		{name: "--mode=skip-build under yarn 1", args: []string{"--mode=skip-build"}},
		{name: "--ignore-scripts", args: []string{"--ignore-scripts"}, by: "ignore_scripts_flag"},

		{name: "enableScripts, yarnPath", yarnrc: berryRC + "enableScripts: false\n", by: "yarnrc_enable_scripts"},
		{name: "enableScripts, quoted", yarnrc: berryRC + "enableScripts: \"false\"\n", by: "yarnrc_enable_scripts"},
		{name: "enableScripts, packageManager", manifest: `{"name":"p","packageManager":"yarn@4.18.1"}`,
			yarnrc: "enableScripts: false\n", by: "yarnrc_enable_scripts"},
		{name: "enableScripts, packageManager yarn 1", manifest: `{"name":"p","packageManager":"yarn@1.22.22"}`,
			yarnrc: "enableScripts: false\n"},
		{name: "--mode=skip-build, yarnPath", yarnrc: berryRC, args: []string{"--mode=skip-build"}, by: "yarn_mode_skip_build"},
		{name: "--mode skip-build, yarnPath", yarnrc: berryRC, args: []string{"--mode", "skip-build"}, by: "yarn_mode_skip_build"},
		{name: "enableScripts with a dependency built anyway", yarnrc: berryRC + "enableScripts: false\n",
			manifest: `{"name":"p","dependenciesMeta":{"some-pkg":{"built":true}}}`},
		{name: "enableScripts under YARN_ENABLE_SCRIPTS=true", yarnrc: berryRC + "enableScripts: false\n",
			env: map[string]string{"YARN_ENABLE_SCRIPTS": "true"}},
		{name: "enableScripts true nearer the working directory", yarnrc: berryRC + "enableScripts: false\n",
			subRC: "enableScripts: true\n"},
		{name: "enableScripts false nearer the working directory", yarnrc: berryRC + "enableScripts: true\n",
			subRC: "enableScripts: false\n", by: "yarnrc_enable_scripts"},
		{name: "enableScripts true", yarnrc: berryRC + "enableScripts: true\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("NVX_NONINTERACTIVE", "1")
			t.Setenv("NVX_YES", "")
			t.Setenv("npm_config_ignore_scripts", "")
			t.Setenv("YARN_ENABLE_SCRIPTS", "")
			t.Setenv("YARN_RC_FILENAME", "")
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			dir := tempDir(t)
			manifest := c.manifest
			if manifest == "" {
				manifest = `{"name":"p"}`
			}
			write := func(path, data string) {
				t.Helper()
				if data == "" {
					return
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(dir, "package.json"), manifest)
			write(filepath.Join(dir, ".npmrc"), c.npmrc)
			write(filepath.Join(dir, ".yarnrc.yml"), c.yarnrc)
			cwd := dir
			if c.subRC != "" {
				cwd = filepath.Join(dir, "sub")
				write(filepath.Join(cwd, ".yarnrc.yml"), c.subRC)
			}
			inProjectDir(t, cwd)

			sc := checkScenario{pkg: "some-pkg", policy: `{"typosquatting":{"enabled":false}}`,
				age: 1000 * time.Hour, scripts: true,
				command: append([]string{"yarn", "add", "some-pkg"}, c.args...), contain: !c.direct}
			code, out, home := runScenario(t, sc)
			if c.by == "" {
				if code == 0 {
					t.Fatalf("the install-script check did not stop it, though yarn runs the scripts:\n%s", out)
				}
				if r := findRecord(readAuditRecords(t, home), "check_refused", "install_scripts"); r == nil {
					t.Errorf("no install_scripts refusal recorded: %v", readAuditRecords(t, home))
				}
				return
			}
			if code != 0 {
				t.Fatalf("a command that runs no scripts was stopped (exit %d):\n%s", code, out)
			}
			if r := findRecord(readAuditRecords(t, home), "check_skipped", "install_scripts"); r == nil || r["by"] != c.by {
				t.Errorf("want a check_skipped record for install_scripts with by=%s, got %v", c.by, readAuditRecords(t, home))
			}
		})
	}
}
