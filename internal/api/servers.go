package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/provisioning"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// ServerOut is a server as the API shows it. Never the agent's key material or the sealed SSH key.
type ServerOut struct {
	ID               string              `json:"id"`
	Name             string              `json:"name"`
	Provider         string              `json:"provider" enum:"byo,proxmox"`
	Kind             string              `json:"kind" enum:"byo,vm,lxc"`
	Status           string              `json:"status" enum:"pending,provisioning,enrolled,failed"`
	Online           bool                `json:"online" doc:"The agent heartbeated within the last 45 seconds."`
	Hostname         *string             `json:"hostname"`
	IPAddress        *string             `json:"ip_address"`
	OS               *string             `json:"os"`
	OSVersion        *string             `json:"os_version"`
	Arch             *string             `json:"arch"`
	Kernel           *string             `json:"kernel"`
	AgentVersion     *string             `json:"agent_version"`
	CredentialID     *string             `json:"credential_id"`
	ProviderRef      *string             `json:"provider_ref"`
	Error            *string             `json:"error"`
	Metrics          *agentproto.Metrics `json:"metrics"`
	Checks           []agentproto.Check  `json:"checks"`
	Facts            *agentproto.Facts   `json:"facts,omitempty"`
	LastSeenAt       *time.Time          `json:"last_seen_at"`
	EnrolledAt       *time.Time          `json:"enrolled_at"`
	CreatedAt        time.Time           `json:"created_at"`
	Enrollment       *EnrollmentOut      `json:"enrollment,omitempty" doc:"Set while an install link is open. Never the token itself."`
	GitHub           ServerGitHubOut     `json:"github" doc:"The server's own GitHub connection (the GitHub CLI on it), as last reported."`
	Tunnel           *TunnelSummary      `json:"tunnel" doc:"The server's tunnel (an active frp one first), or null without one."`
	Color            *string             `json:"color" doc:"#RRGGBB, or null for the default."`
	Icon             *string             `json:"icon" doc:"An icon key (see PATCH), or null for the default."`
	LogoURL          *string             `json:"logo_url" doc:"The image shown instead of the icon: the uploaded logo's relative URL (see PUT .../logo), else the external https logo_url, or null."`
	ParentServerID   *string             `json:"parent_server_id" doc:"The Proxmox node server this server is a guest of, or null."`
	ParentServerName *string             `json:"parent_server_name,omitempty" doc:"The parent's name, when parent_server_id is set."`
	GuestVMID        *int64              `json:"guest_vmid" doc:"This server's Proxmox VMID on its parent node, or null."`
	GuestType        *string             `json:"guest_type" enum:"lxc,qemu" doc:"lxc or qemu, or null."`
	Guests           []GuestOut          `json:"guests,omitempty" doc:"For a Proxmox node: every guest it discovered, enrolled or not."`
}

// GuestOut is one Proxmox guest a node server discovered, whether or not it is also enrolled as a
// server of the team.
type GuestOut struct {
	VMID      string  `json:"vmid"`
	Name      string  `json:"name"`
	Type      string  `json:"type" enum:"lxc,qemu"`
	Status    string  `json:"status"`
	Address   string  `json:"address,omitempty"`
	CPUs      int     `json:"cpus,omitempty"`
	MemBytes  uint64  `json:"mem_bytes,omitempty"`
	DiskBytes uint64  `json:"disk_bytes,omitempty"`
	ServerID  *string `json:"server_id,omitempty" doc:"Set when this guest is also enrolled as a server of the team."`
}

// guestsOf reads the Proxmox guests out of a node's facts (agentproto.Service, kind "guest").
func guestsOf(s db.Server) []GuestOut {
	if s.Facts == nil {
		return nil
	}
	var f agentproto.Facts
	if json.Unmarshal([]byte(*s.Facts), &f) != nil {
		return nil
	}
	var out []GuestOut
	for _, svc := range f.Services {
		if svc.Kind != "guest" || svc.ID == "" {
			continue
		}
		out = append(out, GuestOut{VMID: svc.ID, Name: svc.Name, Type: svc.Type, Status: svc.Status,
			Address: svc.Address, CPUs: svc.CPUs, MemBytes: svc.MemBytes, DiskBytes: svc.DiskBytes})
	}
	return out
}

