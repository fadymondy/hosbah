package agent

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// A server's filesystem, browsed and edited by the agent directly through the os package — never a
// shell, never a glob a caller could smuggle a second path into. Every params.Validate ran already
// (proto.go's Validate is the wire contract, checked again here before anything touches disk), so a
// handler only has to resolve the path and do the one thing it is named for.

type fsHost struct {
	// root remaps every path onto a real filesystem location; "" (production) is the identity — an
	// agent always runs as root on the server it browses, so /etc/passwd IS /etc/passwd. Tests set
	// root to a temp directory so the same handlers run against a throwaway tree instead.
	root string
}

func newFSHost() *fsHost { return &fsHost{} }

func (h *fsHost) resolve(p string) (string, error) {
	real := p
	if h.root != "" {
		real = filepath.Join(h.root, filepath.FromSlash(p))
	}
	if h.followsIntoDenied(real) {
		return "", fmt.Errorf("%s is off limits", p)
	}
	return real, nil
}

// followsIntoDenied: Validate only sees the path as written, so a symlink (/tmp/x -> /etc/hosbah)
// would walk straight past the denylist. The deepest existing ancestor is resolved through its
// links, the not-yet-existing rest appended, and the result checked against the denylist again.
func (h *fsHost) followsIntoDenied(real string) bool {
	existing, rest := real, ""
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return false
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return true // a dangling or looping link: refuse rather than guess where it points
	}
	resolved = filepath.Join(resolved, rest)
	if h.root != "" {
		root, err := filepath.EvalSymlinks(h.root)
		if err != nil {
			return true
		}
		rel, err := filepath.Rel(root, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
		resolved = "/" + filepath.ToSlash(rel)
	}
	return agentproto.FSDenied(filepath.ToSlash(resolved))
}

// ---- fs.list ---------------------------------------------------------------------------------------

func (h *fsHost) list(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.FSListParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	real, err := h.resolve(p.Path)
	if err != nil {
		return nil, 2, err
	}
	des, err := os.ReadDir(real)
	if err != nil {
		return nil, 1, fsErr("could not list", p.Path, err)
	}
	res := agentproto.FSListResult{Entries: []agentproto.FSEntry{}}
	for _, de := range des {
		info, err := de.Info()
		if err != nil {
			continue // gone between ReadDir and Info (a race outside our control): skip it, not fatal
		}
		res.Entries = append(res.Entries, entryOf(de.Name(), info))
	}
	sort.Slice(res.Entries, func(i, j int) bool { return res.Entries[i].Name < res.Entries[j].Name })
	return res, 0, nil
}

func entryOf(name string, info os.FileInfo) agentproto.FSEntry {
	kind := agentproto.FSFile
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		kind = agentproto.FSSymlink
	case info.IsDir():
		kind = agentproto.FSDir
	case info.Mode()&os.ModeType != 0:
		kind = agentproto.FSOther
	}
	return agentproto.FSEntry{Name: name, Type: kind, Size: info.Size(), Mode: info.Mode().Perm().String(),
		MTime: info.ModTime().UTC(), Owner: ownerOf(info)}
}

// ---- fs.read ---------------------------------------------------------------------------------------

func (h *fsHost) read(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.FSReadParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	real, err := h.resolve(p.Path)
	if err != nil {
		return nil, 2, err
	}
	info, err := os.Lstat(real)
	if err != nil {
		return nil, 1, fsErr("could not read", p.Path, err)
	}
	if info.IsDir() {
		return nil, 2, errors.New("that is a directory, not a file")
	}
	max := p.MaxBytes
	if max <= 0 || max > agentproto.MaxFSBytes {
		max = agentproto.MaxFSBytes
	}
	f, err := os.Open(real)
	if err != nil {
		return nil, 1, fsErr("could not read", p.Path, err)
	}
	defer f.Close()
	buf := make([]byte, max+1)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, 1, fsErr("could not read", p.Path, err)
	}
	truncated := n > max
	if truncated {
		n = max
	}
	data := buf[:n]
	return agentproto.FSReadResult{Content: base64.StdEncoding.EncodeToString(data), Size: info.Size(),
		Truncated: truncated, Binary: looksBinary(data)}, 0, nil
}

// looksBinary: a NUL anywhere, or too many bytes outside printable text (tab/newline/CR and the
// rest of ASCII, plus anything UTF-8 multi-byte) count it as binary — the same rough heuristic git
// and most editors use, never definitive, just enough to warn before an edit mangles it.
func looksBinary(data []byte) bool {
	if bytes.IndexByte(data, 0) >= 0 {
		return true
	}
	if len(data) == 0 {
		return false
	}
	bad := 0
	for _, b := range data {
		if b == '\t' || b == '\n' || b == '\r' || (b >= 0x20 && b < 0x7f) || b >= 0x80 {
			continue
		}
		bad++
	}
	return float64(bad)/float64(len(data)) > 0.05
}

