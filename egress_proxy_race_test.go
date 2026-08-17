package main

import (
	"sync"
	"testing"
)

// TestEgressProxyAllowedIsRaceFreeAgainstSessionWrites covers F3: allowed() read
// p.session without holding promptMu, while the prompt path wrote it under the
// lock. Both run on per-connection goroutines, so ordinary parallel npm traffic in
// the default proxy mode could hit a concurrent map read/write -- which is a fatal
// Go runtime error, not a recoverable one.
//
// The writer here takes promptMu and writes session exactly as the prompt path
// does, rather than going through PromptYesNo, which would need a TTY. That models
// the real interleaving: one connection being approved while others are checked.
//
// Run under -race. Before the fix this reports a DATA RACE at the session read.
func TestEgressProxyAllowedIsRaceFreeAgainstSessionWrites(t *testing.T) {
	p := &EgressProxy{
		allow:    map[string]bool{},
		session:  map[string]bool{},
		prompted: map[string]bool{},
		nvxHome:  t.TempDir(),
	}
	// proxy mode with prompting off: allowed() reaches the session lookup and then
	// returns without needing a terminal.
	p.policy.Isolation.Network.Mode = "proxy"
	p.policy.Isolation.Network.PromptUnknown = false

	const goroutines = 8
	const iterations = 200

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = p.allowed(hostPort{host: "registry.npmjs.org", port: 443})
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < iterations; j++ {
			p.promptMu.Lock()
			p.session["registry.npmjs.org:443"] = true
			delete(p.session, "registry.npmjs.org:443")
			p.promptMu.Unlock()
		}
	}()

	wg.Wait()
}
