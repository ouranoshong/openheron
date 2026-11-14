package spec

import (
	"errors"

	"github.com/zalando/go-keyring"
)

// keyringStore is the OS keyring.
//
// ★ It is the only store there is, and that is deliberate: a keyring
//
//	library that can also write a file would put the secret next to the
//	configuration, which is the thing this system exists to avoid.
//
// ★ The store keeps no state of its own. The one thing worth remembering —
//
//	"the keyring is not available on this machine" — is a fact about the
//	machine, and it is asked of the machine every time (see
//	keyringAvailable), not cached by a process that might outlive the
//	answer.
type keyringStore struct{}

func (keyringStore) Get(user string) (string, error) {
	if !keyringAvailable() {
		return "", ErrSecretUnavailable
	}
	v, err := keyring.Get(SecretService, user)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", ErrSecretNotFound
		}
		return "", err
	}
	return v, nil
}

func (keyringStore) Set(user, value string) error {
	if !keyringAvailable() {
		return ErrSecretUnavailable
	}
	return keyring.Set(SecretService, user, value)
}

func (keyringStore) Delete(user string) error {
	if !keyringAvailable() {
		return ErrSecretUnavailable
	}
	if err := keyring.Delete(SecretService, user); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return ErrSecretNotFound
		}
		return err
	}
	return nil
}
