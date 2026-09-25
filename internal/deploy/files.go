package deploy

import (
	"context"
	"encoding/json"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
A server's filesystem, browsed and edited through its agent (fs.* — no shell, os APIs only; see
internal/agentproto/files.go and internal/agent/files.go). Files can hold secrets — connection
strings, .env files, keys someone put there by hand — so every operation, reads included, needs
tenancy.AbilityManageServers, the same as connecting a server or running a provisioning step. Every
write, delete, mkdir and rename is audited like other server actions.
*/

func (s *Service) filesServer(srv db.Server) error {
	if srv.Status != "enrolled" || srv.AgentPublicKey == nil {
		return conflict("that server's agent has not enrolled yet")
	}
	return nil
}

func (s *Service) filesEnqueue(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.Server, kind string, params any) (db.Operation, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return db.Operation{}, err
	}
	op, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: srv.ID, Kind: kind, Params: raw, CreatedBy: actorID(actor)})
	return op, err
}

// FSList lists a directory's entries.
func (s *Service) FSList(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.Server, path string) (db.Operation, error) {
	if err := s.filesServer(srv); err != nil {
		return db.Operation{}, err
	}
	p := agentproto.FSListParams{Path: path}
	if err := p.Validate(); err != nil {
		return db.Operation{}, invalid(err.Error())
	}
	return s.filesEnqueue(ctx, q, teamID, actor, srv, agentproto.KindFSList, p)
}

// FSRead reads a file's content (base64, capped).
func (s *Service) FSRead(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.Server, path string, maxBytes int) (db.Operation, error) {
	if err := s.filesServer(srv); err != nil {
		return db.Operation{}, err
	}
	p := agentproto.FSReadParams{Path: path, MaxBytes: maxBytes}
	if err := p.Validate(); err != nil {
		return db.Operation{}, invalid(err.Error())
	}
	return s.filesEnqueue(ctx, q, teamID, actor, srv, agentproto.KindFSRead, p)
}

// FSWrite writes a file's content (base64, atomic on the agent). Audited (never with the content).
func (s *Service) FSWrite(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.Server, path, content, mode string) (db.Operation, error) {
	if err := s.filesServer(srv); err != nil {
		return db.Operation{}, err
	}
	p := agentproto.FSWriteParams{Path: path, Content: content, Mode: mode}
	if err := p.Validate(); err != nil {
		return db.Operation{}, invalid(err.Error())
	}
	op, err := s.filesEnqueue(ctx, q, teamID, actor, srv, agentproto.KindFSWrite, p)
	if err != nil {
		return db.Operation{}, err
	}
	return op, tenancy.Record(ctx, q, teamID, actor.UserID, "server.file_write", "server", srv.ID,
		actor.audit(map[string]any{"name": srv.Name, "path": path, "operation": op.ID}))
}

// FSMkdir makes one directory. Audited.
func (s *Service) FSMkdir(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.Server, path string) (db.Operation, error) {
	if err := s.filesServer(srv); err != nil {
		return db.Operation{}, err
	}
	p := agentproto.FSMkdirParams{Path: path}
	if err := p.Validate(); err != nil {
		return db.Operation{}, invalid(err.Error())
	}
	op, err := s.filesEnqueue(ctx, q, teamID, actor, srv, agentproto.KindFSMkdir, p)
	if err != nil {
		return db.Operation{}, err
	}
	return op, tenancy.Record(ctx, q, teamID, actor.UserID, "server.file_mkdir", "server", srv.ID,
		actor.audit(map[string]any{"name": srv.Name, "path": path, "operation": op.ID}))
}

// FSDelete removes a file, or a directory (recursive: and everything in it). Audited.
func (s *Service) FSDelete(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.Server, path string, recursive bool) (db.Operation, error) {
	if err := s.filesServer(srv); err != nil {
		return db.Operation{}, err
	}
	p := agentproto.FSDeleteParams{Path: path, Recursive: recursive}
	if err := p.Validate(); err != nil {
		return db.Operation{}, invalid(err.Error())
	}
	op, err := s.filesEnqueue(ctx, q, teamID, actor, srv, agentproto.KindFSDelete, p)
	if err != nil {
		return db.Operation{}, err
	}
	return op, tenancy.Record(ctx, q, teamID, actor.UserID, "server.file_delete", "server", srv.ID,
		actor.audit(map[string]any{"name": srv.Name, "path": path, "recursive": recursive, "operation": op.ID}))
}

// FSRename renames or moves a file or a directory. Audited.
func (s *Service) FSRename(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.Server, from, to string) (db.Operation, error) {
	if err := s.filesServer(srv); err != nil {
		return db.Operation{}, err
	}
	p := agentproto.FSRenameParams{From: from, To: to}
	if err := p.Validate(); err != nil {
		return db.Operation{}, invalid(err.Error())
	}
	op, err := s.filesEnqueue(ctx, q, teamID, actor, srv, agentproto.KindFSRename, p)
	if err != nil {
		return db.Operation{}, err
	}
	return op, tenancy.Record(ctx, q, teamID, actor.UserID, "server.file_rename", "server", srv.ID,
		actor.audit(map[string]any{"name": srv.Name, "from": from, "to": to, "operation": op.ID}))
}