// EnrollmentOut says when the open install link expires.
type EnrollmentOut struct {
	ExpiresAt time.Time `json:"expires_at"`
}

// ServerPath addresses one server of a team.
type ServerPath struct {
	TeamPath
	Server string `path:"server" maxLength:"64"`
}

func toServerOut(s db.Server, now time.Time, detail bool) ServerOut {
	out := ServerOut{
		ID: s.ID, Name: s.Name, Provider: s.Provider, Kind: s.Kind, Status: s.Status,
		Online:   s.Status == "enrolled" && ops.Online(s.LastSeenAt, now),
		Hostname: s.Hostname, IPAddress: s.IpAddress, OS: s.Os, OSVersion: s.OsVersion, Arch: s.Arch, Kernel: s.Kernel,
		AgentVersion: s.AgentVersion, CredentialID: s.CredentialID, ProviderRef: s.ProviderRef, Error: s.Error,
		LastSeenAt: s.LastSeenAt, EnrolledAt: s.EnrolledAt, CreatedAt: s.CreatedAt, Checks: []agentproto.Check{},
		Color: s.Color, Icon: s.Icon, LogoURL: s.LogoUrl,
		ParentServerID: s.ParentServerID, GuestType: s.GuestType,
	}
	if s.GuestVmid.Valid {
		v := int64(s.GuestVmid.Int32)
		out.GuestVMID = &v
	}
	out.GitHub = toServerGitHubOut(deploy.GitHubStateOf(s, now))
	if s.Metrics != nil {
		var m agentproto.Metrics
		if json.Unmarshal([]byte(*s.Metrics), &m) == nil {
			out.Metrics = &m
		}
	}
	if s.Checks != nil {
		_ = json.Unmarshal([]byte(*s.Checks), &out.Checks)
	}
	if detail && s.Facts != nil {
		var f agentproto.Facts
		if json.Unmarshal([]byte(*s.Facts), &f) == nil {
			out.Facts = &f
		}
	}
	return out
}

// serverIn resolves a server of the membership's team, or 404.
func serverIn(ctx context.Context, q *db.Queries, m *tenancy.Membership, id string) (db.Server, error) {
	s, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: id, TeamID: m.TeamID})
	if err != nil {
		return db.Server{}, errNotFound
	}
	return s, nil
}

// ProviderOption tells the add-server wizard what it can offer, and why not when it cannot.
type ProviderOption struct {
	Key         string          `json:"key" enum:"byo,proxmox"`
	Available   bool            `json:"available"`
	Reason      string          `json:"reason,omitempty"`
	Credentials []CredentialRef `json:"credentials"`
}

