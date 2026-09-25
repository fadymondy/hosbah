package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// TestProductKeyPublicAPI exercises the public, bearer-key-authenticated instance API
// (internal/api/product_keys_public.go) end to end through real HTTP calls — never a superuser,
// always the app's own restricted role, exactly like every other integration test in this
// package. This is the positive-path counterpart to TestIsolationSweep's cross-tenant checks:
// it proves a validly-minted key can actually provision, suspend, resume and destroy an instance
// of its own product (MH-576 D7 review: keyAuth used to resolve the key in Scope{TeamID: ""},
// which FORCE RLS made impossible for the non-superuser cloudy_app role — every call failed with
// "invalid key" before reaching any authorization logic, so the isolation checks passed for the
// wrong reason. Fixed by resolving the key in Scope{TokenHash: hash} first, then switching the
// transaction to the key's own team.)
func TestProductKeyPublicAPI(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	ua := newClient(t, ts.URL)
	ua.register("owner-pk-" + suffix + "@example.test")

	var team struct{ ID, Slug string }
	ua.mustJSON("POST", "/api/teams", map[string]any{"name": "PK Team " + suffix}, 201, &team)
	srv, priv := enrolWithServicesKey(t, ua, ts.URL, team.ID, "pk-server-"+suffix[len(suffix)-6:], "203.0.113.60", nil)

	// A fake agent that completes a whole deploy pipeline (every instance's first deploy is a full
	// build — see deploy.ChooseTrack), so DestroyInstance's "a deployment is running" guard clears
	// and the positive destroy path below is real, not a race against an unstarted deployment.
	startFakeAgent(t, ts.URL, srv, priv, func(op agentproto.SignedOp) (any, string) {
		switch op.Kind {
		case agentproto.KindProvisionStep, agentproto.KindAppDrain, agentproto.KindAppRemove:
			return map[string]any{"ok": true}, ""
		case agentproto.KindAppBuild:
			var p agentproto.BuildParams
			_ = json.Unmarshal(op.Params, &p)
			return agentproto.BuildResult{Image: p.Image, DetectResult: agentproto.DetectResult{Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Runtime: "dockerfile"}}, ""
		case agentproto.KindAppStart:
			var p agentproto.StartParams
			_ = json.Unmarshal(op.Params, &p)
			return agentproto.StartResult{Container: agentproto.ContainerName(p.App, p.Process, p.DeploymentID), HostPort: 41500}, ""
		case agentproto.KindRouteSet:
			return map[string]any{"hosts": 0}, ""
		}
		return nil, "this fake agent does not run " + op.Kind
	})

	// Two products in the same team, so we can check that a key minted for one cannot touch an
	// instance of the other.
	var prodX, prodY struct{ ID, Name string }
	ua.mustJSON("POST", "/api/teams/"+team.ID+"/products", map[string]any{"name": "px-" + suffix[len(suffix)-6:],
		"repo_url": "https://github.com/example/app", "branch": "main"}, 201, &prodX)
	ua.mustJSON("POST", "/api/teams/"+team.ID+"/products", map[string]any{"name": "py-" + suffix[len(suffix)-6:],
		"repo_url": "https://github.com/example/app", "branch": "main"}, 201, &prodY)

	var keyXOut, keyYOut struct {
		Key   struct{ ID string } `json:"key"`
		Token string              `json:"token"`
	}
	ua.mustJSON("POST", "/api/teams/"+team.ID+"/products/"+prodX.ID+"/keys", map[string]any{"name": "billing-x"}, 201, &keyXOut)
	ua.mustJSON("POST", "/api/teams/"+team.ID+"/products/"+prodY.ID+"/keys", map[string]any{"name": "billing-y"}, 201, &keyYOut)

	// ---- positive path: provision, suspend, resume, destroy, all with key X on product X ------
	var inst struct {
		ID, Name, Status string
		AppID            string `json:"app_id"`
	}
	provisionBody := map[string]any{"server": srv, "customer_ref": "cust-" + suffix, "name": "cust-" + suffix[len(suffix)-6:]}
	code, text := keyDo(t, ts.URL, keyXOut.Token, "POST", "/api/products/instances", provisionBody)
	if code != http.StatusCreated {
		t.Fatalf("provision: %d: %s", code, text)
	}
	if err := json.Unmarshal([]byte(text), &inst); err != nil {
		t.Fatalf("decode provision response: %v: %s", err, text)
	}
	if inst.Status != "active" {
		t.Fatalf("provisioned instance should be active, got %q", inst.Status)
	}
	waitAppDeploySucceeded(t, ua, team.ID, inst.AppID)

	code, text = keyDo(t, ts.URL, keyXOut.Token, "POST", "/api/products/instances/"+inst.ID+"/suspend", nil)
	if code != http.StatusOK {
		t.Fatalf("suspend: %d: %s", code, text)
	}
	var afterSuspend struct{ Status string }
	if err := json.Unmarshal([]byte(text), &afterSuspend); err != nil {
		t.Fatalf("decode suspend response: %v: %s", err, text)
	}
	if afterSuspend.Status != "suspended" {
		t.Fatalf("instance should be suspended, got %q", afterSuspend.Status)
	}

	code, text = keyDo(t, ts.URL, keyXOut.Token, "POST", "/api/products/instances/"+inst.ID+"/resume", nil)
	if code != http.StatusOK {
		t.Fatalf("resume: %d: %s", code, text)
	}
	var afterResume struct{ Status string }
	if err := json.Unmarshal([]byte(text), &afterResume); err != nil {
		t.Fatalf("decode resume response: %v: %s", err, text)
	}
	if afterResume.Status != "active" {
		t.Fatalf("instance should be active again, got %q", afterResume.Status)
	}

	code, text = keyDo(t, ts.URL, keyXOut.Token, "DELETE", "/api/products/instances/"+inst.ID, map[string]any{"confirm": inst.Name})
	if code != http.StatusNoContent {
		t.Fatalf("destroy: %d: %s", code, text)
	}

	var afterDestroy struct {
		Instances []struct{ ID, Status string } `json:"instances"`
	}
	ua.mustJSON("GET", "/api/teams/"+team.ID+"/products/"+prodX.ID, nil, 200, &afterDestroy)
	found := false
	for _, i := range afterDestroy.Instances {
		if i.ID == inst.ID {
			found = true
			if i.Status != "destroyed" {
				t.Fatalf("instance should be destroyed, got %q", i.Status)
			}
		}
	}
	if !found {
		t.Fatalf("destroyed instance missing from product detail: %+v", afterDestroy.Instances)
	}

	// ---- negative: a revoked key is 401 -------------------------------------------------------
	var revoked struct {
		Instances []struct{ ID string } `json:"instances"`
		Keys      []struct{ ID string } `json:"keys"`
	}
	ua.mustJSON("GET", "/api/teams/"+team.ID+"/products/"+prodX.ID, nil, 200, &revoked)
	ua.mustJSON("POST", "/api/teams/"+team.ID+"/products/"+prodX.ID+"/keys/"+keyXOut.Key.ID+"/revoke", nil, 200, nil)
	code, text = keyDo(t, ts.URL, keyXOut.Token, "POST", "/api/products/instances", provisionBody)
	if code != http.StatusUnauthorized {
		t.Fatalf("revoked key should be 401, got %d: %s", code, text)
	}

	// A fresh, unrevoked key of product X's — keyXOut was just revoked above and must stay revoked
	// for that check to mean anything, so the next check (a valid key of the *wrong* product) needs
	// its own key.
	var keyX2Out struct {
		Key   struct{ ID string } `json:"key"`
		Token string              `json:"token"`
	}
	ua.mustJSON("POST", "/api/teams/"+team.ID+"/products/"+prodX.ID+"/keys", map[string]any{"name": "billing-x-2"}, 201, &keyX2Out)

	// ---- negative: key of product Y acting on an instance of product X (same team) -----------
	var instY struct{ ID, Name, Status string }
	code, text = keyDo(t, ts.URL, keyYOut.Token, "POST", "/api/products/instances",
		map[string]any{"server": srv, "customer_ref": "cust-y-" + suffix, "name": "cust-y-" + suffix[len(suffix)-6:]})
	if code != http.StatusCreated {
		t.Fatalf("provision on Y: %d: %s", code, text)
	}
	if err := json.Unmarshal([]byte(text), &instY); err != nil {
		t.Fatalf("decode provision-on-Y response: %v: %s", err, text)
	}
	code, text = keyDo(t, ts.URL, keyYOut.Token, "POST", "/api/products/instances/"+instY.ID+"/suspend", nil)
	if code != http.StatusOK {
		t.Fatalf("sanity: key Y suspending its own instance: %d: %s", code, text)
	}
	code, text = keyDo(t, ts.URL, keyYOut.Token, "POST", "/api/products/instances/"+instY.ID+"/resume", nil)
	if code != http.StatusOK {
		t.Fatalf("sanity: key Y resuming its own instance: %d: %s", code, text)
	}
	for _, call := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/products/instances/" + instY.ID + "/suspend", nil},
		{"POST", "/api/products/instances/" + instY.ID + "/resume", nil},
		{"DELETE", "/api/products/instances/" + instY.ID, map[string]any{"confirm": instY.Name}},
	} {
		// key X (product X) reaching for Y's instance
		code, text := keyDo(t, ts.URL, keyX2Out.Token, call.method, call.path, call.body)
		if code != http.StatusNotFound {
			t.Errorf("key X on product Y's instance %s %s -> %d (want 404): %s", call.method, call.path, code, text)
		}
	}

	// ---- negative: another team's key is 404 on this team's instance --------------------------
	ub := newClient(t, ts.URL)
	ub.register("owner-pk-b-" + suffix + "@example.test")
	var teamB struct{ ID string }
	ub.mustJSON("POST", "/api/teams", map[string]any{"name": "PK Team B " + suffix}, 201, &teamB)
	var prodB struct{ ID string }
	ub.mustJSON("POST", "/api/teams/"+teamB.ID+"/products", map[string]any{"name": "pb-" + suffix[len(suffix)-6:],
		"repo_url": "https://github.com/example/app", "branch": "main"}, 201, &prodB)
	var keyBOut struct{ Token string }
	ub.mustJSON("POST", "/api/teams/"+teamB.ID+"/products/"+prodB.ID+"/keys", map[string]any{"name": "billing-b"}, 201, &keyBOut)

	for _, call := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/products/instances/" + instY.ID + "/suspend", nil},
		{"POST", "/api/products/instances/" + instY.ID + "/resume", nil},
		{"DELETE", "/api/products/instances/" + instY.ID, map[string]any{"confirm": instY.Name}},
	} {
		code, text := keyDo(t, ts.URL, keyBOut.Token, call.method, call.path, call.body)
		if code != http.StatusNotFound {
			t.Errorf("team B's key on this team's instance %s %s -> %d (want 404): %s", call.method, call.path, code, text)
		}
	}
}

// waitAppDeploySucceeded polls an instance's app's latest deployment (through the team's own
// session, never the product key) until it succeeds, so a subsequent destroy through the key
// doesn't race DestroyInstance's "a deployment is running" guard.
func waitAppDeploySucceeded(t *testing.T, ua *client, teamID, appID string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var feed struct {
			Deployments []struct {
				Status string  `json:"status"`
				Error  *string `json:"error"`
			} `json:"deployments"`
		}
		ua.mustJSON("GET", "/api/teams/"+teamID+"/apps/"+appID+"/deployments", nil, 200, &feed)
		if len(feed.Deployments) > 0 {
			switch feed.Deployments[0].Status {
			case "succeeded":
				return
			case "failed", "cancelled":
				msg := ""
				if feed.Deployments[0].Error != nil {
					msg = *feed.Deployments[0].Error
				}
				t.Fatalf("app %s deployment: %s %s", appID, feed.Deployments[0].Status, msg)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("app %s deployment never succeeded: %+v", appID, feed.Deployments)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// keyDo makes a raw HTTP call bearing a product key (never a browser session/cookie) and returns
// the status code and body text.
func keyDo(t *testing.T, base, token, method, path string, body any) (int, string) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, base+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}
