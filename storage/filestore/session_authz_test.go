package filestore

import (
	"context"
	"testing"

	"github.com/ouranoshong/openheron/contract"
)

// A session that never chose a mode reads as the default — the mode the
// session asked in before the mode existed.
func TestASessionWithoutAModeReadsAsTheDefault(t *testing.T) {
	fm := newManager(t, Options{})
	sess, err := fm.Create(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got := sess.AuthorizationMode(); got != contract.AuthzDefault {
		t.Fatalf("mode = %q, want default", got)
	}
	if sess.AuthorizationConfirmed() {
		t.Fatal("a session that never confirmed must not say it did")
	}
}

// ★ The mode is the session's own property, so it survives a restart — and
//
//	so does the confirmation, which is what keeps it a once-per-session
//	thing rather than a once-per-process thing.
func TestTheModeAndTheConfirmationSurviveARestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, err := NewFileManager(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := fm.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.SetAuthorizationMode(contract.AuthzFull); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetAuthorizationConfirmed(true); err != nil {
		t.Fatal(err)
	}

	fresh, err := NewFileManager(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := fresh.Get(ctx, sess.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.AuthorizationMode(); got != contract.AuthzFull {
		t.Fatalf("mode = %q, want full", got)
	}
	if !reloaded.AuthorizationConfirmed() {
		t.Fatal("the confirmation must survive a restart, or a second surface asks again")
	}
}

// ★ A state file written before the mode existed loads as the default: the
//
//	upgrade needs no migration, and no session changes its behaviour.
func TestALegacyStateWithoutTheModeReadsAsTheDefault(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, err := NewFileManager(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := fm.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.SetAuthorizationMode(contract.AuthzTrusted); err != nil {
		t.Fatal(err)
	}

	st, _, err := readState(statePath(root, sess.ID()))
	if err != nil {
		t.Fatal(err)
	}
	st.Authorization = ""
	st.AuthorizationConfirmed = false
	if err := writeStateFile(statePath(root, sess.ID()), &st); err != nil {
		t.Fatal(err)
	}

	fresh, err := NewFileManager(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := fresh.Get(ctx, sess.ID())
	if err != nil {
		t.Fatalf("a legacy state file must still load: %v", err)
	}
	if got := reloaded.AuthorizationMode(); got != contract.AuthzDefault {
		t.Fatalf("mode = %q, want default", got)
	}
}

// The mode is the session's own: two sessions answer separately.
func TestTheModeIsTheSessionsOwn(t *testing.T) {
	ctx := context.Background()
	fm := newManager(t, Options{})

	first, err := fm.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := fm.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.SetAuthorizationMode(contract.AuthzFull); err != nil {
		t.Fatal(err)
	}
	if got := second.AuthorizationMode(); got != contract.AuthzDefault {
		t.Fatalf("the other session must keep the default, got %q", got)
	}
}
