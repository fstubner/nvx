package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// This module implements REAL npm package provenance verification: the npm
// registry signs "name@version:integrity" with ECDSA P-256, and publishes its
// public keys at /-/npm/v1/keys. nvx fetches those keys and verifies the
// signature attached to each package version — cryptographic proof that the
// registry vouches for exactly that tarball integrity for that name@version.

// URLs are vars (not consts) so tests can point them at a local server.
var (
	npmKeysURL         = "https://registry.npmjs.org/-/npm/v1/keys"
	npmRegistryBaseURL = "https://registry.npmjs.org"
)

type npmSigningKey struct {
	KeyID string
	Pub   *ecdsa.PublicKey
}

// in-process cache of the registry signing keys. Guarded by a mutex and
// memoized once per run — INCLUDING the error, so a transient fetch failure is
// never silently remembered as an empty-but-successful key set (fail-open bug).
var (
	npmKeysMu    sync.Mutex
	npmKeysCache []npmSigningKey
	npmKeysErr   error
	npmKeysDone  bool
)

type npmKeyResponse struct {
	Keys []struct {
		KeyID   string  `json:"keyid"`
		KeyType string  `json:"keytype"`
		Scheme  string  `json:"scheme"`
		Key     string  `json:"key"`     // base64 SPKI (PKIX) DER
		Expires *string `json:"expires"` // RFC3339 or null
	} `json:"keys"`
}

// fetchNpmSigningKeys memoizes the fetch result (keys AND error) exactly once,
// under a mutex. On failure it returns (nil, err) every time — never a
// nil-error empty set — so callers can distinguish "keys unavailable" from
// "registry has no keys".
func fetchNpmSigningKeys() ([]npmSigningKey, error) {
	npmKeysMu.Lock()
	defer npmKeysMu.Unlock()
	if npmKeysDone {
		return npmKeysCache, npmKeysErr
	}
	npmKeysCache, npmKeysErr = doFetchNpmSigningKeys()
	npmKeysDone = true
	return npmKeysCache, npmKeysErr
}

func doFetchNpmSigningKeys() ([]npmSigningKey, error) {
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get(npmKeysURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry keys endpoint returned HTTP %s", resp.Status)
	}

	var kr npmKeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&kr); err != nil {
		return nil, err
	}

	now := time.Now()
	var keys []npmSigningKey
	for _, k := range kr.Keys {
		// Only trust P-256 ECDSA keys (the scheme npm actually uses); reject
		// anything else to prevent algorithm/curve-substitution.
		if k.KeyType != "" && k.KeyType != "ecdsa-sha2-nistp256" {
			continue
		}
		if k.Scheme != "" && k.Scheme != "ecdsa-sha2-nistp256" {
			continue
		}
		// Reject expired keys.
		if k.Expires != nil && *k.Expires != "" {
			if exp, perr := time.Parse(time.RFC3339, *k.Expires); perr == nil && now.After(exp) {
				continue
			}
		}
		der, err := base64.StdEncoding.DecodeString(k.Key)
		if err != nil {
			continue
		}
		pubAny, err := x509.ParsePKIXPublicKey(der)
		if err != nil {
			continue
		}
		ecPub, ok := pubAny.(*ecdsa.PublicKey)
		if !ok || ecPub.Curve != elliptic.P256() {
			continue
		}
		keys = append(keys, npmSigningKey{KeyID: k.KeyID, Pub: ecPub})
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no usable P-256 ECDSA keys returned by registry")
	}
	return keys, nil
}

// provenanceResult is the outcome of a signature check.
type provenanceResult int

const (
	provenanceVerified provenanceResult = iota // signature present and valid
	provenanceUnsigned                         // no signature published
	provenanceInvalid                          // signature present but did NOT verify (serious)
)

