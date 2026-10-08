//go:build !windows

package nvx

import "testing"

// isolatePackageSweep is for the Windows package sweep. There is nothing to
// isolate elsewhere.
func isolatePackageSweep(t *testing.T) { t.Helper() }
