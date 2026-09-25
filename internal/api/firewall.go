package api

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// MH-580 J1: a per-server firewall. Rules live in firewall_rules (team-scoped, RLS), one settings
// row per server in firewall_settings (default policy, SSH port override). Nothing on the server
// itself changes until Apply is called: it renders the whole `table inet hosbah` (see
// internal/agent/firewall.go) and loads it with firewall.apply. Switching the default policy to
// "drop" needs an explicit confirm ("drop") on the settings call — the same confirm-the-name
// pattern services.go uses for a destructive systemd action — because it can cut off anything not
// explicitly allowed.

// FirewallRuleOut is one firewall rule.
type FirewallRuleOut struct {
	ID         string    `json:"id"`
	ServerID   string    `json:"server_id"`
	Action     string    `json:"action" enum:"allow,deny"`
	Proto      string    `json:"proto" enum:"tcp,udp,any"`
	PortStart  int       `json:"port_start"`
	PortEnd    int       `json:"port_end"`
	SourceCIDR string    `json:"source_cidr"`
	Comment    string    `json:"comment"`
	Enabled    bool      `json:"enabled"`
	Position   int       `json:"position"`
	CreatedBy  string    `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func toFirewallRuleOut(r db.FirewallRule) FirewallRuleOut {
	return FirewallRuleOut{ID: r.ID, ServerID: r.ServerID, Action: r.Action, Proto: r.Proto, PortStart: int(r.PortStart),
		PortEnd: int(r.PortEnd), SourceCIDR: r.SourceCidr, Comment: r.Comment, Enabled: r.Enabled, Position: int(r.Position),
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// FirewallRuleIn is what a caller sends to create or change a rule.
type FirewallRuleIn struct {
	Action     string `json:"action" enum:"allow,deny"`
	Proto      string `json:"proto" enum:"tcp,udp,any"`
	PortStart  int    `json:"port_start" minimum:"1" maximum:"65535"`
	PortEnd    int    `json:"port_end" minimum:"1" maximum:"65535"`
	SourceCIDR string `json:"source_cidr,omitempty" maxLength:"64" doc:"\"any\", an IP, or a CIDR. Empty means any."`
	Comment    string `json:"comment,omitempty" maxLength:"200"`
	Enabled    *bool  `json:"enabled,omitempty"`
	Position   *int   `json:"position,omitempty"`
}

func (in FirewallRuleIn) toSpec() agentproto.FirewallRuleSpec {
	return agentproto.FirewallRuleSpec{Action: in.Action, Proto: in.Proto, PortStart: in.PortStart, PortEnd: in.PortEnd,
		SourceCIDR: in.SourceCIDR, Comment: in.Comment}
}

// FirewallRulePath addresses one rule of a server.
type FirewallRulePath struct {
	ServerPath
	Rule string `path:"rule" maxLength:"64"`
}

// FirewallSettingsOut is a server's firewall settings, defaulted when none has been saved yet.
type FirewallSettingsOut struct {
	ServerID      string `json:"server_id"`
	DefaultPolicy string `json:"default_policy" enum:"accept,drop"`
	SSHPort       int    `json:"ssh_port"`
}

func defaultFirewallSettings(serverID string) FirewallSettingsOut {
	return FirewallSettingsOut{ServerID: serverID, DefaultPolicy: agentproto.FirewallPolicyAccept, SSHPort: 22}
}

func toFirewallSettingsOut(s db.FirewallSetting) FirewallSettingsOut {
	return FirewallSettingsOut{ServerID: s.ServerID, DefaultPolicy: s.DefaultPolicy, SSHPort: int(s.SshPort)}
}

func (d *deps) registerFirewall(api huma.API) {
	tags := []string{"Firewall"}
	base := "/api/teams/{team}/servers/{server}/firewall"

	type firewallListOut struct {
		Body struct {
			Rules []FirewallRuleOut `json:"rules"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-firewall-rules", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "A server's firewall rules, in the order they are applied. Any member.",
	}, func(ctx context.Context, in *ServerPath) (*firewallListOut, error) {
		out := &firewallListOut{}
		out.Body.Rules = []FirewallRuleOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			rows, err := q.ListFirewallRulesOfServer(ctx, db.ListFirewallRulesOfServerParams{ServerID: srv.ID, TeamID: m.TeamID})
			if err != nil {
				return huma.Error500InternalServerError("could not list firewall rules")
			}
			for _, r := range rows {
				out.Body.Rules = append(out.Body.Rules, toFirewallRuleOut(r))
			}
			return nil
		})
		return out, err
	})

	type ruleOut struct{ Body FirewallRuleOut }
	huma.Register(api, huma.Operation{OperationID: "create-firewall-rule", Method: http.MethodPost, Path: base,
		DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Add a firewall rule (owner or admin). Not applied to the server until POST .../firewall/apply.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Body FirewallRuleIn
	}) (*ruleOut, error) {
		out := &ruleOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			b := in.Body
			spec := b.toSpec()
			if spec.SourceCIDR == "" {
				spec.SourceCIDR = "any"
			}
			if err := spec.Validate(); err != nil {
				return huma.Error422UnprocessableEntity(err.Error())
			}
			enabled := true
			if b.Enabled != nil {
				enabled = *b.Enabled
			}
			position := b.PortStart
			if b.Position != nil {
				position = *b.Position
			} else {
				rows, err := q.ListFirewallRulesOfServer(ctx, db.ListFirewallRulesOfServerParams{ServerID: srv.ID, TeamID: m.TeamID})
				if err == nil {
					position = len(rows)
				}
			}
			r, err := q.CreateFirewallRule(ctx, db.CreateFirewallRuleParams{TeamID: m.TeamID, ServerID: srv.ID, Action: spec.Action,
				Proto: spec.Proto, PortStart: int32(spec.PortStart), PortEnd: int32(spec.PortEnd), SourceCidr: spec.SourceCIDR,
				Comment: b.Comment, Enabled: enabled, Position: int32(position), CreatedBy: id.ID})
			if err != nil {
				return huma.Error500InternalServerError("could not create the firewall rule")
			}
			out.Body = toFirewallRuleOut(r)
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "firewall_rule.created", "server", srv.ID, map[string]any{"rule_id": r.ID})
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "update-firewall-rule", Method: http.MethodPatch, Path: base + "/{rule}", Tags: tags,
		Summary: "Change a firewall rule (owner or admin). Not applied until POST .../firewall/apply.",
	}, func(ctx context.Context, in *struct {
		FirewallRulePath
		Body FirewallRuleIn
	}) (*ruleOut, error) {
		out := &ruleOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			existing, err := q.FirewallRuleInTeam(ctx, db.FirewallRuleInTeamParams{TeamID: m.TeamID, ID: in.Rule})
			if err != nil {
				return errNotFound
			}
			b := in.Body
			spec := b.toSpec()
			if spec.SourceCIDR == "" {
				spec.SourceCIDR = "any"
			}
			if err := spec.Validate(); err != nil {
				return huma.Error422UnprocessableEntity(err.Error())
			}
			enabled := existing.Enabled
			if b.Enabled != nil {
				enabled = *b.Enabled
			}
			position := int(existing.Position)
			if b.Position != nil {
				position = *b.Position
			}
			r, err := q.UpdateFirewallRule(ctx, db.UpdateFirewallRuleParams{ID: existing.ID, TeamID: m.TeamID, Action: spec.Action,
				Proto: spec.Proto, PortStart: int32(spec.PortStart), PortEnd: int32(spec.PortEnd), SourceCidr: spec.SourceCIDR,
				Comment: b.Comment, Enabled: enabled, Position: int32(position)})
			if err != nil {
				return huma.Error500InternalServerError("could not update the firewall rule")
			}
			out.Body = toFirewallRuleOut(r)
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "firewall_rule.updated", "server", existing.ServerID, map[string]any{"rule_id": r.ID})
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "delete-firewall-rule", Method: http.MethodDelete, Path: base + "/{rule}",
		DefaultStatus: http.StatusNoContent, Tags: tags, Summary: "Remove a firewall rule (owner or admin). Not applied until POST .../firewall/apply.",
	}, func(ctx context.Context, in *FirewallRulePath) (*struct{}, error) {
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			existing, err := q.FirewallRuleInTeam(ctx, db.FirewallRuleInTeamParams{TeamID: m.TeamID, ID: in.Rule})
			if err != nil {
				return errNotFound
			}
			if err := q.DeleteFirewallRuleOfTeam(ctx, db.DeleteFirewallRuleOfTeamParams{ID: existing.ID, TeamID: m.TeamID}); err != nil {
				return huma.Error500InternalServerError("could not delete the firewall rule")
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "firewall_rule.deleted", "server", existing.ServerID, map[string]any{"rule_id": existing.ID})
		})
		return nil, err
	})

	type firewallPresetsOut struct {
		Body struct {
			Presets []agentproto.FirewallPreset `json:"presets"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-firewall-presets", Method: http.MethodGet, Path: base + "/presets", Tags: tags,
		Summary: "Named templates of common rules (web, ssh, postgres-from-cidr). Any member.",
	}, func(ctx context.Context, in *ServerPath) (*firewallPresetsOut, error) {
		out := &firewallPresetsOut{}
		out.Body.Presets = agentproto.FirewallPresets
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(*db.Queries, *sql.Tx, *auth.Identity, *tenancy.Membership) error { return nil })
		return out, err
	})

	type applyPresetOut struct{ Body FirewallRuleOut }
	huma.Register(api, huma.Operation{OperationID: "add-firewall-preset", Method: http.MethodPost, Path: base + "/presets/{preset}",
		DefaultStatus: http.StatusCreated, Tags: tags, Summary: "Add a preset's rule(s) to a server (owner or admin). postgres-from-cidr needs source_cidr.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Preset string `path:"preset" maxLength:"32"`
		Body   struct {
			SourceCIDR string `json:"source_cidr,omitempty" maxLength:"64"`
		}
	}) (*applyPresetOut, error) {
		out := &applyPresetOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			specs, err := agentproto.BuildFirewallPreset(in.Preset, in.Body.SourceCIDR)
			if err != nil {
				return huma.Error422UnprocessableEntity(err.Error())
			}
			rows, _ := q.ListFirewallRulesOfServer(ctx, db.ListFirewallRulesOfServerParams{ServerID: srv.ID, TeamID: m.TeamID})
			pos := len(rows)
			var last db.FirewallRule
			for _, spec := range specs {
				r, err := q.CreateFirewallRule(ctx, db.CreateFirewallRuleParams{TeamID: m.TeamID, ServerID: srv.ID, Action: spec.Action,
					Proto: spec.Proto, PortStart: int32(spec.PortStart), PortEnd: int32(spec.PortEnd), SourceCidr: spec.SourceCIDR,
					Comment: spec.Comment, Enabled: true, Position: int32(pos), CreatedBy: id.ID})
				if err != nil {
					return huma.Error500InternalServerError("could not add the preset")
				}
				last, pos = r, pos+1
			}
			out.Body = toFirewallRuleOut(last)
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "firewall_rule.preset_added", "server", srv.ID, map[string]any{"preset": in.Preset})
		})
		return out, err
	})

	type settingsOut struct{ Body FirewallSettingsOut }
	huma.Register(api, huma.Operation{OperationID: "get-firewall-settings", Method: http.MethodGet, Path: base + "/settings", Tags: tags,
		Summary: "A server's firewall default policy and SSH port. Any member.",
	}, func(ctx context.Context, in *ServerPath) (*settingsOut, error) {
		out := &settingsOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			s, err := q.FirewallSettingOfServer(ctx, db.FirewallSettingOfServerParams{ServerID: srv.ID, TeamID: m.TeamID})
			if err != nil {
				out.Body = defaultFirewallSettings(srv.ID)
				return nil
			}
			out.Body = toFirewallSettingsOut(s)
			return nil
		})
		return out, err
	})

	huma.Register(api, huma.Operation{OperationID: "update-firewall-settings", Method: http.MethodPut, Path: base + "/settings", Tags: tags,
		Summary: "Change the default policy and SSH port (owner or admin). Switching default_policy to \"drop\" needs " +
			"confirm=\"drop\" — it can cut off anything not explicitly allowed. Not applied until POST .../firewall/apply.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Body struct {
			DefaultPolicy string `json:"default_policy" enum:"accept,drop"`
			SSHPort       int    `json:"ssh_port,omitempty" minimum:"1" maximum:"65535"`
			Confirm       string `json:"confirm,omitempty" doc:"Must equal \"drop\" when default_policy is drop."`
		}
	}) (*settingsOut, error) {
		out := &settingsOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			b := in.Body
			if !agentproto.ValidFirewallPolicy(b.DefaultPolicy) {
				return huma.Error422UnprocessableEntity("default_policy must be accept or drop")
			}
			if b.DefaultPolicy == agentproto.FirewallPolicyDrop && b.Confirm != agentproto.FirewallPolicyDrop {
				return huma.Error422UnprocessableEntity(`switching to a drop default policy needs confirm="drop"`)
			}
			sshPort := b.SSHPort
			if sshPort == 0 {
				sshPort = 22
			}
			s, err := q.UpsertFirewallSetting(ctx, db.UpsertFirewallSettingParams{TeamID: m.TeamID, ServerID: srv.ID,
				DefaultPolicy: b.DefaultPolicy, SshPort: int32(sshPort), UpdatedBy: id.ID})
			if err != nil {
				return huma.Error500InternalServerError("could not save the firewall settings")
			}
			out.Body = toFirewallSettingsOut(s)
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "firewall_settings.updated", "server", srv.ID,
				map[string]any{"default_policy": s.DefaultPolicy, "ssh_port": s.SshPort})
		})
		return out, err
	})

	type applyOut struct {
		Body agentproto.FirewallApplyResult
	}
	huma.Register(api, huma.Operation{OperationID: "apply-firewall", Method: http.MethodPost, Path: base + "/apply", Tags: tags,
		Summary: "Render the server's rules (plus SSH and, unless keep_web_ports is false, 80/443) into `table inet hosbah` and " +
			"load it with nft. Owner or admin. Audited.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Body struct {
			KeepWebPorts *bool `json:"keep_web_ports,omitempty" doc:"Default true: keep 80/443 open for Caddy regardless of rules."`
		}
	}) (*applyOut, error) {
		out := &applyOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			settings, err := q.FirewallSettingOfServer(ctx, db.FirewallSettingOfServerParams{ServerID: srv.ID, TeamID: m.TeamID})
			def := defaultFirewallSettings(srv.ID)
			if err == nil {
				def = toFirewallSettingsOut(settings)
			}
			rows, err := q.ListFirewallRulesOfServer(ctx, db.ListFirewallRulesOfServerParams{ServerID: srv.ID, TeamID: m.TeamID})
			if err != nil {
				return huma.Error500InternalServerError("could not list firewall rules")
			}
			keepWeb := true
			if in.Body.KeepWebPorts != nil {
				keepWeb = *in.Body.KeepWebPorts
			}
			p := agentproto.FirewallApplyParams{DefaultPolicy: def.DefaultPolicy, SSHPort: def.SSHPort, KeepWebPorts: keepWeb}
			for _, r := range rows {
				if !r.Enabled {
					continue
				}
				p.Rules = append(p.Rules, agentproto.FirewallRuleSpec{Action: r.Action, Proto: r.Proto, PortStart: int(r.PortStart),
					PortEnd: int(r.PortEnd), SourceCIDR: r.SourceCidr, Comment: r.Comment})
			}
			// p.Rules is already in the server's saved order (ListFirewallRulesOfServer orders by position).
			if err := p.Validate(); err != nil {
				return huma.Error422UnprocessableEntity(err.Error())
			}
			if err := d.runServiceOp(ctx, m.TeamID, srv.ID, agentproto.KindFirewallApply, p, &out.Body); err != nil {
				return err
			}
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "firewall.applied", "server", srv.ID,
				map[string]any{"default_policy": p.DefaultPolicy, "rules": len(p.Rules)})
		})
		return out, err
	})

	type statusOut struct {
		Body agentproto.FirewallStatusResult
	}
	huma.Register(api, huma.Operation{OperationID: "firewall-status", Method: http.MethodGet, Path: base + "/status", Tags: tags,
		Summary: "The active `table inet hosbah` ruleset, read live from the server. Any member.",
	}, func(ctx context.Context, in *ServerPath) (*statusOut, error) {
		out := &statusOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			srv, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			return d.runServiceOp(ctx, m.TeamID, srv.ID, agentproto.KindFirewallStatus, struct{}{}, &out.Body)
		})
		return out, err
	})
}
