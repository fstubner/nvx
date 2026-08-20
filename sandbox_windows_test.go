//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
	"unsafe"
)

// A self-updated npm lands in the version's npm_global prefix, whose directory
// has no sibling node.exe. The rewrite must still produce a direct node.exe
// call: falling back to launching npm.cmd itself means cmd.exe runs the batch
// wrapper, whose `IF EXIST "%dp0%\node.exe"` fails and degrades _prog to a bare
// `node` that is not on PATH inside the container -- surfacing as
// '"node"' is not recognized.
func TestRewriteWindowsNodeCommandUsesFallbackNodeExe(t *testing.T) {
	versionDir := tempDir(t)
	nodeExe := filepath.Join(versionDir, "node.exe")
	if err := os.WriteFile(nodeExe, []byte("stub"), 0o600); err != nil {
		t.Fatal(err)
	}

	// npm_global/npm.cmd with its own npm-cli.js, and deliberately no node.exe.
	globalDir := filepath.Join(versionDir, "npm_global")
	cliPath := filepath.Join(globalDir, "node_modules", "npm", "bin", "npm-cli.js")
	if err := os.MkdirAll(filepath.Dir(cliPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cliPath, []byte("stub"), 0o600); err != nil {
		t.Fatal(err)
	}
	npmCmd := filepath.Join(globalDir, "npm.cmd")
	if err := os.WriteFile(npmCmd, []byte("@echo off"), 0o600); err != nil {
		t.Fatal(err)
	}

	gotPath, gotArgs := rewriteWindowsNodeCommand(npmCmd, []string{"-v"}, nodeExe)

	if gotPath != nodeExe {
		t.Errorf("expected launch via fallback node.exe %q, got %q", nodeExe, gotPath)
	}
	if !strings.HasSuffix(gotPath, ".exe") {
		t.Errorf("expected an .exe launch target, got %q (a batch file would hit the cmd.exe wrapper)", gotPath)
	}
	joined := strings.Join(gotArgs, " ")
	// The self-updated npm's own CLI must still be the one executed.
	if !strings.Contains(joined, cliPath) {
		t.Errorf("expected args to reference the npm_global npm-cli.js %q, got %v", cliPath, gotArgs)
	}
	if !strings.Contains(joined, "-v") {
		t.Errorf("expected caller args preserved, got %v", gotArgs)
	}
}

// Nested node processes (npm scripts spawn them constantly) never see the
// command-line preserve-symlinks flags, so node's entry-point resolution
// realpaths up to the drive root -- which an AppContainer cannot stat unless
// that volume's root was granted. NODE_OPTIONS carries the flags to children.
func TestSetNodeOptionsPreserveSymlinks(t *testing.T) {
	t.Run("adds when absent", func(t *testing.T) {
		got := setNodeOptionsPreserveSymlinks([]string{"PATH=C:\\x"})
		var found string
		for _, e := range got {
			if strings.HasPrefix(e, "NODE_OPTIONS=") {
				found = e
			}
		}
		if !strings.Contains(found, "--preserve-symlinks-main") ||
			!strings.Contains(found, "--preserve-symlinks") {
			t.Errorf("expected both preserve-symlinks flags, got %q", found)
		}
	})

	t.Run("appends to existing value", func(t *testing.T) {
		got := setNodeOptionsPreserveSymlinks([]string{"NODE_OPTIONS=--max-old-space-size=256"})
		if len(got) != 1 {
			t.Fatalf("expected no new entry, got %v", got)
		}
		if !strings.Contains(got[0], "--max-old-space-size=256") {
			t.Errorf("expected existing options preserved, got %q", got[0])
		}
		if !strings.Contains(got[0], "--preserve-symlinks") {
			t.Errorf("expected flags appended, got %q", got[0])
		}
	})

	t.Run("does not duplicate", func(t *testing.T) {
		got := setNodeOptionsPreserveSymlinks([]string{"NODE_OPTIONS=--preserve-symlinks"})
		if n := strings.Count(got[0], "--preserve-symlinks"); n != 1 {
			t.Errorf("expected no duplication, got %q", got[0])
		}
	})
}

// A project on a non-system drive makes tools resolve paths up to that volume's
// root; without a grant there the stat fails as a bare EPERM on e.g. "H:\".
// Setup must therefore cover every fixed drive root, not just the system one.
func TestWindowsAncestorGrantPathsCoversFixedDrives(t *testing.T) {
	paths := windowsAncestorGrantPaths()

	have := map[string]bool{}
	for _, p := range paths {
		have[strings.ToUpper(p)] = true
	}

	roots := fixedDriveRoots()
	if len(roots) == 0 {
		t.Skip("no fixed drives reported")
	}
	for _, r := range roots {
		if !have[strings.ToUpper(r)] {
			t.Errorf("fixed drive root %q missing from grant paths %v", r, paths)
		}
	}

	// Deduplication must hold even though the system drive is added twice.
	seen := map[string]int{}
	for _, p := range paths {
		seen[strings.ToUpper(p)]++
	}
	for p, n := range seen {
		if n > 1 {
			t.Errorf("path %q listed %d times; expected deduplication", p, n)
		}
	}
}

func TestFixedDriveRootsIncludesSystemDrive(t *testing.T) {
	sysDrive := os.Getenv("SystemDrive")
	if sysDrive == "" {
		t.Skip("SystemDrive not set")
	}
	want := strings.ToUpper(sysDrive + `\`)
	for _, r := range fixedDriveRoots() {
		if strings.ToUpper(r) == want {
			return
		}
	}
	t.Errorf("expected fixedDriveRoots() to include the system drive %q", want)
}

// When node.exe does sit beside npm.cmd (the bundled layout), that one wins and
// the fallback is not needed.
func TestRewriteWindowsNodeCommandPrefersSiblingNodeExe(t *testing.T) {
	dir := tempDir(t)
	sibling := filepath.Join(dir, "node.exe")
	if err := os.WriteFile(sibling, []byte("stub"), 0o600); err != nil {
		t.Fatal(err)
	}
	cliPath := filepath.Join(dir, "node_modules", "npm", "bin", "npm-cli.js")
	if err := os.MkdirAll(filepath.Dir(cliPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cliPath, []byte("stub"), 0o600); err != nil {
		t.Fatal(err)
	}
	npmCmd := filepath.Join(dir, "npm.cmd")
	if err := os.WriteFile(npmCmd, []byte("@echo off"), 0o600); err != nil {
		t.Fatal(err)
	}

	gotPath, _ := rewriteWindowsNodeCommand(npmCmd, nil, filepath.Join("C:\\", "unused", "node.exe"))
	if gotPath != sibling {
		t.Errorf("expected sibling node.exe %q, got %q", sibling, gotPath)
	}
}

func TestBuildWindowsCommandLine(t *testing.T) {
	got := buildWindowsCommandLine(`C:\Program Files\node\node.exe`, []string{"-e", "console.log(\"hi\")"})
	want := `"C:\Program Files\node\node.exe" -e "console.log(\"hi\")"`
	if got != want {
		t.Fatalf("buildWindowsCommandLine() = %q, want %q", got, want)
	}
}

func TestQuoteWindowsArg(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"plain", "plain"},
		{"", `""`},
		{"has space", `"has space"`},
		{`say "hi"`, `"say \"hi\""`},
		{`C:\Program Files\foo\`, `"C:\Program Files\foo\\"`},
	}
	for _, tc := range cases {
		if got := quoteWindowsArg(tc.in); got != tc.want {
			t.Errorf("quoteWindowsArg(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBuildWindowsEnvironmentBlock(t *testing.T) {
	block, err := buildWindowsEnvironmentBlock([]string{"FOO=bar", "BAZ=qux"})
	if err != nil {
		t.Fatalf("buildWindowsEnvironmentBlock: %v", err)
	}
	if block == nil {
		t.Fatal("expected non-nil environment block")
	}
}

// TestBuildWindowsEnvironmentBlockHandlesNonBMP covers the bug the previous
// implementation had: it wrote uint16(r) per rune, truncating anything above
// U+FFFF, so an emoji in an environment value silently became a different
// character. It also sized the buffer in bytes while writing per rune. The
// pre-existing test used only ASCII, where neither defect can show.
func TestBuildWindowsEnvironmentBlockHandlesNonBMP(t *testing.T) {
	// U+1F600 needs a surrogate pair; U+00E9 and U+4E2D are multi-byte in UTF-8
	// but single UTF-16 units, which is what exposed the byte-vs-rune sizing.
	entries := []string{"EMOJI=\U0001F600", "ACCENT=café", "CJK=中文"}

	ptr, err := buildWindowsEnvironmentBlock(entries)
	if err != nil {
		t.Fatalf("buildWindowsEnvironmentBlock: %v", err)
	}
	if ptr == nil {
		t.Fatal("expected a block")
	}

	// Walk the block back out: NUL-separated strings, double-NUL terminated.
	//
	// The length is computed rather than over-estimated: unsafe.Slice with a bound
	// past the real allocation is a checkptr violation, which aborts the whole
	// binary under -race ("unsafe.Slice result straddles multiple allocations")
	// rather than failing this one test.
	want := 1 // the block's own terminator
	for _, e := range entries {
		want += len(utf16.Encode([]rune(e))) + 1
	}
	var got []string
	units := unsafe.Slice(ptr, want)
	start := 0
	for i := 0; i < len(units); i++ {
		if units[i] != 0 {
			continue
		}
		if i == start { // second NUL in a row: end of block
			break
		}
		got = append(got, string(utf16.Decode(units[start:i])))
		start = i + 1
	}

	if len(got) != len(entries) {
		t.Fatalf("decoded %d entries %q, want %d", len(got), got, len(entries))
	}
	for i := range entries {
		if got[i] != entries[i] {
			t.Errorf("entry %d round-tripped as %q, want %q", i, got[i], entries[i])
		}
	}
}

func TestBuildWindowsEnvironmentBlockRejectsEmbeddedNUL(t *testing.T) {
	// A NUL would terminate the block early and silently drop everything after it.
	if _, err := buildWindowsEnvironmentBlock([]string{"OK=1", "BAD=a\x00b", "LOST=2"}); err == nil {
		t.Error("an entry containing a NUL must be refused, not silently truncate the block")
	}
}
