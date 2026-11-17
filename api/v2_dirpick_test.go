package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ouranoshong/openheron/agentkit/dirpick"
)

// These cover the folder chooser's contract. Two things matter most:
//   - a **closed** chooser is not a failure: it is how a person says "not
//     now", and the client keeps what it had typed;
//   - only a **local** client may open it: the chooser puts a window on the
//     desktop of the machine that runs the server, and the request starts a
//     process on the host.
//
// ★ The chooser itself is a seam, because the real one opens a window — on
//   the machine the tests run on.

// newPickerServer builds a v2 server with a chooser that answers.
func newPickerServer(t *testing.T, pick func(ctx context.Context, start string) (string, error)) *Server {
	t.Helper()
	srv := newV2Server(t)
	srv.SetDirectoryPicker(pick)
	return srv
}

func decodePick(t *testing.T, body string) pickDirectoryResult {
	t.Helper()
	var out pickDirectoryResult
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode pick: %v (%s)", err, body)
	}
	return out
}

func TestPickDirectoryReturnsTheChosenPath(t *testing.T) {
	dir := t.TempDir()
	srv := newPickerServer(t, func(context.Context, string) (string, error) { return dir, nil })

	got := callJSON(t, srv, "POST", "/v2/fs/pick-directory", map[string]any{"start": ""})
	if got.code != consts.StatusOK {
		t.Fatalf("POST /v2/fs/pick-directory = %d, want 200 (%s)", got.code, got.body)
	}
	view := decodePick(t, got.body)
	if !view.Picked || view.Path != dir {
		t.Fatalf("answer = %+v, want the chosen path %q", view, dir)
	}
}

func TestAClosedChooserIsNotAFailure(t *testing.T) {
	srv := newPickerServer(t, func(context.Context, string) (string, error) {
		return "", dirpick.ErrCancelled
	})

	got := callJSON(t, srv, "POST", "/v2/fs/pick-directory", map[string]any{"start": ""})
	if got.code != consts.StatusOK {
		t.Fatalf("POST /v2/fs/pick-directory = %d, want 200 (%s)", got.code, got.body)
	}
	view := decodePick(t, got.body)
	if view.Picked || view.Path != "" {
		t.Fatalf("answer = %+v, want nothing picked", view)
	}
}

func TestAMachineWithNoChooserIs503(t *testing.T) {
	srv := newPickerServer(t, func(context.Context, string) (string, error) {
		return "", dirpick.ErrUnavailable
	})

	got := callJSON(t, srv, "POST", "/v2/fs/pick-directory", map[string]any{"start": ""})
	if got.code != consts.StatusServiceUnavailable {
		t.Fatalf("POST /v2/fs/pick-directory = %d, want 503 (%s)", got.code, got.body)
	}
	if !strings.Contains(got.body, codeDirectoryPickerUnavailable) {
		t.Fatalf("body = %s, want the code %q", got.body, codeDirectoryPickerUnavailable)
	}
}

func TestAFailedChooserCarriesItsOwnReason(t *testing.T) {
	srv := newPickerServer(t, func(context.Context, string) (string, error) {
		return "", errors.New("the folder chooser failed: cannot open display")
	})

	got := callJSON(t, srv, "POST", "/v2/fs/pick-directory", map[string]any{"start": ""})
	if got.code != consts.StatusInternalServerError {
		t.Fatalf("POST /v2/fs/pick-directory = %d, want 500 (%s)", got.code, got.body)
	}
	if !strings.Contains(got.body, "cannot open display") {
		t.Fatalf("body = %s, want the chooser's own words", got.body)
	}
}

func TestOnlyALocalCallerMayOpenTheChooser(t *testing.T) {
	// ★ A remote peer is refused: the window would open on the server's
	//   desktop, where the caller cannot see it.
	if localCaller(&net.TCPAddr{IP: net.ParseIP("10.0.0.5"), Port: 1234}) {
		t.Fatal("a remote address is taken for a local one")
	}
	if !localCaller(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}) {
		t.Fatal("the loopback is not taken for a local one")
	}
	if !localCaller(&net.TCPAddr{IP: net.ParseIP("::1"), Port: 1234}) {
		t.Fatal("the IPv6 loopback is not taken for a local one")
	}
	// ★ The unspecified address is hertz's "there was no connection" marker —
	//   an in-process call — and not a peer on the network.
	if !localCaller(&net.TCPAddr{IP: net.IPv4zero}) {
		t.Fatal("the in-process marker is not taken for a local one")
	}
	if localCaller(nil) {
		t.Fatal("a missing address is taken for a local one")
	}
}
