package main

import (
	"context"
	"io"
	"net/http"
	"time"
)

// downloadStallTimeout is how long a download may go without delivering any
// bytes before it is abandoned. A variable so a test can shorten it.
//
// This replaces a single 60-second http.Client.Timeout, which covered the
// whole request INCLUDING the body read. A 25-60 MB runtime archive takes
// longer than that below roughly 4-8 Mbps, so on a slow or shared connection
// `nvx install` failed with "context deadline exceeded" partway through a
// download that was making steady progress. What a timeout is for is a
// connection that has stopped, not one that is slow; this one fires only when
// nothing has arrived for the whole period.
var downloadStallTimeout = 60 * time.Second

// newDownloadClient builds the HTTP client for runtime downloads: bounded at
// every step that can hang -- connecting, the TLS handshake, waiting for the
// response headers -- and unbounded on the body, which stallReader watches
// instead.
//
// A clone of the default transport rather than a new one with its own dialer:
// the default already dials with a 30-second timeout, and every explicit dial
// in this package is a named choke point (see egress_dial_choke_point_test.go).
// A runtime download is nvx fetching a release on its own behalf, not egress
// judged for a sandbox, and it has no business adding a dial site of its own.
func newDownloadClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSHandshakeTimeout = 30 * time.Second
	tr.ResponseHeaderTimeout = 60 * time.Second
	return &http.Client{Transport: tr}
}

// stallReader wraps a response body and cancels the request when no bytes
// arrive for stall. Every successful Read resets the clock, so a slow steady
// download runs to completion and a dead one is cut off in one stall period.
type stallReader struct {
	r      io.Reader
	timer  *time.Timer
	stall  time.Duration
	cancel context.CancelFunc
	fired  chan struct{}
}

func newStallReader(ctx context.Context, cancel context.CancelFunc, r io.Reader, stall time.Duration) *stallReader {
	s := &stallReader{r: r, stall: stall, cancel: cancel, fired: make(chan struct{})}
	s.timer = time.AfterFunc(stall, func() {
		close(s.fired)
		cancel()
	})
	_ = ctx
	return s
}

func (s *stallReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		s.timer.Reset(s.stall)
	}
	if err != nil {
		select {
		case <-s.fired:
			// The context was cancelled by the stall timer, not by anything the
			// caller did; say what actually happened.
			return n, errDownloadStalled
		default:
		}
	}
	return n, err
}

// Stop releases the timer once the body has been fully read.
func (s *stallReader) Stop() { s.timer.Stop() }

type stalledError struct{}

func (stalledError) Error() string {
	return "download stalled: no data received for " + downloadStallTimeout.String()
}

var errDownloadStalled error = stalledError{}
