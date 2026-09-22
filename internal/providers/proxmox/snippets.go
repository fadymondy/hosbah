package proxmox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// SnippetStore puts a cloud-init snippet where a Proxmox "snippets" storage can see it and returns
// its volid (e.g. "nfs-snippets:snippets/hosbah-123.yaml"). It exists because the PVE API has no way
// to upload snippets; see the package documentation.
type SnippetStore interface {
	Put(ctx context.Context, name string, content []byte) (volid string, err error)
}

// snippetRemover is implemented by stores that can clean up after Delete. Optional: a snippet left
// behind holds only a one-time enrolment token that expires on its own.
type snippetRemover interface {
	Remove(ctx context.Context, name string) error
}

// DirSnippetStore writes snippets into Dir, a directory mounted on the control plane that backs the
// Proxmox storage named Storage (which must have the "snippets" content type enabled). Files land
// in Dir itself: Dir is the storage's snippets/ directory.
type DirSnippetStore struct {
	Dir     string
	Storage string
}

var snippetName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Put writes content to Dir/name atomically (temp file + rename), so Proxmox never reads a
// half-written snippet.
func (s DirSnippetStore) Put(_ context.Context, name string, content []byte) (string, error) {
	if !snippetName.MatchString(name) {
		return "", fmt.Errorf("proxmox: invalid snippet name %q", name)
	}
	if s.Dir == "" || s.Storage == "" {
		return "", errors.New("proxmox: DirSnippetStore needs Dir and Storage")
	}
	f, err := os.CreateTemp(s.Dir, ".hosbah-snippet-*")
	if err != nil {
		return "", fmt.Errorf("proxmox: writing snippet: %w", err)
	}
	tmp := f.Name()
	_, werr := f.Write(content)
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp, 0o640) // user-data carries an enrolment token
	}
	if werr == nil {
		werr = os.Rename(tmp, filepath.Join(s.Dir, name))
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("proxmox: writing snippet: %w", werr)
	}
	return s.Storage + ":snippets/" + name, nil
}

// Remove deletes Dir/name; a missing file is not an error.
func (s DirSnippetStore) Remove(_ context.Context, name string) error {
	if !snippetName.MatchString(name) {
		return fmt.Errorf("proxmox: invalid snippet name %q", name)
	}
	if err := os.Remove(filepath.Join(s.Dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("proxmox: removing snippet: %w", err)
	}
	return nil
}
