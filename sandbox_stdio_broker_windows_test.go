//go:build windows

package main

import (
	"strings"
	"syscall"
	"testing"
	"time"
)

// The pump is the whole reason two pipes exist instead of one, so it is the part
// worth pinning: bytes written to the child end must come out of the node end,
// and the node end must reach EOF when the writer goes away. Without the second
// property a contained `spawn` would stream correctly and then hang at the end,
// which is the current bug with extra steps.
func TestStdioChannelCarriesBytesAndThenEnds(t *testing.T) {
	sddl := "D:(A;;GA;;;WD)" // this test's endpoints are both the current user
	ch, err := newStdioChannel("brokertest", 0, sddl, false)
	if err != nil {
		t.Skipf("cannot create pipes on this host: %v", err)
	}
	broker := &stdioBroker{channels: []*stdioChannel{ch}}
	defer broker.Close()

	go ch.pump()

	// The grandchild's end: opened the way contained code opens it.
	childEnd, err := openNamedPipe(ch.childPipe)
	if err != nil {
		t.Fatalf("could not open the child end: %v", err)
	}
	// The node end, opened by the same contained process to read from.
	nodeEnd, err := openNamedPipe(ch.nodePipe)
	if err != nil {
		syscall.CloseHandle(childEnd)
		t.Fatalf("could not open the node end: %v", err)
	}
	defer syscall.CloseHandle(nodeEnd)

	const msg = "line one\nline two\n"
	var wrote uint32
	if err := syscall.WriteFile(childEnd, []byte(msg), &wrote, nil); err != nil {
		syscall.CloseHandle(childEnd)
		t.Fatalf("write: %v", err)
	}

	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 64)
		var n uint32
		if err := syscall.ReadFile(nodeEnd, buf, &n, nil); err != nil {
			got <- "READ-FAILED: " + err.Error()
			return
		}
		got <- string(buf[:n])
	}()

	select {
	case v := <-got:
		if !strings.Contains(v, "line one") {
			t.Fatalf("the node end did not receive what the child wrote: %q", v)
		}
	case <-time.After(10 * time.Second):
		syscall.CloseHandle(childEnd)
		t.Fatal("nothing reached the node end; the pump is not carrying bytes")
	}

	// The child exiting must end the stream, not leave the reader waiting.
	syscall.CloseHandle(childEnd)

	ended := make(chan bool, 1)
	go func() {
		buf := make([]byte, 64)
		var n uint32
		err := syscall.ReadFile(nodeEnd, buf, &n, nil)
		ended <- err != nil || n == 0
	}()
	select {
	case ok := <-ended:
		if !ok {
			t.Error("the node end kept returning data after the writer closed")
		}
	case <-time.After(10 * time.Second):
		t.Error("the node end never reached EOF after the writer closed; a contained spawn would " +
			"stream correctly and then hang at the end")
	}
}

// A pool that cannot be provisioned must leave the caller able to run the
// command, because the fallback is the existing limitation and not an error.
func TestStdioChannelsAreOptional(t *testing.T) {
	if broker, names, _ := provisionStdioChannels("", "sess"); broker != nil || names != "" {
		t.Error("provisioning without a container SID should yield nothing to pass on")
	}
	// Nil is a valid broker: the launch path holds one whether or not it worked.
	var nilBroker *stdioBroker
	nilBroker.Close()

	if got := addStdioChannelsEnv([]string{"A=1"}, ""); len(got) != 1 {
		t.Errorf("no channels must add no environment entry, got %v", got)
	}
}

// The names go into a Windows pipe path, so they cannot carry whatever a session
// id happens to contain.
func TestStdioSessionIDIsSafeInAPipeName(t *testing.T) {
	for _, in := range []string{"AbC123", "with-dashes", "with/slash\\and:colon", ""} {
		got := stdioSessionID(in)
		if got == "" {
			t.Errorf("stdioSessionID(%q) produced an empty id", in)
		}
		if strings.ContainsAny(got, `\/:|<>*?"`) {
			t.Errorf("stdioSessionID(%q) = %q, which is not safe in a pipe name", in, got)
		}
	}
}
