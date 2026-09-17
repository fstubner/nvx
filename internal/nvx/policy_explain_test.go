package nvx

import (
	"strings"
	"testing"
)

func settingRow(t *testing.T, exp policyExplanation, name string) policySettingSource {
	t.Helper()
	for _, row := range exp.Rows {
		if row.Setting == name {
			return row
		}
	}
	t.Fatalf("no row for %s; nvx policy explain does not report it at all", name)
	return policySettingSource{}
}

// The question this command exists for is "I set this in my project file, why is
// it not applying". Three different things cause that and they look identical
// from outside, so each setting says where its value came from.
func TestExplainSaysWhereEachSettingCameFrom(t *testing.T) {
	home := tempDir(t)
	writePolicyFixture(t, home, "policy.json", `{"typosquatting": {"max_distance": 4}}`)
	project := tempDir(t)
	writePolicyFixture(t, project, ".nvx-policy.json", `{"release_age": {"min_age_hours": 72}}`)
	inProjectDir(t, project)

	exp, err := explainPolicy(home, project)
	if err != nil {
		t.Fatalf("explainPolicy: %v", err)
	}

	if row := settingRow(t, exp, "typosquatting.max_distance"); row.Value != "4" || !strings.Contains(row.Source, "global policy") {
		t.Errorf("typosquatting.max_distance = %q from %q, want 4 from the global policy", row.Value, row.Source)
	}
	if row := settingRow(t, exp, "release_age.min_age_hours"); row.Value != "72" || !strings.Contains(row.Source, "project policy") {
		t.Errorf("release_age.min_age_hours = %q from %q, want 72 from the project policy", row.Value, row.Source)
	}
	// A setting nobody configured has to say so, rather than reading as a choice
	// somebody made.
	if row := settingRow(t, exp, "isolation.network.mode"); row.Source != "built-in default" {
		t.Errorf("isolation.network.mode came from %q, want the built-in default", row.Source)
	}
}

// A project file that is not in force is the case the command is most needed
// for, and the one where saying nothing is worst: the reader has the file open.
func TestExplainNamesAProjectFileThatIsNotInForce(t *testing.T) {
	t.Run("ignored because it was never trusted", func(t *testing.T) {
		home := tempDir(t)
		project := tempDir(t)
		writePolicyFixture(t, project, ".nvx-policy.json", `{"isolation": {"enabled": false}}`)
		inProjectDir(t, project)

		exp, err := explainPolicy(home, project)
		if err != nil {
			t.Fatalf("explainPolicy: %v", err)
		}
		if len(exp.Issues) != 1 || exp.Issues[0].Kind != "ignored" {
			t.Fatalf("issues = %+v, want one ignored file", exp.Issues)
		}
		if row := settingRow(t, exp, "isolation.enabled"); row.Value != "true" {
			t.Errorf("isolation.enabled = %q, want true: the untrusted file must not apply", row.Value)
		}
	})

	t.Run("refused by an enforced baseline", func(t *testing.T) {
		home := tempDir(t)
		writePolicyFixture(t, home, "policy.json", `{"enforced": true}`)
		project := tempDir(t)
		writePolicyFixture(t, project, ".nvx-policy.json", `{"isolation": {"enabled": false}}`)
		inProjectDir(t, project)

		exp, err := explainPolicy(home, project)
		if err != nil {
			t.Fatalf("explainPolicy: %v", err)
		}
		if len(exp.Issues) != 1 || exp.Issues[0].Kind != "refused" {
			t.Fatalf("issues = %+v, want one refused file", exp.Issues)
		}
		if !strings.Contains(exp.Issues[0].Detail, "isolation.enabled") {
			t.Errorf("the refusal does not name the field: %s", exp.Issues[0].Detail)
		}
	})
}

// An enforced baseline changes what a reader can do about anything in the table,
// so it is stated once at the top rather than left to be inferred.
func TestExplainSaysWhenTheBaselineIsEnforced(t *testing.T) {
	home := tempDir(t)
	writePolicyFixture(t, home, "policy.json", `{"enforced": true}`)
	project := tempDir(t)
	inProjectDir(t, project)

	exp, err := explainPolicy(home, project)
	if err != nil {
		t.Fatalf("explainPolicy: %v", err)
	}
	if !strings.Contains(strings.Join(exp.Notes, " "), "enforced") {
		t.Fatalf("notes = %v, want the enforcement said out loud", exp.Notes)
	}
	if row := settingRow(t, exp, "enforced"); row.Value != "true" {
		t.Errorf("enforced = %q, want true", row.Value)
	}
}
