//go:build windows

package main

import (
	"path/filepath"
	"time"
)

const (
	// ancestorGrantBudget caps the TOTAL time all ancestor grants may consume per
	// launch. Measured on a real machine, granting the ancestors of a working
	// directory under AppData consumed 45.22s on its own -- the entire observed
	// setup stall -- by hanging to the full icacls timeout behind the
	// OneDrive/Defender filter driver. Every other phase of AppContainer setup
	// completed in under 0.15s.
	ancestorGrantBudget = 3 * time.Second

	// ancestorGrantPerPath bounds a single grant, so one pathological directory
	// cannot consume the whole budget by itself.
	ancestorGrantPerPath = 1500 * time.Millisecond
)

// ancestorGrantPaths returns the ancestor directories of workDir that should be
// granted traverse rights, nearest first, stopping below the profile root.
//
// It stops at the profile root deliberately: that root already grants ALL
// APPLICATION PACKAGES for stat/traverse, writing its ACL hangs behind the
// OneDrive/Defender filter driver, and C:\ and C:\Users are handled once by
// `nvx setup`.
func ancestorGrantPaths(workDir, profile string) []string {
	if workDir == "" || profile == "" {
		return nil
	}
	profile = filepath.Clean(profile)

	var out []string
	dir := filepath.Dir(filepath.Clean(workDir))
	for i := 0; i < 40; i++ {
		parent := filepath.Dir(dir)
		if parent == dir {
			break // drive root
		}
		if !isPathStrictlyUnder(dir, profile) {
			break
		}
		out = append(out, dir)
		dir = parent
	}
	return out
}

// grantAncestorsWithinBudget applies grant to each path in order until the total
// budget is exhausted, returning how many it attempted.
//
// These grants are advisory: the result was already discarded by the caller, and a
// command whose ancestor grants time out still runs. So a grant that hangs buys
// nothing and costs the user the whole timeout -- which is why the loop abandons
// the remainder rather than working through it. The cheap grants, which are the
// ones that actually apply, still happen.
func grantAncestorsWithinBudget(paths []string, budget time.Duration, grant func(string) error) (attempted int) {
	deadline := time.Now().Add(budget)
	for _, p := range paths {
		if !time.Now().Before(deadline) {
			return attempted
		}
		attempted++
		_ = grant(p)
	}
	return attempted
}
