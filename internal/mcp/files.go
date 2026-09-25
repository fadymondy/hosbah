package mcp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

func encodeFSContent(b []byte) string        { return base64.StdEncoding.EncodeToString(b) }
func decodeFSContent(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }

// FileTools: a server's filesystem, browsed and edited through its agent (no shell). Every
// operation needs AbilityManageServers (owner or admin) — a file can hold a secret, whether it is
// only being read or being changed.
func FileTools() []Tool {
	serverArg := str("Server name or id")
	pathArg := str("Absolute path")

	return []Tool{
		{
			Name: "server_files_list", Scope: scopes.Read,
			Description: "List a directory on a server, through its agent (name, type, size, mode, modified, owner). Owner or admin.",
			InputSchema: schema([]string{"server", "path"}, map[string]any{"server": serverArg, "path": pathArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return filesRead(ctx, env, args, func(s *deploy.Service, srv db.Server) (db.Operation, error) {
					return s.FSList(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv, args.Str("path"))
				}, func(s *deploy.Service, op db.Operation) (string, error) {
					var r agentproto.FSListResult
					if _, err := s.AwaitResult(ctx, op, 30*time.Second, &r); err != nil {
						return "", err
					}
					if len(r.Entries) == 0 {
						return "(empty)", nil
					}
					var b strings.Builder
					for _, e := range r.Entries {
						fmt.Fprintf(&b, "  %-4s %10d  %-10s %s  %s\n", e.Type, e.Size, e.Mode, e.MTime.UTC().Format("2006-01-02 15:04"), e.Name)
					}
					return b.String(), nil
				})
			},
		},
		{
			Name: "server_file_read", Scope: scopes.Read,
			Description: "Read a file's content on a server, through its agent (at most 1 MiB; a binary or larger file is reported, not dumped). Owner or admin.",
			InputSchema: schema([]string{"server", "path"}, map[string]any{"server": serverArg, "path": pathArg,
				"max_bytes": map[string]any{"type": "integer", "description": "Cap what is relayed (default 1 MiB)", "maximum": agentproto.MaxFSBytes}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return filesRead(ctx, env, args, func(s *deploy.Service, srv db.Server) (db.Operation, error) {
					return s.FSRead(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv, args.Str("path"), args.Int("max_bytes", 0, 0, agentproto.MaxFSBytes))
				}, func(s *deploy.Service, op db.Operation) (string, error) {
					var r agentproto.FSReadResult
					if _, err := s.AwaitResult(ctx, op, 30*time.Second, &r); err != nil {
						return "", err
					}
					if r.Binary {
						return fmt.Sprintf("%s looks binary (%d bytes) — not shown as text.", args.Str("path"), r.Size), nil
					}
					data, err := decodeFSContent(r.Content)
					if err != nil {
						return "", err
					}
					text := string(data)
					if r.Truncated {
						text += fmt.Sprintf("\n… truncated (%d bytes total; raise max_bytes to see more)", r.Size)
					}
					return text, nil
				})
			},
		},
		{
			Name: "server_file_write", Scope: scopes.Write,
			Description: "Write a file's content on a server, through its agent — atomic, keeps the file's existing mode unless mode is given. " +
				"At most 1 MiB. Owner or admin; audited.",
			InputSchema: schema([]string{"server", "path", "content"}, map[string]any{"server": serverArg, "path": pathArg,
				"content": str("The file's new content (plain text)"), "mode": str("An octal permission such as 644 (optional)")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				content := encodeFSContent([]byte(args.Str("content")))
				op, err := s.FSWrite(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv, args.Str("path"), content, args.Str("mode"))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Writing %s on %s (operation %s).\n", args.Str("path"), srv.Name, op.ID), nil
			},
		},
		{
			Name: "server_file_mkdir", Scope: scopes.Write,
			Description: "Make one directory on a server, through its agent (its parent must already exist). Owner or admin; audited.",
			InputSchema: schema([]string{"server", "path"}, map[string]any{"server": serverArg, "path": pathArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				op, err := s.FSMkdir(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv, args.Str("path"))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Creating %s on %s (operation %s).\n", args.Str("path"), srv.Name, op.ID), nil
			},
		},
		{
			Name: "server_file_rename", Scope: scopes.Write,
			Description: "Rename or move a file or a directory on a server, through its agent. Owner or admin; audited.",
			InputSchema: schema([]string{"server", "from", "to"}, map[string]any{"server": serverArg,
				"from": str("Absolute path"), "to": str("Absolute path")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				op, err := s.FSRename(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv, args.Str("from"), args.Str("to"))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Renaming %s to %s on %s (operation %s).\n", args.Str("from"), args.Str("to"), srv.Name, op.ID), nil
			},
		},
		{
			Name: "server_file_delete", Scope: scopes.Write,
			Description: "Delete a file, or a directory, on a server, through its agent. recursive=true also removes a non-empty directory " +
				"and everything in it (otherwise it is refused). Owner or admin; audited.",
			InputSchema: schema([]string{"server", "path"}, map[string]any{"server": serverArg, "path": pathArg,
				"recursive": map[string]any{"type": "boolean", "description": "Remove a non-empty directory and everything in it"}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				op, err := s.FSDelete(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv, args.Str("path"), args.Bool("recursive"))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Deleting %s on %s (operation %s).\n", args.Str("path"), srv.Name, op.ID), nil
			},
		},
	}
}

// filesRead queues a read-only fs.* operation and answers once the server has (after commit).
func filesRead(ctx context.Context, env *Env, args Args, queue func(*deploy.Service, db.Server) (db.Operation, error),
	answer func(*deploy.Service, db.Operation) (string, error)) (string, error) {
	if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
		return "", err
	}
	s, err := deploys(env)
	if err != nil {
		return "", err
	}
	srv, err := findServer(ctx, env, args)
	if err != nil {
		return "", err
	}
	op, err := queue(s, srv)
	if err != nil {
		return "", err
	}
	env.Then = func(context.Context) (string, error) {
		out, err := answer(s, op)
		if errors.Is(err, deploy.ErrStillRunning) {
			return fmt.Sprintf("The server has not answered yet (operation %s).", op.ID), nil
		}
		return out, err
	}
	return "", nil
}