// CredentialRef names a credential (no config, no secret).
type CredentialRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (d *deps) registerServers(api huma.API) {
	type serversOut struct {
		Body struct {
			Servers      []ServerOut `json:"servers"`
			AgentVersion string      `json:"agent_version" doc:"The agent build this control plane serves."`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-servers", Method: http.MethodGet, Path: "/api/teams/{team}/servers",
		Summary: "The team's servers, with status and the last heartbeat's metrics", Tags: []string{"Servers"},
	}, func(ctx context.Context, in *TeamPath) (*serversOut, error) {
		out := &serversOut{}
		out.Body.Servers = []ServerOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			rows, err := q.ListServersOfTeam(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list servers")
			}
			now := time.Now()
			for _, r := range rows {
				o := toServerOut(r, now, false)
				o.Guests = guestsOf(r)
				out.Body.Servers = append(out.Body.Servers, o)
			}
			attachTunnels(ctx, q, m.TeamID, out.Body.Servers)
			attachLogos(ctx, q, m.TeamID, out.Body.Servers)
			attachParentNames(ctx, q, m.TeamID, out.Body.Servers)
			attachGuestServerIDs(ctx, q, m.TeamID, out.Body.Servers)
			out.Body.AgentVersion = d.ops.AvailableAgentVersion()
			return nil
		})
		return out, err
	})

	type providersOut struct {
		Body struct {
			Providers []ProviderOption `json:"providers"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-server-providers", Method: http.MethodGet, Path: "/api/teams/{team}/server-providers",
		Summary: "Where this team can add servers from, and why a provider is unavailable", Tags: []string{"Servers"},
	}, func(ctx context.Context, in *TeamPath) (*providersOut, error) {
		out := &providersOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			creds, err := q.ListCredentialsOfTeam(ctx, m.TeamID)
			if err != nil {
				return huma.Error500InternalServerError("could not list credentials")
			}
			px := ProviderOption{Key: "proxmox", Credentials: []CredentialRef{}}
			for _, c := range creds {
				if c.Provider == "proxmox" {
					px.Credentials = append(px.Credentials, CredentialRef{ID: c.ID, Name: c.Name})
				}
			}
			px.Available = len(px.Credentials) > 0
			if !px.Available {
				px.Reason = "no_credential"
			}
			out.Body.Providers = []ProviderOption{{Key: "byo", Available: true, Credentials: []CredentialRef{}}, px}
			return nil
		})
		return out, err
	})

	type serverCreatedOut struct {
		Body struct {
			Server  ServerOut    `json:"server"`
			Install *ops.Install `json:"install,omitempty" doc:"Bring-your-own: the one-time install command. Shown once; the token is not stored."`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "create-server", Method: http.MethodPost, Path: "/api/teams/{team}/servers",
		DefaultStatus: http.StatusCreated, Tags: []string{"Servers"},
		Summary: "Add a server: bring your own (a one-time install command, or connect over SSH with a password or key) or create one on Proxmox (owner or admin)",
	}, func(ctx context.Context, in *struct {
		TeamPath
		Body struct {
			Name         string     `json:"name" minLength:"1" maxLength:"63" pattern:"^[A-Za-z0-9][A-Za-z0-9._-]*$" doc:"Letters, digits, dot, dash, underscore; unique in the team."`
			Provider     string     `json:"provider" enum:"byo,proxmox"`
			CredentialID string     `json:"credential_id,omitempty" maxLength:"64" doc:"Proxmox: which of the team's credentials to use."`
			Kind         string     `json:"kind,omitempty" enum:"vm,lxc" doc:"Proxmox: a VM cloned from the cloud-init template, or an unprivileged LXC."`
			Size         string     `json:"size,omitempty" enum:"small,medium,large"`
			IPConfig     string     `json:"ipconfig,omitempty" maxLength:"200" doc:"Proxmox: static address for networks without DHCP, e.g. ip=10.10.10.121/24,gw=10.10.10.1 (empty: DHCP)."`
			Nameserver   string     `json:"nameserver,omitempty" maxLength:"45" doc:"Proxmox LXC: resolver for a static address, e.g. 1.1.1.1."`
			Cores        int        `json:"cores,omitempty" minimum:"0" maximum:"64" doc:"Proxmox: vCPUs, overriding the size preset (0: the preset's)."`
			MemoryMB     int        `json:"memory_mb,omitempty" minimum:"0" maximum:"262144" doc:"Proxmox: memory in MiB, overriding the size preset."`
			DiskGB       int        `json:"disk_gb,omitempty" minimum:"0" maximum:"4096" doc:"Proxmox: root disk in GiB, overriding the size preset."`
			Connect      *ConnectIn `json:"connect,omitempty" doc:"Bring-your-own only: log in over SSH and set the server up now, instead of returning an install command."`
		}
	}) (*serverCreatedOut, error) {
		out := &serverCreatedOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			b := in.Body
			var conn *provisioning.ConnectInput
			if c := b.Connect; c != nil {
				conn = &provisioning.ConnectInput{Host: c.Host, Port: c.Port, User: c.User, Auth: c.Auth,
					Password: c.Password, PrivateKey: c.PrivateKey, RegisterProxmox: c.RegisterProxmox}
			}
			row, inst, err := provisioning.AddServer(ctx, d.ops, q, m.TeamID, id.ID, provisioning.AddInput{Name: b.Name, Provider: b.Provider,
				CredentialID: b.CredentialID, Kind: b.Kind, Size: b.Size, IPConfig: b.IPConfig, Nameserver: b.Nameserver,
				Cores: b.Cores, MemoryMB: b.MemoryMB, DiskGB: b.DiskGB, Connect: conn})
			if err != nil {
				return provisioningErr(err)
			}
			out.Body.Install = inst
			out.Body.Server = toServerOut(row, time.Now(), true)
			return nil
		})
		return out, err
	})

	type serverOut struct{ Body ServerOut }
	huma.Register(api, huma.Operation{
		OperationID: "get-server", Method: http.MethodGet, Path: "/api/teams/{team}/servers/{server}",
		Summary: "One server: status, facts, enrolment checks, latest metrics", Tags: []string{"Servers"},
	}, func(ctx context.Context, in *ServerPath) (*serverOut, error) {
		out := &serverOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			out.Body = d.serverDetail(ctx, q, m, s)
			return nil
		})
		return out, err
	})

	type installOut struct{ Body ops.Install }
	huma.Register(api, huma.Operation{
		OperationID: "create-server-enrollment", Method: http.MethodPost, Path: "/api/teams/{team}/servers/{server}/enrollment",
		DefaultStatus: http.StatusCreated, Tags: []string{"Servers"},
		Summary: "A new one-time install command for this server (replaces an unused one; owner or admin)",
	}, func(ctx context.Context, in *ServerPath) (*installOut, error) {
		out := &installOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			inst, err := d.ops.NewEnrollment(ctx, q, m.TeamID, s.ID, id.ID)
			if err != nil {
				return huma.Error500InternalServerError("could not create the install command")
			}
			out.Body = inst
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "server.enrollment_created", "server", s.ID, map[string]any{"expires_at": inst.ExpiresAt})
		})
		return out, err
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-server", Method: http.MethodDelete, Path: "/api/teams/{team}/servers/{server}",
		DefaultStatus: http.StatusNoContent, Tags: []string{"Servers"},
		Summary: "Remove a server (owner or admin). A Proxmox machine Hosbah created is destroyed first; a bring-your-own server is only forgotten.",
	}, func(ctx context.Context, in *ServerPath) (*struct{}, error) {
		var removed string
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			if n, err := q.CountAppsOnServer(ctx, db.CountAppsOnServerParams{ServerID: s.ID, TeamID: m.TeamID}); err == nil && n > 0 {
				return huma.Error409Conflict(fmt.Sprintf("%d app(s) run on this server — delete them first", n))
			}
			// Workloads imported from this server are only records: forgetting the server forgets them
			// (nothing on the host changes).
			if err := q.DeleteImportedAppsOnServer(ctx, db.DeleteImportedAppsOnServerParams{ServerID: s.ID, TeamID: m.TeamID}); err != nil {
				return huma.Error500InternalServerError("could not forget the server's imported apps")
			}
			if err := q.DeleteImportedDataServicesOnServer(ctx, db.DeleteImportedDataServicesOnServerParams{ServerID: s.ID, TeamID: m.TeamID}); err != nil {
				return huma.Error500InternalServerError("could not forget the server's imported databases")
			}
			if s.Provider == "proxmox" && s.ProviderRef != nil && s.CredentialID != nil {
				if err := provisioning.DeleteMachine(ctx, d.a.Keys, q, s); err != nil {
					return huma.Error502BadGateway("the machine could not be deleted on Proxmox, so the server was kept: " + err.Error())
				}
			}
			if n, err := q.DeleteServerInTeam(ctx, db.DeleteServerInTeamParams{ID: s.ID, TeamID: m.TeamID}); err != nil || n == 0 {
				return errNotFound
			}
			removed = s.ID
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "server.deleted", "server", s.ID,
				map[string]any{"name": s.Name, "provider": s.Provider, "provider_ref": deref(s.ProviderRef)})
		})
		if err == nil && removed != "" {
			d.ops.Disconnect(removed)
		}
		return nil, err
	})

	type guestsOutBody struct {
		Body struct {
			Guests []GuestOut `json:"guests"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-server-guests", Method: http.MethodGet, Path: "/api/teams/{team}/servers/{server}/guests",
		Summary: "A Proxmox node's discovered guests, enrolled or not", Tags: []string{"Servers"},
	}, func(ctx context.Context, in *ServerPath) (*guestsOutBody, error) {
		out := &guestsOutBody{}
		out.Body.Guests = []GuestOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			guests := guestsOf(s)
			rows, err := q.ListServersOfTeam(ctx, m.TeamID)
			if err == nil {
				byVMID := map[string]string{}
				for _, r := range rows {
					if r.ParentServerID != nil && *r.ParentServerID == s.ID && r.GuestVmid.Valid {
						byVMID[fmt.Sprint(r.GuestVmid.Int32)] = r.ID
					}
				}
				for i := range guests {
					if id, ok := byVMID[guests[i].VMID]; ok {
						guests[i].ServerID = &id
					}
				}
			}
			out.Body.Guests = guests
			return nil
		})
		return out, err
	})

	type parentIn struct {
		ServerPath
		Body struct {
			ParentServerID *string `json:"parent_server_id" doc:"The node server this is a guest of, or null to unlink."`
			GuestVMID      *int64  `json:"guest_vmid,omitempty" doc:"Its Proxmox VMID on that node."`
		}
	}
	type parentOut struct{ Body ServerOut }
	huma.Register(api, huma.Operation{
		OperationID: "set-server-parent", Method: http.MethodPut, Path: "/api/teams/{team}/servers/{server}/parent",
		Summary: "Manually link (or unlink) a server as the guest of a Proxmox node in the same team (owner or admin)",
		Tags:    []string{"Servers"},
	}, func(ctx context.Context, in *parentIn) (*parentOut, error) {
		out := &parentOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageServers, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			s, err := serverIn(ctx, q, m, in.Server)
			if err != nil {
				return err
			}
			var parentID *string
			var vmid sql.NullInt32
			var guestType *string
			if in.Body.ParentServerID != nil && *in.Body.ParentServerID != "" {
				parent, err := serverIn(ctx, q, m, *in.Body.ParentServerID)
				if err != nil {
					return huma.Error422UnprocessableEntity("parent server not found in this team")
				}
				if parent.ID == s.ID {
					return huma.Error422UnprocessableEntity("a server cannot be its own parent")
				}
				parentID = &parent.ID
				if in.Body.GuestVMID != nil {
					vmid = sql.NullInt32{Int32: int32(*in.Body.GuestVMID), Valid: true}
				}
				// guest_type stays whatever it was (or null): a manual link only names the parent and
				// VMID, and auto-link (or the next facts sync) will not touch it either way since it is
				// now manual.
				guestType = s.GuestType
			}
			row, err := q.SetServerParent(ctx, db.SetServerParentParams{ID: s.ID, TeamID: m.TeamID,
				ParentServerID: parentID, GuestVmid: vmid, GuestType: guestType, Manual: true})
			if err != nil {
				return huma.Error500InternalServerError("could not update the server's parent")
			}
			out.Body = d.serverDetail(ctx, q, m, row)
			return tenancy.Record(ctx, q, m.TeamID, id.ID, "server.parent_set", "server", s.ID,
				map[string]any{"parent_server_id": deref(parentID)})
		})
		return out, err
	})
}

