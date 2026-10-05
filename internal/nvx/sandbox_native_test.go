package nvx

import "testing"

func TestPersistentProfileName(t *testing.T) {
	cases := []struct {
		name string
		cfg  SandboxConfig
		want string
	}{
		{"no tool name", SandboxConfig{Command: "npx", Args: []string{"wrangler", "deploy"}}, ""},
		{"granted tool", SandboxConfig{Command: "npx", Args: []string{"wrangler", "login"}, ToolName: "wrangler"}, "wrangler"},

		// pnpm records its store's path in node_modules/.modules.yaml, so the
		// store has to be where the last install left it.
		{"pnpm install", SandboxConfig{Command: "pnpm", Args: []string{"install"}}, pnpmStoreProfile},
		{"pnpm add", SandboxConfig{Command: "pnpm", Args: []string{"add", "is-number"}}, pnpmStoreProfile},
		{"pnpm entry script", SandboxConfig{Command: "node", Args: []string{`C:\n\node_modules\pnpm\bin\pnpm.cjs`, "install"}}, pnpmStoreProfile},
		{"corepack pnpm", SandboxConfig{Command: "corepack", Args: []string{"pnpm@10.34.6", "install"}}, pnpmStoreProfile},

		{"npm install", SandboxConfig{Command: "npm", Args: []string{"install"}}, ""},
		{"yarn install", SandboxConfig{Command: "yarn", Args: []string{"install"}}, ""},
		{"bun install", SandboxConfig{Command: "bun", Args: []string{"install"}}, ""},
		{"own script", SandboxConfig{Command: "node", Args: []string{"build.js"}}, ""},
	}
	for _, c := range cases {
		if got := persistentProfileName(c.cfg); got != c.want {
			t.Errorf("%s: persistentProfileName = %q, want %q", c.name, got, c.want)
		}
	}
}
