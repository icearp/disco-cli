package snapshot

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

func testManifest() Manifest {
	return Manifest{
		Format:      FormatV1,
		ToolVersion: "v1.2.3",
		GeneratedAt: "2026-06-17T00:00:00Z",
		DBSHA256:    "deadbeef",
		Scans: []ScanRef{
			{ID: "abc123", StartedAt: "2026-06-16T00:00:00Z", Scope: map[string]any{"regions": []any{"us-east-1"}}},
		},
	}
}

// writeFile writes b to a temp file and returns its path.
func writeFile(t *testing.T, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// TestCanonicalManifestBytes_Deterministic pins that the signing payload is
// stable across calls — a non-deterministic payload would make every signature
// fail to re-verify against a re-derived manifest.
func TestCanonicalManifestBytes_Deterministic(t *testing.T) {
	m := testManifest()
	a, err := CanonicalManifestBytes(m)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	b, err := CanonicalManifestBytes(m)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	if string(a) != string(b) {
		t.Errorf("canonical bytes not deterministic:\n%s\n%s", a, b)
	}
}

// TestVerifyDetachedSignature_RoundTrip is the core security contract: a
// signature over the canonical manifest bytes verifies with the matching key.
// Exercised across all accepted public-key encodings.
func TestVerifyDetachedSignature_RoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	m := testManifest()
	payload, err := CanonicalManifestBytes(m)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	sigPath := writeFile(t, "sig.bin", ed25519.Sign(priv, payload))

	t.Run("raw32", func(t *testing.T) {
		keyPath := writeFile(t, "raw.key", pub)
		if err := VerifyDetachedSignature(m, sigPath, keyPath); err != nil {
			t.Errorf("verify raw key: %v", err)
		}
	})

	t.Run("pem-pkix", func(t *testing.T) {
		der, err := x509.MarshalPKIXPublicKey(pub)
		if err != nil {
			t.Fatalf("marshal pkix: %v", err)
		}
		pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
		keyPath := writeFile(t, "key.pem", pemBytes)
		if err := VerifyDetachedSignature(m, sigPath, keyPath); err != nil {
			t.Errorf("verify pem key: %v", err)
		}
	})

	t.Run("openssh", func(t *testing.T) {
		sshPub, err := ssh.NewPublicKey(pub)
		if err != nil {
			t.Fatalf("ssh pubkey: %v", err)
		}
		keyPath := writeFile(t, "key.pub", ssh.MarshalAuthorizedKey(sshPub))
		if err := VerifyDetachedSignature(m, sigPath, keyPath); err != nil {
			t.Errorf("verify openssh key: %v", err)
		}
	})
}

// TestVerifyDetachedSignature_Tamper pins that mutating the manifest after
// signing breaks verification — the whole point of the signature.
func TestVerifyDetachedSignature_Tamper(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	m := testManifest()
	payload, _ := CanonicalManifestBytes(m)
	sigPath := writeFile(t, "sig.bin", ed25519.Sign(priv, payload))
	keyPath := writeFile(t, "raw.key", pub)

	tampered := m
	tampered.DBSHA256 = "0000beef" // attacker swaps the frozen-DB hash
	if err := VerifyDetachedSignature(tampered, sigPath, keyPath); err == nil {
		t.Error("verify accepted a tampered manifest")
	}
}

// TestVerifyDetachedSignature_WrongSignature pins rejection of a valid-length
// but incorrect signature (e.g. signed by a different key).
func TestVerifyDetachedSignature_WrongSignature(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	m := testManifest()
	payload, _ := CanonicalManifestBytes(m)
	sigPath := writeFile(t, "sig.bin", ed25519.Sign(otherPriv, payload))
	keyPath := writeFile(t, "raw.key", pub)

	if err := VerifyDetachedSignature(m, sigPath, keyPath); err == nil {
		t.Error("verify accepted a signature from the wrong key")
	}
}

// TestLoadEd25519PublicKey_RejectsNonEd25519 pins that a well-formed PEM key of
// the wrong algorithm is rejected rather than silently mis-typed.
func TestLoadEd25519PublicKey_RejectsNonEd25519(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa keygen: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal pkix: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	keyPath := writeFile(t, "rsa.pem", pemBytes)

	if _, err := LoadEd25519PublicKey(keyPath); err == nil {
		t.Error("loaded an RSA key as ed25519")
	}
}

// TestLoadEd25519PublicKey_RejectsGarbage pins the catch-all error path for
// input that is neither PEM, OpenSSH, nor a raw 32-byte key.
func TestLoadEd25519PublicKey_RejectsGarbage(t *testing.T) {
	keyPath := writeFile(t, "junk.bin", []byte("not a key"))
	if _, err := LoadEd25519PublicKey(keyPath); err == nil {
		t.Error("loaded garbage as a key")
	}
}
