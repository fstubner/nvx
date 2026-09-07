package main

import (
	"encoding/json"
	"os"
)

// bareFileIsNvxPolicy reports whether a file named exactly `policy.json` is
// nvx's, judged by what is in it rather than by where it sits.
//
// `.nvx-policy.json` is unambiguous. The bare name is accepted as a convenience
// and belongs to several other tools -- Open Policy Agent bundles, IAM policy
// exports, Terraform, a few linters all write that exact filename -- and nvx
// walks every ancestor of the working directory looking for one. Anyone with
// such a file above their project got a trust prompt about a document they
// never wrote for nvx, warnings that its keys were misspelt nvx settings, and a
// pinned hash that breaks whenever the other tool rewrites it.
//
// An unreadable file counts as nvx's on purpose: whose it is cannot be decided
// from bytes that could not be read, and the caller reports the read failure.
// Skipping it here would turn an unreadable policy into no policy, which is the
// wrong direction for a file whose job is to add restrictions.
func bareFileIsNvxPolicy(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(withoutUTF8BOM(data), &top) != nil {
		// Malformed JSON under the bare name: also the caller's to report, for
		// the same reason.
		return true
	}
	known, _ := policyKeyPaths()
	for key := range top {
		if known[key] {
			return true
		}
	}
	return false
}
