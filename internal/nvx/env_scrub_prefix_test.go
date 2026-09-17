package nvx

import (
	"strings"
	"testing"
)

// The refusal list is the backstop that a checked-in project file cannot pass
// a credential into contained install code through isolation.environment.allow.
// The 2026-09-17 audit (probe P7) found whole families missing: npm's own
// config namespace carries registry auth tokens, the deployment providers are
// the same class as the cloud entries already listed, and NODE_OPTIONS and
// NODE_EXTRA_CA_CERTS are not credentials but change what a contained node
// does, which is worse.
func TestSensitiveEnvPrefixesCoverTheAuditedFamilies(t *testing.T) {
	refused := func(key string) bool {
		upper := strings.ToUpper(key)
		for _, prefix := range sensitiveEnvPrefixes {
			if strings.HasPrefix(upper, prefix) {
				return true
			}
		}
		return false
	}

	for _, key := range []string{
		"NPM_CONFIG__AUTH",
		"NPM_CONFIG_USERCONFIG",
		"npm_config_//registry.npmjs.org/:_authToken",
		"CLOUDFLARE_API_TOKEN",
		"VERCEL_TOKEN",
		"NETLIFY_AUTH_TOKEN",
		"NODE_OPTIONS",
		"NODE_EXTRA_CA_CERTS",
		"DATABASE_URL",
		// Already covered before the audit; here so a reordering cannot drop them.
		"AWS_SECRET_ACCESS_KEY",
		"GITHUB_TOKEN",
		"ANTHROPIC_API_KEY",
	} {
		if !refused(key) {
			t.Errorf("%s is not refused; a project file could pass it into a contained install", key)
		}
	}

	// The list must stay a list of secrets, not a deny-all. These are the
	// variables a contained build legitimately reads.
	for _, key := range []string{"PATH", "TEMP", "HOME", "USERPROFILE", "LANG", "CI", "NODE_ENV"} {
		if refused(key) {
			t.Errorf("%s is refused; the scrub list has grown into the ordinary environment", key)
		}
	}
}
