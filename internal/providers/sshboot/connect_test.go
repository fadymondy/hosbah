package sshboot

import (
	"strings"
	"testing"
	"time"
)

const testPassword = "s3cret-root"

func TestConnectWithPasswordRunsSeveralCommands(t *testing.T) {
	pub, _ := newKey(t)
	srv := startServer(t, "127.0.0.1:0", pub)
	c, err := Connect(ctxFor(t, 10*time.Second), Config{Addr: srv.addr, User: "root", Password: testPassword, FailFastAuth: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, cmd := range []string{"uname", "id -u"} {
		out, err := c.Output(ctxFor(t, 5*time.Second), cmd)
		if err != nil || !strings.Contains(out, "ran: "+cmd) {
			t.Fatalf("%s: out=%q err=%v", cmd, out, err)
		}
	}
}

func TestConnectWrongPasswordFailsFast(t *testing.T) {
	pub, _ := newKey(t)
	srv := startServer(t, "127.0.0.1:0", pub)
	start := time.Now()
	_, err := Connect(ctxFor(t, 20*time.Second), Config{Addr: srv.addr, User: "root", Password: "nope", FailFastAuth: true})
	if err == nil || !strings.Contains(err.Error(), "refused the username, password or key") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("a wrong password was retried for %s", time.Since(start))
	}
}

func TestConnectNeedsKeyOrPassword(t *testing.T) {
	if _, err := Connect(ctxFor(t, time.Second), Config{Addr: "127.0.0.1:1", User: "root"}); err == nil {
		t.Fatal("expected an error without a key or password")
	}
}
