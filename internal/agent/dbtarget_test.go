package agent

import (
	"reflect"
	"testing"
)

// TestDBTargetArgvContainer: a plain container (VMID "") runs a single docker exec, env as -e flags.
func TestDBTargetArgvContainer(t *testing.T) {
	bin, argv := dbTargetArgv([]string{"PGPASSWORD=secret"}, dbTargetOf("", "hosbah-db-pg1"), []string{"psql", "-U", "postgres"})
	if bin != "docker" {
		t.Fatalf("bin = %q, want docker", bin)
	}
	want := []string{"exec", "-i", "-e", "PGPASSWORD", "hosbah-db-pg1", "psql", "-U", "postgres"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
}

// TestDBTargetArgvNativeGuest: a native LXC guest (VMID set, no Container) runs pct exec directly —
// no docker anywhere, and no -e flag (pct exec has none).
func TestDBTargetArgvNativeGuest(t *testing.T) {
	bin, argv := dbTargetArgv(nil, dbTargetOf("102", ""), []string{"runuser", "-u", "postgres", "--", "psql", "-U", "postgres", "-d", "postgres"})
	if bin != "pct" {
		t.Fatalf("bin = %q, want pct", bin)
	}
	want := []string{"exec", "102", "--", "runuser", "-u", "postgres", "--", "psql", "-U", "postgres", "-d", "postgres"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
}

// TestDBTargetIsNativeGuest: a guest is always native now — validDBExecTarget (agentproto) refuses a
// guest with a non-empty Container before an op is ever built, so isNativeGuest only needs to check
// VMID.
func TestDBTargetIsNativeGuest(t *testing.T) {
	if dbTargetOf("", "c1").isNativeGuest() {
		t.Fatal("a container is not a native guest")
	}
	if !dbTargetOf("102", "").isNativeGuest() {
		t.Fatal("a guest is native")
	}
}

// TestClientLoginContainer: a container still logs in with the stored credentials, password on env,
// never on argv.
func TestClientLoginContainer(t *testing.T) {
	root := map[string]string{"user": "app", "password": "secret", "database": "appdb"}
	t1 := dbTargetOf("", "hosbah-db-pg1")
	login, env := clientLogin("postgres", t1, root, "")
	if !reflect.DeepEqual(login, []string{"-h", "127.0.0.1", "-U", "app", "-d", "appdb"}) {
		t.Fatalf("postgres login = %v", login)
	}
	if !reflect.DeepEqual(env, []string{"PGPASSWORD=secret"}) {
		t.Fatalf("postgres env = %v", env)
	}
	for _, s := range login {
		if s == "secret" {
			t.Fatal("the password must never be on the login argv")
		}
	}
}

// TestClientLoginNativeGuest: a native guest never sends a secret at all — OS-user auth instead —
// and defaults a Postgres connection to the postgres database.
func TestClientLoginNativeGuest(t *testing.T) {
	root := map[string]string{"user": "app", "password": "secret", "database": "appdb"}
	t1 := dbTargetOf("102", "")

	login, env := clientLogin("postgres", t1, root, "")
	if env != nil {
		t.Fatalf("a native guest must send no env: %v", env)
	}
	if !reflect.DeepEqual(login, []string{"-U", "postgres", "-d", "postgres"}) {
		t.Fatalf("native postgres login = %v", login)
	}

	login2, _ := clientLogin("postgres", t1, root, "appdb")
	if !reflect.DeepEqual(login2, []string{"-U", "postgres", "-d", "appdb"}) {
		t.Fatalf("native postgres login with a database = %v", login2)
	}

	loginMySQL, envMySQL := clientLogin("mysql", t1, root, "")
	if envMySQL != nil {
		t.Fatalf("a native mysql guest must send no env: %v", envMySQL)
	}
	if !reflect.DeepEqual(loginMySQL, []string{"-u", "root"}) {
		t.Fatalf("native mysql login = %v", loginMySQL)
	}
}

// TestClientArgvNativeGuestPostgres: only a native guest's Postgres client must run as the postgres
// OS user for peer auth — mysql (auth_socket as root, and pct exec already runs as root) and a
// container/docker-in-guest path need no such wrapper.
func TestClientArgvNativeGuestPostgres(t *testing.T) {
	t1 := dbTargetOf("102", "")
	argv := clientArgv("postgres", t1, []string{"psql", "-U", "postgres"})
	want := []string{"runuser", "-u", "postgres", "--", "psql", "-U", "postgres"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("native postgres argv = %v, want %v", argv, want)
	}

	argvMySQL := clientArgv("mysql", t1, []string{"mysql", "-u", "root"})
	if !reflect.DeepEqual(argvMySQL, []string{"mysql", "-u", "root"}) {
		t.Fatalf("native mysql argv must be unwrapped: %v", argvMySQL)
	}

	t2 := dbTargetOf("", "hosbah-db-pg1")
	argvContainer := clientArgv("postgres", t2, []string{"psql", "-U", "postgres"})
	if !reflect.DeepEqual(argvContainer, []string{"psql", "-U", "postgres"}) {
		t.Fatalf("container postgres argv must be unwrapped: %v", argvContainer)
	}
}

// TestPctStatusRunning: pct status exits 0 even for a stopped guest, so checkDBTarget must read the
// status line itself, not just the exit code.
func TestPctStatusRunning(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{"running", "status: running\n", true},
		{"running with other fields", "102: status: running\nlock: \n", true},
		{"stopped", "status: stopped\n", false},
		{"empty", "", false},
		{"case insensitive", "Status: Running\n", true},
	}
	for _, c := range cases {
		if got := pctStatusRunning(c.out); got != c.want {
			t.Errorf("%s: pctStatusRunning(%q) = %v, want %v", c.name, c.out, got, c.want)
		}
	}
}
