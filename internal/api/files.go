package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// A server's filesystem, browsed and edited through its agent. Every operation — reads included,
// since a file can hold a secret — needs AbilityManageServers (owner or admin), the same as
// connecting a server or running a provisioning step.

// FSEntryOut is one entry of a listed directory.
type FSEntryOut struct {
	Name  string    `json:"name"`
	Type  string    `json:"type" enum:"file,dir,symlink,other"`
	Size  int64     `json:"size"`
	Mode  string    `json:"mode"`
	MTime time.Time `json:"mtime"`
	Owner string    `json:"owner,omitempty"`
}

func toFSEntryOut(e agentproto.FSEntry) FSEntryOut {
	return FSEntryOut{Name: e.Name, Type: e.Type, Size: e.Size, Mode: e.Mode, MTime: e.MTime, Owner: e.Owner}
}

// filesQueue resolves the server, checks ability, and queues one fs.* operation.
func (d *deps) filesQueue(ctx context.Context, team, server string,
	queue func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, s db.Server) (db.Operation, error)) (db.Operation, error) {
	var op db.Operation
	err := d.inTeam(ctx, team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
		s, err := serverIn(ctx, q, m, server)
		if err != nil {
			return err
		}
		op, err = queue(q, webActor(id), m, s)
		return actionErr(err, "could not queue the operation")
	})
	return op, err
}

// filesAwait waits for an fs.* operation (after its queuing transaction committed).
func (d *deps) filesAwait(ctx context.Context, op db.Operation, wait time.Duration, out any) error {
	_, err := d.deploys().AwaitResult(ctx, op, wait, out)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, deploy.ErrStillRunning):
		return huma.Error504GatewayTimeout("the server did not answer in time — is its agent connected? (operation " + op.ID + ")")
	}
	return actionErr(err, "the operation failed")
}

