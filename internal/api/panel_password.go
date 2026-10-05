package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/mail"
)

// MH-1141: a panel account's password cycle. Reset codes reuse account_codes (purpose "reset")
// keyed by the namespaced panel address, so they never collide with a Hosbah account's codes. The
// mail carries the server's name only, nothing of Hosbah.

const (
	panelCodeTTL      = 15 * time.Minute
	panelCodeAttempts = 5
	panelMinPassword  = 10
)

var (
	panelForgots  = &panelLimiter{hits: map[string][]time.Time{}}
	panelMailOnce sync.Once
	panelMail     mail.Sender
)

func panelSender() mail.Sender {
	panelMailOnce.Do(func() { panelMail = mail.FromEnv(slog.Default()) })
	return panelMail
}

// readPanelJSON decodes a JSON body; false after writing the error.
func readPanelJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeJSONError(w, http.StatusUnsupportedMediaType, "send JSON")
		return false
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(v); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid body")
		return false
	}
	return true
}

func panelPasswordOK(w http.ResponseWriter, p string) bool {
	if len(p) < panelMinPassword || len(p) > 72 {
		writeJSONError(w, http.StatusUnprocessableEntity, fmt.Sprintf("the password must be %d to 72 characters", panelMinPassword))
		return false
	}
	return true
}

// handlePanelForgot mails a reset code when the address has an account here. The answer is the
// same either way, so it does not tell who has an account.
func (d *deps) handlePanelForgot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, ok := d.panelServer(ctx, chi.URLParam(r, "server"))
	if !ok {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	if !readPanelJSON(w, r, &body) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if !panelForgots.allow(panelClientIP(r)+"|"+s.ID) || !panelForgots.allow("mail|"+s.ID+"|"+email) {
		writeJSONError(w, http.StatusTooManyRequests, "too many attempts, try again in a few minutes")
		return
	}
	defer writePanelJSON(w, http.StatusOK, map[string]bool{"ok": true})
	if !validPanelEmail(email) {
		return
	}
	q := db.New(d.a.SQLDB)
	key := panelKey(email, s.ID)
	acc, err := q.PanelAccountByEmail(ctx, db.PanelAccountByEmailParams{Email: key, PanelServerID: &s.ID})
	if err != nil || acc.DisabledAt != nil {
		return
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return
	}
	code := fmt.Sprintf("%06d", n.Int64())
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	if err := q.UpsertAccountCode(ctx, db.UpsertAccountCodeParams{Email: key, Purpose: "reset", CodeHash: string(hash), ExpiresAt: now.Add(panelCodeTTL), SentAt: now}); err != nil {
		return
	}
	name := s.Name
	msg := mail.Message{
		To:      email,
		Subject: fmt.Sprintf("%s: your password reset code", name),
		Text:    fmt.Sprintf("Your %s password reset code is %s.\n\nIt works for 15 minutes. If you did not ask for it, ignore this email.\n", name, code),
		HTML: fmt.Sprintf(`<p>Your <strong>%s</strong> password reset code is</p><p style="font-size:28px;font-weight:700;letter-spacing:6px">%s</p><p>It works for 15 minutes. If you did not ask for it, ignore this email.</p>`,
			html.EscapeString(name), code),
	}
	// In the background: an SMTP round trip must not hold the request or reveal that the account exists.
	send := panelSender()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := send(ctx, msg); err != nil {
			slog.Warn("panel: could not send the reset code", "error", err)
		}
	}()
}

// handlePanelReset sets a new password with a mailed code, ends every session and signs in.
func (d *deps) handlePanelReset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, ok := d.panelServer(ctx, chi.URLParam(r, "server"))
	if !ok {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	if !panelLogins.allow(panelClientIP(r) + "|" + s.ID) {
		writeJSONError(w, http.StatusTooManyRequests, "too many attempts, try again in a few minutes")
		return
	}
	var body struct {
		Email    string `json:"email"`
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if !readPanelJSON(w, r, &body) || !panelPasswordOK(w, body.Password) {
		return
	}
	q := db.New(d.a.SQLDB)
	key := panelKey(strings.ToLower(strings.TrimSpace(body.Email)), s.ID)
	codeKey := db.AccountCodeParams{Email: key, Purpose: "reset"}
	bad := func() { writeJSONError(w, http.StatusBadRequest, "the code is wrong or has expired") }
	row, err := q.AccountCode(ctx, codeKey)
	if err != nil {
		bad()
		return
	}
	if row.Attempts >= panelCodeAttempts || time.Now().After(row.ExpiresAt) {
		_, _ = q.DeleteAccountCode(ctx, db.DeleteAccountCodeParams(codeKey))
		bad()
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(row.CodeHash), []byte(strings.TrimSpace(body.Code))) != nil {
		_ = q.CountAccountCodeAttempt(ctx, db.CountAccountCodeAttemptParams(codeKey))
		bad()
		return
	}
	// Only the request that deletes the row wins, so a code cannot be spent twice.
	if n, err := q.DeleteAccountCode(ctx, db.DeleteAccountCodeParams(codeKey)); err != nil || n != 1 {
		bad()
		return
	}
	acc, err := q.PanelAccountByEmail(ctx, db.PanelAccountByEmailParams{Email: key, PanelServerID: &s.ID})
	if err != nil || acc.DisabledAt != nil {
		bad()
		return
	}
	if !d.setPanelPassword(w, r, acc.ID, s.ID, body.Password) {
		return
	}
	writePanelJSON(w, http.StatusOK, map[string]any{"account": map[string]string{"id": acc.ID, "email": panelEmail(acc.Email)}})
}

// handlePanelChangePassword changes a signed-in account's password after checking the current one.
func (d *deps) handlePanelChangePassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	serverID := chi.URLParam(r, "server")
	sess, ok := d.panelAccount(r, serverID)
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !panelLogins.allow(panelClientIP(r) + "|" + serverID) {
		writeJSONError(w, http.StatusTooManyRequests, "too many attempts, try again in a few minutes")
		return
	}
	var body struct {
		Current  string `json:"current"`
		Password string `json:"password"`
	}
	if !readPanelJSON(w, r, &body) || !panelPasswordOK(w, body.Password) {
		return
	}
	acc, err := db.New(d.a.SQLDB).PanelAccountByEmail(ctx, db.PanelAccountByEmailParams{Email: sess.Email, PanelServerID: &serverID})
	if err != nil || bcrypt.CompareHashAndPassword([]byte(acc.PasswordHash), []byte(body.Current)) != nil {
		writeJSONError(w, http.StatusBadRequest, "the current password is wrong")
		return
	}
	if !d.setPanelPassword(w, r, acc.ID, serverID, body.Password) {
		return
	}
	writePanelJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// setPanelPassword stores the new hash, ends every session of the account and opens a fresh one.
func (d *deps) setPanelPassword(w http.ResponseWriter, r *http.Request, userID, serverID, password string) bool {
	ctx := r.Context()
	q := db.New(d.a.SQLDB)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not set the password")
		return false
	}
	if n, err := q.SetPanelAccountPassword(ctx, db.SetPanelAccountPasswordParams{ID: userID, PanelServerID: &serverID, PasswordHash: string(hash)}); err != nil || n != 1 {
		writeJSONError(w, http.StatusInternalServerError, "could not set the password")
		return false
	}
	if err := q.DeletePanelSessionsOfUser(ctx, userID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not set the password")
		return false
	}
	return d.startPanelSession(w, r, userID, serverID)
}
