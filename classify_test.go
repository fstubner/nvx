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
