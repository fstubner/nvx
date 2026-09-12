//go:build !windows

package nvx

// AppContainer packages exist only on Windows. A Landlock or Seatbelt sandbox
// registers nothing with the OS that needs reclaiming, so there is nothing here
// to sweep and nothing to record.
func sweepOrphanedSandboxPackages(nvxHome string, budget int) int { return 0 }

func noteSandboxPackageUse(nvxHome, pkgName string) {}
