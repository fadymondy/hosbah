package agent

import (
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// ---- volume.files ----------------------------------------------------------------------------------

// volumeFiles browses an app's Docker volume on the host. Every access goes through an os.Root
// opened at the volume's mountpoint, so neither ".." nor a symlink written by the app can reach
// outside the volume.
func (h *storageHost) volumeFiles(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.VolumeFilesParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	p.Volume = resolveName(oc.Ctx, h.run, kindVolume, p.Volume)
	out, err := h.run(oc.Ctx, nil, "docker", "volume", "inspect", "--format", "{{.Mountpoint}}", p.Volume)
	mount := strings.TrimSpace(string(out))
	if err != nil || !strings.HasPrefix(mount, "/") {
		return nil, 1, errors.New("the volume does not exist on the server yet — it is made on the app's next deploy")
	}
	root, err := os.OpenRoot(mount)
	if err != nil {
		return nil, 1, errors.New("could not open the volume")
	}
	defer root.Close()
	rel := strings.TrimSuffix(p.Path, "/")
	if rel == "" {
		rel = "."
	}

	switch p.Mode {
	case "list":
		d, err := root.Open(rel)
		if err != nil {
			return nil, 1, errors.New("the folder could not be found")
		}
		defer d.Close()
		ents, err := d.ReadDir(-1)
		if err != nil {
			return nil, 1, errors.New("could not list the folder")
		}
		sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
		res := agentproto.ObjectBrowseResult{Entries: []agentproto.ObjectEntry{}}
		for _, e := range ents {
			if len(res.Entries) >= agentproto.ObjectMaxListing {
				res.Truncated = true
				break
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			key := p.Path + e.Name()
			en := agentproto.ObjectEntry{Key: key, Modified: info.ModTime().UTC().Format(time.RFC3339)}
			switch {
			case e.IsDir():
				en.Key, en.Folder = key+"/", true
			case info.Mode().IsRegular():
				en.Size = info.Size()
			default:
				continue // symlinks, sockets, devices: not shown
			}
			res.Entries = append(res.Entries, en)
		}
		return res, 0, nil

	case "get":
		info, err := root.Lstat(rel)
		if err != nil || !info.Mode().IsRegular() {
			return nil, 1, errors.New("the file could not be found")
		}
		if info.Size() > agentproto.ObjectMaxBytes {
			return nil, 1, errors.New("the file is larger than 2 MiB")
		}
		f, err := root.Open(rel)
		if err != nil {
			return nil, 1, errors.New("could not read the file")
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, agentproto.ObjectMaxBytes+1))
		if err != nil || len(data) > agentproto.ObjectMaxBytes {
			return nil, 1, errors.New("could not read the file")
		}
		return agentproto.ObjectBrowseResult{Data: base64.StdEncoding.EncodeToString(data), Size: int64(len(data))}, 0, nil

	case "put":
		data, err := base64.StdEncoding.DecodeString(p.Data)
		if err != nil || len(data) > agentproto.ObjectMaxBytes {
			return nil, 2, errors.New("the upload is not valid base64 of at most 2 MiB")
		}
		if dir := path.Dir(rel); dir != "." {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return nil, 1, errors.New("could not make the folder")
			}
		}
		f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return nil, 1, errors.New("could not write the file")
		}
		_, werr := f.Write(data)
		if cerr := f.Close(); werr != nil || cerr != nil {
			return nil, 1, errors.New("could not write the file")
		}
		oc.Info("wrote %s (%d bytes) in %s", p.Path, len(data), p.Volume)
		return agentproto.ObjectBrowseResult{Size: int64(len(data))}, 0, nil

	case "mkdir":
		if err := root.MkdirAll(rel, 0o755); err != nil {
			return nil, 1, errors.New("could not make the folder")
		}
		return agentproto.ObjectBrowseResult{}, 0, nil

	case "delete":
		if _, err := root.Lstat(rel); errors.Is(err, fs.ErrNotExist) {
			return agentproto.ObjectBrowseResult{}, 0, nil
		}
		if err := root.RemoveAll(rel); err != nil {
			return nil, 1, errors.New("could not delete")
		}
		oc.Info("deleted %s in %s", p.Path, p.Volume)
		return agentproto.ObjectBrowseResult{}, 0, nil
	}
	return nil, 2, errors.New("unknown mode")
}
