// Package mail sends Hosbah's transactional email (account codes) over SMTP.
//
// Ported from fadymondy.com-v2's internal/mail (same owner), trimmed to what Hosbah needs: one
// SMTP service from the MAIL_* environment, and a Sender that can be swapped for a log line in
// local development (DEV_MAIL_LOG=1) or a capture in tests.
//
// net/smtp rather than a library: the whole job is connect, authenticate, send. The part that
// varies between providers — implicit TLS on 465 versus STARTTLS on 587 — is decided here.
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"
)

// TLS modes (MAIL_ENCRYPTION).
const (
	TLSNone     = "none"     // plaintext; only sane for a local relay
	TLSStartTLS = "starttls" // 587: connect plain, upgrade
	TLSImplicit = "tls"      // 465: TLS from the first byte
)

// ErrNotConfigured: MAIL_HOST or MAIL_FROM is missing, so nothing can be sent.
var ErrNotConfigured = errors.New("mail: MAIL_HOST and MAIL_FROM are not set")

// Message is one email. Text is required; HTML, when present, makes it multipart/alternative.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Sender delivers a message.
type Sender func(ctx context.Context, msg Message) error

// Config is one SMTP service.
type Config struct {
	Host, Username, Password, From, FromName, Encryption string
	Port                                                 int
}

// ConfigFromEnv reads MAIL_HOST, MAIL_PORT, MAIL_USERNAME, MAIL_PASSWORD, MAIL_FROM,
// MAIL_FROM_NAME (default "Hosbah") and MAIL_ENCRYPTION (starttls | tls | none; default tls on
// port 465 and starttls otherwise). ok is false when host or sender is missing.
func ConfigFromEnv() (Config, bool) {
	c := Config{
		Host:     strings.TrimSpace(os.Getenv("MAIL_HOST")),
		Username: os.Getenv("MAIL_USERNAME"),
		Password: os.Getenv("MAIL_PASSWORD"),
		From:     strings.TrimSpace(os.Getenv("MAIL_FROM")),
		FromName: strings.TrimSpace(os.Getenv("MAIL_FROM_NAME")),
		Port:     587,
	}
	if c.FromName == "" {
		c.FromName = "Hosbah"
	}
	if p, err := strconv.Atoi(strings.TrimSpace(os.Getenv("MAIL_PORT"))); err == nil && p > 0 {
		c.Port = p
	}
	switch mode := strings.ToLower(strings.TrimSpace(os.Getenv("MAIL_ENCRYPTION"))); mode {
	case TLSNone, TLSStartTLS, TLSImplicit:
		c.Encryption = mode
	case "ssl":
		c.Encryption = TLSImplicit
	default:
		if c.Port == 465 {
			c.Encryption = TLSImplicit
		} else {
			c.Encryption = TLSStartTLS
		}
	}
	return c, c.Host != "" && c.From != ""
}

// IsProduction reports whether APP_ENV (or ENV / TOGO_ENV) says production.
func IsProduction() bool {
	for _, k := range []string{"APP_ENV", "ENV", "TOGO_ENV"} {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(k))) {
		case "production", "prod":
			return true
		}
	}
	return false
}

// FromEnv returns the sender the server uses:
//
//   - DEV_MAIL_LOG=1 outside production: the message is written to the log (codes included) and
//     not sent, so the flows can be walked locally without an inbox. Refused in production: a
//     code in a production log is a credential in a production log.
//   - otherwise SMTP from MAIL_*; with nothing configured every send fails with ErrNotConfigured
//     (logged by the caller; the HTTP answers do not change).
func FromEnv(log *slog.Logger) Sender {
	if log == nil {
		log = slog.Default()
	}
	if os.Getenv("DEV_MAIL_LOG") == "1" {
		if !IsProduction() {
			log.Warn("DEV_MAIL_LOG=1: emails are logged, not sent")
			return func(_ context.Context, msg Message) error {
				log.Warn("DEV_MAIL_LOG: email not sent", "to", msg.To, "subject", msg.Subject, "text", msg.Text)
				return nil
			}
		}
		log.Error("DEV_MAIL_LOG=1 is ignored in production")
	}
	cfg, ok := ConfigFromEnv()
	if !ok {
		log.Warn("mail is not configured (MAIL_HOST / MAIL_FROM): account codes cannot be emailed")
		return func(context.Context, Message) error { return ErrNotConfigured }
	}
	return func(ctx context.Context, msg Message) error { return Send(ctx, cfg, msg) }
}

// Send delivers one message. Errors carry the mail server's own refusal.
func Send(ctx context.Context, cfg Config, msg Message) error {
	if msg.To == "" || strings.ContainsAny(msg.To, "\r\n") {
		return errors.New("mail: bad recipient")
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var err error
	if cfg.Encryption == TLSImplicit {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("mail: connect to %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("mail: SMTP handshake: %w", err)
	}
	defer client.Quit() //nolint:errcheck

	if cfg.Encryption == TLSStartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("mail: %s does not offer STARTTLS", addr)
		}
		if err := client.StartTLS(&tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("mail: STARTTLS: %w", err)
		}
	}
	if cfg.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return fmt.Errorf("mail: authenticate as %s: %w", cfg.Username, err)
		}
	}
	if err := client.Mail(cfg.From); err != nil {
		return fmt.Errorf("mail: sender %s rejected: %w", cfg.From, err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return fmt.Errorf("mail: recipient %s rejected: %w", msg.To, err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("mail: start message body: %w", err)
	}
	if _, err := w.Write([]byte(build(cfg, msg, time.Now()))); err != nil {
		return fmt.Errorf("mail: write message body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: deliver: %w", err)
	}
	return nil
}

// build renders the RFC 5322 message.
func build(cfg Config, msg Message, now time.Time) string {
	var b strings.Builder
	if cfg.FromName != "" {
		// Encoded, so an Arabic display name survives.
		fmt.Fprintf(&b, "From: %s <%s>\r\n", mime.QEncoding.Encode("utf-8", cfg.FromName), cfg.From)
	} else {
		fmt.Fprintf(&b, "From: %s\r\n", cfg.From)
	}
	fmt.Fprintf(&b, "To: %s\r\n", msg.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", msg.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", now.Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	if msg.HTML == "" {
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		b.WriteString(msg.Text)
		b.WriteString("\r\n")
		return b.String()
	}
	boundary := fmt.Sprintf("hosbah-alt-%d", now.UnixNano())
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
	// Plain text first: clients render the last part they understand.
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", boundary, msg.Text)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/html; charset=utf-8\r\n\r\n%s\r\n", boundary, msg.HTML)
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.String()
}
