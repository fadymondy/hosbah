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

// TestDBTargetArgvDockerInGuest: a guest whose engine itself runs in Docker nests pct exec around a
// docker exec, with env carried as -e flags on the innermost (docker) exec, exactly as a plain
// container — pct exec still gets none, since it has no such flag.
func TestDBTargetArgvDockerInGuest(t *testing.T) {
	bin, argv := dbTargetArgv([]string{"MYSQL_PWD=secret"}, dbTargetOf("102", "hosbah-db-my1"), []string{"mysql", "-u", "root"})
	if bin != "pct" {
		t.Fatalf("bin = %q, want pct", bin)
	}
	want := []string{"exec", "102", "--", "docker", "exec", "-i", "-e", "MYSQL_PWD", "hosbah-db-my1", "mysql", "-u", "root"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
}

func TestDBTargetIsNativeGuest(t *testing.T) {
	if dbTargetOf("", "c1").isNativeGuest() {
		t.Fatal("a container is not a native guest")
	}
	if dbTargetOf("102", "c1").isNativeGuest() {
		t.Fatal("a guest running its engine in docker is not native")
	}
	if !dbTargetOf("102", "").isNativeGuest() {
		t.Fatal("a guest with no container is native")
	}
}

// TestClientLoginContainer: a container (or docker-in-guest) still logs in with the stored
// credentials, password on env, never on argv — clientLogin does not know or care which of the two
// it is, since dbTargetArgv already made that distinction.
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

	t2 := dbTargetOf("102", "hosbah-db-my1") // docker-in-guest
	login2, env2 := clientLogin("mysql", t2, root, "")
	if !reflect.DeepEqual(env2, []string{"MYSQL_PWD=secret"}) {
		t.Fatalf("mysql env = %v", env2)
	}
	for _, s := range login2 {
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
