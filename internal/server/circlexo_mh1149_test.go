package server

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"testing"

	circlexo "github.com/circlexo/circlexo-go"
	"github.com/circlexo/circlexo-go/oidc"
	"github.com/circlexo/circlexo-go/webhooks"

	db "github.com/fadymondy/hosbah/internal/db/gen"
)

// MH-1149: deleting the org's team in Hosbah is final. The next sign-in does not make a new one;
// it tells the hub, which shows the app as not installed until it is installed again.
func TestCircleXONoRecreate(t *testing.T) {
	s := bootHub(t)
	org := hubOrg()
	s.install(org, [2]string{"own-" + rnd(), "owner"})
	sub, email := "sub-"+rnd(), "keep-"+rnd()+"@example.test"
	if _, _, err := s.login(sub, email, true, org, "owner", "sid-"+rnd()); err != nil {
		t.Fatal(err)
	}
	team, _ := s.teamOf(org)
	// As the owner of the row, the way the team-delete endpoint does (RLS hides it otherwise).
	s.inTeam(team, func(_ *db.Queries, tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM teams WHERE id = $1`, team)
		return err
	})
	if s.count(`SELECT count(*) FROM circlexo_org_links WHERE org_id = $1`, org) != 0 {
		t.Fatal("deleting the team kept its link")
	}
	before := s.count(`SELECT count(*) FROM teams`)
	if _, to, err := s.login(sub, email, true, org, "owner", "sid-"+rnd()); err != nil || to != "/" {
		t.Fatalf("after delete: %q %v", to, err)
	}
	if s.count(`SELECT count(*) FROM teams`) != before || s.count(`SELECT count(*) FROM circlexo_org_links WHERE org_id = $1`, org) != 0 {
		t.Fatal("a sign-in re-created the deleted team")
	}
	s.hub.Lock()
	tn, still := s.hub.Tenants[org]
	s.hub.Unlock()
	if still && tn.Status != "removed" {
		t.Fatalf("the hub was not told: tenant %q", tn.Status)
	}
	// Installing again from the hub provisions a fresh one.
	s.hub.Install(org)
	if _, _, err := s.login(sub, email, true, org, "owner", "sid-"+rnd()); err != nil {
		t.Fatal(err)
	}
	if fresh, _ := s.teamOf(org); fresh == team {
		t.Fatal("reinstall reused the deleted team id")
	}
}

// MH-1149: a verified address change on the hub moves the linked account, at sign-in and on
// user.updated; an unverified one does not.
func TestCircleXOProfileSync(t *testing.T) {
	s := bootHub(t)
	sub, email := "sub-"+rnd(), "prof-"+rnd()+"@example.test"
	signIn := func(addr string) {
		t.Helper()
		id := &circlexo.Claims{Subject: sub, SessionID: "sid-" + rnd(), Raw: map[string]any{"email": addr, "email_verified": true}}
		if _, err := s.svc.FinishLogin(context.Background(), httptest.NewRecorder(), &oidc.Login{IDClaims: id, AccessClaims: &circlexo.Claims{Subject: sub}}); err != nil {
			t.Fatal(err)
		}
	}
	mail := func() string {
		t.Helper()
		var m string
		if err := s.sql.QueryRow(`SELECT u.email FROM circlexo_user_links l JOIN users u ON u.id = l.user_id WHERE l.subject = $1`, sub).Scan(&m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	signIn(email)
	moved := "moved-" + rnd() + "@example.test"
	signIn(moved)
	if m := mail(); m != moved {
		t.Fatalf("sign-in sync: %q", m)
	}
	again := "again-" + rnd() + "@example.test"
	user := map[string]any{"id": sub, "email": again, "email_verified": true, "display_name": "X"}
	if code := s.event(webhooks.UserUpdated, hubOrg(), map[string]any{"user": user}, "evt-"+rnd()); code >= 300 {
		t.Fatalf("user.updated: %d", code)
	}
	if m := mail(); m != again {
		t.Fatalf("user.updated sync: %q", m)
	}
	user["email"], user["email_verified"] = "unverified-"+rnd()+"@example.test", false
	s.event(webhooks.UserUpdated, hubOrg(), map[string]any{"user": user}, "evt-"+rnd())
	if m := mail(); m != again {
		t.Fatalf("an unverified address replaced the account's: %q", m)
	}
}
