package main

import "testing"

func TestClassifyInvocation(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		args []string
		want invocationClass
	}{
		// your-code
		{"node run file", "node", []string{"app.js"}, classYourCode},
		{"npm run script", "npm", []string{"run", "build"}, classYourCode},
		{"npm test", "npm", []string{"test"}, classYourCode},
		{"npm start", "npm", []string{"start"}, classYourCode},
		{"bun run script", "bun", []string{"run", "dev"}, classYourCode},
		{"bun run file directly", "bun", []string{"app.ts"}, classYourCode},
		{"python script", "python", []string{"x.py"}, classYourCode},

		// install
		{"npm install", "npm", []string{"install"}, classInstall},
		{"npm i shorthand", "npm", []string{"i"}, classInstall},
		{"npm ci", "npm", []string{"ci"}, classInstall},
		{"yarn add", "yarn", []string{"add", "lodash"}, classInstall},
		{"pnpm install", "pnpm", []string{"install"}, classInstall},
		{"bun install", "bun", []string{"install"}, classInstall},
		{"bun add", "bun", []string{"add", "lodash"}, classInstall},
		{"bun add short alias", "bun", []string{"a", "lodash"}, classInstall},
		{"uv add", "uv", []string{"add", "requests"}, classInstall},
		{"uv pip install", "uv", []string{"pip", "install", "requests"}, classInstall},
		{"deno add npm pkg", "deno", []string{"add", "npm:lodash"}, classInstall},

		// ad-hoc-tool
		{"npx tool", "npx", []string{"cowsay", "hi"}, classAdHocTool},
		{"bunx tool", "bunx", []string{"cowsay", "hi"}, classAdHocTool},
		{"uvx tool", "uvx", []string{"ruff", "check"}, classAdHocTool},
		{"pyx tool", "pyx", []string{"ruff", "check"}, classAdHocTool},

		// leading flags must not defeat subcommand detection
		{"npm with global flag then install", "npm", []string{"--loglevel=error", "install", "pkg"}, classInstall},
		{"npm with global flag then run", "npm", []string{"--silent", "run", "build"}, classYourCode},

		// an UNRECOGNIZED value-taking flag ahead of the real subcommand must
		// not misclassify an install as your-code (the original bug: a fixed
		// "flags that take a value" allowlist let any flag outside it hide
		// the subcommand behind its own value).
		{"npm unknown value flag then install", "npm", []string{"--loglevel", "verbose", "install", "evil-pkg"}, classInstall},
		{"npm unrecognized flag then run stays your-code", "npm", []string{"--loglevel", "verbose", "run", "build"}, classYourCode},
		{"yarn unknown value flag then add", "yarn", []string{"--cwd", "/tmp/proj", "add", "lodash"}, classInstall},
		{"bun unknown value flag then install", "bun", []string{"--registry", "https://example.com", "install"}, classInstall},

		// a literal "install"-shaped word passed through `--` to your own
		// script must NOT be misread as the subcommand.
		{"npm run passthrough word install stays your-code", "npm", []string{"run", "deploy", "--", "install"}, classYourCode},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyInvocation(tc.cmd, tc.args)
			if got != tc.want {
				t.Errorf("classifyInvocation(%q, %v) = %v, want %v", tc.cmd, tc.args, got, tc.want)
			}
		})
	}
}
