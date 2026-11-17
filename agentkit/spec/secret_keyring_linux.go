//go:build linux

package spec

import "os"

// keyringAvailable reports whether this machine can reach a keyring.
//
// ★ The Secret Service rides on the session bus, and a machine without one
//
//	— a headless server, a container, a CI runner — has no keyring at all.
//	Asking the bus anyway would make it try to autolaunch one: a process
//	spawn and a wait, on every configuration load, for a keyring that will
//	never be there.
//
// ★ The address is the cheap test, and it is enough: no address means no
//
//	session bus, and no session bus means no Secret Service.
func keyringAvailable() bool {
	return os.Getenv("DBUS_SESSION_BUS_ADDRESS") != ""
}
