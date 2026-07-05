package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// provenanceServer serves the npm keys endpoint and a package-metadata endpoint.
// signed controls whether the served version carries a valid signature; if
// tamper is set, a signature is present but won't verify.
func provenanceServer(t *testing.T, key *ecdsa.PublicKey, sigB64 string, signed, tamper bool) *httptest.Server {
	t.Helper()
	spki, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyB64 := base64.StdEncoding.EncodeToString(spki)

	mux := http.NewServeMux()
	mux.HandleFunc("/-/npm/v1/keys", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"keys":[{"keyid":"SHA256:test","keytype":"ecdsa-sha2-nistp256","scheme":"ecdsa-sha2-nistp256","key":%q,"expires":null}]}`, keyB64)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		dist := map[string]any{"integrity": "sha512-abc", "tarball": "http://x/t.tgz"}
		if signed {
			sig := sigB64
			if tamper {
				sig = base64.StdEncoding.EncodeToString([]byte("not-a-valid-sig"))
			}
			dist["signatures"] = []map[string]string{{"keyid": "SHA256:test", "sig": sig}}
		}
		meta := map[string]any{
			"dist-tags": map[string]string{"latest": "1.0.0"},
			"versions":  map[string]any{"1.0.0": map[string]any{"dist": dist}},
		}
		_ = json.NewEncoder(w).Encode(meta)
	})
	return httptest.NewServer(mux)
}

func resetKeyCache() {
	npmKeysMu.Lock()
	npmKeysDone, npmKeysCache, npmKeysErr = false, nil, nil
	npmKeysMu.Unlock()
}

func TestVerifyPackageProvenanceEndToEnd(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// A valid signature over the exact message nvx verifies.
	digest := sha256.Sum256([]byte("pkg@1.0.0:sha512-abc"))
	sigDER, _ := ecdsa.SignASN1(rand.Reader, key, digest[:])
	sigB64 := base64.StdEncoding.EncodeToString(sigDER)

	withServer := func(srv *httptest.Server) func() {
		ok, ob, or := npmKeysURL, npmRegistryBaseURL, npmKeysURL
		_ = or
		npmKeysURL = srv.URL + "/-/npm/v1/keys"
		npmRegistryBaseURL = srv.URL
		resetKeyCache()
		return func() { npmKeysURL, npmRegistryBaseURL = ok, ob; srv.Close(); resetKeyCache() }
	}

	// 1. Valid signature -> proceed.
	srv := provenanceServer(t, &key.PublicKey, sigB64, true, false)
	done := withServer(srv)
	if !verifyPackageProvenance(DefaultPolicy(), "pkg", "1.0.0") {
		t.Error("valid signature should allow the install to proceed")
	}
	done()

	// 2. Stripped signature while the registry publishes keys -> the C1 downgrade
	// must be BLOCKED (PromptYesNo returns false with no TTY in tests).
	srv = provenanceServer(t, &key.PublicKey, sigB64, false, false)
	done = withServer(srv)
	if verifyPackageProvenance(DefaultPolicy(), "pkg", "1.0.0") {
		t.Error("missing signature (downgrade) should be blocked")
	}
	done()

	// 3. Invalid signature -> blocked.
	srv = provenanceServer(t, &key.PublicKey, sigB64, true, true)
	done = withServer(srv)
	if verifyPackageProvenance(DefaultPolicy(), "pkg", "1.0.0") {
		t.Error("invalid signature should be blocked")
	}
	done()
}

func TestVerifyPackageProvenanceKeysUnavailable(t *testing.T) {
	// Keys endpoint 500s -> cannot verify. fail_closed decides.
	mux := http.NewServeMux()
	mux.HandleFunc("/-/npm/v1/keys", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "down", 500) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"dist-tags":{"latest":"1.0.0"},"versions":{"1.0.0":{"dist":{"integrity":"sha512-abc"}}}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ok, ob := npmKeysURL, npmRegistryBaseURL
	npmKeysURL, npmRegistryBaseURL = srv.URL+"/-/npm/v1/keys", srv.URL
	defer func() { npmKeysURL, npmRegistryBaseURL = ok, ob }()

	strict := DefaultPolicy()
	strict.FailClosed = true
	resetKeyCache()
	if verifyPackageProvenance(strict, "pkg", "1.0.0") {
		t.Error("fail_closed + keys unavailable should block")
	}

	lax := DefaultPolicy() // FailClosed=false
	resetKeyCache()
	if !verifyPackageProvenance(lax, "pkg", "1.0.0") {
		t.Error("default (degraded) + keys unavailable should proceed with a warning")
	}
}
