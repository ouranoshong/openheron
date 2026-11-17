package contract

import "testing"

// ★ The three modes are the wire names, and they are the whole vocabulary:
// a client that names one of them is understood by every version.
func TestTheThreeModesParse(t *testing.T) {
	for _, name := range []string{"default", "trusted", "full"} {
		got, err := ParseAuthorizationMode(name)
		if err != nil {
			t.Fatalf("%s must parse: %v", name, err)
		}
		if got.String() != name {
			t.Fatalf("%s parsed as %q", name, got)
		}
	}
}

// ★ An unknown name is an **error**, not the default. A name nobody
//
//	recognises must not silently become the safest one: the user would
//	think they had widened the mode, and the session would still ask.
func TestAnUnknownModeIsRefused(t *testing.T) {
	for _, name := range []string{"yolo", "FULL", "allow", ""} {
		if _, err := ParseAuthorizationMode(name); err == nil {
			t.Fatalf("%q must not parse into a mode", name)
		}
	}
}

// The surrounding spaces are not part of the name.
func TestAModeIsTrimmedBeforeItIsRead(t *testing.T) {
	got, err := ParseAuthorizationMode("  trusted  ")
	if err != nil {
		t.Fatalf("trimmed mode must parse: %v", err)
	}
	if got != AuthzTrusted {
		t.Fatalf("mode = %q, want trusted", got)
	}
}
