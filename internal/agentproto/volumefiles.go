package agentproto

import (
	"errors"
	"fmt"
	"strings"
)

// The file browser of an app's persistent volume: list a folder, read, write and delete one file,
// make a folder. The agent works under the volume's mountpoint through an os.Root, so no path (and
// no symlink inside the volume) reaches outside it. Paths are relative, folder paths end in '/'.
const KindVolumeFiles = "volume.files"

// ValidVolumeFilesMode: what volume.files can do.
func ValidVolumeFilesMode(s string) bool {
	switch s {
	case "list", "get", "put", "delete", "mkdir":
		return true
	}
	return false
}

// VolumeFilesParams: volume.files. Volume is the derived Docker name of one of the app's volumes.
// Data (put) is base64; the listing and the bytes reuse the object browser's types and limits.
type VolumeFilesParams struct {
	AppID  string `json:"app_id"`
	Volume string `json:"volume"`
	Mode   string `json:"mode"`
	Path   string `json:"path,omitempty"`
	Data   string `json:"data,omitempty"`
}

func (p VolumeFilesParams) Validate() error {
	switch {
	case !ValidVolumeName(p.AppID, p.Volume):
		return errors.New("bad volume")
	case !ValidVolumeFilesMode(p.Mode):
		return errors.New("mode is list, get, put, delete or mkdir")
	}
	if p.Mode == "list" && p.Path == "" {
		return nil
	}
	if !ValidObjectKey(p.Path) {
		return errors.New("bad path")
	}
	folder := strings.HasSuffix(p.Path, "/")
	switch p.Mode {
	case "list", "mkdir":
		if !folder {
			return errors.New("a folder path ends in /")
		}
	case "get", "put":
		if folder {
			return errors.New("a file path does not end in /")
		}
		if p.Mode == "put" && len(p.Data) > ObjectMaxBytes*4/3+4 {
			return fmt.Errorf("an upload is at most %d MiB", ObjectMaxBytes>>20)
		}
	}
	return nil
}
