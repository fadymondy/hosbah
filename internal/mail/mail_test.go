package mail

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCodeEmailLocalesAndPurposes(t *testing.T) {
	for _, locale := range []string{"en", "ar"} {
		for _, purpose := range []string{"verify", "reset", "login"} {
			subject, text, body := CodeEmail(locale, purpose, "123456")
			if subject == "" || !strings.Contains(text, "1 2 3 4 5 6") || !strings.Contains(body, "123456") {
				t.Errorf("%s/%s: missing parts: %q %q", locale, purpose, subject, text)
			}
			brand := BrandEN
			if locale == "ar" {
				brand = BrandAR
				if !strings.Contains(body, `dir="rtl"`) {
					t.Errorf("%s/%s: Arabic body is not RTL", locale, purpose)
				}
			}
			if !strings.Contains(subject+text, brand) {
				t.Errorf("%s/%s: brand %q missing", locale, purpose, brand)
			}
		}
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("MAIL_HOST", "")
	t.Setenv("MAIL_FROM", "")
	if _, ok := ConfigFromEnv(); ok {
		t.Fatal("configured without a host")
	}
	t.Setenv("MAIL_HOST", "smtp.example.test")
	t.Setenv("MAIL_FROM", "no-reply@example.test")
	t.Setenv("MAIL_PORT", "465")
	t.Setenv("MAIL_ENCRYPTION", "")
	t.Setenv("MAIL_FROM_NAME", "")
	c, ok := ConfigFromEnv()
	if !ok || c.Encryption != TLSImplicit || c.FromName != "Hosbah" {
		t.Fatalf("465 defaults: %+v", c)
	}
	t.Setenv("MAIL_PORT", "587")
	if c, _ := ConfigFromEnv(); c.Encryption != TLSStartTLS {
		t.Fatalf("587 default: %+v", c)
	}
	t.Setenv("MAIL_ENCRYPTION", "none")
	if c, _ := ConfigFromEnv(); c.Encryption != TLSNone {
		t.Fatalf("explicit none: %+v", c)
	}
}

func TestDevMailLogRefusedInProduction(t *testing.T) {
	t.Setenv("MAIL_HOST", "")
	t.Setenv("MAIL_FROM", "")
	t.Setenv("DEV_MAIL_LOG", "1")
	t.Setenv("APP_ENV", "development")
	if err := FromEnv(nil)(context.Background(), Message{To: "a@b.test"}); err != nil {
		t.Fatalf("dev log should swallow the send: %v", err)
	}
	t.Setenv("APP_ENV", "production")
	if err := FromEnv(nil)(context.Background(), Message{To: "a@b.test"}); err != ErrNotConfigured {
		t.Fatalf("production must not log codes: %v", err)
	}
}

func TestBuildEncodesArabicName(t *testing.T) {
	raw := build(Config{From: "x@example.test", FromName: BrandAR}, Message{To: "y@example.test", Subject: "مرحبا", Text: "t", HTML: "<p>h</p>"}, time.Unix(0, 0))
	if strings.Contains(raw, BrandAR) || !strings.Contains(raw, "=?utf-8?q?") || !strings.Contains(raw, "multipart/alternative") {
		t.Fatalf("headers not encoded: %s", raw)
	}
}
