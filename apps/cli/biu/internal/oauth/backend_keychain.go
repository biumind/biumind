// OAuth Tokens keychain backend — now a thin adapter over the shared
// internal/oskeychain primitive (R6.4). The platform-specific
// `security` / `secret-tool` shell-out moved to oskeychain so oauth and
// secretstore share one implementation; this file keeps the Tokens-JSON
// marshalling that's specific to the OAuth credential.
//
// Behaviour is unchanged from the previous per-GOOS backends: same
// keychain service ("com.biumind.biu") + account ("default"), same JSON
// blob on disk, so `biu auth migrate` and existing entries keep working.

package oauth

import (
	"encoding/json"
	"errors"

	"github.com/biumind/biumind/apps/cli/biu/internal/oskeychain"
)

// openKeychainBackend returns a Tokens backend over the OS keychain, or
// (nil, false) when no keychain is reachable. Honours the package test
// override via resolveKeychainBackend (so oauth tests inject a fake
// tokenBackend exactly as before).
func openKeychainBackend() (tokenBackend, bool) {
	return resolveKeychainBackend(func() (tokenBackend, bool) {
		k, ok := oskeychain.Open()
		if !ok {
			return nil, false
		}
		return &keychainBackend{kc: k}, true
	})
}

type keychainBackend struct {
	kc oskeychain.Keychain
}

func (b *keychainBackend) Name() string { return b.kc.Name() }

func (b *keychainBackend) Path() string {
	return "OS keychain (" + keychainService + ")"
}

func (b *keychainBackend) Load() (Tokens, error) {
	raw, found, err := b.kc.Get(keychainService, keychainAccount)
	if err != nil {
		return Tokens{}, err
	}
	if !found || raw == "" {
		return Tokens{}, nil
	}
	var t Tokens
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return Tokens{}, errors.New("oauth: keychain entry corrupt — re-login with `biu auth login`")
	}
	return t, nil
}

func (b *keychainBackend) Save(t Tokens) error {
	body, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return b.kc.Set(keychainService, keychainAccount, string(body))
}

func (b *keychainBackend) Delete() error {
	return b.kc.Delete(keychainService, keychainAccount)
}