// ---- fs.write --------------------------------------------------------------------------------------

// write is atomic: a temp file in the same directory, written and chmod'd, then renamed onto the
// target — a reader never sees a partial file. The existing file's mode (and, on the platforms that
// have one, its owner) carries over unless Mode was given.
func (h *fsHost) write(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.FSWriteParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	data, err := base64.StdEncoding.DecodeString(p.Content)
	if err != nil {
		return nil, 2, errors.New("content must be base64")
	}
	real, err := h.resolve(p.Path)
	if err != nil {
		return nil, 2, err
	}
	mode := os.FileMode(0o644)
	existing, statErr := os.Lstat(real)
	created := statErr != nil
	if statErr == nil {
		if existing.IsDir() {
			return nil, 2, errors.New("that is a directory, not a file")
		}
		mode = existing.Mode().Perm()
	}
	if p.Mode != "" {
		if v, err := strconv.ParseUint(p.Mode, 8, 32); err == nil {
			mode = os.FileMode(v).Perm()
		}
	}
	dir := filepath.Dir(real)
	tmp, err := os.CreateTemp(dir, ".fswrite-*")
	if err != nil {
		return nil, 1, fsErr("could not write", p.Path, err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return nil, 1, fsErr("could not write", p.Path, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		oc.Info("could not set mode on %s: %v", p.Path, err)
	}
	if statErr == nil {
		preserveOwner(tmpName, existing)
	}
	if err := tmp.Close(); err != nil {
		return nil, 1, fsErr("could not write", p.Path, err)
	}
	if err := os.Rename(tmpName, real); err != nil {
		return nil, 1, fsErr("could not write", p.Path, err)
	}
	ok = true
	return agentproto.FSWriteResult{Bytes: len(data), Mode: mode.String(), Created: created}, 0, nil
}

// ---- fs.mkdir --------------------------------------------------------------------------------------

func (h *fsHost) mkdir(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.FSMkdirParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	real, err := h.resolve(p.Path)
	if err != nil {
		return nil, 2, err
	}
	if err := os.Mkdir(real, 0o755); err != nil {
		if os.IsExist(err) {
			return nil, 1, fmt.Errorf("%s already exists", p.Path)
		}
		return nil, 1, fsErr("could not create", p.Path, err)
	}
	return agentproto.FSMkdirResult{}, 0, nil
}

// ---- fs.delete -------------------------------------------------------------------------------------

// delete never follows a symlink: os.Remove/os.RemoveAll unlink the path itself (the link, not what
// it points to), and RemoveAll only ever unlinks the entries it lists under a directory — it does
// not open a symlinked child as a directory to recurse into it.
func (h *fsHost) delete(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.FSDeleteParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	real, err := h.resolve(p.Path)
	if err != nil {
		return nil, 2, err
	}
	if _, err := os.Lstat(real); err != nil {
		if os.IsNotExist(err) {
			return agentproto.FSDeleteResult{}, 0, nil // already gone
		}
		return nil, 1, fsErr("could not delete", p.Path, err)
	}
	if p.Recursive {
		if err := os.RemoveAll(real); err != nil {
			return nil, 1, fsErr("could not delete", p.Path, err)
		}
		return agentproto.FSDeleteResult{}, 0, nil
	}
	if err := os.Remove(real); err != nil {
		if isDirNotEmpty(err) {
			return nil, 1, errors.New("that directory is not empty — delete recursively to remove it and everything in it")
		}
		return nil, 1, fsErr("could not delete", p.Path, err)
	}
	return agentproto.FSDeleteResult{}, 0, nil
}

func isDirNotEmpty(err error) bool {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err != nil && (pe.Err.Error() == "directory not empty" || pe.Err.Error() == "The directory is not empty.")
	}
	return false
}

// ---- fs.rename -------------------------------------------------------------------------------------

func (h *fsHost) rename(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.FSRenameParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	from, err := h.resolve(p.From)
	if err != nil {
		return nil, 2, err
	}
	to, err := h.resolve(p.To)
	if err != nil {
		return nil, 2, err
	}
	if _, err := os.Lstat(to); err == nil {
		return nil, 1, fmt.Errorf("%s already exists", p.To)
	}
	if err := os.Rename(from, to); err != nil {
		return nil, 1, fsErr("could not rename", p.From, err)
	}
	return agentproto.FSRenameResult{}, 0, nil
}

// fsErr strips the real (possibly root-remapped, in tests) path from an *os.PathError so what
// reaches the operation's log and the caller is always the path asked for.
func fsErr(verb, path string, err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return fmt.Errorf("%s %s: %s", verb, path, pe.Err)
	}
	return fmt.Errorf("%s %s: %w", verb, path, err)
}
