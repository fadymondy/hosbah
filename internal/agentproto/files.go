package agentproto

import (
	"encoding/base64"
	"errors"
	"path"
	"strings"
	"time"
)

/*
A server's filesystem, browsed and edited directly by the agent (os APIs, never a shell).

Every path is absolute and clean (path.Clean, POSIX rules — the same check runs on the control
plane, wherever it happens to be built and run, and on the agent, always Linux, so the two sides
never disagree about what a path means). A fixed denylist is refused outright, whatever the
operation and whoever asks: Hosbah's own secrets and state (StateDir, WorkDir, and their pre-rename
names), and the kernel's virtual filesystems. Writing, deleting or renaming onto a whole top-level
directory ("/", "/etc", "/var", …) is refused too — a file or a directory INSIDE one is fine.
*/

// Operation kinds.
const (
	KindFSList   = "fs.list"
	KindFSRead   = "fs.read"
	KindFSWrite  = "fs.write"
	KindFSMkdir  = "fs.mkdir"
	KindFSDelete = "fs.delete"
	KindFSRename = "fs.rename"
)

// FSKinds are the fs.* operations, for the control plane's bookkeeping.
var FSKinds = []string{KindFSList, KindFSRead, KindFSWrite, KindFSMkdir, KindFSDelete, KindFSRename}

// MaxFSPath bounds a path's length; MaxFSBytes bounds a read or a write's content.
const (
	MaxFSPath  = 4096
	MaxFSBytes = 1 << 20 // 1 MiB
)

// maxFSContentB64 is the largest a base64-encoded MaxFSBytes payload can be (with slack for line-free
// standard encoding's padding).
const maxFSContentB64 = ((MaxFSBytes + 2) / 3 * 4) + 4

// fsDenylist is refused outright: Hosbah's own secrets and state, in both the current and the
// pre-rename name, and the kernel's virtual filesystems (never meaningfully a file to browse, and
// /proc in particular can hang or crash a read of the wrong node).
var fsDenylist = []string{StateDir, LegacyStateDir, WorkDir, LegacyWorkDir, "/proc", "/sys", "/dev"}

// FSDenied reports whether path is refused outright, whatever the operation and whoever asks.
func FSDenied(p string) bool {
	for _, d := range fsDenylist {
		if p == d || strings.HasPrefix(p, d+"/") {
			return true
		}
	}
	return false
}

// fsTopLevelDirs are the FHS's standard top-level directories: never a legitimate write, delete or
// rename target on their own (the files and directories inside one are) — but an arbitrary file
// someone put directly under "/" is not specially protected just for being there.
var fsTopLevelDirs = map[string]bool{
	"bin": true, "boot": true, "dev": true, "etc": true, "home": true, "lib": true, "lib32": true,
	"lib64": true, "libx32": true, "media": true, "mnt": true, "opt": true, "proc": true, "root": true,
	"run": true, "sbin": true, "srv": true, "sys": true, "tmp": true, "usr": true, "var": true,
}

// fsTopLevel reports whether path is "/" itself or one of its standard top-level directories.
func fsTopLevel(p string) bool {
	if p == "/" {
		return true
	}
	if path.Dir(p) != "/" {
		return false
	}
	return fsTopLevelDirs[strings.TrimPrefix(p, "/")]
}

// ValidFSPath: absolute and clean — path.Clean(p) == p, so no "..", no ".", no trailing slash, no
// doubled slash — within MaxFSPath, and free of NUL (which would truncate it in a syscall).
func ValidFSPath(p string) bool {
	if p == "" || len(p) > MaxFSPath || strings.ContainsRune(p, 0) {
		return false
	}
	if !strings.HasPrefix(p, "/") {
		return false
	}
	return path.Clean(p) == p
}

// checkFSPath is every params.Validate's shared core. write also refuses "/" and a bare top-level
// directory as the target.
func checkFSPath(p string, write bool) error {
	if !ValidFSPath(p) {
		return errors.New("path must be an absolute, clean path (no .., no trailing slash, no NUL)")
	}
	if FSDenied(p) {
		return errors.New("that path is not reachable through the file browser")
	}
	if write && fsTopLevel(p) {
		return errors.New("refused: that is a top-level system directory, not a file or a directory of its own")
	}
	return nil
}

// FSEntry type values.
const (
	FSFile    = "file"
	FSDir     = "dir"
	FSSymlink = "symlink"
	FSOther   = "other" // a device, socket or named pipe: shown, never opened
)

