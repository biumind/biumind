// Thin re-export of packages/go-sdk/biu/skillsign for the CLI's
// existing skill_cmd consumers. The original implementation moved
// out of this package so services/runtime can verify archives at
// install time without taking a CLI dependency. Tests still live
// next to the CLI commands they exercise.

package skillpack

import (
	"crypto/ed25519"

	"github.com/biumind/biumind/packages/go-sdk/biu/skillsign"
)

// ErrBadSignature is re-exported so existing callers don't have to
// import skillsign directly. Identity comparison still works because
// it's the SAME variable reference.
var ErrBadSignature = skillsign.ErrBadSignature

func GenerateKeypair() (privPEM, pubPEM []byte, err error) {
	return skillsign.GenerateKeypair()
}

func ParsePrivateKey(pemBytes []byte) (ed25519.PrivateKey, error) {
	return skillsign.ParsePrivateKey(pemBytes)
}

func ParsePublicKey(pemBytes []byte) (ed25519.PublicKey, error) {
	return skillsign.ParsePublicKey(pemBytes)
}

func Sign(archiveBytes []byte, priv ed25519.PrivateKey) (string, error) {
	return skillsign.Sign(archiveBytes, priv)
}

func Verify(archiveBytes []byte, sigB64 string, pub ed25519.PublicKey) error {
	return skillsign.Verify(archiveBytes, sigB64, pub)
}
