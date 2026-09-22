package nvx

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// An enforced global policy is an organisation baseline that a project may
// tighten and may not loosen.
//
// Without it, a project's own .nvx-policy.json can switch off what the global
// policy set: MergePolicies honours a local typosquatting.enabled of false, a
// lower release_age.min_age_hours, a higher vulnerabilities.min_severity, an
// isolation.enabled of false, and every addition to a trusted-package or host
// allowlist. The approve-once trust prompt stands in front of all of that, and
// answering it is a decision the developer at the keyboard is allowed to make.
// That is the right default for someone running nvx on their own machine and the
// wrong one for a machine whose baseline was set by somebody else, because the
// person being asked is the person the baseline exists to constrain.
//
// Opt-in, and global-only: `"enforced": true` in ~/.nvx/policy.json. Absent, not
// a single decision in this file changes.
//
// A loosening project file is REFUSED, not ignored. Ignoring it is what the
// untrusted-policy path already does, and it leaves a developer reading a
// setting in their own repository that has quietly never applied. Refusing costs
// them a failed command and tells them which setting, what the baseline says,
// and what their file asked for.

// policyEnforcementError is the refusal, carrying every field that was refused
// so the message can name all of them rather than the first.
type policyEnforcementError struct {
	Path       string
	Loosenings []policyLoosening
}

func (e *policyEnforcementError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "project policy %s loosens the enforced global policy", e.Path)
	for _, l := range e.Loosenings {
		fmt.Fprintf(&b, "\n  %s: the global policy says %s, this file asks for %s", l.Field, l.Before, l.After)
	}
	b.WriteString("\n  The global policy sets \"enforced\": true, so a project file may only make settings stricter.")
	return b.String()
}

// MergeUnderBaseline merges a project policy into the accumulated one, refusing
// any loosening when the global policy is an enforced baseline.
//
// Without enforcement this is MergePolicies and nothing else, so every existing
// caller and every policy file on disk behaves exactly as it did.
//
// The comparison is against the ACCUMULATED policy rather than the global file,
// because project files nest: an outer .nvx-policy.json that tightened the
// baseline is part of the baseline for the inner one. Otherwise an inner file
// could undo an outer file's tightening and still satisfy the global minimum.
func MergeUnderBaseline(accumulated, local Policy, sourcePath string) (Policy, error) {
	merged := MergePolicies(accumulated, local)
	if local.Enforced && !accumulated.Enforced {
		warnEnforcedInProjectFile(sourcePath)
	}
	// Whatever a project file says, enforcement is the global policy's to declare.
	// MergePolicies copies the accumulated value through, and this makes that
	// explicit rather than incidental to the order of the struct copy.
	merged.Enforced = accumulated.Enforced
	if !accumulated.Enforced {
		return merged, nil
	}
	if loosenings := policyLoosenings(accumulated, merged); len(loosenings) > 0 {
		return accumulated, &policyEnforcementError{Path: sourcePath, Loosenings: loosenings}
	}
	return merged, nil
}

// warnEnforcedInProjectFile reports a project file that tried to declare itself
// an enforced baseline.
//
// Said out loud rather than dropped. The key is read from the global policy
// only, so honouring it here would let the file being constrained appoint itself
// the constraint, and silently ignoring a security setting someone wrote down is
// the failure this codebase keeps finding.
var enforcedInProjectWarned sync.Map

func warnEnforcedInProjectFile(path string) {
	if path == "" {
		return
	}
	if _, seen := enforcedInProjectWarned.LoadOrStore(filepath.Clean(path), true); seen {
		return
	}
	// No escaped quotes around the key name. TestEveryLogWarnUsesALiteralFormat
	// scans for the first quote after LogWarn's opening one to find where the
	// format literal ends, so an escaped quote inside it reads as a format
	// built from runtime data, which is the leak that guard exists to catch.
	// This was the only LogWarn call in the tree carrying one.
	LogWarn("%s sets enforced true, which is read from the global policy only and is being ignored here.", path)
	LogInfo("An enforced baseline belongs in ~/.nvx/policy.json, where the project cannot edit it.")
}
