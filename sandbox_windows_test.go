//go:build windows

package main

import "testing"

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
