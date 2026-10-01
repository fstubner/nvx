package nvx

import (
	"strings"
	"testing"
)

// Every package-manager verb nvx has an opinion on, with the class it must get.
//
// Classification is an allowlist: a verb nobody listed is "your code" and runs
// uncontained with no pre-install checks. So the table is the place to add a
// verb a package manager grows, one line each, and a verb missing from it is the
// defect to look for first.
//
// The install-class rows marked "measured" ran a dependency's postinstall on
// 2026-10-01, from a project holding a lockfile and no node_modules, against npm
// 11.19.0, pnpm 8.3.1 and bun 1.4.2. The your-code rows marked "measured" did
// not. Rows with no mark follow the tool's own help or source, and the ones
// that could not be checked without the tool are classified as contained.
func TestEveryPackageManagerVerbHasItsClass(t *testing.T) {
	const (
		install = classInstall
		adhoc   = classAdHocTool
		yours   = classYourCode
	)
	cases := []struct {
		line string
		want invocationClass
	}{
		// npm
		{"npm install", install},
		{"npm install left-pad", install},
		{"npm i left-pad", install},
		{"npm add left-pad", install},
		{"npm isntall left-pad", install},
		{"npm install-test", install},
		{"npm cit", install},
		{"npm ci", install},
		{"npm clean-install", install},
		{"npm update", install},
		{"npm up", install},
		{"npm udpate", install},
		{"npm rebuild", install},
		{"npm rb", install},
		{"npm dedupe", install},
		{"npm ddp", install},
		{"npm audit fix", install},
		{"npm uninstall left-pad", install}, // measured
		{"npm unlink left-pad", install},    // measured
		{"npm remove left-pad", install},
		{"npm rm left-pad", install},
		{"npm r left-pad", install},
		{"npm un left-pad", install},
		{"npm prune", install},         // measured
		{"npm edit left-pad", install}, // runs `npm rebuild` on the package afterwards
		{"npm link left-pad", install}, // installs into the global prefix first
		{"npm link ../other", install}, // measured, ran the other package's dependency scripts
		{"npm ln left-pad", install},   // link's alias
		{"npm link", yours},            // measured, runs only this package's own scripts
		{"npm exec cowsay", adhoc},
		{"npm x cowsay", adhoc},
		{"npm create vite", adhoc},
		{"npm init vite", adhoc},
		{"npm pack github:user/repo", adhoc}, // a git dependency's prepare runs
		{"npm pack git+https://example.com/r.git", adhoc},
		{"npm pack user/repo", adhoc},
		{"npm pack https://example.com/x.tgz", adhoc},
		{"npm diff --diff=github:user/repo", adhoc},
		{"npm diff --diff github:user/repo", adhoc},
		{"npm view git+ssh://git@example.com/r.git", adhoc},
		{"npm cache add github:user/repo", adhoc},
		{"npm pack", yours},          // measured, this package's own prepack
		{"npm pack left-pad", yours}, // a registry tarball, no scripts
		{"npm pack ../other", yours}, // a local directory, your own code
		{"npm view left-pad version", yours},
		{"npm diff", yours},
		{"npm init", yours},
		{"npm run build", yours},
		{"npm run-script update", yours},
		{"npm test", yours},
		{"npm start", yours},
		{"npm publish", yours},
		{"npm version patch", yours}, // measured
		{"npm config get registry", yours},
		{"npm config edit", yours},
		{"npm whoami", yours},
		{"npm ls", yours},                            // measured
		{"npm audit", yours},                         // measured
		{"npm outdated", yours},                      // measured
		{"npm explore left-pad", yours},              // measured, runs the command you give it
		{"npm approve-scripts left-pad", yours},      // measured
		{"npm approve-scripts --all", yours},         // measured
		{"npm install-scripts approve --all", yours}, // measured
		{"npm deny-scripts --all", yours},            // measured
		{"npm trust list", yours},                    // OIDC publishing trust, not bun's pm trust

		// pnpm
		{"pnpm install", install},
		{"pnpm i", install},
		{"pnpm add left-pad", install},
		{"pnpm install-test", install},
		{"pnpm update", install},
		{"pnpm up", install},
		{"pnpm rebuild", install}, // measured
		{"pnpm rb", install},
		{"pnpm dedupe", install},          // measured
		{"pnpm remove left-pad", install}, // measured
		{"pnpm rm left-pad", install},
		{"pnpm uninstall left-pad", install},
		{"pnpm un left-pad", install},
		{"pnpm unlink left-pad", install}, // re-installs the dependency, per its help
		{"pnpm prune", install},           // measured
		{"pnpm fetch", install},           // measured
		{"pnpm deploy out", install},
		{"pnpm approve-builds", install}, // runs the builds it approves
		{"pnpm patch left-pad", yours},   // pnpm 8.3.1 source copies the package from the store, no scripts
		{"pnpm patch-commit dir", install},
		{"pnpm patch-remove left-pad", install},
		{"pnpm self-update", install},
		{"pnpm env use --global 20", install},
		{"pnpm env add --global 20", install},
		{"pnpm link ../other", install}, // measured, ran the other package's prepare
		{"pnpm ln ../other", install},
		{"pnpm dlx cowsay", adhoc},
		{"pnpm create vite", adhoc},
		{"pnpm exec tsc", yours},
		{"pnpm run build", yours},
		{"pnpm test", yours},
		{"pnpm publish", yours},
		{"pnpm pack", yours}, // measured
		{"pnpm ls", yours},   // measured
		{"pnpm store prune", yours},
		{"pnpm store status", yours},
		{"pnpm env list", yours},
		{"pnpm setup", yours},

		// yarn (not installed where this was measured, so contained where unsure)
		{"yarn", install},
		{"yarn --frozen-lockfile", install},
		{"yarn install", install},
		{"yarn add left-pad", install},
		{"yarn upgrade", install},
		{"yarn up left-pad", install},
		{"yarn rebuild", install},
		{"yarn dedupe", install},
		{"yarn remove left-pad", install},
		{"yarn unlink left-pad", install},
		{"yarn unplug left-pad", install},
		{"yarn upgrade-interactive", install},
		{"yarn patch left-pad", install},
		{"yarn patch-commit dir", install},
		{"yarn link ../other", install},
		{"yarn workspaces focus", install},
		{"yarn set version stable", install},
		{"yarn policies set-version 1.22.22", install},
		{"yarn plugin import interactive-tools", install},
		{"yarn dlx cowsay", adhoc},
		{"yarn create vite", adhoc},
		{"yarn run build", yours},
		{"yarn test", yours},
		{"yarn exec tsc", yours},
		{"yarn node app.js", yours},
		{"yarn --version", yours},
		{"yarn config set version 1", yours},
		{"yarn workspace focus run build", yours}, // a workspace named "focus"
		{"yarn pack", yours},
		{"yarn npm publish", yours},
		{"yarn version patch", yours},

		// bun
		{"bun install", install},
		{"bun i", install},
		{"bun add left-pad", install},
		{"bun a left-pad", install},
		{"bun update", install},
		{"bun up", install},
		{"bun remove left-pad", install}, // measured
		{"bun rm left-pad", install},
		{"bun patch left-pad", install},                       // measured
		{"bun patch --commit node_modules/left-pad", install}, // measured
		{"bun patch-commit node_modules/left-pad", install},
		{"bun link left-pad", install},     // measured
		{"bun pm trust left-pad", install}, // measured
		{"bun pm trust --all", install},    // measured
		{"bun x cowsay", adhoc},
		{"bun create vite", adhoc},
		{"bun c vite", adhoc},
		{"bun link", yours},         // measured
		{"bun prune", yours},        // measured
		{"bun pm untrusted", yours}, // measured
		{"bun pm ls", yours},        // measured
		{"bun pm pack", yours},      // measured
		{"bun pm cache rm", yours},
		{"bun pm migrate", yours},
		{"bun outdated", yours},     // measured
		{"bun audit", yours},        // measured
		{"bun why left-pad", yours}, // measured
		{"bun run build", yours},
		{"bun test", yours},
		{"bun publish", yours},
		{"bun build ./a.ts --outdir c", yours},
		{"bun app.ts", yours},

		// corepack runs the package manager it names, so that command decides.
		{"corepack pnpm add left-pad", install},
		{"corepack pnpm@8.3.1 install", install},
		{"corepack yarn add left-pad", install},
		{"corepack yarnpkg add left-pad", install},
		{"corepack npm install left-pad", install},
		{"corepack npx cowsay", adhoc},
		{"corepack pnpx cowsay", adhoc},
		{"corepack pnpm run build", yours},
		{"corepack yarn test", yours},
		{"corepack use pnpm@9", install}, // installs with the new version
		{"corepack up", install},
		{"corepack enable", yours},
		{"corepack install -g pnpm@9", yours}, // downloads the manager, runs nothing
		{"corepack --version", yours},

		// node running a package manager's entry script is that package manager.
		{"node /usr/lib/node_modules/npm/bin/npm-cli.js install left-pad", install},
		{`node C:\nodejs\node_modules\npm\bin\npm-cli.js install left-pad`, install},
		{"node /usr/lib/node_modules/npm/bin/npm-cli.js run build", yours},
		{"node /usr/lib/node_modules/npm/bin/npx-cli.js cowsay", adhoc},
		{"node /x/pnpm/bin/pnpm.cjs add left-pad", install},
		{"node /x/pnpm/bin/pnpx.cjs cowsay", adhoc},
		{"node /x/pnpm/dist/pnpm.cjs approve-builds", install},
		{"node /x/node_modules/yarn/bin/yarn.js add left-pad", install},
		{"node .yarn/releases/yarn-4.5.0.cjs add left-pad", install},
		{"node /x/node_modules/corepack/dist/pnpm.js add left-pad", install},
		{"node /x/node_modules/corepack/dist/corepack.js pnpm add left-pad", install},
		{"node --no-warnings /x/npm-cli.js install left-pad", install},
		{"node --require ./hook.js /x/npm-cli.js install left-pad", install},
		{"node -- /x/npm-cli.js install left-pad", install},
		{"node app.js install left-pad", yours},
		{"node yarn.js add left-pad", yours}, // your own file, not an installed yarn
		{"node -e 0 /x/npm-cli.js install", yours},
		{"node server.js", yours},
		{"bun /x/npm-cli.js exec cowsay", adhoc},
		{"bun run /x/npm-cli.js exec cowsay", adhoc},
	}
	for _, tc := range cases {
		fields := strings.Fields(tc.line)
		if got := classifyInvocation(fields[0], fields[1:]); got != tc.want {
			t.Errorf("`%s` classified as %v, want %v", tc.line, got, tc.want)
		}
	}
}
