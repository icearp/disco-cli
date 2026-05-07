package snapshot

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
)

// CanonicalManifestBytes returns the deterministic byte form of m used as
// the signing payload for a disco-snapshot. The shape is the JCS-style
// (RFC 8785) canonical JSON: compact (no whitespace), struct fields in
// declaration order. encoding/json preserves struct field order, and the
// Manifest struct fields are all simple types (strings, []string), so a
// plain Marshal call is canonical without further normalisation.
//
// The byte sequence is what `disco snapshot --signing-payload` writes and
// what `disco verify --signature ...` re-derives from the embedded
// manifest.json before checking the ed25519 signature.
func CanonicalManifestBytes(m Manifest) ([]byte, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("canonicalise manifest: %w", err)
	}
	return b, nil
}

// VerifyDetachedSignature loads the ed25519 public key at pubKeyPath, the
// signature at sigPath, and validates that the canonical bytes of m carry
// the signature. Returns nil on a verified match.
func VerifyDetachedSignature(m Manifest, sigPath, pubKeyPath string) error {
	payload, err := CanonicalManifestBytes(m)
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		return fmt.Errorf("read signature: %w", err)
	}
	pub, err := LoadEd25519PublicKey(pubKeyPath)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, payload, sig) {
		return fmt.Errorf("ed25519 signature does not verify against canonical manifest")
	}
	return nil
}

// LoadEd25519PublicKey reads an ed25519 public key from path. Accepts:
//   - PEM-wrapped X.509/PKIX SubjectPublicKeyInfo (the format `openssl
//     genpkey -algorithm ed25519` produces with `-pubout`).
//   - Raw 32-byte key (binary).
//
// OpenSSH `ssh-ed25519 AAAAC3...` text format is intentionally out of scope
// — convert with `ssh-keygen -e -m PKCS8 ...` first. Keeping the parser
// stdlib-only avoids pulling in `golang.org/x/crypto` for OSS.
func LoadEd25519PublicKey(path string) (ed25519.PublicKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read pubkey: %w", err)
	}
	if block, _ := pem.Decode(b); block != nil {
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse pem pubkey: %w", err)
		}
		ed, ok := key.(ed25519.PublicKey)
		if !ok {
			return nil, fmt.Errorf("pubkey is not ed25519 (got %T)", key)
		}
		return ed, nil
	}
	if len(b) == ed25519.PublicKeySize {
		return ed25519.PublicKey(b), nil
	}
	return nil, fmt.Errorf("pubkey: not PEM and not %d raw bytes (got %d)", ed25519.PublicKeySize, len(b))
}
