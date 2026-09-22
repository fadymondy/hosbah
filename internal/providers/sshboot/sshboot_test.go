package sshboot

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// testServer is an in-process SSH server that accepts only one public key and answers "exec"
// requests by echoing the command, writing a line to stderr, and exiting with N for "exit N".
type testServer struct {
	addr    string
	hostKey ssh.PublicKey
}

func startServer(t *testing.T, addr string, allowed ssh.PublicKey) *testServer {
	t.Helper()
	_, hostPriv, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(k.Marshal(), allowed.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("key not allowed")
		},
		PasswordCallback: func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if string(pw) == testPassword {
				return nil, nil
			}
			return nil, errors.New("wrong password")
		},
	}
	cfg.AddHostKey(hostSigner)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveConn(c, cfg)
		}
	}()
	return &testServer{addr: ln.Addr().String(), hostKey: hostSigner.PublicKey()}
}

func serveConn(c net.Conn, cfg *ssh.ServerConfig) {
	defer c.Close()
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			return
		}
		go func() {
			defer ch.Close()
			for r := range creqs {
				if r.Type != "exec" {
					_ = r.Reply(false, nil)
					continue
				}
				var p struct{ Command string }
				if err := ssh.Unmarshal(r.Payload, &p); err != nil {
					_ = r.Reply(false, nil)
					return
				}
				_ = r.Reply(true, nil)
				fmt.Fprintf(ch, "ran: %s\nno trailing newline", p.Command)
				fmt.Fprint(ch.Stderr(), "warning on stderr\n")
				status := 0
				if n, ok := strings.CutPrefix(p.Command, "exit "); ok {
					status, _ = strconv.Atoi(n)
				}
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(status)}))
				return
			}
		}()
	}
}

func newKey(t *testing.T) (ssh.PublicKey, []byte) {
	t.Helper()
	auth, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(auth))
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func ctxFor(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func TestGenerateKey(t *testing.T) {
	auth, privPEM, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(auth, "ssh-ed25519 ") || strings.HasSuffix(auth, "\n") {
		t.Fatalf("authorized key = %q", auth)
	}
	if !bytes.HasPrefix(privPEM, []byte("-----BEGIN OPENSSH PRIVATE KEY-----")) {
		t.Fatalf("private key is not OpenSSH PEM: %.40s", privPEM)
	}
	signer, err := ssh.ParsePrivateKey(privPEM)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))); got != auth {
		t.Fatal("private and public halves do not match")
	}
}

func TestRunStreamsOutputAndReportsHostKey(t *testing.T) {
	pub, priv := newKey(t)
	srv := startServer(t, "127.0.0.1:0", pub)
	var out bytes.Buffer
	var seen string
	cfg := Config{Addr: srv.addr, User: "hosbah", PrivateKeyPEM: priv, HostKey: HostKeyPolicy{Observed: func(fp string) { seen = fp }}}
	if err := Run(ctxFor(t, 10*time.Second), cfg, "install agent", &out); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"ran: install agent\n", "no trailing newline\n", "warning on stderr\n"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("output %q lacks %q", out.String(), line)
		}
	}
	if strings.Count(out.String(), "\n") != 3 {
		t.Errorf("output not line-split: %q", out.String())
	}
	if seen != ssh.FingerprintSHA256(srv.hostKey) {
		t.Fatalf("observed %q, want %q", seen, ssh.FingerprintSHA256(srv.hostKey))
	}
}

func TestRunNonZeroExit(t *testing.T) {
	pub, priv := newKey(t)
	srv := startServer(t, "127.0.0.1:0", pub)
	err := Run(ctxFor(t, 10*time.Second), Config{Addr: srv.addr, User: "root", PrivateKeyPEM: priv}, "exit 3", &bytes.Buffer{})
	var ee *ssh.ExitError
	if !errors.As(err, &ee) || ee.ExitStatus() != 3 || !strings.Contains(err.Error(), "status 3") {
		t.Fatalf("got %v, want exit status 3", err)
	}
	if strings.Contains(err.Error(), "exit 3") {
		t.Fatal("error echoes the command")
	}
}

func TestRunWrongKeyFailsAuth(t *testing.T) {
	pub, _ := newKey(t)
	_, otherPriv := newKey(t)
	srv := startServer(t, "127.0.0.1:0", pub)
	err := Run(ctxFor(t, 1500*time.Millisecond), Config{Addr: srv.addr, User: "root", PrivateKeyPEM: otherPriv}, "x", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "unable to authenticate") || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want an auth failure after retrying", err)
	}
}

func TestRunPinnedHostKey(t *testing.T) {
	pub, priv := newKey(t)
	srv := startServer(t, "127.0.0.1:0", pub)
	for _, pin := range []string{ssh.FingerprintSHA256(srv.hostKey), string(ssh.MarshalAuthorizedKey(srv.hostKey))} {
		if err := Run(ctxFor(t, 10*time.Second), Config{Addr: srv.addr, User: "u", PrivateKeyPEM: priv, HostKey: HostKeyPolicy{Pinned: pin}}, "ok", &bytes.Buffer{}); err != nil {
			t.Fatalf("pin %q refused: %v", pin, err)
		}
	}
	otherHost, _ := newKey(t)
	start := time.Now()
	err := Run(ctxFor(t, 10*time.Second), Config{Addr: srv.addr, User: "u", PrivateKeyPEM: priv, HostKey: HostKeyPolicy{Pinned: ssh.FingerprintSHA256(otherHost)}}, "ok", &bytes.Buffer{})
	if !errors.Is(err, ErrHostKeyMismatch) {
		t.Fatalf("got %v, want ErrHostKeyMismatch", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("a host key mismatch was retried")
	}
	if err := Run(ctxFor(t, time.Second), Config{Addr: srv.addr, PrivateKeyPEM: priv, HostKey: HostKeyPolicy{Pinned: "garbage"}}, "ok", nil); err == nil {
		t.Fatal("garbage pin accepted")
	}
}

func TestRunRetriesUntilSSHDIsUp(t *testing.T) {
	pub, priv := newKey(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens yet: the first attempts are refused

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(800 * time.Millisecond)
		startServer(t, addr, pub)
	}()
	var out bytes.Buffer
	err = Run(ctxFor(t, 15*time.Second), Config{Addr: addr, User: "u", PrivateKeyPEM: priv, DialTimeout: time.Second}, "late", &out)
	wg.Wait()
	if err != nil || !strings.Contains(out.String(), "ran: late") {
		t.Fatalf("got %v, output %q", err, out.String())
	}
}

func TestRunBadPrivateKey(t *testing.T) {
	err := Run(ctxFor(t, time.Second), Config{Addr: "127.0.0.1:1", PrivateKeyPEM: []byte("-----BEGIN NOPE-----")}, "x", nil)
	if err == nil || strings.Contains(err.Error(), "NOPE") {
		t.Fatalf("got %v", err)
	}
}