// FSEntry is one entry of fs.list.
type FSEntry struct {
	Name  string    `json:"name"`
	Type  string    `json:"type" enum:"file,dir,symlink,other"`
	Size  int64     `json:"size"`
	Mode  string    `json:"mode"` // rwx-style, as fs.FileMode.String() renders it
	MTime time.Time `json:"mtime"`
	Owner string    `json:"owner,omitempty"` // the file's user name, when it could be looked up
}

// FSListParams: fs.list.
type FSListParams struct {
	Path string `json:"path"`
}

// Validate checks fs.list on both sides of the wire.
func (p FSListParams) Validate() error { return checkFSPath(p.Path, false) }

// FSListResult: fs.list, the directory's entries (not sorted by the wire contract — sort to taste).
type FSListResult struct {
	Entries []FSEntry `json:"entries"`
}

// FSReadParams: fs.read. MaxBytes caps what is relayed; 0 means MaxFSBytes.
type FSReadParams struct {
	Path     string `json:"path"`
	MaxBytes int    `json:"max_bytes,omitempty"`
}

// Validate checks fs.read on both sides of the wire.
func (p FSReadParams) Validate() error {
	if err := checkFSPath(p.Path, false); err != nil {
		return err
	}
	if p.MaxBytes < 0 || p.MaxBytes > MaxFSBytes {
		return errors.New("max_bytes is 0 (meaning the default) to 1048576")
	}
	return nil
}

// FSReadResult: fs.read. Content is base64 (Truncated: only the first bytes up to the request's
// cap; Binary: a NUL or a high share of non-text bytes was seen in what was read, so it likely is
// not meant to be edited as text).
type FSReadResult struct {
	Content   string `json:"content"`
	Size      int64  `json:"size"` // the file's real size, even when truncated
	Truncated bool   `json:"truncated"`
	Binary    bool   `json:"binary"`
}

// FSWriteParams: fs.write. Content is base64, at most MaxFSBytes decoded. Mode, given, is a POSIX
// permission string ("644", "0644"); empty keeps the file's existing mode (or 0644 for a new one).
type FSWriteParams struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    string `json:"mode,omitempty"`
}

var fsModeRe = mustModeRe()

func mustModeRe() *modeMatcher { return &modeMatcher{} }

// modeMatcher: a tiny octal-permission check (3 or 4 digits, each 0-7) without pulling in regexp
// for four characters.
type modeMatcher struct{}

func (modeMatcher) MatchString(s string) bool {
	if len(s) != 3 && len(s) != 4 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '7' {
			return false
		}
	}
	return true
}

// Validate checks fs.write on both sides of the wire (the decoded size, not the base64 text).
func (p FSWriteParams) Validate() error {
	if err := checkFSPath(p.Path, true); err != nil {
		return err
	}
	if len(p.Content) > maxFSContentB64 {
		return errors.New("content is larger than 1 MiB")
	}
	if _, err := base64.StdEncoding.DecodeString(p.Content); err != nil {
		return errors.New("content must be base64")
	}
	if p.Mode != "" && !fsModeRe.MatchString(p.Mode) {
		return errors.New("mode must be an octal permission such as 644 or 0755")
	}
	return nil
}

// FSWriteResult: fs.write.
type FSWriteResult struct {
	Bytes   int    `json:"bytes"`
	Mode    string `json:"mode"`
	Created bool   `json:"created"` // the file did not exist before this write
}

// FSMkdirParams: fs.mkdir. One directory, in an existing parent (not MkdirAll — a browser's "new
// folder" button makes one directory in the folder it is looking at).
type FSMkdirParams struct {
	Path string `json:"path"`
}

// Validate checks fs.mkdir on both sides of the wire.
func (p FSMkdirParams) Validate() error { return checkFSPath(p.Path, true) }

// FSMkdirResult: fs.mkdir.
type FSMkdirResult struct{}

// FSDeleteParams: fs.delete. Recursive removes a non-empty directory and everything in it;
// without it, a non-empty directory is refused untouched.
type FSDeleteParams struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive,omitempty"`
}

// Validate checks fs.delete on both sides of the wire.
func (p FSDeleteParams) Validate() error { return checkFSPath(p.Path, true) }

// FSDeleteResult: fs.delete.
type FSDeleteResult struct{}

// FSRenameParams: fs.rename (also how a browser "renames" — same directory, new name — and how it
// moves something to another directory).
type FSRenameParams struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Validate checks fs.rename on both sides of the wire.
func (p FSRenameParams) Validate() error {
	if err := checkFSPath(p.From, true); err != nil {
		return err
	}
	if err := checkFSPath(p.To, true); err != nil {
		return err
	}
	if p.From == p.To {
		return errors.New("from and to are the same path")
	}
	return nil
}

// FSRenameResult: fs.rename.
type FSRenameResult struct{}
