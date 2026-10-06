package nvx

import (
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
