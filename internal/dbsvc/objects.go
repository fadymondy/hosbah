package dbsvc

import (
	"context"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// ObjectInput is one object-browser call on a MinIO service (MH-1113).
type ObjectInput struct {
	Mode      string
	Bucket    string
	Prefix    string
	Key       string
	Data      string
	Recursive bool
}

// Objects runs one object.browse call against a running MinIO service, synchronously. Writes and
// deletes are audited; reads are not.
func (s *Service) Objects(ctx context.Context, q *db.Queries, teamID string, actor Actor, svc db.DataService, in ObjectInput) (agentproto.ObjectBrowseResult, error) {
	if err := usable(svc); err != nil {
		return agentproto.ObjectBrowseResult{}, err
	}
	if svc.Kind != "minio" {
		return agentproto.ObjectBrowseResult{}, invalid("the object browser applies to MinIO storage only")
	}
	if svc.Status != "running" {
		return agentproto.ObjectBrowseResult{}, conflict("the storage service is not running")
	}
	var result agentproto.ObjectBrowseResult
	err := s.withServer(ctx, teamID, svc.ServerID, func(agentKey string, dek []byte) (any, string, error) {
		rootBox, err := s.sealRoot(agentKey, dek, teamID, svc)
		if err != nil {
			return nil, "", err
		}
		p := agentproto.ObjectBrowseParams{ServiceID: svc.ID, Container: contOf(svc), Mode: in.Mode, Bucket: in.Bucket,
			Prefix: in.Prefix, Key: in.Key, Data: in.Data, Recursive: in.Recursive, RootSecret: rootBox}
		if err := p.Validate(); err != nil {
			return nil, "", invalid(err.Error())
		}
		return p, opID(svc.ID, "objects:"+newUUID()), nil
	}, agentproto.KindObjectBrowse, &result)
	if err != nil {
		return agentproto.ObjectBrowseResult{}, err
	}
	if in.Mode == "put" || in.Mode == "delete" {
		_ = tenancy.Record(ctx, q, teamID, actor.UserID, "storage_object."+in.Mode, "data_service", svc.ID,
			auditExtra(actor, map[string]any{"bucket": in.Bucket, "key": in.Key, "recursive": in.Recursive}))
	}
	return result, nil
}

// SplitObjectPath turns a path parameter ("photos/2026/a.png") into a key, refusing what the
// agent would refuse anyway, early and with a 400.
func SplitObjectPath(p string) (string, error) {
	p = strings.TrimPrefix(p, "/")
	if !agentproto.ValidObjectKey(p) {
		return "", invalid("bad object key")
	}
	return p, nil
}

// VolumeFilesInput is one file-browser call on an app's volume.
type VolumeFilesInput struct {
	Mode string
	Path string
	Data string
}

// VolumeFiles runs one volume.files call on the app's server, synchronously. Writes, folders and
// deletes are audited; reads are not.
func (s *Service) VolumeFiles(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, v db.AppVolume, in VolumeFilesInput) (agentproto.ObjectBrowseResult, error) {
	if a.Source == "imported" {
		return agentproto.ObjectBrowseResult{}, invalid("an imported app's volumes are not browsed here")
	}
	var result agentproto.ObjectBrowseResult
	err := s.withServer(ctx, teamID, a.ServerID, func(string, []byte) (any, string, error) {
		p := agentproto.VolumeFilesParams{AppID: a.ID, Volume: agentproto.VolumeName(a.ID, v.Name), Mode: in.Mode, Path: in.Path, Data: in.Data}
		if err := p.Validate(); err != nil {
			return nil, "", invalid(err.Error())
		}
		return p, opID(v.ID, "files:"+newUUID()), nil
	}, agentproto.KindVolumeFiles, &result)
	if err != nil {
		return agentproto.ObjectBrowseResult{}, err
	}
	if in.Mode != "list" && in.Mode != "get" {
		_ = tenancy.Record(ctx, q, teamID, actor.UserID, "volume_file."+in.Mode, "app", a.ID,
			auditExtra(actor, map[string]any{"volume": v.ID, "path": in.Path}))
	}
	return result, nil
}
