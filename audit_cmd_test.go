package main

import (
	"strings"
	"testing"
)

// `nvx audit` must not show a reader the printf verbs from a recorded warning.
//
// The log deliberately stores each warning's FORMAT STRING and never the
// rendered text -- rendering once put a live password in it. The side effect was
// that audit output read "Ignoring project policy %s: ...", which looks like a
// formatting bug rather than a redaction, and an acceptance pass reported it as
// one. Redacted on the way out, so the stored bytes keep their exact aggregation
// key and records already on disk read correctly too.
//
// Driven through formatAuditEntry rather than the redactor, because the defect
// was never in redacting: it was that nothing redacted on the path that prints.
func TestAuditDoesNotShowFormatVerbsToTheReader(t *testing.T) {
	out := formatAuditEntry(map[string]string{
		"event":    "run",
		"warnings": "Ignoring project policy %s: it loosens things. | Retried %d times at 50%% capacity.",
	})
	if strings.Contains(out, "%s") || strings.Contains(out, "%d") {
		t.Fatalf("a printf verb reached the reader:\n%s", out)
	}
	if !strings.Contains(out, "[…]") {
		t.Fatalf("withheld data was not marked as withheld:\n%s", out)
	}
	// An escaped percent is a literal sign, not withheld data.
	if !strings.Contains(out, "50%% capacity") {
		t.Fatalf("an escaped percent was treated as a verb:\n%s", out)
	}
	// The surrounding text is the whole value of the record; it must survive.
	if !strings.Contains(out, "it loosens things.") {
		t.Fatalf("the warning text was lost:\n%s", out)
	}
}
