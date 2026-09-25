package agent

import "testing"

// Golden statements for the standalone-user grant presets (MH-574 F1). These are the pure
// SQL-building halves of dbUserPostgres/dbUserMySQL — identifiers here are agentproto-validated
// names (never free text), and pgGrantSQL/pgRevokeSQL/mysqlPrivOf are the only place the actual
// GRANT/REVOKE statements are assembled, so a change to their shape belongs here, not spotted in
// a live database.
func TestPgGrantSQL(t *testing.T) {
	cases := map[string]struct{ database, user, grants, want string }{
		"all": {"appdb", "alice", "all", `GRANT ALL PRIVILEGES ON DATABASE "appdb" TO "alice";
GRANT USAGE, CREATE ON SCHEMA public TO "alice";
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO "alice";
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO "alice";
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL PRIVILEGES ON TABLES TO "alice";
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL PRIVILEGES ON SEQUENCES TO "alice";`},
		"readwrite": {"appdb", "bob", "readwrite", `GRANT CONNECT ON DATABASE "appdb" TO "bob";
GRANT USAGE, CREATE ON SCHEMA public TO "bob";
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO "bob";
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO "bob";
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO "bob";
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE ON SEQUENCES TO "bob";`},
		"readonly": {"appdb", "carol", "readonly", `GRANT CONNECT ON DATABASE "appdb" TO "carol";
GRANT USAGE ON SCHEMA public TO "carol";
GRANT SELECT ON ALL TABLES IN SCHEMA public TO "carol";
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO "carol";`},
		"unknown": {"appdb", "dave", "none", ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := pgGrantSQL(c.database, c.user, c.grants)
			if got != c.want {
				t.Errorf("pgGrantSQL(%q, %q, %q):\ngot:\n%s\nwant:\n%s", c.database, c.user, c.grants, got, c.want)
			}
		})
	}
}

func TestPgRevokeSQL(t *testing.T) {
	want := `REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM "alice";
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM "alice";
ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE ALL PRIVILEGES ON TABLES FROM "alice";
ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE ALL PRIVILEGES ON SEQUENCES FROM "alice";
REVOKE ALL PRIVILEGES ON SCHEMA public FROM "alice";
REVOKE ALL PRIVILEGES ON DATABASE "appdb" FROM "alice";`
	if got := pgRevokeSQL("appdb", "alice"); got != want {
		t.Errorf("pgRevokeSQL:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestMysqlPrivOf(t *testing.T) {
	cases := map[string]string{
		"all":       "ALL PRIVILEGES",
		"readwrite": "SELECT, INSERT, UPDATE, DELETE",
		"readonly":  "SELECT",
		"none":      "ALL PRIVILEGES", // default: never silently narrows a grant preset we don't recognise
		"":          "ALL PRIVILEGES",
	}
	for grants, want := range cases {
		if got := mysqlPrivOf(grants); got != want {
			t.Errorf("mysqlPrivOf(%q) = %q, want %q", grants, got, want)
		}
	}
}
