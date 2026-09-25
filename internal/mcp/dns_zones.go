package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// DNSZoneTools: the DNS zone manager (MH-578 E8) — every zone and record the team's Cloudflare
// credentials can see, not just the one record each app's own hostname needs (DNSTools, dns.go).
func DNSZoneTools() []Tool {
	credArg := str("Cloudflare credential id or name")
	zoneArg := str("Zone id or name (e.g. example.com)")
	recordArg := str("Record id (from dns_records_list)")
	typeArg := map[string]any{"type": "string", "enum": []string{"A", "AAAA", "CNAME", "TXT", "MX", "SRV", "CAA"}, "description": "Record type"}
	return []Tool{
		{
			Name: "dns_zones_list", Scope: scopes.Read,
			Description: "Every DNS zone the team's Cloudflare credentials can see.",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				zones, err := s.ListDNSZones(ctx, env.Q, env.Caller.TeamID)
				if err != nil {
					return "", err
				}
				if len(zones) == 0 {
					return "No Cloudflare credential of this team holds any zone.", nil
				}
				var b strings.Builder
				for _, z := range zones {
					fmt.Fprintf(&b, "  %-32s status %-10s credential %s (id %s)\n", z.Name, z.Status, z.CredentialName, z.CredentialID)
				}
				return b.String(), nil
			},
		},
		{
			Name: "dns_records_list", Scope: scopes.Read,
			Description: "Every record of a zone, flagged where Hosbah already manages the hostname elsewhere (an app domain, a proxy host, " +
				"or mail DNS) — treat those with care before changing or deleting them.",
			InputSchema: schema([]string{"credential", "zone"}, map[string]any{"credential": credArg, "zone": zoneArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				z, recs, err := s.ListDNSZoneRecords(ctx, env.Q, env.Caller.TeamID, args.Str("credential"), args.Str("zone"))
				if err != nil {
					return "", err
				}
				if len(recs) == 0 {
					return z.Name + " has no records.", nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "zone %s (%s):\n", z.Name, z.CredentialName)
				for _, r := range recs {
					b.WriteString(dnsZoneRecordLine(r))
				}
				return b.String(), nil
			},
		},
		{
			Name: "dns_record_upsert", Scope: scopes.Write,
			Description: "Create a record (record omitted) or change one (record given) in a zone of the team's: A, AAAA, CNAME, TXT, MX " +
				"(priority required), SRV or CAA. Owner or admin.",
			InputSchema: schema([]string{"credential", "zone"}, map[string]any{
				"credential": credArg, "zone": zoneArg, "record": str("Omit to create; give a record id to change it"),
				"type": typeArg, "name": str("Hostname, e.g. www or www.example.com (required to create)"),
				"content": str("Record content, e.g. an IP, hostname or text value (required to create)"),
				"ttl":     map[string]any{"type": "integer", "description": "Seconds; 1 or omitted = automatic"},
				"priority": map[string]any{"type": "integer", "description": "MX (and SRV) only"},
				"proxied":  map[string]any{"type": "boolean", "description": "Put the record behind Cloudflare's proxy"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				var proxied *bool
				if v, ok := args["proxied"].(bool); ok {
					proxied = &v
				}
				in := deploy.DNSZoneRecordInput{Type: args.Str("type"), Name: args.Str("name"), Content: args.Str("content"),
					TTL: intArg(args, "ttl"), Priority: intArg(args, "priority"), Proxied: proxied}
				if rec := args.Str("record"); rec != "" {
					_, r, err := s.UpdateDNSZoneRecord(ctx, env.Q, env.Caller.TeamID, mcpActor(env), args.Str("credential"), args.Str("zone"), rec, in)
					if err != nil {
						return "", err
					}
					return "Updated:\n" + dnsZoneRecordLine(r), nil
				}
				_, r, err := s.CreateDNSZoneRecord(ctx, env.Q, env.Caller.TeamID, mcpActor(env), args.Str("credential"), args.Str("zone"), in)
				if err != nil {
					return "", err
				}
				return "Created:\n" + dnsZoneRecordLine(r), nil
			},
		},
		{
			Name: "dns_record_delete", Scope: scopes.Destructive, Destructive: true, ResourceType: "domain",
			Description: "Remove a record from a zone of the team's. If dns_records_list flagged it as managed by Hosbah elsewhere (an app " +
				"domain, proxy host or mail DNS record), removing it here can break that — check first.",
			InputSchema: schema([]string{"credential", "zone", "record", "confirm"}, map[string]any{
				"credential": credArg, "zone": zoneArg, "record": recordArg,
				"confirm": map[string]any{"type": "boolean", "description": "Must be true"},
			}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				if err := s.DeleteDNSZoneRecord(ctx, env.Q, env.Caller.TeamID, mcpActor(env), args.Str("credential"), args.Str("zone"), args.Str("record")); err != nil {
					return "", err
				}
				return "Record removed.", nil
			},
		},
	}
}

func intArg(args Args, key string) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func dnsZoneRecordLine(r deploy.DNSZoneRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  %-6s %-32s %-32s", r.Type, r.Name, r.Content)
	if r.Priority > 0 {
		fmt.Fprintf(&b, " priority %d", r.Priority)
	}
	if r.Proxied != nil && *r.Proxied {
		b.WriteString(" proxied")
	}
	if r.ManagedByHosbah {
		b.WriteString(" managed-by-hosbah")
	}
	if r.ManagedKind != "" {
		fmt.Fprintf(&b, " [hosbah manages this hostname: %s — warn before editing/deleting]", r.ManagedKind)
	}
	fmt.Fprintf(&b, " (id %s)\n", r.ID)
	return b.String()
}
