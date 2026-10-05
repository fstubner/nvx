//go:build !windows

package nvx

import "testing"

// assumeProtectedProfile is a no-op off Windows, where doctor has no profile
// protection check.
func assumeProtectedProfile(t *testing.T) { t.Helper() }
