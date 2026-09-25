package agentproto

import "testing"

func validQueryParams() DBQueryParams {
	return DBQueryParams{
		ServiceID: "00000000-0000-4000-8000-000000000001", Kind: "postgres", Container: "hosbah-db-x",
		Database: "app_db", Mode: "rows", Table: "public.users", Limit: 100, Offset: 0,
		TimeoutSeconds: 30, RootSecret: "box",
	}
}

func TestDBQueryParamsValidate(t *testing.T) {
	if err := validQueryParams().Validate(); err != nil {
		t.Fatalf("a well-formed params should validate: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*DBQueryParams)
	}{
		{"bad service id", func(p *DBQueryParams) { p.ServiceID = "not-a-uuid" }},
		{"redis kind", func(p *DBQueryParams) { p.Kind = "redis" }},
		{"minio kind", func(p *DBQueryParams) { p.Kind = "minio" }},
		{"bad database", func(p *DBQueryParams) { p.Database = "Not Snake" }},
		{"bad mode", func(p *DBQueryParams) { p.Mode = "delete" }},
		{"empty container", func(p *DBQueryParams) { p.Container = "" }},
		{"empty root secret", func(p *DBQueryParams) { p.RootSecret = "" }},
		{"limit too big", func(p *DBQueryParams) { p.Limit = DBQueryMaxLimit + 1 }},
		{"negative limit", func(p *DBQueryParams) { p.Limit = -1 }},
		{"negative offset", func(p *DBQueryParams) { p.Offset = -1 }},
		{"timeout zero", func(p *DBQueryParams) { p.TimeoutSeconds = 0 }},
		{"timeout too big", func(p *DBQueryParams) { p.TimeoutSeconds = DBQueryMaxTimeout + 1 }},
		{"rows without table", func(p *DBQueryParams) { p.Table = "" }},
		{"columns with bad table", func(p *DBQueryParams) { p.Mode = "columns"; p.Table = "bad table name" }},
		{"mysql schema-qualified table refused", func(p *DBQueryParams) { p.Kind = "mysql"; p.Table = "public.users" }},
		{"sql mode without sql", func(p *DBQueryParams) { p.Mode = "sql"; p.SQL = "" }},
		{"sql too long", func(p *DBQueryParams) {
			p.Mode = "sql"
			big := make([]byte, DBQueryMaxSQLBytes+1)
			for i := range big {
				big[i] = 'a'
			}
			p.SQL = string(big)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := validQueryParams()
			c.mut(&p)
			if err := p.Validate(); err == nil {
				t.Fatalf("expected an error")
			}
		})
	}
}

func TestDBQueryParamsValidateSQLModeOK(t *testing.T) {
	p := validQueryParams()
	p.Mode, p.Table, p.SQL = "sql", "", "SELECT 1"
	if err := p.Validate(); err != nil {
		t.Fatalf("sql mode with no table should validate: %v", err)
	}
}

func TestDBQueryParamsValidateTablesModeOK(t *testing.T) {
	p := validQueryParams()
	p.Mode, p.Table = "tables", ""
	if err := p.Validate(); err != nil {
		t.Fatalf("tables mode with no table should validate: %v", err)
	}
}

func TestValidDBQualifiedTable(t *testing.T) {
	ok := []string{"users", "public.users", "app_events"}
	bad := []string{"", "a.b.c", "Users", "with space", "with-dash"}
	for _, s := range ok {
		if !ValidDBQualifiedTable(s) {
			t.Errorf("%q should be a valid table reference", s)
		}
	}
	for _, s := range bad {
		if ValidDBQualifiedTable(s) {
			t.Errorf("%q should not be a valid table reference", s)
		}
	}
}

func TestSplitQualifiedTable(t *testing.T) {
	if s, tn := SplitQualifiedTable("public.users"); s != "public" || tn != "users" {
		t.Fatalf("got schema=%q table=%q", s, tn)
	}
	if s, tn := SplitQualifiedTable("users"); s != "" || tn != "users" {
		t.Fatalf("got schema=%q table=%q", s, tn)
	}
}
