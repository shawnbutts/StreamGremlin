package updates

import "crypto/ed25519"

// trusted are the public keys whose signatures on SHA256SUMS.txt the app
// accepts (D-120). The private key is the user's, on their own computer
// (`go run ./cmd/release keygen`); it's never in this repository or on
// GitHub. To change keys, add the new one here, release a version signed
// with the old one, then sign with the new one and remove the old.
var trusted = []string{
	// The user's key, made 2026-11-16; key ID 36989dcb9aa25b1d.
	"4vNWQL7AJpNfPd9fIYBiCPA1I4DAxahBHvLUdH73IMI=",
}

// TrustedKeys returns the keys the app trusts.
func TrustedKeys() []ed25519.PublicKey {
	out := make([]ed25519.PublicKey, 0, len(trusted))
	for _, s := range trusted {
		k, err := ParsePublicKey(s)
		if err != nil {
			panic("updates: a trusted key is damaged: " + s)
		}
		out = append(out, k)
	}
	return out
}
