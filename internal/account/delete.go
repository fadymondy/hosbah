package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/platform"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// Account deletion codes.
const (
	CodeConfirmMismatch    = "confirm_mismatch"
	CodeTeamHasResources   = "team_has_resources"
	CodeInvalidCredentials = "invalid_credentials"
)

/*
POST /api/me/delete {password?, confirm?} → 200 {status:"deleted"}: deletes the signed-in account,
from the web app or the phone app (App Store review guideline 5.1.1(v): an app that lets people
create an account must let them delete it in the app).

  - Proof it is really you: the current password when the account has one; otherwise (signed up
    with Google, GitHub or Apple) the account's email address typed into confirm.
  - Teams you own alone go with you, but only when they run nothing: a team with servers or apps
    answers 409 team_has_resources {teams:[{id, slug, name}]} — remove those (or hand the team to
    someone) first, so deleting an account never orphans a running server.
  - Teams you share keep going: ownership passes on and your membership goes (platform.DeleteUser's
    steps). The last platform admin cannot delete themselves.
  - Apple: the refresh tokens kept at sign-in are revoked at Apple, best effort.

Your sessions, devices, linked identities and two-factor go with the account (foreign keys).
*/
func (s *Service) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := me(w, r)
	if !ok {
		return
	}
	if !s.tries.allow("delete:" + id.ID) {
		tooMany(w)
		return
	}
	var in struct {
		Password string `json:"password"`
		Confirm  string `json:"confirm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, CodeInvalidRequest, "invalid body")
		return
	}
	ctx := r.Context()
	u, err := s.userByID(ctx, id.ID)
	if err != nil {
		fail(w, http.StatusUnauthorized, CodeUnauthorized, "sign in first")
		return
	}
	hash, err := s.q().UserPasswordHash(ctx, u.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "could not delete the account")
		return
	}
	if strings.HasPrefix(hash, "$2") {
		if in.Password == "" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
			fail(w, http.StatusUnprocessableEntity, CodeInvalidCredentials, "the password is not right")
			return
		}
	} else if normEmail(in.Confirm) != normEmail(u.Email) {
		fail(w, http.StatusUnprocessableEntity, CodeConfirmMismatch, "type your email address to confirm")
		return
	}

	// The teams that would be left without an owner, and whether any still runs something.
	var sole []db.SoleOwnedTeamsRow
	var idents []db.UserIdentity
	err = tenancy.Tx(ctx, s.DB, tenancy.Scope{UserID: u.ID}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		if sole, err = q.SoleOwnedTeams(ctx, u.ID); err != nil {
			return err
		}
		idents, err = q.ListUserIdentities(ctx, u.ID)
		return err
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "could not delete the account")
		return
	}
	busy := []map[string]string{}
	for _, t := range sole {
		var n db.TeamResourceCountsRow
		err := tenancy.Tx(ctx, s.DB, tenancy.Scope{UserID: u.ID, TeamID: t.ID}, func(q *db.Queries, _ *sql.Tx) error {
			var err error
			n, err = q.TeamResourceCounts(ctx, t.ID)
			return err
		})
		if err != nil {
			fail(w, http.StatusInternalServerError, CodeInternal, "could not delete the account")
			return
		}
		if n.Servers > 0 || n.Apps > 0 {
			busy = append(busy, map[string]string{"id": t.ID, "slug": t.Slug, "name": t.Name})
		}
	}
	if len(busy) > 0 {
		fail(w, http.StatusConflict, CodeTeamHasResources,
			"you are the only owner of teams that still have servers or apps — remove them or hand the team over first", "teams", busy)
		return
	}

	err = tenancy.Tx(ctx, s.DB, tenancy.Scope{UserID: u.ID, PlatformAdmin: true}, func(q *db.Queries, _ *sql.Tx) error {
		for _, t := range sole {
			if err := q.DeleteTeam(ctx, t.ID); err != nil {
				return err
			}
		}
		return platform.DeleteOwnAccount(ctx, q, u.ID)
	})
	var pe *platform.Error
	switch {
	case errors.As(err, &pe):
		fail(w, pe.Status, "account_delete_refused", pe.Msg)
		return
	case err != nil:
		s.Log.Error("account: self-service deletion failed", "error", err)
		fail(w, http.StatusInternalServerError, CodeInternal, "could not delete the account")
		return
	}
	s.revokeProviderTokens(context.WithoutCancel(ctx), idents)
	s.Log.Info("account: deleted by its owner", "user", u.ID, "teams_deleted", len(sole))
	clearSession(w, r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