func (d *deps) registerFiles(api huma.API) {
	tags := []string{"Files"}
	base := "/api/teams/{team}/servers/{server}/files"
	const wait = 30 * time.Second

	type filesListOut struct {
		Body struct {
			Path      string       `json:"path"`
			Entries   []FSEntryOut `json:"entries"`
			Operation string       `json:"operation"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-server-files", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "List a directory on the server, through its agent. Owner or admin (a file can hold a secret).",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Path string `query:"path" maxLength:"4096"`
	}) (*filesListOut, error) {
		op, err := d.filesQueue(ctx, in.Team, in.Server, func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, s db.Server) (db.Operation, error) {
			return d.deploys().FSList(ctx, q, m.TeamID, a, s, in.Path)
		})
		if err != nil {
			return nil, err
		}
		var res agentproto.FSListResult
		if err := d.filesAwait(ctx, op, wait, &res); err != nil {
			return nil, err
		}
		out := &filesListOut{}
		out.Body.Path, out.Body.Operation = in.Path, op.ID
		out.Body.Entries = make([]FSEntryOut, 0, len(res.Entries))
		for _, e := range res.Entries {
			out.Body.Entries = append(out.Body.Entries, toFSEntryOut(e))
		}
		return out, nil
	})

	type fileContentOut struct {
		Body struct {
			Path      string `json:"path"`
			Content   string `json:"content" doc:"base64."`
			Size      int64  `json:"size"`
			Truncated bool   `json:"truncated"`
			Binary    bool   `json:"binary"`
			Operation string `json:"operation"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "read-server-file", Method: http.MethodGet, Path: base + "/content", Tags: tags,
		Summary: "Read a file's content (base64, at most 1 MiB — set max_bytes lower for a quick peek). Owner or admin.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Path     string `query:"path" maxLength:"4096"`
		MaxBytes int    `query:"max_bytes" minimum:"0" maximum:"1048576"`
	}) (*fileContentOut, error) {
		op, err := d.filesQueue(ctx, in.Team, in.Server, func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, s db.Server) (db.Operation, error) {
			return d.deploys().FSRead(ctx, q, m.TeamID, a, s, in.Path, in.MaxBytes)
		})
		if err != nil {
			return nil, err
		}
		var res agentproto.FSReadResult
		if err := d.filesAwait(ctx, op, wait, &res); err != nil {
			return nil, err
		}
		out := &fileContentOut{}
		out.Body.Path, out.Body.Operation = in.Path, op.ID
		out.Body.Content, out.Body.Size, out.Body.Truncated, out.Body.Binary = res.Content, res.Size, res.Truncated, res.Binary
		return out, nil
	})

	type fileOpOut struct {
		Status int
		Body   struct {
			Operation OperationOut `json:"operation"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "write-server-file", Method: http.MethodPut, Path: base + "/content",
		DefaultStatus: http.StatusAccepted, Tags: tags,
		Summary: "Write a file's content (base64, at most 1 MiB), atomically. Owner or admin; audited.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Body struct {
			Path    string `json:"path" maxLength:"4096"`
			Content string `json:"content" doc:"base64, at most 1 MiB decoded."`
			Mode    string `json:"mode,omitempty" doc:"an octal permission such as 644; empty keeps the file's existing mode."`
		}
	}) (*fileOpOut, error) {
		op, err := d.filesQueue(ctx, in.Team, in.Server, func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, s db.Server) (db.Operation, error) {
			return d.deploys().FSWrite(ctx, q, m.TeamID, a, s, in.Body.Path, in.Body.Content, in.Body.Mode)
		})
		if err != nil {
			return nil, err
		}
		out := &fileOpOut{Status: http.StatusAccepted}
		out.Body.Operation = toOperationOut(op)
		return out, nil
	})

	huma.Register(api, huma.Operation{OperationID: "mkdir-server-file", Method: http.MethodPost, Path: base + "/mkdir",
		DefaultStatus: http.StatusAccepted, Tags: tags,
		Summary: "Make one directory (its parent must already exist). Owner or admin; audited.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Body struct {
			Path string `json:"path" maxLength:"4096"`
		}
	}) (*fileOpOut, error) {
		op, err := d.filesQueue(ctx, in.Team, in.Server, func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, s db.Server) (db.Operation, error) {
			return d.deploys().FSMkdir(ctx, q, m.TeamID, a, s, in.Body.Path)
		})
		if err != nil {
			return nil, err
		}
		out := &fileOpOut{Status: http.StatusAccepted}
		out.Body.Operation = toOperationOut(op)
		return out, nil
	})

	huma.Register(api, huma.Operation{OperationID: "rename-server-file", Method: http.MethodPost, Path: base + "/rename",
		DefaultStatus: http.StatusAccepted, Tags: tags,
		Summary: "Rename or move a file or a directory. Owner or admin; audited.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Body struct {
			From string `json:"from" maxLength:"4096"`
			To   string `json:"to" maxLength:"4096"`
		}
	}) (*fileOpOut, error) {
		op, err := d.filesQueue(ctx, in.Team, in.Server, func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, s db.Server) (db.Operation, error) {
			return d.deploys().FSRename(ctx, q, m.TeamID, a, s, in.Body.From, in.Body.To)
		})
		if err != nil {
			return nil, err
		}
		out := &fileOpOut{Status: http.StatusAccepted}
		out.Body.Operation = toOperationOut(op)
		return out, nil
	})

	huma.Register(api, huma.Operation{OperationID: "delete-server-file", Method: http.MethodDelete, Path: base,
		DefaultStatus: http.StatusAccepted, Tags: tags,
		Summary: "Delete a file, or a directory (recursive: and everything in it — otherwise a non-empty directory is refused). Owner or admin; audited.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Path      string `query:"path" maxLength:"4096"`
		Recursive bool   `query:"recursive"`
	}) (*fileOpOut, error) {
		op, err := d.filesQueue(ctx, in.Team, in.Server, func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, s db.Server) (db.Operation, error) {
			return d.deploys().FSDelete(ctx, q, m.TeamID, a, s, in.Path, in.Recursive)
		})
		if err != nil {
			return nil, err
		}
		out := &fileOpOut{Status: http.StatusAccepted}
		out.Body.Operation = toOperationOut(op)
		return out, nil
	})
}
