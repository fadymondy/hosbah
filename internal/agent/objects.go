package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// ---- object.browse (MH-1113) ------------------------------------------------------------------------

// objectBrowse lists, reads, writes and deletes objects of a MinIO data service with mc inside its
// container. The root key reaches mc through the environment only (MC_HOST_local); bucket and key
// are validated and passed as one argv element each, after the fixed "local/" alias.
func (h *storageHost) objectBrowse(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.ObjectBrowseParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	p.Container = resolveName(oc.Ctx, h.run, kindContainer, p.Container)
	root, err := oc.openSecret(p.ServiceID, "root", p.RootSecret)
	if err != nil {
		return nil, 1, err
	}
	if root["password"] == "" {
		return nil, 2, errors.New("a secret is malformed")
	}
	if !h.running(oc, p.Container) {
		return nil, 1, errors.New("the storage service is not running")
	}
	env := []string{mcHost(root["password"])}
	fail := func(out, msg string) (any, int, error) {
		oc.Info("%s", scrub(clip(strings.TrimSpace(out), 600), root["password"]))
		return nil, 1, errors.New(msg)
	}

	switch p.Mode {
	case "buckets", "list":
		target, base := "local", ""
		if p.Mode == "list" {
			target, base = "local/"+p.Bucket+"/"+p.Prefix, p.Prefix
		}
		out, err := h.mc(oc, p.Container, env, "mc", "ls", "--json", target)
		if err != nil {
			return fail(out, "could not list the storage")
		}
		res := agentproto.ObjectBrowseResult{Entries: []agentproto.ObjectEntry{}}
		sc := bufio.NewScanner(strings.NewReader(out))
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			var e struct {
				Status       string `json:"status"`
				Type         string `json:"type"`
				LastModified string `json:"lastModified"`
				Size         int64  `json:"size"`
				Key          string `json:"key"`
			}
			if json.Unmarshal(sc.Bytes(), &e) != nil || e.Status != "success" || e.Key == "" {
				continue
			}
			if len(res.Entries) >= agentproto.ObjectMaxListing {
				res.Truncated = true
				break
			}
			folder := e.Type == "folder"
			key := base + e.Key
			if p.Mode == "buckets" {
				key = strings.TrimSuffix(e.Key, "/")
			}
			res.Entries = append(res.Entries, agentproto.ObjectEntry{Key: key, Folder: folder, Size: e.Size, Modified: e.LastModified})
		}
		return res, 0, nil

	case "get":
		obj := "local/" + p.Bucket + "/" + p.Key
		out, err := h.mc(oc, p.Container, env, "mc", "stat", "--json", obj)
		if err != nil {
			return fail(out, "the object could not be found")
		}
		var st struct {
			Size int64 `json:"size"`
		}
		if json.Unmarshal([]byte(firstLine(out)), &st) != nil {
			return fail(out, "could not read the object")
		}
		if st.Size > agentproto.ObjectMaxBytes {
			return nil, 1, fmt.Errorf("the object is larger than %d MiB; download it with an S3 client", agentproto.ObjectMaxBytes>>20)
		}
		data, err := h.mc(oc, p.Container, env, "mc", "cat", obj)
		if err != nil {
			return fail(data, "could not read the object")
		}
		return agentproto.ObjectBrowseResult{Data: base64.StdEncoding.EncodeToString([]byte(data)), Size: int64(len(data))}, 0, nil

	case "put":
		if strings.HasSuffix(p.Key, "/") {
			return nil, 2, errors.New("an object key cannot end in /")
		}
		data, err := base64.StdEncoding.DecodeString(p.Data)
		if err != nil || len(data) > agentproto.ObjectMaxBytes {
			return nil, 2, errors.New("the upload is not valid base64 of at most 2 MiB")
		}
		run := h.runIn
		if run == nil {
			run = execRunIn
		}
		full := append([]string{"exec", "-i"}, envArgs(mapEnv(env))...)
		full = append(full, p.Container, "mc", "pipe", "local/"+p.Bucket+"/"+p.Key)
		if out, err := run(oc.Ctx, env, data, "docker", full...); err != nil {
			return fail(string(out)+"\n"+err.Error(), "could not upload the object")
		}
		oc.Info("uploaded %s/%s (%d bytes)", p.Bucket, p.Key, len(data))
		return agentproto.ObjectBrowseResult{Size: int64(len(data))}, 0, nil

	case "delete":
		argv := []string{"mc", "rm", "local/" + p.Bucket + "/" + p.Key}
		if p.Recursive {
			argv = []string{"mc", "rm", "--recursive", "--force", "local/" + p.Bucket + "/" + p.Key}
		}
		if out, err := h.mc(oc, p.Container, env, argv...); err != nil {
			return fail(out, "could not delete the object")
		}
		oc.Info("deleted %s/%s", p.Bucket, p.Key)
		return agentproto.ObjectBrowseResult{}, 0, nil
	}
	return nil, 2, errors.New("unknown mode")
}

// execRunIn is execRun with stdin: an upload's bytes reach `mc pipe` there, never an argv or env.
func execRunIn(ctx context.Context, env []string, stdin []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) //#nosec G204 -- fixed argv from validated params
	cmd.Env = append(append(os.Environ(), "LC_ALL=C"), env...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return out, fmt.Errorf("%s: %s", name, clip(msg, 600))
	}
	return out, nil
}
