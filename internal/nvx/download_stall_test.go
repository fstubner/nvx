package nvx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A download that stops delivering bytes is abandoned after one stall period,
// not after a whole-request timeout that a slow connection also trips.
//
// The client had Timeout: 60s, which covers the whole request including the
// body. A 25-60 MB runtime archive takes longer than that below roughly 4-8
// Mbps, so `nvx install` failed on a slow connection that was making steady
// progress. The right question is not "has this taken long" but "has this
// stopped", and that is what is asked now: a stall period with no bytes at all.
//
// This is the observable half: a server that sends some of the body and then
// goes silent. Before the fix the download sat in the whole-request timeout
// for the full minute; the test bounds it at a few seconds.
func TestAStalledDownloadIsAbandonedAfterTheStallPeriod(t *testing.T) {
	orig := downloadStallTimeout
	downloadStallTimeout = 300 * time.Millisecond
	t.Cleanup(func() { downloadStallTimeout = orig })

	hang := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(strings.Repeat("x", 4096)))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-hang // and then nothing, for ever
	}))
	// Cleanups run last-in-first-out: the handler must be released BEFORE the
	// server is closed, or Close waits on the hung handler for ever. The first
	// version of this test registered them the other way round and hung at
	// cleanup after the assertion had passed.
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(hang) })

	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- DownloadFile(srv.URL+"/archive.tar.gz", filepath.Join(tempDir(t), "archive.tar.gz")) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a download whose body stopped after 4 KiB of a 1 MB archive reported success")
		}
		if !strings.Contains(err.Error(), "stalled") {
			t.Fatalf("the download failed, but not as a stall: %v", err)
		}
		if el := time.Since(start); el > 3*time.Second {
			t.Fatalf("the stall was detected after %v; the stall period is %v", el, downloadStallTimeout)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the download is still waiting 5s after its body stalled; the whole-request timeout would fire at 60s")
	}
}

// The complementary half: bytes that keep arriving keep the download alive
// past the stall period, so a slow steady connection is never cut off. Total
// duration well past the stall period, gaps well inside it.
//
// The gaps are a twentieth of the stall period. They were half of it, so one
// late write failed a healthy download. Measured 2026-10-07 by holding one chunk
// back: an extra 150ms failed the old numbers, and these pass with 1500ms extra
// and fail at 2100ms. The download still outlasts the stall period by half
// again, so a timeout on the whole request, set to the stall period, would still
// cut it off every time.
func TestASlowButSteadyDownloadIsNotCutOff(t *testing.T) {
	orig := downloadStallTimeout
	downloadStallTimeout = 2 * time.Second
	t.Cleanup(func() { downloadStallTimeout = orig })

	const chunks = 30 // 30 x 100ms = 3s, one and a half stall periods
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "30720")
		w.WriteHeader(200)
		f, _ := w.(http.Flusher)
		for i := 0; i < chunks; i++ {
			_, _ = w.Write([]byte(strings.Repeat("y", 1024)))
			if f != nil {
				f.Flush()
			}
			time.Sleep(100 * time.Millisecond)
		}
	}))
	t.Cleanup(srv.Close)

	dest := filepath.Join(tempDir(t), "steady.bin")
	if err := DownloadFile(srv.URL+"/steady.bin", dest); err != nil {
		t.Fatalf("a slow but steady download was cut off: %v", err)
	}
}

// slowBody answers each Read after its delay, with one byte, and then EOF.
type slowBody struct{ delays []time.Duration }

func (b *slowBody) Read(p []byte) (int, error) {
	if len(b.delays) == 0 {
		return 0, io.EOF
	}
	time.Sleep(b.delays[0])
	b.delays = b.delays[1:]
	p[0] = 'x'
	return 1, nil
}

// A Read that returns data after the timer fired re-armed it, and the second
// fire closed an already closed channel. That panic is in the timer's own
// goroutine, so it takes the whole process down.
func TestStallTimerFiringTwiceDoesNotPanic(t *testing.T) {
	const stall = 50 * time.Millisecond
	s := newStallReader(context.Background(), func() {}, &slowBody{delays: []time.Duration{3 * stall, 3 * stall}}, stall)
	defer s.Stop()
	if _, err := io.Copy(io.Discard, s); err != errDownloadStalled {
		t.Errorf("err = %v, want the stall error", err)
	}
}

// Time the caller spends between Reads, writing to a slow disk say, is not a
// network stall. The clock ran between Reads and reported one.
func TestASlowConsumerIsNotAStall(t *testing.T) {
	const stall = 50 * time.Millisecond
	var cancelled atomic.Bool
	s := newStallReader(context.Background(), func() { cancelled.Store(true) }, &slowBody{delays: []time.Duration{0, 0, 0}}, stall)
	defer s.Stop()
	buf := make([]byte, 1)
	for {
		_, err := s.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		time.Sleep(3 * stall)
	}
	if cancelled.Load() {
		t.Error("a body that answered every Read at once was cancelled as stalled")
	}
}

// And the client no longer carries a whole-request timeout at all: every
// bound it has is on a step that can hang, never on how long the body takes.
func TestTheDownloadClientHasNoWholeRequestTimeout(t *testing.T) {
	c := newDownloadClient()
	if c.Timeout != 0 {
		t.Fatalf("the download client has a whole-request Timeout of %v; a large archive on a slow link fails on it", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatal("the download client has no transport of its own to bound connecting and headers on")
	}
	if tr.ResponseHeaderTimeout == 0 || tr.TLSHandshakeTimeout == 0 {
		t.Fatalf("the transport leaves a hangable step unbounded: header=%v tls=%v", tr.ResponseHeaderTimeout, tr.TLSHandshakeTimeout)
	}
}