// verifyNpmSignatures checks the registry's ECDSA signature over
// "name@version:integrity" against the supplied signing keys. It is pure (keys
// are injected, not fetched) so it is fully unit-testable.
func verifyNpmSignatures(name, version string, dist NpmDist, keys []npmSigningKey) provenanceResult {
	if len(dist.Signatures) == 0 || dist.Integrity == "" {
		return provenanceUnsigned
	}
	keyByID := map[string]*ecdsa.PublicKey{}
	for _, k := range keys {
		keyByID[k.KeyID] = k.Pub
	}

	message := fmt.Sprintf("%s@%s:%s", name, version, dist.Integrity)
	digest := sha256.Sum256([]byte(message))

	for _, s := range dist.Signatures {
		pub, ok := keyByID[s.KeyID]
		if !ok {
			continue // unknown key id; try the next signature
		}
		sigDER, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s.Sig))
		if err != nil {
			continue
		}
		if ecdsa.VerifyASN1(pub, digest[:], sigDER) {
			return provenanceVerified
		}
	}
	// A signature was published but none verified against a known key.
	return provenanceInvalid
}

// ResolveNpmDist fetches the dist block (integrity + signatures) for a specific
// package version from the registry.
func ResolveNpmDist(pkgName, version string) (NpmDist, error) {
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get(fmt.Sprintf("%s/%s", npmRegistryBaseURL, EscapeScopedPackage(pkgName)))
	if err != nil {
		return NpmDist{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return NpmDist{}, fmt.Errorf("registry returned HTTP %s", resp.Status)
	}
	var meta NpmRegistryMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return NpmDist{}, err
	}
	vd, ok := meta.Versions[version]
	if !ok {
		return NpmDist{}, fmt.Errorf("version %s not found in registry metadata", version)
	}
	return vd.Dist, nil
}

// failClosedDecision returns whether to proceed: false (abort) under
// fail_closed, otherwise true with a degraded-mode warning.
func failClosedDecision(policy Policy, format string, a ...interface{}) bool {
	if policy.FailClosed {
		LogError(format+" Aborting (fail_closed policy).", a...)
		return false
	}
	LogWarn(format+" Proceeding in degraded mode (set \"fail_closed\": true to block).", a...)
	return true
}

// verifyPackageProvenance verifies the registry signature for a resolved package
// version and returns whether the install should proceed. It never calls
// os.Exit — the caller decides. Crucially, it is downgrade-resistant: when the
// registry publishes signing keys but a package carries NO signature, that is
// treated as a tampering/downgrade signal (blocked/prompted), not a passive
// warning — closing the "strip dist.signatures" bypass.
func verifyPackageProvenance(policy Policy, pkgName, version string) bool {
	dist, err := ResolveNpmDist(pkgName, version)
	if err != nil {
		return failClosedDecision(policy, "Could not fetch signing metadata for %s@%s: %v.", pkgName, version, err)
	}

	keys, keyErr := fetchNpmSigningKeys()
	if keyErr != nil || len(keys) == 0 {
		// We cannot verify anything this run — honor fail_closed rather than
		// silently accepting unverified packages.
		return failClosedDecision(policy, "Registry signing keys unavailable; cannot verify %s@%s: %v.", pkgName, version, keyErr)
	}

	// Keys are available → the registry signs packages. A package with no
	// signature is suspicious (npm/corepack treat this as an error).
	if len(dist.Signatures) == 0 || dist.Integrity == "" {
		LogError("No registry signature for %s@%s, but the registry publishes signing keys — possible tampering or a stripped-signature downgrade.", pkgName, version)
		return PromptYesNo("Proceed despite the missing registry signature?")
	}

	switch verifyNpmSignatures(pkgName, version, dist, keys) {
	case provenanceVerified:
		LogSuccess("Registry signature verified for %s@%s.", pkgName, version)
		return true
	case provenanceInvalid:
		LogError("SIGNATURE INVALID for %s@%s — the registry signature did not verify.", pkgName, version)
		return PromptYesNo("The registry signature failed verification (possible tampering). Proceed anyway?")
	default:
		LogError("Signature for %s@%s could not be evaluated.", pkgName, version)
		return PromptYesNo("Proceed anyway?")
	}
}