// serverDetail is the detail view (also the SSE snapshot).
// attachParentNames fills in ParentServerName for servers whose ParentServerID is set.
func attachParentNames(ctx context.Context, q *db.Queries, teamID string, servers []ServerOut) {
	var ids []string
	for _, s := range servers {
		if s.ParentServerID != nil {
			ids = append(ids, *s.ParentServerID)
		}
	}
	if len(ids) == 0 {
		return
	}
	names := map[string]string{}
	for _, s := range servers {
		names[s.ID] = s.Name
	}
	for i := range servers {
		if servers[i].ParentServerID != nil {
			if n, ok := names[*servers[i].ParentServerID]; ok {
				servers[i].ParentServerName = &n
			}
		}
	}
}

// attachGuestServerIDs fills in ServerID for a node's discovered guests that are also enrolled as
// servers of the team (matched on VMID against those servers' guest_vmid/parent_server_id).
func attachGuestServerIDs(ctx context.Context, q *db.Queries, teamID string, servers []ServerOut) {
	byParentVMID := map[string]string{}
	for _, s := range servers {
		if s.ParentServerID != nil && s.GuestVMID != nil {
			byParentVMID[*s.ParentServerID+":"+fmt.Sprint(*s.GuestVMID)] = s.ID
		}
	}
	if len(byParentVMID) == 0 {
		return
	}
	for i := range servers {
		if len(servers[i].Guests) == 0 {
			continue
		}
		for j := range servers[i].Guests {
			if id, ok := byParentVMID[servers[i].ID+":"+servers[i].Guests[j].VMID]; ok {
				servers[i].Guests[j].ServerID = &id
			}
		}
	}
}

