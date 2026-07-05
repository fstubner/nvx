package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

// TestVerifyNpmSignatures exercises the real ECDSA verification path with a
// synthetic registry key + signature (the live registry is not reachable from
// the test sandbox, but the crypto is what matters).
func TestVerifyNpmSignatures(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys := []npmSigningKey{{KeyID: "SHA256:test", Pub: &key.PublicKey}}

	name, version, integrity := "left-pad", "1.3.0", "sha512-abc123=="
	msg := name + "@" + version + ":" + integrity
	digest := sha256.Sum256([]byte(msg))

	sigDER, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sigB64 := base64.StdEncoding.EncodeToString(sigDER)

	// 1. Valid signature -> verified.
	distValid := NpmDist{
		Integrity:  integrity,
		Signatures: []NpmSignature{{KeyID: "SHA256:test", Sig: sigB64}},
	}
	if res := verifyNpmSignatures(name, version, distValid, keys); res != provenanceVerified {
		t.Errorf("valid signature: got %v, want verified", res)
	}

	// 2. Tampered integrity (signature no longer matches) -> invalid.
	distTampered := NpmDist{
		Integrity:  "sha512-TAMPERED==",
		Signatures: []NpmSignature{{KeyID: "SHA256:test", Sig: sigB64}},
	}
	if res := verifyNpmSignatures(name, version, distTampered, keys); res != provenanceInvalid {
		t.Errorf("tampered integrity: got %v, want invalid", res)
	}

	// 3. Unknown key id (attacker-supplied signature, key we don't trust) -> invalid.
	distUnknownKey := NpmDist{
		Integrity:  integrity,
		Signatures: []NpmSignature{{KeyID: "SHA256:attacker", Sig: sigB64}},
	}
	if res := verifyNpmSignatures(name, version, distUnknownKey, keys); res != provenanceInvalid {
		t.Errorf("unknown key id: got %v, want invalid", res)
	}

	// 4. No signatures published -> unsigned (caller decides downgrade policy).
	distUnsigned := NpmDist{Integrity: integrity}
	if res := verifyNpmSignatures(name, version, distUnsigned, keys); res != provenanceUnsigned {
		t.Errorf("no signatures: got %v, want unsigned", res)
	}
}
