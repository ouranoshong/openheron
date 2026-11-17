package api

import (
	"encoding/json"
	"net/url"
	"strconv"
	"testing"

	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ouranoshong/openheron/storage"
)

// readOperations reads a session's operation timeline.
func readOperations(t *testing.T, srv *Server, id, query string) operationsView {
	t.Helper()
	got := callJSON(t, srv, "GET", "/v2/sessions/"+id+"/operations"+query, nil)
	if got.code != consts.StatusOK {
		t.Fatalf("= %d, want 200: %s", got.code, got.body)
	}
	var view operationsView
	if err := json.Unmarshal([]byte(got.body), &view); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	return view
}

// The timeline opens with the session itself, and it names the agent the
// session started with.
func TestTheOperationLogOpensWithTheSessionItself(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	view := readOperations(t, srv, id, "")
	if len(view.Items) != 1 {
		t.Fatalf("want 1 line, got %d: %s", len(view.Items), view.ThreadID)
	}
	if view.Items[0].Kind != string(storage.OpSessionCreated) {
		t.Fatalf("want the creation, got %q", view.Items[0].Kind)
	}
	if view.Items[0].Actor != string(storage.ActorUser) {
		t.Fatalf("want the user as the actor, got %q", view.Items[0].Actor)
	}
	if view.Items[0].At == "" {
		t.Fatal("every line carries the moment it happened")
	}
}

// A rename is an operation: the session record holds the title that is, and
// the timeline holds the fact that it was changed.
func TestTheOperationLogRecordsTheRename(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	got := callJSON(t, srv, "PATCH", "/v2/sessions/"+id, map[string]string{"title": "renamed"})
	if got.code != consts.StatusOK {
		t.Fatalf("rename = %d, want 200: %s", got.code, got.body)
	}

	view := readOperations(t, srv, id, "")
	if len(view.Items) != 2 {
		t.Fatalf("want 2 lines, got %d", len(view.Items))
	}
	last := view.Items[1]
	if last.Kind != string(storage.OpSessionRenamed) {
		t.Fatalf("want the rename, got %q", last.Kind)
	}
	if last.Title != "renamed" {
		t.Fatalf("want the new title, got %q", last.Title)
	}
	if last.Seq <= view.Items[0].Seq {
		t.Fatalf("the lines are out of order: %d then %d", view.Items[0].Seq, last.Seq)
	}
}

// ★ The confirmation and the mode change are two lines, in the order the
//
//	code wrote them: a confirmation that was recorded while the mode write
//	failed is still a confirmation, and merging the two would hide exactly
//	that.
func TestTheOperationLogRecordsTheTwoAuthorizationLinesInOrder(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	got := callJSON(t, srv, "PATCH", "/v2/sessions/"+id+"/authorization",
		map[string]any{"mode": "full", "confirm": true})
	if got.code != consts.StatusOK {
		t.Fatalf("switch = %d, want 200: %s", got.code, got.body)
	}

	view := readOperations(t, srv, id, "")
	if len(view.Items) != 3 {
		t.Fatalf("want 3 lines, got %d", len(view.Items))
	}
	confirmed := view.Items[1]
	changed := view.Items[2]
	if confirmed.Kind != string(storage.OpSessionAuthorizationConfirmed) {
		t.Fatalf("want the confirmation, got %q", confirmed.Kind)
	}
	if changed.Kind != string(storage.OpSessionAuthorizationChanged) {
		t.Fatalf("want the mode change, got %q", changed.Kind)
	}
	if confirmed.Mode != "full" || changed.Mode != "full" {
		t.Fatalf("want the full mode on both lines, got %q and %q", confirmed.Mode, changed.Mode)
	}
	if confirmed.Seq >= changed.Seq {
		t.Fatalf("the confirmation must come first: %d then %d", confirmed.Seq, changed.Seq)
	}
}

// ★ A session born with a mode records the same two lines a switch records:
// the confirmation and the change. A reader sees the same story either way,
// and the confirmation stays a line of its own.
func TestTheOperationLogRecordsTheModeASessionWasBornWith(t *testing.T) {
	srv := newV2Server(t)

	got := callJSON(t, srv, "POST", "/v2/sessions", map[string]any{
		"authorizationMode":      "full",
		"authorizationConfirmed": true,
	})
	if got.code != consts.StatusCreated {
		t.Fatalf("create = %d, want 201: %s", got.code, got.body)
	}
	var meta threadDetail
	if err := json.Unmarshal([]byte(got.body), &meta); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}

	view := readOperations(t, srv, meta.ThreadID, "")
	if len(view.Items) != 3 {
		t.Fatalf("want 3 lines, got %d", len(view.Items))
	}
	if view.Items[0].Kind != string(storage.OpSessionCreated) {
		t.Fatalf("want the creation first, got %q", view.Items[0].Kind)
	}
	if view.Items[1].Kind != string(storage.OpSessionAuthorizationConfirmed) {
		t.Fatalf("want the confirmation, got %q", view.Items[1].Kind)
	}
	if view.Items[2].Kind != string(storage.OpSessionAuthorizationChanged) {
		t.Fatalf("want the mode change, got %q", view.Items[2].Kind)
	}
	if view.Items[2].Mode != "full" {
		t.Fatalf("want the full mode on the line, got %q", view.Items[2].Mode)
	}
}

