package filestore

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// 本文件钉住 session.jsonl 的**磁盘格式契约**。
//
// ★ 这个包之前把编解码放在 storage/filestore/mem 里，而那个包零测试 ——
//
//	格式（header 字段、message_kind、version）没有任何东西拦着它被改掉。
//	下面是那道闸门。

// rewriteHeader replaces the first line of a transcript and keeps the message
// lines intact. It is how the tests forge a header openheron should reject.
func rewriteHeader(t *testing.T, path string, mutate func(*logHeader)) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(string(data), "\n", 2)

	var h logHeader
	if err := json.Unmarshal([]byte(lines[0]), &h); err != nil {
		t.Fatal(err)
	}
	mutate(&h)

	raw, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	out := string(raw) + "\n"
	if len(lines) > 1 {
		out += lines[1]
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A transcript written by another format version must not load silently.
func TestTranscriptVersionMismatchIsRejected(t *testing.T) {
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})
	sess, _ := fm.Create(context.Background(), "")
	rewriteHeader(t, messagesPath(root, sess.ID()), func(h *logHeader) {
		h.Version = formatVersion + 1
	})

	fresh, _ := NewFileManager(root, Options{})
	if _, err := fresh.Get(context.Background(), sess.ID()); err == nil {
		t.Fatal("a transcript from another format version must not load")
	}
}

// A log written for a different message representation must be refused rather
// than decoded into something openheron does not understand.
func TestTranscriptForeignMessageKindIsRejected(t *testing.T) {
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})
	sess, _ := fm.Create(context.Background(), "")
	rewriteHeader(t, messagesPath(root, sess.ID()), func(h *logHeader) {
		h.MessageKind = "something-else"
	})

	fresh, _ := NewFileManager(root, Options{})
	if _, err := fresh.Get(context.Background(), sess.ID()); err == nil {
		t.Fatal("a transcript with a foreign message_kind must not load")
	}
}

// The header is the transcript's identity: when it disagrees with the
// directory name, the header wins.
//
// ★ 后果是已知的：后续追加会写向 header 指向的目录（那个目录可能并不存在）。
//
//	这是「以 header 为准」这条规则的直接后果，readTranscript 会为它打一条
//	告警。把目录名当权威反而会让会话读得到、写不进另一个目录。
func TestTranscriptHeaderIDWinsOverDirectoryName(t *testing.T) {
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})
	sess, _ := fm.Create(context.Background(), "")

	other := "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	rewriteHeader(t, messagesPath(root, sess.ID()), func(h *logHeader) { h.ID = other })

	fresh, _ := NewFileManager(root, Options{})
	reloaded, err := fresh.Get(context.Background(), sess.ID())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ID() != other {
		t.Fatalf("want the header id %q, got %q", other, reloaded.ID())
	}
}

// The header a fresh session gets must stay exactly as older openheron wrote it:
// the on-disk value is the contract, not a detail.
func TestCreatedTranscriptCarriesTheFormatContract(t *testing.T) {
	root := t.TempDir()
	fm, _ := NewFileManager(root, Options{})
	sess, _ := fm.Create(context.Background(), "")

	data, err := os.ReadFile(messagesPath(root, sess.ID()))
	if err != nil {
		t.Fatal(err)
	}
	first := strings.SplitN(string(data), "\n", 2)[0]

	var h logHeader
	if err := json.Unmarshal([]byte(first), &h); err != nil {
		t.Fatal(err)
	}
	if h.Type != "header" {
		t.Fatalf("want type %q, got %q", "header", h.Type)
	}
	if h.Version != formatVersion {
		t.Fatalf("want version %d, got %d", formatVersion, h.Version)
	}
	if h.MessageKind != "message" {
		t.Fatalf("want message_kind %q, got %q", "message", h.MessageKind)
	}
	if h.ID != sess.ID() {
		t.Fatalf("want id %q, got %q", sess.ID(), h.ID)
	}
}
