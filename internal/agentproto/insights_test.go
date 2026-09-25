package agentproto

import "testing"

// MH-575 F6: db.insights / db.cancel_query Validate().

func TestDBInsightsValidate(t *testing.T) {
	base := DBInsightsParams{
		ServiceID: testSvcID, Kind: "postgres", Container: "hosbah-db-pg1", TimeoutSeconds: 15, RootSecret: "r",
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid insights params rejected: %v", err)
	}

	mysql := base
	mysql.Kind = "mysql"
	mysql.Container = "hosbah-db-my1"
	if err := mysql.Validate(); err != nil {
		t.Fatalf("valid mysql insights params rejected: %v", err)
	}

	redis := base
	redis.Kind = "redis"
	if redis.Validate() == nil {
		t.Fatal("db.insights must be refused for redis")
	}

	noContainer := base
	noContainer.Container = ""
	if noContainer.Validate() == nil {
		t.Fatal("db.insights needs a container")
	}

	noSecret := base
	noSecret.RootSecret = ""
	if noSecret.Validate() == nil {
		t.Fatal("db.insights needs the root secret")
	}

	badTimeout := base
	badTimeout.TimeoutSeconds = 0
	if badTimeout.Validate() == nil {
		t.Fatal("timeout_seconds below 1 must be refused")
	}
	badTimeout.TimeoutSeconds = DBInsightsMaxTimeout + 1
	if badTimeout.Validate() == nil {
		t.Fatal("timeout_seconds above the max must be refused")
	}

	badDB := base
	badDB.Database = "bad name; drop table x"
	if badDB.Validate() == nil {
		t.Fatal("a bad database name must be refused")
	}

	badID := base
	badID.ServiceID = "not-a-uuid"
	if badID.Validate() == nil {
		t.Fatal("a bad service id must be refused")
	}
}

func TestDBCancelQueryValidate(t *testing.T) {
	base := DBCancelQueryParams{
		ServiceID: testSvcID, Kind: "postgres", Container: "hosbah-db-pg1", ID: 42, RootSecret: "r",
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid cancel params rejected: %v", err)
	}

	redis := base
	redis.Kind = "redis"
	if redis.Validate() == nil {
		t.Fatal("db.cancel_query must be refused for redis")
	}

	noID := base
	noID.ID = 0
	if noID.Validate() == nil {
		t.Fatal("id 0 must be refused")
	}
	negID := base
	negID.ID = -1
	if negID.Validate() == nil {
		t.Fatal("a negative id must be refused")
	}

	noSecret := base
	noSecret.RootSecret = ""
	if noSecret.Validate() == nil {
		t.Fatal("db.cancel_query needs the root secret")
	}

	noContainer := base
	noContainer.Container = ""
	if noContainer.Validate() == nil {
		t.Fatal("db.cancel_query needs a container")
	}
}
