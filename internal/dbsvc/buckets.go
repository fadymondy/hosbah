package dbsvc

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
App buckets: an S3 bucket of a site on one of the team's MinIO data services, with an access key
whose MinIO policy reaches that one bucket only (agent op bucket.create — unlike db.create's
built-in readwrite policy, which spans the service).

How the key's secret travels, exactly like a database user's password: it is generated HERE, sealed
with the team DEK bound to (service, bucket) for the row, and sealed again to the target agent's own
key (a box, context DBSecretContext(service, "bucket:<bucket>")) for the operation — so
operations.params holds two boxes and no plaintext, and the agent hands it to mc through the
environment, never an argv or a log line. The app receives it as the sealed env var
<PREFIX>_SECRET_ACCESS_KEY (deploy.SetEnv), re-sealed per deploy like every env value.
*/

const (
	// MaxBucketsPerApp bounds an app's buckets.
	MaxBucketsPerApp = 5
	// DefaultBucketPrefix is the env prefix when the caller gives none.
	DefaultBucketPrefix = "S3"

	BucketCreating = "creating"
	BucketReady    = "ready"
	BucketFailed   = "failed"
	BucketRemoving = "removing"

	// bucketBusyFor: a creating/removing bucket younger than this is left alone; older, the worker
	// that owned it is taken to be gone and a removal may take over.
	bucketBusyFor = 10 * time.Minute
)

var bucketPrefixRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,20}$`)

// bucketSealContext binds the stored secret to its service, bucket and field.
func bucketSealContext(serviceID, bucket string) string {
	return "appbucket:" + serviceID + ":" + bucket + ":secret"
}

// BucketEnvNames are the variables a bucket sets on its app, in a fixed order.
func BucketEnvNames(prefix string) []string {
	return []string{prefix + "_ENDPOINT", prefix + "_BUCKET", prefix + "_ACCESS_KEY_ID", prefix + "_SECRET_ACCESS_KEY",
		prefix + "_REGION", prefix + "_USE_PATH_STYLE"}
}

// Bucket is an app bucket as the API shows it. The secret is never part of it.
type Bucket struct {
	ID        string    `json:"id"`
	ServiceID string    `json:"service_id"`
	Service   string    `json:"service" doc:"The MinIO data service's name."`
	Bucket    string    `json:"bucket"`
	AccessKey string    `json:"access_key" doc:"The key's id. Its secret is only ever in the app's sealed <env_prefix>_SECRET_ACCESS_KEY."`
	EnvPrefix string    `json:"env_prefix"`
	Env       []string  `json:"env" doc:"The variables this bucket set on the app."`
	Status    string    `json:"status" enum:"creating,ready,failed,removing"`
	Error     *string   `json:"error"`
	CreatedAt time.Time `json:"created_at"`
}

func bucketView(b db.AppBucket, serviceName string) Bucket {
	return Bucket{ID: b.ID, ServiceID: b.ServiceID, Service: serviceName, Bucket: b.Bucket, AccessKey: b.AccessKey, EnvPrefix: b.EnvPrefix,
		Env: BucketEnvNames(b.EnvPrefix), Status: b.Status, Error: b.Error, CreatedAt: b.CreatedAt}
}

// ListBuckets is the app's buckets (team scope).
func ListBuckets(ctx context.Context, q *db.Queries, teamID string, a db.App) ([]Bucket, error) {
	rows, err := q.ListBucketsOfApp(ctx, db.ListBucketsOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	out := make([]Bucket, 0, len(rows))
	for _, b := range rows {
		if _, ok := names[b.ServiceID]; !ok {
			if svc, err := q.DataServiceInTeam(ctx, db.DataServiceInTeamParams{ID: b.ServiceID, TeamID: teamID}); err == nil {
				names[b.ServiceID] = svc.Name
			}
		}
		out = append(out, bucketView(b, names[b.ServiceID]))
	}
	return out, nil
}

// FindBucket resolves one bucket of an app by id or bucket name.
func FindBucket(ctx context.Context, q *db.Queries, teamID string, a db.App, ref string) (db.AppBucket, error) {
	b, err := q.BucketOfApp(ctx, db.BucketOfAppParams{AppID: a.ID, TeamID: teamID, Ref: strings.TrimSpace(ref)})
	if err != nil {
		return db.AppBucket{}, notFound("bucket")
	}
	return b, nil
}

// bucketService checks that svc can hold app buckets and returns its server (with the agent's key).
func (s *Service) bucketService(ctx context.Context, q *db.Queries, teamID string, svc db.DataService) (db.Server, error) {
	if svc.Kind != "minio" {
		return db.Server{}, invalid(svc.Name + " is a " + svc.Kind + " service — buckets live on a MinIO data service")
	}
	if isImported(svc) {
		return db.Server{}, invalid("buckets need a MinIO service Hosbah provisioned — " + svc.Name + " is imported")
	}
	if svc.Status != "running" || svc.Host == nil || svc.Port == nil {
		return db.Server{}, conflict("the service is not running yet")
	}
	srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: svc.ServerID, TeamID: teamID})
	if err != nil {
		return db.Server{}, notFound("server")
	}
	if srv.AgentPublicKey == nil || *srv.AgentPublicKey == "" {
		return db.Server{}, conflict("the service's server has no enrolled agent")
	}
	return srv, nil
}

// CreateBucket records the bucket, sets its six sealed variables on the app and queues bucket.create
// — all in the caller's transaction, so a refused request leaves nothing. The bucket is 'creating'
// until the agent answers (FollowBucket, after the commit). The variables apply from the app's next
// deploy or restart. A variable that already exists on the app is never overwritten: 409.
func (s *Service) CreateBucket(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, serviceRef, bucket, prefix string) (Bucket, db.Operation, error) {
	none := db.Operation{}
	if deploy.IsImported(a) {
		return Bucket{}, none, invalid("storage belongs to a site Hosbah deploys — " + a.Name + " is an imported app")
	}
	bucket = strings.TrimSpace(bucket)
	if !agentproto.ValidBucket(bucket) {
		return Bucket{}, none, invalid("a bucket name is 3 to 63 lowercase letters, digits and dashes, starting and ending with a letter or digit")
	}
	if prefix = strings.TrimSpace(prefix); prefix == "" {
		prefix = DefaultBucketPrefix
	}
	if !bucketPrefixRE.MatchString(prefix) {
		return Bucket{}, none, invalid("env_prefix is an upper-case letter, then up to 20 upper-case letters, digits and _")
	}
	svc, err := ServiceByRef(ctx, q, teamID, serviceRef)
	if err != nil {
		return Bucket{}, none, err
	}
	srv, err := s.bucketService(ctx, q, teamID, svc)
	if err != nil {
		return Bucket{}, none, err
	}
	existing, err := q.ListBucketsOfApp(ctx, db.ListBucketsOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return Bucket{}, none, err
	}
	if len(existing) >= MaxBucketsPerApp {
		return Bucket{}, none, conflict(fmt.Sprintf("an app has at most %d buckets", MaxBucketsPerApp))
	}
	if _, err := q.BucketOnService(ctx, db.BucketOnServiceParams{ServiceID: svc.ID, TeamID: teamID, Bucket: bucket}); err == nil {
		return Bucket{}, none, conflict("bucket " + bucket + " already exists on " + svc.Name)
	}
	dbs, err := q.ListDatabasesOfService(ctx, db.ListDatabasesOfServiceParams{DataServiceID: svc.ID, TeamID: teamID})
	if err != nil {
		return Bucket{}, none, err
	}
	for _, d := range dbs {
		if agentproto.BucketName(d.Name) == bucket {
			return Bucket{}, none, conflict("bucket " + bucket + " already exists on " + svc.Name)
		}
	}
	names := BucketEnvNames(prefix)
	for _, n := range names {
		if _, err := q.EnvVarInApp(ctx, db.EnvVarInAppParams{AppID: a.ID, TeamID: teamID, Name: n}); err == nil {
			return Bucket{}, none, conflict(n + " is already set on " + a.Name + " — choose another env_prefix (a bucket never overwrites a variable)")
		}
	}

	dek, err := s.ops.TeamDEK(ctx, q, teamID)
	if err != nil {
		return Bucket{}, none, err
	}
	accessKey, secret := "cb"+randPassword(18), randPassword(40)
	sealed, err := crypto.Seal(dek, teamID, bucketSealContext(svc.ID, bucket), []byte(secret))
	if err != nil {
		return Bucket{}, none, err
	}
	row, err := q.InsertAppBucket(ctx, db.InsertAppBucketParams{ID: newUUID(), TeamID: teamID, AppID: a.ID, ServiceID: svc.ID, Bucket: bucket,
		AccessKey: accessKey, SecretSealed: sealed, EnvPrefix: prefix})
	if err != nil {
		return Bucket{}, none, conflict("bucket " + bucket + " or env_prefix " + prefix + " is already in use")
	}
	endpoint := fmt.Sprintf("http://%s:%d", derefStr(svc.Host), derefInt(svc.Port))
	for i, v := range []string{endpoint, bucket, accessKey, secret, "us-east-1", "true"} {
		if err := s.deploy.SetEnv(ctx, q, teamID, actor, a, names[i], v); err != nil {
			return Bucket{}, none, err
		}
	}

	rootBox, err := s.sealRoot(*srv.AgentPublicKey, dek, teamID, svc)
	if err != nil {
		return Bucket{}, none, err
	}
	keyBox, err := agentproto.SealBox(*srv.AgentPublicKey, agentproto.DBSecretContext(svc.ID, agentproto.BucketSecretPurpose(bucket)),
		mustJSON(map[string]string{"secret": secret}))
	if err != nil {
		return Bucket{}, none, err
	}
	p := agentproto.BucketCreateParams{ServiceID: svc.ID, Container: contOf(svc), Bucket: bucket, AccessKey: accessKey,
		Policy: agentproto.BucketPolicyName(accessKey), Secret: keyBox, RootSecret: rootBox}
	if err := p.Validate(); err != nil {
		return Bucket{}, none, invalid(err.Error())
	}
	op, _, err := s.ops.Enqueue(ctx, q, opsSpecWithID(opID(row.ID, "bucket-create"), teamID, srv.ID, agentproto.KindBucketCreate, mustJSON(p), actor))
	if err != nil {
		return Bucket{}, none, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "storage.bucket_created", "app", a.ID, auditExtra(actor, map[string]any{
		"bucket": bucket, "service": svc.ID, "env_prefix": prefix, "access_key": accessKey, "operation": op.ID})); err != nil {
		return Bucket{}, none, err
	}
	return bucketView(row, svc.Name), op, nil
}

func opsSpecWithID(id, teamID, serverID, kind string, params []byte, actor Actor) ops.EnqueueSpec {
	sp := opsSpec(teamID, serverID, kind, params, actor)
	sp.OpID = id
	return sp
}

// FollowBucket waits, in the background, for a bucket.create queued by CreateBucket and records how
// it ended. Call it after the request's transaction committed.
func (s *Service) FollowBucket(teamID, bucketID string, op db.Operation) {
	s.spawn(func(ctx context.Context) {
		done, err := s.awaitVisible(ctx, op)
		status, errStr := BucketReady, (*string)(nil)
		switch {
		case err != nil:
			status, errStr = BucketFailed, strptr(err.Error())
		case done.Status != "ok":
			status, errStr = BucketFailed, strptr(opError(done))
		}
		_ = s.sys(ctx, teamID, func(q *db.Queries) error {
			_, err := q.SetAppBucketStatus(ctx, db.SetAppBucketStatusParams{ID: bucketID, TeamID: teamID, Status: status, Error: errStr, From: BucketCreating})
			return err
		})
	})
}

// awaitVisible is ops.Await, tolerant of an operation whose transaction commits a moment later.
func (s *Service) awaitVisible(ctx context.Context, op db.Operation) (db.Operation, error) {
	deadline := time.Now().Add(30 * time.Second)
	for {
		done, err := s.ops.Await(ctx, op.ID, op.ServerID, nil)
		if err == nil || ctx.Err() != nil || time.Now().After(deadline) {
			return done, err
		}
		select {
		case <-ctx.Done():
			return done, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func opError(op db.Operation) string {
	if op.Error != nil && *op.Error != "" {
		return *op.Error
	}
	return op.Status
}

// BucketRemoval is a removal that was begun: what FinishBucketRemoval needs once the agent answered.
type BucketRemoval struct {
	Bucket     db.AppBucket
	Operation  db.Operation // zero: nothing to wait for (a bucket that never came to exist)
	DeleteData bool
	Force      bool
}

// BeginBucketRemoval checks the request, marks the bucket 'removing' and queues bucket.remove in the
// caller's transaction. The caller commits, waits for the operation (AwaitBucketRemoval) and then
// calls FinishBucketRemoval in a new transaction. Nothing of the app changes before the agent has
// answered: a bucket that is not empty (without force) leaves key, bucket and variables as they were.
func (s *Service) BeginBucketRemoval(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, b db.AppBucket, confirm string, deleteData, force bool) (BucketRemoval, error) {
	rm := BucketRemoval{Bucket: b, DeleteData: deleteData, Force: force}
	if deploy.IsImported(a) {
		return rm, invalid("storage belongs to a site Hosbah deploys — " + a.Name + " is an imported app")
	}
	if confirm != b.Bucket {
		return rm, invalid("confirm must be the bucket's name: " + b.Bucket)
	}
	if force && !deleteData {
		return rm, invalid("force applies to delete_data only")
	}
	if (b.Status == BucketCreating || b.Status == BucketRemoving) && time.Since(b.UpdatedAt) < bucketBusyFor {
		return rm, conflict("the bucket is still " + b.Status + " — try again when that has finished")
	}
	svc, err := q.DataServiceInTeam(ctx, db.DataServiceInTeamParams{ID: b.ServiceID, TeamID: teamID})
	if err != nil {
		return rm, notFound("data service")
	}
	srv, err := s.bucketService(ctx, q, teamID, svc)
	if err != nil {
		if b.Status == BucketFailed && !deleteData {
			return rm, nil // it never came to exist, and its service cannot be asked: only the record goes
		}
		return rm, err
	}
	if !s.ops.Connected(srv.ID) && !ops.Online(srv.LastSeenAt, time.Now()) {
		return rm, conflict(srv.Name + " is offline — the key can be revoked once its agent is connected")
	}
	dek, err := s.ops.TeamDEK(ctx, q, teamID)
	if err != nil {
		return rm, err
	}
	rootBox, err := s.sealRoot(*srv.AgentPublicKey, dek, teamID, svc)
	if err != nil {
		return rm, err
	}
	p := agentproto.BucketRemoveParams{ServiceID: svc.ID, Container: contOf(svc), Bucket: b.Bucket, AccessKey: b.AccessKey,
		Policy: agentproto.BucketPolicyName(b.AccessKey), DeleteData: deleteData, Force: force, RootSecret: rootBox}
	if err := p.Validate(); err != nil {
		return rm, invalid(err.Error())
	}
	if n, err := q.SetAppBucketStatus(ctx, db.SetAppBucketStatusParams{ID: b.ID, TeamID: teamID, Status: BucketRemoving, From: b.Status}); err != nil || n == 0 {
		return rm, conflict("the bucket changed meanwhile — try again")
	}
	sp := opsSpec(teamID, srv.ID, agentproto.KindBucketRemove, mustJSON(p), actor)
	sp.QueueTTL = 2 * time.Minute // a removal nobody waits for any more must not run an hour later
	rm.Operation, _, err = s.ops.Enqueue(ctx, q, sp)
	return rm, err
}

// BucketRemoveWait is how long a DELETE waits for the agent.
const BucketRemoveWait = 90 * time.Second

// AwaitBucketRemoval waits for the removal's operation. "" means it succeeded (or there was none).
func (s *Service) AwaitBucketRemoval(ctx context.Context, rm BucketRemoval) (failure string) {
	if rm.Operation.ID == "" {
		return ""
	}
	wctx, cancel := context.WithTimeout(ctx, BucketRemoveWait)
	defer cancel()
	done, err := s.awaitVisible(wctx, rm.Operation)
	switch {
	case err != nil:
		return "the server did not answer in time (operation " + rm.Operation.ID + ")"
	case done.Status != "ok":
		return opError(done)
	}
	return ""
}

// FinishBucketRemoval ends a removal in a new team transaction. failure "" : the variables the
// bucket set are unset, the record goes, and it is audited. Otherwise the bucket is as it was
// (status back to what it had) and the failure is a 409.
func (s *Service) FinishBucketRemoval(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, rm BucketRemoval, failure string) error {
	b := rm.Bucket
	if failure != "" {
		back := b.Status
		if back != BucketFailed {
			back = BucketReady
		}
		_, err := q.SetAppBucketStatus(ctx, db.SetAppBucketStatusParams{ID: b.ID, TeamID: teamID, Status: back, Error: strptr(failure), From: BucketRemoving})
		return err // the caller answers with BucketRemovalRefused AFTER this transaction committed
	}
	for _, n := range BucketEnvNames(b.EnvPrefix) {
		if _, err := q.DeleteEnvVarInApp(ctx, db.DeleteEnvVarInAppParams{AppID: a.ID, TeamID: teamID, Name: n}); err != nil {
			return err
		}
	}
	if _, err := q.DeleteAppBucketInTeam(ctx, db.DeleteAppBucketInTeamParams{ID: b.ID, TeamID: teamID}); err != nil {
		return err
	}
	extra := map[string]any{"bucket": b.Bucket, "service": b.ServiceID, "delete_data": rm.DeleteData, "force": rm.Force, "env_prefix": b.EnvPrefix}
	if rm.Operation.ID != "" {
		extra["operation"] = rm.Operation.ID
	}
	return tenancy.Record(ctx, q, teamID, actor.UserID, "storage.bucket_removed", "app", a.ID, auditExtra(actor, extra))
}

// refuseServiceWithBuckets: a MinIO service that apps still hold buckets on is not deleted.
func refuseServiceWithBuckets(ctx context.Context, q *db.Queries, teamID string, svc db.DataService) error {
	n, err := q.CountBucketsOfService(ctx, db.CountBucketsOfServiceParams{ServiceID: svc.ID, TeamID: teamID})
	if err != nil {
		return err
	}
	if n > 0 {
		return conflict(fmt.Sprintf("%d app bucket(s) live on %s — remove them from their apps' storage first", n, svc.Name))
	}
	return nil
}


// BucketRemovalRefused is the 409 of a removal the agent did not carry out.
func BucketRemovalRefused(failure string) error {
	if strings.Contains(failure, agentproto.ErrBucketNotEmpty) {
		return conflict(failure)
	}
	return conflict("the bucket was not removed: " + failure + " — try again")
}
