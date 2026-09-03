package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// No shipped document may say contained `npx` requires an elevated `nvx setup`.
//
// This one constraint has been written down wrong three times in four days,
// each time as a measurement:
//
//	2026-08-30  asserted: npx fails with EPERM without the grant
//	2026-09-01  retracted: the EPERM was inside ~/.nvx, npx "works unelevated"
//	2026-09-02  a commit, a README rewrite and a doctor change shipped on that
//	2026-09-03  measured with the grant actually removed: npx failed again
//
// The third entry is the expensive one. Every run behind it happened on a
// machine where the grant was present and nobody checked, so "works without P"
// was never measured with P absent. It cost a 22-minute elevated ACL write over
// 5.6 million entries on a volume nothing needed it for.
//
// It is settled now, and by code rather than by wording: sandbox_walkup_shim.js
// answers the stat that made npx need the grant, and
// TestWalkUpShimAnswersForUnreadableAncestors asserts that against a real
// AppContainer holding no drive-root grant. So a document claiming npx needs
// setup is now simply false — and on 2026-09-03 README's command reference still
// said it while the binary's own help said the opposite, because the parity test
// next door compares command NAMES and not what is said about them.
//
// A grep, deliberately. It is crude, it cannot understand a sentence, and it
// pins exactly the claim that keeps coming back — which is what the recurrence
// earns. Delete it if the underlying fact ever genuinely changes, and change the
// code and the probe first.
func TestNoDocumentSaysNpxNeedsElevatedSetup(t *testing.T) {
	// "npx" and a requirement, close together, in a sentence about setup.
	claim := regexp.MustCompile(`(?is)\bnpx\b[^.\n]{0,80}\b(needs? it|requires? it|needs? ` + "`?nvx setup" + `|requires? ` + "`?nvx setup" + `)`)
	reverse := regexp.MustCompile(`(?is)\bnvx setup\b[^.\n]{0,80}\brequired\b[^.\n]{0,40}\bnpx\b`)

	for _, doc := range []string{"README.md", "PRODUCT.md", "SECURITY.md", "CONTRIBUTING.md"} {
		b, err := os.ReadFile(doc)
		if err != nil {
			t.Fatalf("read %s: %v", doc, err)
		}
		text := string(b)
		for _, re := range []*regexp.Regexp{claim, reverse} {
			for _, hit := range re.FindAllString(text, -1) {
				// The history above is written down on purpose and quotes the old
				// claim to explain what was wrong with it. A line that is plainly
				// recording the correction is not the product making the claim.
				line := lineContaining(text, hit)
				if isHistoricalNote(line) || isNegated(line) {
					continue
				}
				t.Errorf("%s still says contained npx needs an elevated setup:\n  %s\n"+
					"Measured 2026-09-03 with no drive-root grant on any volume: `npx -y cowsay hi` "+
					"runs contained. If you believe this changed, change sandbox_walkup_shim.js and its "+
					"probe first, then this test.", doc, strings.TrimSpace(line))
			}
		}
	}
}

// lineContaining returns the whole line that hit sits on.
func lineContaining(text, hit string) string {
	i := strings.Index(text, hit)
	if i < 0 {
		return hit
	}
	start := strings.LastIndex(text[:i], "\n") + 1
	end := strings.Index(text[i:], "\n")
	if end < 0 {
		return text[start:]
	}
	return text[start : i+end]
}

// isHistoricalNote reports whether a line is recording a past, corrected claim
// rather than making one. These documents deliberately keep their own mistakes
// visible; a rule that forbade quoting them would push the history out instead.
func isHistoricalNote(line string) bool {
	l := strings.ToLower(line)
	for _, marker := range []string{
		"used to", "was wrong", "no longer", "until 2026", "measured 2026-08",
		"retracted", "corrected", "this entry", "earlier version", "withdrawn",
	} {
		if strings.Contains(l, marker) {
			return true
		}
	}
	return false
}

// isNegated reports whether the line says npx does NOT need setup, which is the
// correct claim and matches the same words in the opposite order. Without this
// the check fires on its own fix -- "installs and `npx` do not need it" contains
// "need it".
func isNegated(line string) bool {
	l := strings.ToLower(line)
	for _, marker := range []string{"do not need", "does not need", "don't need", "doesn't need",
		"not required", "never need", "no longer need", "optional"} {
		if strings.Contains(l, marker) {
			return true
		}
	}
	return false
}
