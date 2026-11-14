package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	hertzconfig "github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/route"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/agentkit/run"
	"github.com/ouranoshong/openheron/storage/filestore"
	"github.com/ouranoshong/openheron/transport"
)

// breakingWriter captures frames, and can be told to fail the way a
// connection that went away does.
//
// ★ The session's channel has no terminal frame: it ends when the client
//
//	goes away. ★ The test has to be able to end it, or the handler would sit
//	on the channel for ever.
type breakingWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	broken bool
}

func (w *breakingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.broken {
		return 0, errors.New("the connection is gone")
	}
	return w.buf.Write(p)
}

func (w *breakingWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// Break makes the next write fail, the way a dropped connection does.
func (w *breakingWriter) Break() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.broken = true
}

func (w *breakingWriter) Flush() error    { return nil }
func (w *breakingWriter) Finalize() error { return nil }

// customFrame is the shape of the session's custom frames.
type customFrame struct {
	Name  string         `json:"name"`
	Value map[string]any `json:"value"`
}

// sessionFrames reads the custom frames the session's channel wrote.
//
// ★ The wire is SSE, so each frame is one `data:` line. ★ The frames are
//
//	read while they are written, so a frame that is not there **yet** is
//	not an error — only a frame that never arrives is.
//
// ★ The keepalive carries no payload, and it is not a frame: it is the
//
//	probe that tells the server the client is gone.
func sessionFrames(t *testing.T, w *breakingWriter) []customFrame {
	t.Helper()
	var out []customFrame
	for _, block := range strings.Split(w.String(), "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		payload, ok := strings.CutPrefix(block, "data:")
		if !ok {
			continue
		}
		payload = strings.TrimSpace(payload)
		if payload == "" {
			continue
		}
		var frame customFrame
		if err := json.Unmarshal([]byte(payload), &frame); err != nil {
			t.Fatalf("decode %q: %v", payload, err)
		}
		out = append(out, frame)
	}
	return out
}

// waitForFrame waits for a custom frame with the given name.
func waitForFrame(t *testing.T, w *breakingWriter, name string) customFrame {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, frame := range sessionFrames(t, w) {
			if frame.Name == name {
				return frame
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the session's channel never carried %q: %s", name, w.String())
	return customFrame{}
}

// newSessionEventsServer wires a server whose agent asks for approval.
//
// ★ The two streams are captured apart: the run's own stream and the
//
//	session's channel write to the same handler, and a test that mixed
//	them would read the run's frames as the session's facts.
func newSessionEventsServer(t *testing.T) (*Server, *run.Runtime, *breakingWriter, *breakingWriter) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "sessions")

	fm, err := filestore.NewFileManager(root, filestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	rt := run.New(run.Config{
		AgentBuilder:   interruptingBuilder{},
		SessionManager: fm,
		Input:          run.InputConfig{},
		StartTimeout:   5 * time.Second,
	})
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })

	engine := route.NewEngine(hertzconfig.NewOptions(nil))
	srv := New(rt, fm, obs.Discard())
	srv.h = engine
	// ★ The keepalive is the disconnect probe, and the test has to wait for
	//   it — so it is shortened here.
	srv.sessionKeepalive = 20 * time.Millisecond
	var sessionOut, runOut breakingWriter
	srv.newStream = func(c *app.RequestContext) *transport.SSEStream {
		if strings.HasSuffix(string(c.Request.URI().Path()), "/events") {
			return transport.NewWithWriter(c, &sessionOut)
		}
		return transport.NewWithWriter(c, &runOut)
	}
	srv.registerRoutes()
	t.Cleanup(func() { _ = engine.Close() })
	return srv, rt, &sessionOut, &runOut
}

// The session's channel carries the facts that outlive a run.
//
// ★ This is the whole point of the channel: a second surface is waiting on
//
//	an approval, the approval is answered elsewhere, and the second
//	surface has to hear about it — on a channel that is not the run's.
func TestTheSessionChannelCarriesTheFacts(t *testing.T) {
	srv, rt, frames, _ := newSessionEventsServer(t)
	id := createThread(t, srv)

	// ★ The channel never ends on its own, so the request runs in the
	//   background and the test breaks it when it is done.
	done := make(chan struct{})
	go func() {
		defer close(done)
		call(t, srv, "GET", "/v2/sessions/"+id+"/events", "")
	}()

	// ★ Wait until the handler is listening. A fact published before the
	//   subscription exists is gone for good: the session's channel has no
	//   replay, and the past is the scan's job.
	deadline := time.Now().Add(5 * time.Second)
	for rt.AnnounceWatchers(id) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the session's channel was never opened")
		}
		time.Sleep(10 * time.Millisecond)
	}

	got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id,
		"query":    "write the config",
	})
	if got.code != 200 {
		t.Fatalf("run = %d (%s)", got.code, got.body)
	}

	started := waitForFrame(t, frames, "run_started")
	if started.Value["run_id"] == "" {
		t.Fatalf("the frame must name the run: %v", started.Value)
	}
	if started.Value["kind"] != "query" {
		t.Fatalf("the frame must carry the kind's name, got %v", started.Value["kind"])
	}

	// ★ Now the approval is answered — in another surface, as far as this
	//   one is concerned. The fact has to reach this channel.
	sess, err := srv.sm.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	waitForPending(t, sess, 1)
	got = callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": id,
		"resume": []map[string]any{{
			"interruptId": "int-e2e",
			"status":      "resolved",
			"payload":     map[string]any{"approved": true},
		}},
	})
	if got.code != 200 {
		t.Fatalf("the approval must be accepted: %d (%s)", got.code, got.body)
	}

	resolved := waitForFrame(t, frames, "approval_resolved")
	if resolved.Value["interrupt_id"] != "int-e2e" {
		t.Fatalf("the frame names the wrong approval: %v", resolved.Value)
	}
	if resolved.Value["approved"] != true {
		t.Fatalf("the frame must carry the decision: %v", resolved.Value)
	}
	if resolved.Value["run_id"] == "" {
		t.Fatalf("the frame must name the answering run: %v", resolved.Value)
	}

	// ★ The round that followed the answer is announced on the same
	//   channel: it is the fact that lets the other surface follow it.
	waitForFrame(t, frames, "run_started")

	frames.Break()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the session's channel did not end")
	}
}

// An unknown session has no channel to open.
func TestTheSessionChannelRefusesAnUnknownSession(t *testing.T) {
	srv, _, _, _ := newSessionEventsServer(t)
	got := call(t, srv, "GET", "/v2/sessions/not-a-uuid/events", "")
	if got.code == 200 {
		t.Fatalf("an unknown session must not have a channel: %s", got.body)
	}
}
