package nvx

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// A contained process may not read the project's dotenv files: .env, and
// .env.<anything> such as .env.local or .env.production. The project has to be
// readable for an install to work, and these files hold the project's secrets
// next to everything the install does need. Scrubbing the environment does not
// reach them, because they are files.
//
// The name is matched in any case. macOS volumes are case-insensitive by
// default, so .ENV opens .env there, and Linux follows the same rule so the two
// platforms hide the same set.

// dotenvTemplates are the dotenv names that stay readable, matched exactly.
// Each is a template that exists to be committed and copied: .env.example,
// .env.sample and .env.template are the common names in dotenv's own
// documentation and in project scaffolders, and .env.dist is Symfony's. Other
// names, .env.development and .env.test among them, are committed in some
// projects and hold real credentials in others, so they stay hidden.
var dotenvTemplates = []string{".env.example", ".env.sample", ".env.template", ".env.dist"}

// isDotenvName reports whether a file named name is hidden from a contained
// process.
func isDotenvName(name string) bool {
	lower := strings.ToLower(name)
	if lower != ".env" && !strings.HasPrefix(lower, ".env.") {
		return false
	}
	return !slices.Contains(dotenvTemplates, name)
}

// dotenvScanLimit is how many directory entries findDotenvFiles examines before
// it stops. A project's own files rarely come near it once node_modules and
// .git are left out. The walk is breadth first, so a project that does reach it
// still has its shallowest dotenv files found, the one at the root first.
const dotenvScanLimit = 50000

// dotenvSkipDirs are directories findDotenvFiles does not enter. node_modules
// holds packages, which are not the project's secrets, and both it and .git
// are large.
var dotenvSkipDirs = []string{"node_modules", ".git"}

// findDotenvFiles returns the paths under root whose names isDotenvName
// accepts, breadth first. It does not follow symbolic links to directories.
// complete is false when it stopped after limit entries.
func findDotenvFiles(root string, limit int) (found []string, complete bool) {
	queue := []string{root}
	seen := 0
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			seen++
			if seen > limit {
				return found, false
			}
			name := e.Name()
			switch {
			case e.IsDir():
				if !slices.Contains(dotenvSkipDirs, name) {
					queue = append(queue, filepath.Join(dir, name))
				}
			case isDotenvName(name):
				found = append(found, filepath.Join(dir, name))
			}
		}
	}
	return found, true
}

// protectedDotenv is a dotenv file whose permissions nvx changed on Windows to
// keep the sandbox out, with the permissions it had before, so `nvx grants
// reset` can put them back. See hideDotenvFromSandbox.
type protectedDotenv struct {
	Path string `json:"path"`
	// SDDL is the file's permission list before nvx changed it.
	SDDL string `json:"sddl"`
}

// recordProtectedDotenv adds the record for path, or replaces it when replace
// is set. The caller replaces a record only for a file that inherits its
// permissions, which is a new file an editor or git put in place, so the
// record then holds what the new file came with. A file that is already
// protected and has a record keeps it: its list may be one nvx wrote with an
// entry added since, and recording that would lose the permissions from before
// nvx.
func recordProtectedDotenv(existing []protectedDotenv, path, sddl string, replace bool) []protectedDotenv {
	path = filepath.Clean(path)
	for i, r := range existing {
		if sameGrantPath(r.Path, path) {
			if replace {
				existing[i].SDDL = sddl
			}
			return existing
		}
	}
	return append(existing, protectedDotenv{Path: path, SDDL: sddl})
}

// restoreAllProtectedDotenv puts back the permissions of every recorded dotenv
// file under root, for `nvx grants reset`. It returns the records to keep:
// those it could not restore, which a later reset retries.
func restoreAllProtectedDotenv(root string, records []protectedDotenv) (restored int, keep []protectedDotenv) {
	for _, r := range records {
		err := restoreProtectedDotenv(root, r)
		switch {
		case err == nil:
			restored++
		case errors.Is(err, errNothingToWithdraw):
			// Gone, or replaced by a file that inherits its permissions again.
		case errors.Is(err, errDotenvChanged):
			LogWarn("Left the permissions of %s as they are: they were changed after nvx hid the file from the sandbox.", r.Path)
		default:
			LogWarn("Could not put back the permissions of %s: %v", r.Path, err)
			keep = append(keep, r)
		}
	}
	return restored, keep
}