func (d *deps) serverDetail(ctx context.Context, q *db.Queries, m *tenancy.Membership, s db.Server) ServerOut {
	out := toServerOut(s, time.Now(), true)
	out.Guests = guestsOf(s)
	out.Tunnel = serverTunnel(ctx, q, m.TeamID, s.ID)
	if out.ParentServerID != nil {
		if p, err := serverIn(ctx, q, m, *out.ParentServerID); err == nil {
			out.ParentServerName = &p.Name
		}
	}
	if all, err := q.ListServersOfTeam(ctx, m.TeamID); err == nil {
		outs := make([]ServerOut, 0, len(all))
		for _, r := range all {
			outs = append(outs, toServerOut(r, time.Now(), false))
		}
		outs = append(outs, out)
		attachGuestServerIDs(ctx, q, m.TeamID, outs)
		out = outs[len(outs)-1]
	}
	if etag, err := q.ServerLogoVersion(ctx, db.ServerLogoVersionParams{ServerID: s.ID, TeamID: m.TeamID}); err == nil {
		out.LogoURL = serverLogoURL(m.TeamID, s.ID, etag) // an uploaded logo wins over the external logo_url
	}
	if s.Status == "pending" || s.Status == "provisioning" {
		if e, err := q.OpenEnrollmentForServer(ctx, db.OpenEnrollmentForServerParams{ServerID: s.ID, TeamID: m.TeamID}); err == nil {
			out.Enrollment = &EnrollmentOut{ExpiresAt: e.ExpiresAt}
		}
	}
	return out
}

