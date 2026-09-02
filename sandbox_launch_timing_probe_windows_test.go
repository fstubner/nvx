//go:build windows

package main

// Measurement probe (NVX_PROBE=1) for F76: a contained launch costs seconds, not
// the ~38ms of shim dispatch the README publishes.
//
// This does not assert a budget. It attributes the time to phases so a fix aims
// at the right one -- the acceptance pass reported the total and could not say
// where it went, and guessing at that is how the wrong thing gets optimised.
//
// Each phase is timed twice: once cold (nothing granted yet, as on a project's
// first sandboxed command) and once warm (everything already granted, which is
// what every later run pays and therefore what "steady state" means).

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMeasureContainedLaunchPhases(t *testing.T) {
	if os.Getenv("NVX_PROBE") != "1" {
		t.Skip("set NVX_PROBE=1 to run")
	}

	const probeProfile = "nvx.sandbox.timingprobe"
	nvxHome := tempDir(t)

	// A project under the user profile, which is the layout the ancestor walk
	// actually runs over.
	home := os.Getenv("USERPROFILE")
	project, err := os.MkdirTemp(home, "nvx-timing")
	if err != nil {
		t.Skipf("cannot create a directory under the user profile: %v", err)
	}
	defer os.RemoveAll(project)

	type phase struct {
		name string
		cold time.Duration
		warm time.Duration
	}
	var phases []phase

	measure := func(name string, fn func()) {
		start := time.Now()
		fn()
		cold := time.Since(start)
		start = time.Now()
		fn()
		warm := time.Since(start)
		phases = append(phases, phase{name, cold, warm})
	}

	var sid uintptr
	measure("ensureAppContainerSID", func() {
		s, err := ensureAppContainerSID(probeProfile)
		if err != nil {
			t.Fatalf("profile: %v", err)
		}
		if sid != 0 {
			syscall.LocalFree(syscall.Handle(sid))
		}
		sid = s
	})
	defer syscall.LocalFree(syscall.Handle(sid))
	defer deleteAppContainerProfile(probeProfile)

	guestHome, err := os.MkdirTemp("", "nvxg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(guestHome)

	measure("scopeCapabilitySID", func() {
		if _, err := scopeCapabilitySID(sandboxScopeForWorkDir(project)); err != nil {
			t.Fatalf("capability: %v", err)
		}
	})

	capSID, _ := scopeCapabilitySID(sandboxScopeForWorkDir(project))
	measure("grantSandboxModify(guestHome)", func() {
		if err := grantSandboxModify(capSID, guestHome); err != nil {
			t.Fatalf("grant guest home: %v", err)
		}
	})
	measure("labelLowIntegrity(guestHome)", func() {
		if err := labelLowIntegrity(guestHome); err != nil {
			t.Fatalf("label: %v", err)
		}
	})
	measure("grantSandboxModify(workDir)", func() {
		if err := grantSandboxModify(capSID, project); err != nil {
			t.Fatalf("grant workdir: %v", err)
		}
	})
	measure("grantWorkdirAncestors(workDir)", func() {
		grantWorkdirAncestors("", project)
	})
	measure("grantWorkdirAncestors(guestHome)", func() {
		grantWorkdirAncestors("", guestHome)
	})
	measure("stageAppContainerSupervisor", func() {
		if _, err := stageAppContainerSupervisor(nvxHome); err != nil {
			t.Fatalf("stage supervisor: %v", err)
		}
	})

	// A launch of a trivial contained process, which is the floor: whatever this
	// costs is CreateProcess plus the container, and no grant work at all.
	childExe := stageProbeChild(t, guestHome, "timingprobe.exe")
	measure("launchAppContainerProcess(no-op child)", func() {
		env := append(scrubEnvironment(guestHome), "NVX_TIMING_CHILD=1")
		_, _ = launchAppContainerProcess(childExe,
			[]string{"-test.run=TestMeasureContainedLaunchPhasesNoOpChild"},
			env, project, sid, 0, []string{capSID})
	})

	var b strings.Builder
	b.WriteString("\n  phase                                      cold        warm\n")
	b.WriteString("  ---------------------------------------------------------\n")
	var coldTotal, warmTotal time.Duration
	for _, p := range phases {
		coldTotal += p.cold
		warmTotal += p.warm
		fmt.Fprintf(&b, "  %-40s %7dms %7dms\n", p.name, p.cold.Milliseconds(), p.warm.Milliseconds())
	}
	fmt.Fprintf(&b, "  %-40s %7dms %7dms\n", "TOTAL", coldTotal.Milliseconds(), warmTotal.Milliseconds())
	t.Log(b.String())
}

// TestMeasureContainedLaunchPhasesNoOpChild is the child the launch phase runs.
// It exists so that measurement times the container, not a workload.
func TestMeasureContainedLaunchPhasesNoOpChild(t *testing.T) {
	if os.Getenv("NVX_TIMING_CHILD") != "1" {
		t.Skip("internal child for the launch timing probe")
	}
}
