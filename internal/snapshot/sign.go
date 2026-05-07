package snapshot

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"

	"golang.org/x/crypto/ssh"
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
//   - PEM-wrapped X.509/PKIX SubjectPublicKeyInfo (`openssl pkey -pubout`).
//   - OpenSSH authorized-keys / `.pub` line (`ssh-ed25519 AAAAC3... [comment]`).
//   - Raw 32-byte key (binary).
//
// OpenSSH parsing uses x/crypto/ssh, which is already a transitive dep — no
// new module required. SSHSIG-armored signatures (`ssh-keygen -Y sign` output)
// are NOT accepted on the --signature path; pair an OpenSSH pubkey with a
// raw 64-byte ed25519 signature produced by openssl/cosign/minisign instead.
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
	// OpenSSH wire-format detection: an authorized-keys line starts with
	// `ssh-ed25519 ` (other algos rejected below). Trim any trailing
	// whitespace so multi-line files with a blank tail still parse.
	trimmed := bytes.TrimSpace(b)
	if bytes.HasPrefix(trimmed, []byte("ssh-")) {
		pk, _, _, _, err := ssh.ParseAuthorizedKey(trimmed)
		if err != nil {
			return nil, fmt.Errorf("parse openssh pubkey: %w", err)
		}
		ck, ok := pk.(ssh.CryptoPublicKey)
		if !ok {
			return nil, fmt.Errorf("openssh pubkey wrapper has no CryptoPublicKey accessor")
		}
		ed, ok := ck.CryptoPublicKey().(ed25519.PublicKey)
		if !ok {
			return nil, fmt.Errorf("openssh pubkey is not ed25519 (got %s)", pk.Type())
		}
		return ed, nil
	}
	if len(b) == ed25519.PublicKeySize {
		return ed25519.PublicKey(b), nil
	}
	return nil, fmt.Errorf("pubkey: expected PEM, OpenSSH ssh-ed25519, or %d raw bytes (got %d)", ed25519.PublicKeySize, len(b))
}
