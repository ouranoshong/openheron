package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route"

	"github.com/ouranoshong/openheron/agentkit/imaging"
)

// callRaw performs a GET and hands back the response as it is.
//
// ★ The raw endpoint serves **bytes**, so the JSON helper is the wrong tool:
// decoding a picture into a string would hide exactly what this endpoint
// is about.
func callRaw(t *testing.T, srv *Server, path string) (int, string, []byte) {
	t.Helper()
	engine, ok := srv.h.(*route.Engine)
	if !ok {
		t.Fatal("server is not backed by a route.Engine")
	}
	resp := ut.PerformRequest(engine, "GET", path, &ut.Body{})
	code := resp.Code
	if code == 0 {
		code = consts.StatusOK
	}
	return code, string(resp.Header().Peek("Content-Type")), resp.Body.Bytes()
}

// testPNG is a small picture, encoded the real way.
func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 7), G: uint8(y * 5), B: 40, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// uploadOne puts one file into a session and returns what the server said.
func uploadOne(t *testing.T, srv *Server, id, name string, content []byte) uploadResult {
	t.Helper()
	got := callMultipart(t, srv, "/v2/sessions/"+id+"/files", "files",
		map[string]string{name: string(content)})
	if got.code != consts.StatusCreated {
		t.Fatalf("upload = %d (%s)", got.code, got.body)
	}
	var out uploadResult
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if len(out.Files) != 1 {
		t.Fatalf("want 1 file, got %d", len(out.Files))
	}
	return out
}

// ★ A picture comes back as a picture.
func TestTheRawEndpointServesAPictureAsAPicture(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	pic := uploadOne(t, srv, id, "shot.png", testPNG(t, 40, 30)).Files[0]
	code, ctype, body := callRaw(t, srv,
		"/v2/sessions/"+id+"/files/raw?path="+url.QueryEscape(pic.Path))

	if code != consts.StatusOK {
		t.Fatalf("= %d", code)
	}
	if !strings.HasPrefix(ctype, "image/png") {
		t.Fatalf("content type = %q, want image/png", ctype)
	}
	if len(body) == 0 {
		t.Fatal("the body is empty")
	}
}

// ★ Text is served as text, whatever the file calls itself.
//
// ★ This is the line that keeps an uploaded script from running on this
//
//	origin: a `.html` file served as text/html would be a document the
//	browser renders, and it would run whatever it carries.
func TestTheRawEndpointServesTextAsPlainText(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	for _, name := range []string{"notes.md", "page.html", "data.csv", "config.json"} {
		uploaded := uploadOne(t, srv, id, name, []byte("<script>alert(1)</script>")).Files[0]
		code, ctype, body := callRaw(t, srv,
			"/v2/sessions/"+id+"/files/raw?path="+url.QueryEscape(uploaded.Path))

		if code != consts.StatusOK {
			t.Fatalf("%s: = %d", name, code)
		}
		if !strings.HasPrefix(ctype, "text/plain") {
			t.Fatalf("%s: content type = %q, want text/plain", name, ctype)
		}
		if !bytes.Contains(body, []byte("<script>")) {
			t.Fatalf("%s: the bytes must come through untouched", name)
		}
	}
}

// ★ Anything else is a download — the browser is not given the chance to
//
//	interpret it.
func TestTheRawEndpointServesAnythingElseAsADownload(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	uploaded := uploadOne(t, srv, id, "bundle.zip", []byte("PK\x03\x04not a real zip")).Files[0]
	engine, ok := srv.h.(*route.Engine)
	if !ok {
		t.Fatal("server is not backed by a route.Engine")
	}
	resp := ut.PerformRequest(engine, "GET",
		"/v2/sessions/"+id+"/files/raw?path="+url.QueryEscape(uploaded.Path), &ut.Body{})

	if got := string(resp.Header().Peek("Content-Type")); got != "application/octet-stream" {
		t.Fatalf("content type = %q, want application/octet-stream", got)
	}
	disp := string(resp.Header().Peek("Content-Disposition"))
	if !strings.Contains(disp, "attachment") {
		t.Fatalf("content disposition = %q, want an attachment", disp)
	}
}

// ★ A name that escapes the workspace is refused.
func TestTheRawEndpointRefusesAPathThatEscapesTheWorkspace(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	for _, name := range []string{"../escape.txt", "/abs.txt", "sub/../../escape.txt"} {
		code, _, _ := callRaw(t, srv,
			"/v2/sessions/"+id+"/files/raw?path="+url.QueryEscape(name))
		if code != consts.StatusBadRequest {
			t.Fatalf("the name %q must be refused, got %d", name, code)
		}
	}
}

// ★ A file that is not there is a 404, not an empty body.
func TestTheRawEndpointIs404ForAMissingFile(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	code, _, _ := callRaw(t, srv,
		"/v2/sessions/"+id+"/files/raw?path="+url.QueryEscape("gone.png"))
	if code != consts.StatusNotFound {
		t.Fatalf("= %d, want 404", code)
	}
}

