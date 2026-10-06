//go:build !windows

package nvx

import "errors"

// errDotenvChanged is only returned on Windows; see sandbox_dotenv_windows.go.
var errDotenvChanged = errors.New("its permissions were changed after nvx protected it")

// restoreProtectedDotenv has nothing to put back off Windows: Linux hides
// dotenv files with mounts that end with the run, and nothing is recorded.
func restoreProtectedDotenv(root string, r protectedDotenv) error { return errNothingToWithdraw }
