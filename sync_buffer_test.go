package main

import (
	"strings"
	"sync"
)

// syncBuffer collects a child process's output for a test that also reads it
// while the child is still running.
//
// os/exec writes cmd.Stdout and cmd.Stderr from its own copier goroutines, so a
// plain strings.Builder handed to both and polled from the test goroutine is a
// data race on every read. It is invisible without -race and harmless-looking
// with it, because the assertion usually still passes -- the test fails on the
// race report rather than on the thing it checks.
//
// That combination hid one for a while: CI runs -race but not the probe suite,
// and the probe suite ran without -race, so the two were never combined and no
// gate could see it. Both now use -race; this is the type that lets those probes
// pass under it.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