// ConnectIn is how to log in to a server the user already has. The password or key is sealed with
// the team key for the connect operation only, then forgotten; Hosbah installs its own key.
type ConnectIn struct {
	Host            string `json:"host" minLength:"1" maxLength:"253" doc:"IP address or hostname."`
	Port            int    `json:"port,omitempty" minimum:"1" maximum:"65535" doc:"Default 22."`
	User            string `json:"user" minLength:"1" maxLength:"64" pattern:"^[a-z_][a-z0-9_.-]*$" doc:"root, or a user with passwordless sudo."`
	Auth            string `json:"auth" enum:"password,key"`
	Password        string `json:"password,omitempty" maxLength:"1024" writeOnly:"true"`
	PrivateKey      string `json:"private_key,omitempty" maxLength:"16384" writeOnly:"true" doc:"OpenSSH or PEM private key, without a passphrase."`
	RegisterProxmox *bool  `json:"register_proxmox,omitempty" doc:"If the host is a Proxmox VE node, also create an API token and save it as a credential. Default true."`
}

// provisioningErr maps a provisioning refusal to 422/409 and anything else to 500.
func provisioningErr(err error) error {
	if pe := platformErr(err); pe != nil {
		return pe
	}
	if qe := quotaErr(err); qe != nil {
		return qe
	}
	var ue *provisioning.UserError
	if errors.As(err, &ue) {
		if ue.Conflict {
			return huma.Error409Conflict(ue.Msg)
		}
		return huma.Error422UnprocessableEntity(ue.Msg)
	}
	return huma.Error500InternalServerError(err.Error())
}