// A session born in the default mode has nothing to record: nobody chose
// anything, so the timeline opens with the session and nothing else.
func TestTheOperationLogOfADefaultSessionHasOnlyTheCreation(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	view := readOperations(t, srv, id, "")
	if len(view.Items) != 1 {
		t.Fatalf("want 1 line, got %d", len(view.Items))
	}
}

// The log is never trimmed, so a client that has been away asks for the
// tail and then for what came after it.
func TestTheOperationLogPagesWithAfterAndLimit(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	for _, title := range []string{"one", "two", "three"} {
		got := callJSON(t, srv, "PATCH", "/v2/sessions/"+id, map[string]string{"title": title})
		if got.code != consts.StatusOK {
			t.Fatalf("rename = %d, want 200: %s", got.code, got.body)
		}
	}

	all := readOperations(t, srv, id, "")
	if len(all.Items) != 4 {
		t.Fatalf("want 4 lines, got %d", len(all.Items))
	}

	tail := readOperations(t, srv, id, "?limit=2")
	if len(tail.Items) != 2 {
		t.Fatalf("want the last 2 lines, got %d", len(tail.Items))
	}
	if tail.Items[0].Title != "two" || tail.Items[1].Title != "three" {
		t.Fatalf("want the last two renames, got %q and %q", tail.Items[0].Title, tail.Items[1].Title)
	}
	if tail.Items[0].Seq >= tail.Items[1].Seq {
		t.Fatal("the tail keeps the ascending order")
	}

	after := readOperations(t, srv, id, "?after="+strconv.FormatUint(all.Items[0].Seq, 10))
	if len(after.Items) != 3 {
		t.Fatalf("want 3 lines after the first, got %d", len(after.Items))
	}
	if after.Items[0].Seq <= all.Items[0].Seq {
		t.Fatal("after gives the lines the client does not have")
	}
}

// A sequence number that is not a number is a wrong request.
func TestTheOperationLogRefusesABadQuery(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	for _, q := range []string{"?after=abc", "?limit=abc", "?limit=-1"} {
		got := callJSON(t, srv, "GET", "/v2/sessions/"+id+"/operations"+q, nil)
		if got.code != consts.StatusBadRequest {
			t.Fatalf("%s = %d, want 400: %s", q, got.code, got.body)
		}
	}
}

// The upload and the delete are operations: the workspace holds the files
// that are, and the timeline holds the ones that came and went.
func TestTheOperationLogRecordsTheFiles(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	uploaded := callMultipart(t, srv, "/v2/sessions/"+id+"/files", "files",
		map[string]string{"a.txt": "hello"})
	if uploaded.code != consts.StatusCreated {
		t.Fatalf("upload = %d, want 201: %s", uploaded.code, uploaded.body)
	}
	var out uploadResult
	if err := json.Unmarshal([]byte(uploaded.body), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, uploaded.body)
	}
	if len(out.Files) != 1 {
		t.Fatalf("want 1 file, got %d", len(out.Files))
	}

	view := readOperations(t, srv, id, "")
	if len(view.Items) != 2 {
		t.Fatalf("want 2 lines, got %d", len(view.Items))
	}
	if view.Items[1].Kind != string(storage.OpFileUploaded) {
		t.Fatalf("want the upload, got %q", view.Items[1].Kind)
	}
	if view.Items[1].Name != "a.txt" {
		t.Fatalf("want the name the person knows, got %q", view.Items[1].Name)
	}

	del := callJSON(t, srv, "DELETE", "/v2/sessions/"+id+"/files?path="+
		url.QueryEscape(out.Files[0].Path), nil)
	if del.code != consts.StatusOK {
		t.Fatalf("delete = %d, want 200: %s", del.code, del.body)
	}

	view = readOperations(t, srv, id, "")
	if len(view.Items) != 3 {
		t.Fatalf("want 3 lines, got %d", len(view.Items))
	}
	if view.Items[2].Kind != string(storage.OpFileDeleted) {
		t.Fatalf("want the delete, got %q", view.Items[2].Kind)
	}
}
