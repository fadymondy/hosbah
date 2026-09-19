// Package sshboot installs the Hosbah agent over SSH on a freshly created server whose provider
// cannot deliver cloud-init user-data (Proxmox LXC, or a Proxmox VM without a snippet store).
//
// The control plane generates a per-server key with GenerateKey, has the provider put the public
// half on the guest, and then calls Run with the install command. After the agent has enrolled the
// key can be discarded; SSH is not how Hosbah manages servers day to day.
package sshboot

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
)

// ErrHostKeyMismatch means the server presented a host key other than the expected one.
var ErrHostKeyMismatch = errors.New("sshboot: host key mismatch")

// GenerateKey returns a new ed25519 key: the public half as an authorized_keys line and the private
// half as an OpenSSH-format PEM block.
func GenerateKey() (authorizedKey string, privatePEM []byte, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", nil, fmt.Errorf("sshboot: generating key: %w", err)
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", nil, fmt.Errorf("sshboot: generating key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "cloudy-bootstrap")
	if err != nil {
		return "", nil, fmt.Errorf("sshboot: encoding key: %w", err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp))), pem.EncodeToMemory(block), nil
}

// HostKeyPolicy decides which host key Run accepts.
//
// With Pinned set, only that key is accepted. Pinned is either a SHA256 fingerprint
// ("SHA256:...") or an authorized_keys-style public key line.
//
// With Pinned empty, Run trusts on first use: it accepts whatever key the server presents on the
// first successful handshake, requires the same key on any reconnect within the same Run, and
// reports its fingerprint through Observed. This is a deliberate trade-off: the guest was created a
// minute ago by Hosbah itself, and neither Proxmox nor most clouds expose a new guest's host key
// through their API, so there is nothing to pin against. The window for a man in the middle is the
// first connection on the operator's own network; callers should store the observed fingerprint
// and pin it on every later connection (break-glass access).
type HostKeyPolicy struct {
	Pinned   string
	Observed func(fingerprint string)
}

// Config says where and as whom to connect.
type Config struct {
	Addr          string // host:22
	User          string
	PrivateKeyPEM []byte
	Password      string // offered after the key, if set (connecting a server the user already has)
	HostKey       HostKeyPolicy
	DialTimeout   time.Duration // per attempt, including the handshake; default 10s
	// FailFastAuth stops retrying on an authentication failure. For a server that already exists a
	// wrong password will not fix itself, and retrying risks tripping fail2ban.
	FailFastAuth bool
}

// Client is one SSH connection that can run several commands (probe, install, …) without logging
// in again.
type Client struct{ c *ssh.Client }

// Close ends the connection.
func (c *Client) Close() error { return c.c.Close() }

// Connect dials (retrying as Run does) and authenticates.
func Connect(ctx context.Context, cfg Config) (*Client, error) {
	auth, err := authMethods(cfg)
	if err != nil {
		return nil, err
	}
	hk, mismatch, err := hostKeyCallback(cfg.HostKey)
	if err != nil {
		return nil, err
	}
	timeout := cfg.DialTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	sc := &ssh.ClientConfig{User: cfg.User, Auth: auth, HostKeyCallback: hk, Timeout: timeout}
	c, err := dial(ctx, cfg.Addr, sc, timeout, mismatch, cfg.FailFastAuth)
	if err != nil {
		return nil, err
	}
	return &Client{c: c}, nil
}

// Output runs command and returns its stdout (stderr is dropped). Non-zero exit is an error.
func (c *Client) Output(ctx context.Context, command string) (string, error) {
	var buf bytes.Buffer
	err := c.Run(ctx, command, &buf, io.Discard)
	return buf.String(), err
}

// Stream runs command, streaming stdout and stderr into out line by line.
func (c *Client) Stream(ctx context.Context, command string, out io.Writer) error {
	var mu sync.Mutex
	stdout, stderr := &lineWriter{mu: &mu, out: out}, &lineWriter{mu: &mu, out: out}
	err := c.Run(ctx, command, stdout, stderr)
	stdout.flush()
	stderr.flush()
	return err
}

// Run runs command with the given writers. The command never appears in errors.
func (c *Client) Run(ctx context.Context, command string, stdout, stderr io.Writer) error {
	sess, err := c.c.NewSession()
	if err != nil {
		return fmt.Errorf("sshboot: opening session: %w", err)
	}
	defer sess.Close()
	sess.Stdout, sess.Stderr = stdout, stderr
	if err := sess.Start(command); err != nil {
		return fmt.Errorf("sshboot: starting remote command: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		c.c.Close()
		<-done
		return fmt.Errorf("sshboot: remote command interrupted: %w", ctx.Err())
	}
	var ee *ssh.ExitError
	switch {
	case errors.As(err, &ee):
		return fmt.Errorf("sshboot: remote command exited with status %d: %w", ee.ExitStatus(), err)
	case err != nil:
		return fmt.Errorf("sshboot: remote command: %w", err)
	}
	return nil
}

// authMethods offers the key first, then the password (plain and keyboard-interactive, since many
// sshd configs enable only the latter).
func authMethods(cfg Config) ([]ssh.AuthMethod, error) {
	var out []ssh.AuthMethod
	if len(cfg.PrivateKeyPEM) > 0 {
		signer, err := ssh.ParsePrivateKey(cfg.PrivateKeyPEM)
		if err != nil {
			return nil, errors.New("sshboot: the private key could not be parsed")
		}
		out = append(out, ssh.PublicKeys(signer))
	}
	if pw := cfg.Password; pw != "" {
		out = append(out, ssh.Password(pw), ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
			answers := make([]string, len(qs))
			for i := range answers {
				answers[i] = pw
			}
			return answers, nil
		}))
	}
	if len(out) == 0 {
		return nil, errors.New("sshboot: no private key or password")
	}
	return out, nil
}

