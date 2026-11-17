package api

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ouranoshong/openheron/agentkit/dirpick"
)

// The folder chooser.
//
// ★ It is the **server's** dialog, and that is not a choice of convenience:
//
//	the browser will not tell a page where a chosen directory lives — it
//	hands the page the name of the folder and the paths inside it — and the
//	absolute path is the one thing a project root has to be. ⇒ openheron
//	asks the machine it runs on.
//
// ★ **Only a client on this machine may use it.** The chooser puts a window
//
//	on the desktop of the machine that runs the server: a remote caller
//	would not see it, and would leave a window behind on somebody else's
//	screen. ★ It is also the only guard this endpoint needs — the request
//	starts a process on the host, and that must not be something anybody on
//	the network can ask for.
const (
	// codeDirectoryPickerUnavailable says this machine has no chooser.
	codeDirectoryPickerUnavailable = "directory_picker_unavailable"
	// codeDirectoryPickerRemote says the caller is not on this machine.
	codeDirectoryPickerRemote = "directory_picker_remote"
	// codeDirectoryPickerFailed says the chooser could not be opened.
	codeDirectoryPickerFailed = "directory_picker_failed"
)

// pickDirectoryBody is the request.
type pickDirectoryBody struct {
	// Start is where the chooser opens. Empty means the user's home.
	Start string `json:"start"`
}

// pickDirectoryResult is the answer.
type pickDirectoryResult struct {
	// Path is the chosen directory. Empty when the chooser was closed.
	Path string `json:"path"`
	// Picked says whether a directory was chosen.
	//
	// ★ It is its own field because "the chooser was closed" and "a
	//   directory was chosen" are different answers, and the second one
	//   without the first would be a path of "".
	Picked bool `json:"picked"`
}

// v2PickDirectory opens the operating system's folder chooser.
func (s *Server) v2PickDirectory(ctx context.Context, c *app.RequestContext) {
	if !localCaller(c.RemoteAddr()) {
		s.v2Fail(c, consts.StatusForbidden, codeDirectoryPickerRemote,
			"the folder chooser opens on the machine that runs the server, so only a local client may use it",
			false)
		return
	}
	var body pickDirectoryBody
	// ★ An empty body is a body: the start is optional, and a malformed one
	//   is treated as "no start" rather than as a failure.
	_ = c.Bind(&body)
	path, err := s.pickDirectory(ctx, strings.TrimSpace(body.Start))
	switch {
	case errors.Is(err, dirpick.ErrCancelled):
		// ★ A closed chooser is not a failure: it is how a person says
		//   "not now", and the client keeps whatever it had typed.
		c.JSON(consts.StatusOK, pickDirectoryResult{Picked: false})
		return
	case errors.Is(err, dirpick.ErrUnavailable):
		s.v2Fail(c, consts.StatusServiceUnavailable, codeDirectoryPickerUnavailable,
			"this machine has no folder chooser — type the path instead", false)
		return
	case err != nil:
		s.v2Fail(c, consts.StatusInternalServerError, codeDirectoryPickerFailed, err.Error(), false)
		return
	}
	c.JSON(consts.StatusOK, pickDirectoryResult{Picked: true, Path: path})
}

// pickDirectory opens the chooser.
//
// ★ The seam is here because the real one opens a **window** — on the
//
//	machine the tests run on, which is not a thing a test may do.
func (s *Server) pickDirectory(ctx context.Context, start string) (string, error) {
	if s.pickDirFn != nil {
		return s.pickDirFn(ctx, start)
	}
	return dirpick.Pick(ctx, start)
}

// SetDirectoryPicker installs the chooser the endpoint uses.
//
// ★ It exists for the tests, and for an embedding application that has a
//
//	chooser of its own.
func (s *Server) SetDirectoryPicker(fn func(ctx context.Context, start string) (string, error)) {
	s.pickDirFn = fn
}

// localCaller reports whether a request came from this machine.
//
// ★ The unspecified address is hertz's "there was no connection" marker —
//
//	an in-process call, the tests — and not a peer on the network: a socket
//	never has 0.0.0.0 as its source.
func localCaller(addr net.Addr) bool {
	if addr == nil {
		// ★ No peer at all is not a local peer: the rule fails closed.
		return false
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		host = addr.String()
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return false
	}
	if ip.IsUnspecified() {
		return true
	}
	return ip.IsLoopback()
}
