// Command nvx is the entry point; everything it does lives in internal/nvx.
//
// The package was one flat directory of 388 files at the repository root, which
// compiled fine and told a reader nothing. Splitting the entry point out is what
// lets the rest move under internal/, where `internal` also stops anything
// outside this module importing it -- nvx has no library surface and should not
// acquire one by accident.
package main

import "github.com/fstubner/nvx/internal/nvx"

func main() {
	nvx.Main()
}
