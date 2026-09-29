package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Adopting an imported app: turning what Hosbah found running on a server into a normal, Hosbah-built
git app in place, with no rebuild required to take it over (internal/deploy/adopt.go). Missing
information never blocks it — GET .../adopt/proposal guesses what it can from the container's own
configuration (container.inspect, never logged) and a connected GitHub repo's name, each field
carrying a confidence and where it came from; the confirm dialog opens pre-filled with that. A
container gets release #0 pointing at the image already running, so a rollback has somewhere to go,
and its old container keeps running after the first real deploy too (whatever still routes to it
directly moves only when the team says so). A guest or systemd unit gets no
release #0. No old workload is ever stopped automatically; the team confirms it with one click, and
only once a real deploy is live.
*/
func TestAdoptImportedApp(t *testing.T) {
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	appsDomain := "adopt-" + suffix[len(suffix)-6:] + ".test"
	t.Setenv("HOSBAH_APPS_DOMAIN", appsDomain)
	ts, stop := bootTestServer(t)
	defer stop()

	owner := newClient(t, ts.URL)
	owner.register("adopt-owner-" + suffix + "@example.test")
	var team struct{ ID, Slug string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Adopt " + suffix}, 201, &team)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": "adopt-viewer-" + suffix + "@example.test", "role": "viewer"}, 201, &inv)
	viewer := newClient(t, ts.URL)
	viewer.register("adopt-viewer-" + suffix + "@example.test")
	viewer.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)

	srv, priv := enrolWithServicesKey(t, owner, ts.URL, team.ID, "adopt-"+suffix, "10.9.9.9", []agentproto.Service{
		{Kind: "container", Name: "shop-web", Image: "ghcr.io/acme/shop-web:1.2.3", Status: "running", Ports: []string{"0.0.0.0:8081->3000/tcp"}},
		{Kind: "guest", Name: "wordpress", ID: "201", Type: "lxc", Status: "running", Address: "10.0.0.21"},
	})

	agent := startFakeAgent(t, ts.URL, srv, priv, func(op agentproto.SignedOp) (any, string) {
		switch op.Kind {
		case agentproto.KindContainerInspect:
			// Mirrors what the real agent does: the env never travels as plaintext, only sealed to
			// the control plane's own op-signing key (the same key the fake agent itself verifies
			// operations against, svcOf(t).PublicKey()).
			envJSON, err := json.Marshal(map[string]string{"SECRET_TOKEN": "s3cret!", "PORT": "3000"})
			if err != nil {
				t.Fatal(err)
			}
			box, err := agentproto.SealBox(svcOf(t).PublicKey(), agentproto.AdoptEnvContext(op.OpID, "shop-web"), envJSON)
			if err != nil {
				t.Fatal(err)
			}
			return agentproto.ContainerInspectResult{
				Status: "running", Image: "ghcr.io/acme/shop-web:1.2.3", ImageDigest: "sha256:deadbeef",
				EnvBox: box,
				Ports:  []string{"3000/tcp -> 0.0.0.0:8081"},
				Labels: map[string]string{"org.opencontainers.image.source": "https://github.com/acme/shop-web", "org.opencontainers.image.revision": "main"},
			}, ""
		case agentproto.KindWorkloadPower:
			return agentproto.PowerResult{Status: "exited"}, ""
		}
		return map[string]any{"ok": true}, ""
	})

	apps, _ := listImported(t, owner, team.ID, srv)
	web, wp := apps["shop-web"], apps["wordpress"]
	if web.ID == "" || wp.ID == "" {
		t.Fatalf("imports: %v", keys(apps))
	}
	appPath := func(a importedApp) string { return "/api/teams/" + team.ID + "/apps/" + a.ID }

	t.Run("a proposal guesses a container's repo, branch and port from its own configuration", func(t *testing.T) {
		var prop struct {
			RepoURL struct{ Value, Confidence, Source string } `json:"repo_url"`
			Branch  struct{ Value, Confidence, Source string } `json:"branch"`
			Port    struct{ Value, Confidence, Source string } `json:"port"`
			Missing []string
		}
		owner.mustJSON("GET", appPath(web)+"/adopt/proposal", nil, 200, &prop)
		if prop.RepoURL.Value != "https://github.com/acme/shop-web" || prop.RepoURL.Confidence != "high" {
			t.Fatalf("repo_url: %+v", prop.RepoURL)
		}
		if prop.Branch.Value != "main" || prop.Branch.Confidence != "high" {
			t.Fatalf("branch: %+v", prop.Branch)
		}
		if prop.Port.Value != "8081" {
			t.Fatalf("port: %+v", prop.Port)
		}
		if len(prop.Missing) != 0 {
			t.Fatalf("nothing should be missing for this container: %v", prop.Missing)
		}
	})

	t.Run("a guest with nothing to inspect reports what it could not guess, never blocking anything", func(t *testing.T) {
		var prop struct {
			RepoURL     struct{ Value, Confidence, Source string } `json:"repo_url"`
			Missing     []string
			ContainerOK bool `json:"container_ok"`
		}
		owner.mustJSON("GET", appPath(wp)+"/adopt/proposal", nil, 200, &prop)
		if prop.ContainerOK {
			t.Fatal("a guest has nothing for container.inspect to read")
		}
		found := false
		for _, m := range prop.Missing {
			found = found || m == "repo_url"
		}
		if !found {
			t.Fatalf("repo_url should be reported missing, not guessed: %+v", prop)
		}
	})

	t.Run("viewers cannot adopt", func(t *testing.T) {
		viewer.expect("POST", appPath(web)+"/adopt", map[string]any{"repo_url": "https://github.com/acme/shop-web"}, 403)
	})

	t.Run("adopting a container seals its env, points release #0 at the running image, and never stops it", func(t *testing.T) {
		var out struct {
			App struct {
				ID, Source string
				RepoURL    *string `json:"repo_url"`
				Branch     *string `json:"branch"`
				Port       int64   `json:"port"`
			} `json:"app"`
			Release *struct {
				Version int64
				Image   string
				Status  string
			} `json:"release"`
		}
		owner.mustJSON("POST", appPath(web)+"/adopt", map[string]any{
			"repo_url": "https://github.com/acme/shop-web", "branch": "main", "build": "auto",
		}, 200, &out)
		if out.App.Source != "git" {
			t.Fatalf("source: %q", out.App.Source)
		}
		if out.App.RepoURL == nil || *out.App.RepoURL != "https://github.com/acme/shop-web" {
			t.Fatalf("repo_url: %v", out.App.RepoURL)
		}
		if out.Release == nil || out.Release.Version != 0 || out.Release.Image != "ghcr.io/acme/shop-web:1.2.3" || out.Release.Status != "live" {
			t.Fatalf("release #0: %+v", out.Release)
		}
		// Now a Hosbah-built app, it gets its platform hostname like a new one, to check its first
		// build on before any traffic moves.
		var domains struct {
			Domains []struct{ Hostname string } `json:"domains"`
		}
		owner.mustJSON("GET", appPath(web)+"/domains", nil, 200, &domains)
		if len(domains.Domains) != 1 || !strings.HasSuffix(domains.Domains[0].Hostname, "."+appsDomain) {
			t.Fatalf("an adopted app gets one platform hostname: %+v", domains.Domains)
		}

		var env struct {
			Variables []struct{ Name string } `json:"variables"`
		}
		owner.mustJSON("GET", appPath(web)+"/env", nil, 200, &env)
		names := map[string]bool{}
		for _, v := range env.Variables {
			names[v.Name] = true
		}
		if !names["SECRET_TOKEN"] {
			t.Fatalf("the captured env var must be sealed onto the app: %v", names)
		}
		if names["PORT"] {
			t.Fatal("PORT is Hosbah's own — it must not be captured as a user env var")
		}
		var reveal struct{ Value string }
		owner.mustJSON("POST", appPath(web)+"/env/SECRET_TOKEN/reveal", nil, 200, &reveal)
		if reveal.Value != "s3cret!" {
			t.Fatalf("sealed round-trip: got %q", reveal.Value)
		}

		// The old container is untouched: adopt never queues a destroy, and no destroy for shop-web
		// has reached the fake agent.
		for _, o := range agentOps(agent, agentproto.KindWorkloadDestroy) {
			t.Fatalf("adopt must never stop the old workload itself: %s", o.Params)
		}

		// operations.result is persisted as jsonb: the container.inspect op's stored result must
		// carry only the sealed EnvBox, never SECRET_TOKEN's or PORT's plaintext values.
		sqlDB := testDB(t)
		var results []string
		err := tenancy.Tx(context.Background(), sqlDB, tenancy.Scope{TeamID: team.ID}, func(_ *db.Queries, tx *sql.Tx) error {
			rows, err := tx.Query(`SELECT result::text FROM operations WHERE team_id = $1 AND kind = $2 AND result IS NOT NULL`,
				team.ID, agentproto.KindContainerInspect)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var r string
				if err := rows.Scan(&r); err != nil {
					return err
				}
				results = append(results, r)
			}
			return rows.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(results) == 0 {
			t.Fatal("expected at least one stored container.inspect result")
		}
		for _, r := range results {
			if strings.Contains(r, "s3cret") {
				t.Fatalf("operations.result leaked the plaintext env: %s", r)
			}
		}
	})

	t.Run("adopting again is refused: it is already a Hosbah build", func(t *testing.T) {
		owner.expectCode("POST", appPath(web)+"/adopt", map[string]any{"repo_url": "https://github.com/acme/shop-web"}, 409, "not_imported")
	})

	t.Run("confirming a container's old workload stopped is refused before a real deploy is live", func(t *testing.T) {
		owner.expect("POST", appPath(web)+"/adopt/confirm-stop", nil, 409)
	})

	t.Run("adopting a guest or unit gets no release #0, and the old workload is never stopped until confirmed", func(t *testing.T) {
		var out struct {
			App struct {
				ID, Source, OriginKind string
				RepoURL                string `json:"repo_url"`
			} `json:"app"`
			Release *struct{ Version int64 } `json:"release"`
		}
		owner.mustJSON("POST", appPath(wp)+"/adopt", map[string]any{"repo_url": "github.com/acme/wordpress-site.git", "branch": "main"}, 200, &out)
		if out.App.RepoURL != "https://github.com/acme/wordpress-site" {
			t.Fatalf("a bare github.com/owner/repo is stored as its https URL: %q", out.App.RepoURL)
		}
		if out.App.Source != "git" {
			t.Fatalf("source: %q", out.App.Source)
		}
		if out.Release != nil {
			t.Fatalf("a guest has no image to point a release at: %+v", out.Release)
		}
		// Confirming before a real deploy is live is refused — the old workload keeps serving.
		owner.expect("POST", appPath(wp)+"/adopt/confirm-stop", nil, 409)
		for _, o := range agentOps(agent, agentproto.KindWorkloadPower) {
			var p agentproto.PowerParams
			_ = json.Unmarshal(o.Params, &p)
			if p.Action == agentproto.PowerShutdown {
				t.Fatalf("the old guest must never be shut down before a real deploy is live: %s", o.Params)
			}
		}
	})
}
