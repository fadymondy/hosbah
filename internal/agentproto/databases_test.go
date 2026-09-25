package agentproto

import "testing"

const testSvcID = "00000000-0000-4000-8000-0000000000d1"
const testBkID = "00000000-0000-4000-8000-0000000000b1"

func TestDBImageAndPort(t *testing.T) {
	if DBImage("postgres", "16") != "postgres:16" || DBInternalPort("postgres") != 5432 {
		t.Fatal("postgres image/port")
	}
	if DBImage("redis", "7.2-alpine") != "redis:7.2-alpine" || DBInternalPort("redis") != 6379 {
		t.Fatal("redis image/port")
	}
	if DBImage("nope", "1") != "" {
		t.Fatal("unknown kind should have no image")
	}
}

func TestValidDBName(t *testing.T) {
	for _, ok := range []string{"app", "my_db", "_x", "a1_2"} {
		if !ValidDBName(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "1db", "Up", "a-b", "drop table", "a;b"} {
		if ValidDBName(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestProvisionValidate(t *testing.T) {
	base := DBProvisionParams{
		ServiceID: testSvcID, Name: "pg1", Kind: "postgres", Version: "16",
		Image: "postgres:16", Container: "hosbah-db-pg1", Volume: "hosbah-dbdata-pg1",
		Bind: "10.0.0.5", Port: 5432, RootSecret: "cab1:abc",
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid provision rejected: %v", err)
	}
	pub := base
	pub.Bind = "0.0.0.0"
	if pub.Validate() == nil {
		t.Fatal("binding to 0.0.0.0 must be refused")
	}
	wrong := base
	wrong.Image = "postgres:15"
	if wrong.Validate() == nil {
		t.Fatal("image must match kind:version")
	}
	noSecret := base
	noSecret.RootSecret = ""
	if noSecret.Validate() == nil {
		t.Fatal("postgres needs a root secret")
	}
}

func TestCreateValidateRefusesRedis(t *testing.T) {
	p := DBCreateParams{ServiceID: testSvcID, Kind: "redis", Database: "app", User: "app", Grants: "all", RootSecret: "x", UserSecret: "y"}
	if p.Validate() == nil {
		t.Fatal("db.create must be refused for redis")
	}
}

func TestDBUserValidate(t *testing.T) {
	base := DBUserParams{ServiceID: testSvcID, Kind: "postgres", Container: "hosbah-db-pg1", Action: "create",
		Database: "app", User: "app_ro", Grants: "readonly", RootSecret: "r", UserSecret: "u"}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid db.user create rejected: %v", err)
	}
	noUserSecret := base
	noUserSecret.UserSecret = ""
	if noUserSecret.Validate() == nil {
		t.Fatal("create needs a user secret")
	}
	badGrants := base
	badGrants.Grants = "superuser"
	if badGrants.Validate() == nil {
		t.Fatal("create must reject unknown grants")
	}
	rotate := DBUserParams{ServiceID: testSvcID, Kind: "postgres", Container: "hosbah-db-pg1", Action: "rotate", User: "app_ro", RootSecret: "r", UserSecret: "u2"}
	if err := rotate.Validate(); err != nil {
		t.Fatalf("valid rotate rejected: %v", err)
	}
	rotate.UserSecret = ""
	if rotate.Validate() == nil {
		t.Fatal("rotate needs a new user secret")
	}
	del := DBUserParams{ServiceID: testSvcID, Kind: "postgres", Container: "hosbah-db-pg1", Action: "delete", User: "app_ro", RootSecret: "r"}
	if err := del.Validate(); err != nil {
		t.Fatalf("valid delete rejected: %v", err)
	}
	grant := DBUserParams{ServiceID: testSvcID, Kind: "mysql", Container: "hosbah-db-my1", Action: "grant", User: "app_rw", Database: "app", Grants: "readwrite", RootSecret: "r"}
	if err := grant.Validate(); err != nil {
		t.Fatalf("valid grant rejected: %v", err)
	}
	grant.Database = ""
	if grant.Validate() == nil {
		t.Fatal("grant needs a database")
	}
	revoke := DBUserParams{ServiceID: testSvcID, Kind: "mysql", Container: "hosbah-db-my1", Action: "revoke", User: "app_rw", Database: "app", RootSecret: "r"}
	if err := revoke.Validate(); err != nil {
		t.Fatalf("valid revoke rejected: %v", err)
	}
	redis := base
	redis.Kind = "redis"
	if redis.Validate() == nil {
		t.Fatal("db.user must be refused for redis")
	}
	badAction := base
	badAction.Action = "drop_everything"
	if badAction.Validate() == nil {
		t.Fatal("db.user must reject an unknown action")
	}
}

// TestDBExecTargetValidate covers validDBExecTarget across every params struct it guards: a
// container (implicit or explicit target "container"), a native LXC guest, a QEMU guest (refused),
// and an unknown target string (refused).
func TestDBExecTargetValidate(t *testing.T) {
	base := DBUserParams{ServiceID: testSvcID, Kind: "postgres", Action: "delete", User: "app_ro", RootSecret: "r"}

	container := base
	container.Container = "hosbah-db-pg1"
	if err := container.Validate(); err != nil {
		t.Fatalf("a plain container target: %v", err)
	}

	explicitContainer := base
	explicitContainer.Target, explicitContainer.Container = TargetContainer, "hosbah-db-pg1"
	if err := explicitContainer.Validate(); err != nil {
		t.Fatalf("target=container with a container: %v", err)
	}

	noContainer := base
	if noContainer.Validate() == nil {
		t.Fatal("a container target with no container name must be refused")
	}

	nativeGuest := base
	nativeGuest.Target, nativeGuest.VMID, nativeGuest.GuestType = TargetGuest, "102", "lxc"
	if err := nativeGuest.Validate(); err != nil {
		t.Fatalf("a native lxc guest target: %v", err)
	}

	dockerInGuest := base
	dockerInGuest.Target, dockerInGuest.VMID, dockerInGuest.GuestType, dockerInGuest.Container = TargetGuest, "102", "lxc", "hosbah-db-pg1"
	if err := dockerInGuest.Validate(); err != nil {
		t.Fatalf("a docker-in-guest target: %v", err)
	}

	noVMID := base
	noVMID.Target, noVMID.GuestType = TargetGuest, "lxc"
	if noVMID.Validate() == nil {
		t.Fatal("a guest target with no vmid must be refused")
	}

	qemu := base
	qemu.Target, qemu.VMID, qemu.GuestType = TargetGuest, "102", "qemu"
	if qemu.Validate() == nil {
		t.Fatal("a QEMU guest must be refused")
	}

	badTarget := base
	badTarget.Target, badTarget.Container = "somewhere-else", "hosbah-db-pg1"
	if badTarget.Validate() == nil {
		t.Fatal("an unknown target must be refused")
	}
}

// TestDBCreateValidateRefusesMinioInGuest: minio has no client that runs through pct exec.
func TestDBCreateValidateRefusesMinioInGuest(t *testing.T) {
	p := DBCreateParams{ServiceID: testSvcID, Kind: "minio", Target: TargetGuest, VMID: "102", GuestType: "lxc",
		Database: "b1", User: "k1", Grants: "all", RootSecret: "x", UserSecret: "y"}
	if p.Validate() == nil {
		t.Fatal("db.create for minio in a guest must be refused")
	}
}

// TestDBCreateValidateNeedsContainer closes the gap DBCreateParams.Validate used to have: it never
// checked Container at all before validDBExecTarget was added.
func TestDBCreateValidateNeedsContainer(t *testing.T) {
	p := DBCreateParams{ServiceID: testSvcID, Kind: "postgres", Database: "app", User: "app", Grants: "all", RootSecret: "x", UserSecret: "y"}
	if p.Validate() == nil {
		t.Fatal("db.create with no container and no guest target must be refused")
	}
}

func TestRepoValidate(t *testing.T) {
	if (RepoSpec{Target: "local", Repo: "/var/lib/hosbah/restic/x"}).Validate() != nil {
		t.Fatal("valid local repo rejected")
	}
	if (RepoSpec{Target: "local", Repo: "/etc/passwd"}).Validate() == nil {
		t.Fatal("local repo outside /var/lib/hosbah must be refused")
	}
	if (RepoSpec{Target: "s3", Repo: "http://x"}).Validate() == nil {
		t.Fatal("s3 repo must start with s3:")
	}
}

func TestRestoreValidateThrowaway(t *testing.T) {
	p := DBRestoreParams{
		ServiceID: testSvcID, Kind: "postgres", Version: "16", Image: "postgres:16",
		BackupID: testBkID, SnapshotID: "deadbeef", Container: DBThrowawayName(testBkID),
		Volume: "v", Repo: RepoSpec{Target: "local", Repo: "/var/lib/cloudy/restic/x"}, RootSecret: "x",
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("valid restore rejected: %v", err)
	}
	p.Container = "hosbah-db-live"
	if p.Validate() == nil {
		t.Fatal("restore must only write its throwaway container")
	}
}

// A service, repository or restore made before the rename carries cloudy-… names and
// /var/lib/cloudy paths: an agent accepts them (it keeps using those objects), and nothing else.
func TestPreRenameNamesStillValidate(t *testing.T) {
	old := DBProvisionParams{
		ServiceID: testSvcID, Name: "pg1", Kind: "postgres", Version: "16",
		Image: "postgres:16", Container: "cloudy-db-pg1", Volume: "cloudy-dbdata-pg1",
		Bind: "10.0.0.5", Port: 5432, RootSecret: "cab1:abc",
	}
	if err := old.Validate(); err != nil {
		t.Fatalf("a pre-rename service: %v", err)
	}
	other := old
	other.Container = "cloudy-db-pg2"
	if other.Validate() == nil {
		t.Fatal("another service's container must still be refused")
	}
	if (RepoSpec{Target: "local", Repo: "/var/lib/cloudy/restic/x"}).Validate() != nil {
		t.Fatal("a pre-rename local repository")
	}
	if (RepoSpec{Target: "local", Repo: "/var/lib/cloudyx/restic/x"}).Validate() == nil {
		t.Fatal("a look-alike path must be refused")
	}
	p := DBRestoreParams{
		ServiceID: testSvcID, Kind: "postgres", Version: "16", Image: "postgres:16",
		BackupID: testBkID, SnapshotID: "deadbeef", Container: LegacyName(DBThrowawayName(testBkID)),
		Volume: "v", Repo: RepoSpec{Target: "local", Repo: "/var/lib/cloudy/restic/x"}, RootSecret: "x",
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("a pre-rename restore throwaway: %v", err)
	}
	for _, v := range []string{"cloudy-vol-" + testSvcID + "-data", "hosbah-vol-" + testSvcID + "-data"} {
		if !ValidVolumeName(testSvcID, v) {
			t.Errorf("%s is the app's volume", v)
		}
	}
	if ValidVolumeName(testSvcID, "other-vol-"+testSvcID+"-data") {
		t.Error("a foreign prefix")
	}
	if !IsAppImage("shop", "cloudy/shop:1") || !IsAppImage("shop", "hosbah/shop:1") || IsAppImage("shop", "cloudy/shopx:1") {
		t.Error("IsAppImage")
	}
	if got := NameCandidates("hosbah-db-x"); len(got) != 2 || got[0] != "cloudy-db-x" || got[1] != "hosbah-db-x" {
		t.Errorf("NameCandidates: %v", got)
	}
	if LegacyLabel(LabelApp) != "cloudy.app" || LegacyLabel(InstallAppLabel) != "cloudy.install-app" {
		t.Error("LegacyLabel")
	}
}
