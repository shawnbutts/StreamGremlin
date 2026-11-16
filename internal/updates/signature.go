package updates

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// A release is signed by signing its SHA256SUMS.txt (D-120,
// docs/adr/0052-updates.md). The key is the user's, kept on their own
// computer, never on GitHub; the app trusts only the public keys in
// TrustedKeys. The signature file, SHA256SUMS.txt.sig, is one line:
//
//	streamgremlin-sig-v1 KEYID SIGNATURE
//
// KEYID is the first 8 bytes of the public key's SHA-256, in hex.
// SIGNATURE is base64 (standard, padded) of an Ed25519 signature over
// sigContext followed by the exact bytes of SHA256SUMS.txt.

// SumsFile and SigFile are the files a release carries for updates.
const (
	SumsFile = "SHA256SUMS.txt"
	SigFile  = SumsFile + ".sig"
)

const (
	sigPrefix = "streamgremlin-sig-v1"
	// sigContext keeps a signature over SHA256SUMS.txt from meaning
	// anything else the key might ever sign.
	sigContext = "StreamGremlin release SHA256SUMS v1\n"
	keyPrefix  = "streamgremlin-release-key-v1"
	// MaxSigBytes caps the signature file read from a release.
	MaxSigBytes = 1024
)

// ErrSignature means a release's signature is missing, malformed, by a
// key the app doesn't trust, or doesn't match.
var ErrSignature = errors.New("the release's signature doesn't check out")

// KeyID names a public key in a signature file.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// NewKey makes a signing key. It returns the private key file's text and
// the public key, base64, for TrustedKeys.
func NewKey() (private []byte, public string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	text := fmt.Sprintf("%s %s\n", keyPrefix, base64.StdEncoding.EncodeToString(priv.Seed()))
	return []byte(text), base64.StdEncoding.EncodeToString(pub), nil
}

// ParsePrivateKey reads a private key file made by NewKey.
func ParsePrivateKey(text []byte) (ed25519.PrivateKey, error) {
	f := strings.Fields(string(text))
	if len(f) != 2 || f[0] != keyPrefix {
		return nil, errors.New("not a StreamGremlin release key")
	}
	seed, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("the release key is damaged")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// ParsePublicKey reads a public key, base64.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("not an Ed25519 public key")
	}
	return ed25519.PublicKey(b), nil
}

// Sign makes SHA256SUMS.txt.sig's text for sums.
func Sign(priv ed25519.PrivateKey, sums []byte) []byte {
	pub, _ := priv.Public().(ed25519.PublicKey)
	sig := ed25519.Sign(priv, signed(sums))
	return fmt.Appendf(nil, "%s %s %s\n", sigPrefix, KeyID(pub), base64.StdEncoding.EncodeToString(sig))
}

// Verify checks sig over sums with one of keys.
func Verify(keys []ed25519.PublicKey, sums, sig []byte) error {
	if len(sig) > MaxSigBytes {
		return ErrSignature
	}
	f := strings.Fields(string(sig))
	if len(f) != 3 || f[0] != sigPrefix {
		return ErrSignature
	}
	raw, err := base64.StdEncoding.DecodeString(f[2])
	if err != nil || len(raw) != ed25519.SignatureSize {
		return ErrSignature
	}
	for _, k := range keys {
		if KeyID(k) == f[1] && ed25519.Verify(k, signed(sums), raw) {
			return nil
		}
	}
	return ErrSignature
}

func signed(sums []byte) []byte {
	return append([]byte(sigContext), sums...)
}

// ParseSums reads SHA256SUMS.txt (shasum -a 256's lines, "HEX  NAME" or
// "HEX *NAME") into name → hex.
func ParseSums(sums []byte) (map[string]string, error) {
	out := map[string]string{}
	for line := range bytes.Lines(sums) {
		s := strings.TrimRight(string(line), "\r\n")
		if s == "" {
			continue
		}
		hexSum, name, ok := strings.Cut(s, " ")
		name = strings.TrimPrefix(strings.TrimPrefix(name, " "), "*")
		if b, err := hex.DecodeString(hexSum); !ok || err != nil || len(b) != sha256.Size || name == "" {
			return nil, fmt.Errorf("%s has a line it can't read: %q", SumsFile, s)
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("%s lists %s twice", SumsFile, name)
		}
		out[name] = strings.ToLower(hexSum)
	}
	return out, nil
}
