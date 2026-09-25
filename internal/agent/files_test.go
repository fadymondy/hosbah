package agent

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// Every fs.* handler is exercised against a throwaway directory: fsHost.root remaps every absolute
// path onto it, so the handlers run exactly as they would on a server, without touching this
// machine's real /etc, /home or anywhere else.

func newTestFSHost(t *testing.T) *fsHost {
	t.Helper()
	return &fsHost{root: t.TempDir()}
}

func TestFSListMkdirDelete(t *testing.T) {
	h := newTestFSHost(t)
	if _, _, err := h.mkdir(mustOC(t, agentproto.FSMkdirParams{Path: "/sub"})); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(h.root, "sub", "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _, err := h.list(mustOC(t, agentproto.FSListParams{Path: "/sub"}))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	entries := res.(agentproto.FSListResult).Entries
	if len(entries) != 1 || entries[0].Name != "a.txt" || entries[0].Type != agentproto.FSFile || entries[0].Size != 5 {
		t.Fatalf("unexpected entries: %+v", entries)
	}

	// A non-empty directory refuses a non-recursive delete …
	if _, _, err := h.delete(mustOC(t, agentproto.FSDeleteParams{Path: "/sub"})); err == nil {
		t.Fatal("non-recursive delete of a non-empty directory succeeded")
	}
	// … but succeeds recursively.
	if _, _, err := h.delete(mustOC(t, agentproto.FSDeleteParams{Path: "/sub", Recursive: true})); err != nil {
		t.Fatalf("recursive delete: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(h.root, "sub")); !os.IsNotExist(err) {
		t.Fatal("directory still there after a recursive delete")
	}
	// Deleting something already gone is not an error.
	if _, _, err := h.delete(mustOC(t, agentproto.FSDeleteParams{Path: "/sub", Recursive: true})); err != nil {
		t.Fatalf("delete of an already-gone path: %v", err)
	}
}

func TestFSWriteReadRoundtrip(t *testing.T) {
	h := newTestFSHost(t)
	content := base64.StdEncoding.EncodeToString([]byte("hello world"))
	wres, _, err := h.write(mustOC(t, agentproto.FSWriteParams{Path: "/note.txt", Content: content, Mode: "600"}))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	wr := wres.(agentproto.FSWriteResult)
	if wr.Bytes != len("hello world") || !wr.Created {
		t.Fatalf("unexpected write result: %+v", wr)
	}

	rres, _, err := h.read(mustOC(t, agentproto.FSReadParams{Path: "/note.txt"}))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	rr := rres.(agentproto.FSReadResult)
	got, err := base64.StdEncoding.DecodeString(rr.Content)
	if err != nil || string(got) != "hello world" {
		t.Fatalf("read back %q, err %v", got, err)
	}
	if rr.Truncated || rr.Binary {
		t.Fatalf("unexpected read flags: %+v", rr)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Lstat(filepath.Join(h.root, "note.txt"))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("mode not applied: %v %v", info, err)
		}
	}

	// A second write keeps the mode (Mode not given) and is atomic: no partial file is ever visible
	// (nothing to assert on that directly, but the rename must still leave the new content).
	content2 := base64.StdEncoding.EncodeToString([]byte("bye"))
	if _, _, err := h.write(mustOC(t, agentproto.FSWriteParams{Path: "/note.txt", Content: content2})); err != nil {
		t.Fatalf("second write: %v", err)
	}
	rres2, _, err := h.read(mustOC(t, agentproto.FSReadParams{Path: "/note.txt"}))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	got2, _ := base64.StdEncoding.DecodeString(rres2.(agentproto.FSReadResult).Content)
	if string(got2) != "bye" {
		t.Fatalf("re-read got %q", got2)
	}
}

func TestFSReadTruncatesAndDetectsBinary(t *testing.T) {
	h := newTestFSHost(t)
	if err := os.WriteFile(filepath.Join(h.root, "bin"), []byte{0, 1, 2, 3, 'x'}, 0o644); err != nil {
		t.Fatal(err)
	}
	res, _, err := h.read(mustOC(t, agentproto.FSReadParams{Path: "/bin"}))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !res.(agentproto.FSReadResult).Binary {
		t.Fatal("a NUL-containing file was not flagged binary")
	}

	big := make([]byte, 100)
	for i := range big {
		big[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(h.root, "big.txt"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	res2, _, err := h.read(mustOC(t, agentproto.FSReadParams{Path: "/big.txt", MaxBytes: 10}))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	rr := res2.(agentproto.FSReadResult)
	if !rr.Truncated || rr.Size != 100 {
		t.Fatalf("unexpected truncated read: %+v", rr)
	}
	got, _ := base64.StdEncoding.DecodeString(rr.Content)
	if len(got) != 10 {
		t.Fatalf("expected 10 bytes back, got %d", len(got))
	}
}

func TestFSRename(t *testing.T) {
	h := newTestFSHost(t)
	if _, _, err := h.write(mustOC(t, agentproto.FSWriteParams{Path: "/a", Content: base64.StdEncoding.EncodeToString([]byte("x"))})); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.rename(mustOC(t, agentproto.FSRenameParams{From: "/a", To: "/b"})); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(h.root, "a")); !os.IsNotExist(err) {
		t.Fatal("source still exists after rename")
	}
	if _, err := os.Lstat(filepath.Join(h.root, "b")); err != nil {
		t.Fatalf("destination missing after rename: %v", err)
	}
	// Renaming onto an existing path is refused.
	if _, _, err := h.write(mustOC(t, agentproto.FSWriteParams{Path: "/c", Content: base64.StdEncoding.EncodeToString([]byte("y"))})); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.rename(mustOC(t, agentproto.FSRenameParams{From: "/b", To: "/c"})); err == nil {
		t.Fatal("rename onto an existing path succeeded")
	}
}

// TestFSDenylist exercises the denylist through the real (unremapped) handlers: fsHost.root is ""
// (production behaviour), and every call must be refused by Validate before anything opens a real
// path — /etc/hosbah and friends are never actually touched by this test.
func TestFSDenylist(t *testing.T) {
	h := &fsHost{} // root "": paths are exactly what a server would see
	denied := []agentproto.FSListParams{{Path: "/etc/hosbah"}, {Path: "/proc"}, {Path: "/sys"}, {Path: "/dev"}}
	for _, p := range denied {
		if _, _, err := h.list(mustOC(t, p)); err == nil {
			t.Errorf("list(%q) on the denylist succeeded", p.Path)
		}
	}
	if _, _, err := h.write(mustOC(t, agentproto.FSWriteParams{Path: "/var/lib/hosbah/x", Content: "eA=="})); err == nil {
		t.Fatal("write into the denylist succeeded")
	}
	if _, _, err := h.delete(mustOC(t, agentproto.FSDeleteParams{Path: "/", Recursive: true})); err == nil {
		t.Fatal("recursive delete of / succeeded")
	}
}

func mustOC(t *testing.T, params any) *OpContext {
	oc, _ := opCtx(t, params)
	return oc
}

func TestFSSymlinkIntoDeniedDirIsRefused(t *testing.T) {
	h := newTestFSHost(t)
	secret := filepath.Join(h.root, "etc", "hosbah")
	if err := os.MkdirAll(secret, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secret, "agent.key"), []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.root, "tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(h.root, "tmp", "x")); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	if _, _, err := h.read(mustOC(t, agentproto.FSReadParams{Path: "/tmp/x/agent.key", MaxBytes: 100})); err == nil {
		t.Fatal("read through a symlink into /etc/hosbah must be refused")
	}
	if _, _, err := h.list(mustOC(t, agentproto.FSListParams{Path: "/tmp/x"})); err == nil {
		t.Fatal("listing a symlink to /etc/hosbah must be refused")
	}
	if _, _, err := h.write(mustOC(t, agentproto.FSWriteParams{Path: "/tmp/x/new.key", Content: "YQ=="})); err == nil {
		t.Fatal("writing a new file through the symlink must be refused")
	}
	if _, _, err := h.rename(mustOC(t, agentproto.FSRenameParams{From: "/tmp/x/agent.key", To: "/tmp/stolen"})); err == nil {
		t.Fatal("renaming out of the denied dir through the symlink must be refused")
	}
}