const (
	firstBackoff = 500 * time.Millisecond
	maxBackoff   = 5 * time.Second
)

// Run connects (retrying until ctx expires, since a fresh guest's sshd and its authorized keys take
// a while to appear), runs command, and streams the command's stdout and stderr into out line by
// line. A non-zero exit status is returned as an error that carries the status.
//
// The command itself never appears in errors: install commands carry an enrolment token.
func Run(ctx context.Context, cfg Config, command string, out io.Writer) error {
	c, err := Connect(ctx, cfg)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Stream(ctx, command, out)
}

// hostKeyCallback implements the policy. The returned flag is set when a key was refused, which
// makes dial stop retrying: a wrong host key will not fix itself, and retrying would only keep
// talking to whoever is presenting it.
func hostKeyCallback(p HostKeyPolicy) (ssh.HostKeyCallback, *atomic.Bool, error) {
	var want string
	if pin := strings.TrimSpace(p.Pinned); strings.HasPrefix(pin, "SHA256:") {
		want = pin
	} else if pin != "" {
		k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pin))
		if err != nil {
			return nil, nil, fmt.Errorf("sshboot: pinned host key is neither a SHA256 fingerprint nor a public key: %w", err)
		}
		want = ssh.FingerprintSHA256(k)
	}
	var (
		mu       sync.Mutex
		mismatch atomic.Bool
		reported bool
	)
	cb := func(_ string, _ net.Addr, key ssh.PublicKey) error {
		got := ssh.FingerprintSHA256(key)
		mu.Lock()
		defer mu.Unlock()
		if want == "" {
			want = got // trust on first use, then hold every reconnect to it
		}
		if got != want {
			mismatch.Store(true)
			return fmt.Errorf("%w: server presented %s, expected %s", ErrHostKeyMismatch, got, want)
		}
		if !reported && p.Observed != nil {
			reported = true
			p.Observed(got)
		}
		return nil
	}
	return cb, &mismatch, nil
}

// dial retries with exponential backoff until it connects, the host key is refused, or ctx ends.
// Authentication failures are retried too: sshd often starts before cloud-init has written the
// authorized keys.
func dial(ctx context.Context, addr string, sc *ssh.ClientConfig, timeout time.Duration, mismatch *atomic.Bool, failFastAuth bool) (*ssh.Client, error) {
	backoff := firstBackoff
	var last error
	for attempt := 1; ; attempt++ {
		c, err := dialOnce(ctx, addr, sc, timeout)
		if err == nil {
			return c, nil
		}
		if mismatch.Load() {
			return nil, err
		}
		if failFastAuth && strings.Contains(err.Error(), "unable to authenticate") {
			return nil, errors.New("sshboot: the server refused the username, password or key")
		}
		last = err
		select {
		case <-ctx.Done():
			// Wrap both, so callers can test for the deadline and for the underlying cause.
			return nil, fmt.Errorf("sshboot: could not connect to %s after %d attempts (%w): %w", addr, attempt, ctx.Err(), last)
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func dialOnce(ctx context.Context, addr string, sc *ssh.ClientConfig, timeout time.Duration) (*ssh.Client, error) {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	// Bound the handshake by the attempt timeout, and abort it if ctx ends first.
	_ = conn.SetDeadline(time.Now().Add(timeout))
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	cc, chans, reqs, err := ssh.NewClientConn(conn, addr, sc)
	stop()
	if err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(cc, chans, reqs), nil
}

// lineWriter forwards whole lines to out so stdout and stderr interleave by line, never mid-line.
type lineWriter struct {
	mu  *sync.Mutex
	out io.Writer
	buf []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		w.mu.Lock()
		_, err := w.out.Write(w.buf[:i+1])
		w.mu.Unlock()
		w.buf = w.buf[i+1:]
		if err != nil {
			return len(p), err
		}
	}
}

// flush writes a trailing partial line, terminated.
func (w *lineWriter) flush() {
	if len(w.buf) == 0 {
		return
	}
	w.mu.Lock()
	_, _ = w.out.Write(append(w.buf, '\n'))
	w.mu.Unlock()
	w.buf = nil
}
