package server

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

/*
Cost and usage (MH-575 H10) end to end: a team prices a server, splits it across two apps by their
configured memory limit, adds an extra line item, and the overview totals and per-app allocation
come back right over both REST and MCP. A viewer may read the overview but not set a price or a
line item (owner/admin only); another team gets 404/not-found for all of it.
*/
func TestCostsAndUsage(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]

	owner := newClient(t, ts.URL)
	owner.register("costs-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Costs " + suffix}, 201, &team)

	stranger := newClient(t, ts.URL)
	stranger.register("costs-stranger-" + suffix + "@example.test")
	var teamB struct{ ID string }
	stranger.mustJSON("POST", "/api/teams", map[string]any{"name": "Elsewhere " + suffix}, 201, &teamB)

	srv, _ := enrolServer(t, owner, ts.URL, team.ID, "costs-"+short)
	base := "/api/teams/" + team.ID + "/costs"

	// A viewer may read the overview but not write to it.
	viewerEmail := "costs-viewer-" + suffix + "@example.test"
	viewer := newClient(t, ts.URL)
	viewer.register(viewerEmail)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
	viewer.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)

	// A freshly enrolled server starts unpriced.
	type currencyAmount struct {
		Currency string  `json:"currency"`
		Amount   float64 `json:"amount"`
	}
	type usageOut struct {
		Readings      int64   `json:"readings"`
		AvgCPUPercent float64 `json:"avg_cpu_percent"`
		P95CPUPercent float64 `json:"p95_cpu_percent"`
		MemPercent    float64 `json:"mem_percent"`
		DiskPercent   float64 `json:"disk_percent"`
	}
	type appCostOut struct {
		AppID         string  `json:"app_id"`
		AppName       string  `json:"app_name"`
		Amount        float64 `json:"amount"`
		Currency      string  `json:"currency"`
		MemoryLimitMB int     `json:"memory_limit_mb"`
		Hint          string  `json:"hint"`
	}
	type serverCostOut struct {
		ServerID   string       `json:"server_id"`
		ServerName string       `json:"server_name"`
		Priced     bool         `json:"priced"`
		Amount     float64      `json:"amount"`
		Currency   string       `json:"currency"`
		Usage      *usageOut    `json:"usage"`
		Hint       string       `json:"hint"`
		Apps       []appCostOut `json:"apps"`
		IdleAmount float64      `json:"idle_amount"`
	}
	type lineItemOut struct {
		ID       string  `json:"id"`
		Name     string  `json:"name"`
		Amount   float64 `json:"amount"`
		Currency string  `json:"currency"`
	}
	type overviewOut struct {
		Window               string           `json:"window"`
		Totals               []currencyAmount `json:"totals"`
		AllocatedTotals      []currencyAmount `json:"allocated_totals"`
		IdleTotals           []currencyAmount `json:"idle_totals"`
		OverProvisionedCount int              `json:"over_provisioned_count"`
		UnderPressureCount   int              `json:"under_pressure_count"`
		Servers              []serverCostOut  `json:"servers"`
		LineItems            []lineItemOut    `json:"line_items"`
	}

	var ov overviewOut
	owner.mustJSON("GET", base+"/overview", nil, 200, &ov)
	if len(ov.Servers) != 1 || ov.Servers[0].Priced || len(ov.Totals) != 0 {
		t.Fatalf("a fresh team should have one unpriced server and no cost: %+v", ov)
	}

	// A viewer cannot set the price.
	if code, _ := viewer.do("PUT", base+"/servers/"+srv, map[string]any{"amount": 12.5}); code != 403 {
		t.Fatalf("viewer setting a server's price -> %d, want 403", code)
	}

	// The owner prices the server at $30/month.
	var priced struct {
		Amount   float64 `json:"amount"`
		Currency string  `json:"currency"`
	}
	owner.mustJSON("PUT", base+"/servers/"+srv, map[string]any{"amount": 30}, 200, &priced)
	if priced.Amount != 30 || priced.Currency != "USD" {
		t.Fatalf("expected 30 USD, got %+v", priced)
	}

	// Two apps on that server, with memory limits 512MB and 1536MB (1:3 share).
	var app1, app2 struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "costs-a-" + short, "server": srv,
		"repo_url": "https://github.com/octocat/api.git"}, 201, &app1)
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "costs-b-" + short, "server": srv,
		"repo_url": "https://github.com/octocat/api.git"}, 201, &app2)
	owner.mustJSON("PUT", "/api/teams/"+team.ID+"/apps/"+app1.App.ID+"/resources", map[string]any{"resources": map[string]any{"memory_mb": 512}}, 200, nil)
	owner.mustJSON("PUT", "/api/teams/"+team.ID+"/apps/"+app2.App.ID+"/resources", map[string]any{"resources": map[string]any{"memory_mb": 1536}}, 200, nil)

	// A line item on top: Cloudflare, $5/month.
	var li lineItemOut
	owner.mustJSON("POST", base+"/line-items", map[string]any{"name": "Cloudflare", "amount": 5}, 201, &li)
	if li.Name != "Cloudflare" || li.Amount != 5 || li.Currency != "USD" {
		t.Fatalf("expected the line item to be created as given: %+v", li)
	}

	owner.mustJSON("GET", base+"/overview", nil, 200, &ov)
	if len(ov.Totals) != 1 || ov.Totals[0].Currency != "USD" || ov.Totals[0].Amount != 35 {
		t.Fatalf("expected a single USD total of 35 (30 server + 5 line item), got %+v", ov.Totals)
	}
	if len(ov.LineItems) != 1 || ov.LineItems[0].Name != "Cloudflare" {
		t.Fatalf("expected the Cloudflare line item to show, got %+v", ov.LineItems)
	}
	var so *serverCostOut
	for i := range ov.Servers {
		if ov.Servers[i].ServerID == srv {
			so = &ov.Servers[i]
		}
	}
	if so == nil || !so.Priced || so.Amount != 30 {
		t.Fatalf("expected the priced server to show 30, got %+v", so)
	}
	if len(so.Apps) != 2 {
		t.Fatalf("expected 2 apps under the server, got %+v", so.Apps)
	}
	byName := map[string]appCostOut{}
	for _, a := range so.Apps {
		byName[a.AppName] = a
	}
	a1, a2 := byName[app1.App.Name], byName[app2.App.Name]
	// No memory usage was ever reported, so the split falls back to the sum of the configured
	// limits (512+1536=2048): a1 gets 1/4, a2 gets 3/4, and nothing is left idle.
	if a1.Amount <= 0 || a2.Amount <= a1.Amount {
		t.Fatalf("expected app 2 (1536MB) to cost more than app 1 (512MB): a1=%+v a2=%+v", a1, a2)
	}
	wantA1, wantA2 := 7.5, 22.5 // 30 * 512/2048, 30 * 1536/2048
	if a1.Amount != wantA1 || a2.Amount != wantA2 {
		t.Fatalf("expected a 1:3 split of 30 (%.2f/%.2f), got a1=%.2f a2=%.2f", wantA1, wantA2, a1.Amount, a2.Amount)
	}
	if so.IdleAmount != 0 {
		t.Fatalf("fully claimed memory should leave nothing idle, got idle=%.2f", so.IdleAmount)
	}

	// Rename and reprice the line item.
	var updated lineItemOut
	owner.mustJSON("PUT", base+"/line-items/"+li.ID, map[string]any{"name": "Cloudflare DNS", "amount": 8}, 200, &updated)
	if updated.Name != "Cloudflare DNS" || updated.Amount != 8 {
		t.Fatalf("expected the rename/reprice to stick: %+v", updated)
	}

	// A viewer cannot touch line items either.
	if code, _ := viewer.do("POST", base+"/line-items", map[string]any{"name": "x", "amount": 1}); code != 403 {
		t.Fatalf("viewer creating a line item -> %d, want 403", code)
	}

	// Clearing the server's price takes it back to unpriced, and its apps drop off the totals.
	owner.mustJSON("DELETE", base+"/servers/"+srv, nil, 204, nil)
	owner.mustJSON("GET", base+"/overview", nil, 200, &ov)
	for i := range ov.Servers {
		if ov.Servers[i].ServerID == srv && ov.Servers[i].Priced {
			t.Fatalf("expected the server to be unpriced after DELETE: %+v", ov.Servers[i])
		}
	}
	if len(ov.Totals) != 1 || ov.Totals[0].Amount != 8 {
		t.Fatalf("expected only the line item (8) to remain in the total, got %+v", ov.Totals)
	}

	owner.mustJSON("DELETE", base+"/line-items/"+li.ID, nil, 204, nil)
	owner.mustJSON("GET", base+"/overview", nil, 200, &ov)
	if len(ov.Totals) != 0 {
		t.Fatalf("expected no cost left after removing the line item, got %+v", ov.Totals)
	}

	// Server usage endpoint: no metrics were ever reported for this server.
	var usage struct {
		Window string   `json:"window"`
		Usage  usageOut `json:"usage"`
	}
	owner.mustJSON("GET", "/api/teams/"+team.ID+"/servers/"+srv+"/usage", nil, 200, &usage)
	if usage.Usage.Readings != 0 {
		t.Fatalf("expected no readings for a server with no metrics: %+v", usage)
	}

	// Team isolation: the stranger's team gets 404 for all of it.
	if code, _ := stranger.do("GET", base+"/overview", nil); code != 404 {
		t.Fatalf("stranger reading team A's cost overview -> %d, want 404", code)
	}
	if code, _ := stranger.do("PUT", base+"/servers/"+srv, map[string]any{"amount": 1}); code != 404 {
		t.Fatalf("stranger pricing team A's server -> %d, want 404", code)
	}
	if code, _ := stranger.do("GET", base+"/line-items", nil); code != 404 {
		t.Fatalf("stranger reading team A's line items -> %d, want 404", code)
	}

	// ---- MCP -----------------------------------------------------------------------------------
	var tok struct{ Token string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "costs-mcp", "scopes": []string{"read", "write"}, "expires_in_days": 30}, 201, &tok)

	set := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "set_server_cost", map[string]any{"server": srv, "amount": 20, "currency": "eur"})
	if set.IsError || !strings.Contains(set.Text, "20.00 EUR") {
		t.Fatalf("MCP set_server_cost: %+v", set)
	}
	usageRes := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "server_usage", map[string]any{"server": srv})
	if usageRes.IsError || !strings.Contains(usageRes.Text, "no metrics") {
		t.Fatalf("MCP server_usage: %+v", usageRes)
	}
	items := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "cost_line_items", map[string]any{"action": "add", "name": "Backups", "amount": 3})
	if items.IsError || !strings.Contains(items.Text, "Backups") {
		t.Fatalf("MCP cost_line_items add: %+v", items)
	}
	overviewRes := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "cost_overview", nil)
	if overviewRes.IsError || !strings.Contains(overviewRes.Text, "EUR") || !strings.Contains(overviewRes.Text, "USD") {
		t.Fatalf("MCP cost_overview should show both the EUR server price and the USD line item: %+v", overviewRes)
	}
	list := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "cost_line_items", map[string]any{"action": "list"})
	if list.IsError || !strings.Contains(list.Text, "Backups") {
		t.Fatalf("MCP cost_line_items list: %+v", list)
	}

	// A read-only (viewer-role) token cannot set a price. Minting a token needs owner/admin
	// (AbilityManageTokens), so a plain viewer can never mint their own — instead a second member is
	// invited as admin (so they can mint a token for themselves), then demoted to viewer. A token
	// "acts as the user who minted it, with that user's CURRENT role" (internal/mcp/user.go), so the
	// same token now carries the viewer role live and should be refused.
	restrictedEmail := "costs-restricted-" + suffix + "@example.test"
	restricted := newClient(t, ts.URL)
	restricted.register(restrictedEmail)
	var inv2 struct {
		AcceptURL string `json:"accept_url"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": restrictedEmail, "role": "admin"}, 201, &inv2)
	restricted.mustJSON("POST", "/api/invitations/"+inv2.AcceptURL[strings.LastIndex(inv2.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)

	var viewerTok struct{ Token string }
	restricted.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "costs-viewer-mcp", "scopes": []string{"read", "write"}, "expires_in_days": 30}, 201, &viewerTok)

	var members struct {
		Members []struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"members"`
	}
	owner.mustJSON("GET", "/api/teams/"+team.ID+"/members", nil, 200, &members)
	var memberID string
	for _, mm := range members.Members {
		if mm.Email == restrictedEmail {
			memberID = mm.ID
		}
	}
	if memberID == "" {
		t.Fatalf("could not find the restricted member to demote: %+v", members)
	}
	owner.mustJSON("PATCH", "/api/teams/"+team.ID+"/members/"+memberID, map[string]any{"role": "viewer"}, 200, nil)

	forbidden := mcpCall(t, ts.URL, "/api/mcp", viewerTok.Token, "set_server_cost", map[string]any{"server": srv, "amount": 1})
	if !forbidden.IsError {
		t.Fatalf("viewer-role token should not be able to set a server's price: %+v", forbidden)
	}
}