// ★ A thumbnail is scaled on the way out, and nothing is stored.
//
// ★ The point of the test is the **size**: a scaled picture has to be
//
//	smaller, or the interface downloads the whole thing for a 48-pixel
//	chip.
func TestTheRawEndpointScalesAPictureOnRequest(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	big := testPNG(t, 400, 300)
	uploaded := uploadOne(t, srv, id, "big.png", big).Files[0]

	_, _, full := callRaw(t, srv,
		"/v2/sessions/"+id+"/files/raw?path="+url.QueryEscape(uploaded.Path))
	_, _, small := callRaw(t, srv,
		"/v2/sessions/"+id+"/files/raw?path="+url.QueryEscape(uploaded.Path)+"&max=64")

	if len(small) >= len(full) {
		t.Fatalf("the thumbnail is %d bytes, the original %d — it was not scaled", len(small), len(full))
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(small))
	if err != nil {
		t.Fatalf("the thumbnail does not decode: %v", err)
	}
	if cfg.Width != 64 && cfg.Height != 64 {
		t.Fatalf("the thumbnail is %dx%d, want the long edge at 64", cfg.Width, cfg.Height)
	}
}

// ★ Two uploads of the same name do not overwrite each other.
//
// ★ An overwrite makes the transcript lie: the first message's attachment
//
//	list says "this file", and the file is now the second one.
func TestTwoUploadsOfTheSameNameDoNotOverwrite(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	first := uploadOne(t, srv, id, "shot.png", testPNG(t, 40, 30)).Files[0]
	second := uploadOne(t, srv, id, "shot.png", testPNG(t, 50, 50)).Files[0]

	if first.Path == second.Path {
		t.Fatalf("both uploads landed on %q", first.Path)
	}
	// ★ And both are still there.
	var ls struct {
		Files []workspaceEntry `json:"files"`
	}
	listing := callJSON(t, srv, "GET", "/v2/sessions/"+id+"/files", nil)
	if err := json.Unmarshal([]byte(listing.body), &ls); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	if len(ls.Files) != 2 {
		t.Fatalf("want 2 files in the workspace, got %d", len(ls.Files))
	}
}

// ★ The stored name carries the hash, and the hash can be recomputed.
//
// ★ Read the file, stat it, hash the name, the size and the time — the
//
//	name checks out. A hash that cannot be recomputed is decoration.
func TestTheStoredNameCarriesTheHashOfNameSizeAndTime(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	out := uploadOne(t, srv, id, "shot.png", testPNG(t, 40, 30))
	uploaded := out.Files[0]

	if !strings.HasPrefix(uploaded.Path, "shot-") {
		t.Fatalf("the stored name is %q, want it to start with the original name", uploaded.Path)
	}
	if !strings.HasSuffix(uploaded.Path, ".png") {
		t.Fatalf("the stored name is %q, want it to keep the extension", uploaded.Path)
	}
	// The hash is eight hex characters between the name and the extension.
	middle := strings.TrimSuffix(strings.TrimPrefix(uploaded.Path, "shot-"), ".png")
	if len(middle) != 8 {
		t.Fatalf("the hash is %q, want eight characters", middle)
	}
	if _, err := hex.DecodeString(middle); err != nil {
		t.Fatalf("the hash %q is not hex: %v", middle, err)
	}

	// ★ Recompute it from the file on disk.
	full := filepath.Join(out.WorkspacePath, filepath.FromSlash(uploaded.Path))
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	again := imaging.HashedName("shot.png", info.Size(), info.ModTime())
	if again != uploaded.Path {
		t.Fatalf("the name does not check out: %q on disk, %q recomputed",
			uploaded.Path, again)
	}
}

// ★ The listing shows the name the user typed, not the hash.
//
// ★ The interface shows names. A listing of hashes is a listing of hashes.
func TestTheListingShowsTheOriginalName(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	uploadOne(t, srv, id, "shot.png", testPNG(t, 40, 30))
	uploadOne(t, srv, id, "notes.md", []byte("# hello"))

	var ls struct {
		Files []workspaceEntry `json:"files"`
	}
	listing := callJSON(t, srv, "GET", "/v2/sessions/"+id+"/files", nil)
	if err := json.Unmarshal([]byte(listing.body), &ls); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	names := map[string]string{}
	for _, f := range ls.Files {
		names[f.Name] = f.Path
	}
	if _, ok := names["shot.png"]; !ok {
		t.Fatalf("the listing does not show the original name: %+v", ls.Files)
	}
	if _, ok := names["notes.md"]; !ok {
		t.Fatalf("the listing does not show the original name: %+v", ls.Files)
	}
}

// ★ The upload response names the file the user knows too.
func TestTheUploadResponseNamesTheOriginal(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	uploaded := uploadOne(t, srv, id, "notes.md", []byte("# hello")).Files[0]
	if uploaded.Name != "notes.md" {
		t.Fatalf("the name is %q, want the original", uploaded.Name)
	}
	if uploaded.Path == "notes.md" {
		t.Fatal("the path must carry the hash, or a second upload would overwrite this one")
	}
}
