package agentproto

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// The object browser of a MinIO data service (MH-1113): list a bucket by prefix, read, write and
// delete one object. mc runs inside the service's container with the root key in its environment
// only; bucket and key are validated on both sides of the wire and reach mc as one argv element.
const KindObjectBrowse = "object.browse"

// Limits. An object's bytes travel base64 in one websocket message (4 MiB on both ends).
const (
	ObjectMaxBytes   = 2 << 20
	ObjectMaxKey     = 1024
	ObjectMaxListing = 1000
)

// ValidObjectMode: what object.browse can do.
func ValidObjectMode(s string) bool {
	switch s {
	case "buckets", "list", "get", "put", "delete":
		return true
	}
	return false
}

// ValidObjectKey: an S3 key as the browser allows it — at most ObjectMaxKey bytes of valid UTF-8,
// no control characters, no leading '/', no "." or ".." segment. A trailing '/' names a folder.
func ValidObjectKey(k string) bool {
	if k == "" || len(k) > ObjectMaxKey || !utf8.ValidString(k) || strings.HasPrefix(k, "/") {
		return false
	}
	for _, r := range k {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	for _, seg := range strings.Split(strings.TrimSuffix(k, "/"), "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// ObjectBrowseParams: object.browse. Prefix (list) is "" or a folder key ending in '/'. Data (put)
// is base64. Recursive (delete) removes every object under a folder key.
type ObjectBrowseParams struct {
	ServiceID  string `json:"service_id"`
	Container  string `json:"container"`
	Mode       string `json:"mode"`
	Bucket     string `json:"bucket,omitempty"`
	Prefix     string `json:"prefix,omitempty"`
	Key        string `json:"key,omitempty"`
	Data       string `json:"data,omitempty"`
	Recursive  bool   `json:"recursive,omitempty"`
	RootSecret string `json:"root_secret"`
}

func (p ObjectBrowseParams) Validate() error {
	switch {
	case !ValidID(p.ServiceID):
		return errors.New("bad service id")
	case !containerRE.MatchString(p.Container):
		return errors.New("bad container name")
	case !ValidObjectMode(p.Mode):
		return errors.New("mode is buckets, list, get, put or delete")
	case p.RootSecret == "":
		return errors.New("object.browse needs the root secret")
	}
	if p.Mode == "buckets" {
		return nil
	}
	if !ValidBucket(p.Bucket) {
		return fmt.Errorf("bad bucket name %q", p.Bucket)
	}
	switch p.Mode {
	case "list":
		if p.Prefix != "" && (!ValidObjectKey(p.Prefix) || !strings.HasSuffix(p.Prefix, "/")) {
			return errors.New("a prefix is a folder key ending in /")
		}
	case "get", "put", "delete":
		if !ValidObjectKey(p.Key) {
			return errors.New("bad object key")
		}
		if p.Mode == "get" && strings.HasSuffix(p.Key, "/") {
			return errors.New("a folder cannot be downloaded")
		}
		if p.Mode == "put" && len(p.Data) > ObjectMaxBytes*4/3+4 {
			return fmt.Errorf("an upload is at most %d MiB", ObjectMaxBytes>>20)
		}
		if p.Recursive && (p.Mode != "delete" || !strings.HasSuffix(p.Key, "/")) {
			return errors.New("recursive applies to deleting a folder only")
		}
	}
	return nil
}

// ObjectEntry is one bucket, folder or object.
type ObjectEntry struct {
	Key      string `json:"key"`
	Folder   bool   `json:"folder,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Modified string `json:"modified,omitempty"`
}

// ObjectBrowseResult: the listing (buckets, list), the object (get: base64 Data and its size), or
// nothing (put, delete). Truncated: the listing stopped at ObjectMaxListing entries.
type ObjectBrowseResult struct {
	Entries   []ObjectEntry `json:"entries,omitempty"`
	Truncated bool          `json:"truncated,omitempty"`
	Data      string        `json:"data,omitempty"`
	Size      int64         `json:"size,omitempty"`
}
