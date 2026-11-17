//go:build !linux

package spec

// keyringAvailable reports whether this machine can reach a keyring.
//
// ★ Windows and macOS always have one: the Credential Manager and the
//
//	Keychain are part of the system, and the libraries reach them without a
//	session bus. The check that Linux needs — is there a bus — has no
//	counterpart here, so the answer is simply yes.
func keyringAvailable() bool { return true }
