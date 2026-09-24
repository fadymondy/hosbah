package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// AutoLinkFacts parses a facts document (agentproto.Facts JSON, as SyncFacts does) and auto-links
// its guests.
func AutoLinkFacts(ctx context.Context, sqlDB *sql.DB, teamID, serverID string, facts []byte) (int, error) {
	var f struct {
		Services *[]agentproto.Service `json:"services"`
	}
	if err := json.Unmarshal(facts, &f); err != nil || f.Services == nil {
		return 0, nil
	}
	return AutoLinkGuests(ctx, sqlDB, teamID, serverID, *f.Services)
}

// GuestFact is one Proxmox guest a node agent discovered (agentproto.Service, kind "guest").
type GuestFact struct {
	VMID    string
	Type    string // lxc | qemu
	Name    string
	Address string
}

// GuestServer is a candidate the guest could be: another server of the same team.
type GuestServer struct {
	ID        string
	Hostname  string
	IPAddress string
	// Manual is true when the server's parent link was set (or explicitly cleared) by a member;
	// auto-link must never touch it.
	Manual bool
}

// GuestLink is what auto-link decided for one server: it is the guest of the node, as VMID/Type.
type GuestLink struct {
	ServerID string
	VMID     string
	Type     string
}

// GuestsOf extracts the Proxmox guests from a node's discovered services (kind "guest"), skipping
// Hosbah's own.
func GuestsOf(services []agentproto.Service) []GuestFact {
	var out []GuestFact
	for _, s := range services {
		if s.Kind != "guest" || s.ID == "" {
			continue
		}
		name := strings.TrimSpace(s.Name)
		if IsHosbahOwned(name) {
			continue
		}
		out = append(out, GuestFact{VMID: s.ID, Type: s.Type, Name: name, Address: s.Address})
	}
	return out
}

// MatchGuests matches a node's discovered guests to other servers of the same team: by guest IP
// address against a candidate's IP first, then by the candidate's hostname against the guest's
// name (case-insensitive equal, or one a prefix of the other after stripping ".local" etc). Each
// guest and each candidate is used at most once. A candidate already linked by hand (Manual) is
// never matched. Pure: no I/O, for unit tests.
func MatchGuests(guests []GuestFact, candidates []GuestServer) []GuestLink {
	usedGuest := map[int]bool{}
	usedServer := map[string]bool{}
	var links []GuestLink

	matchOne := func(pred func(GuestFact, GuestServer) bool) {
		for i, g := range guests {
			if usedGuest[i] || g.VMID == "" {
				continue
			}
			for _, c := range candidates {
				if usedServer[c.ID] || c.Manual {
					continue
				}
				if pred(g, c) {
					links = append(links, GuestLink{ServerID: c.ID, VMID: g.VMID, Type: g.Type})
					usedGuest[i] = true
					usedServer[c.ID] = true
					break
				}
			}
		}
	}

	// Pass 1: IP address.
	matchOne(func(g GuestFact, c GuestServer) bool {
		return g.Address != "" && c.IPAddress != "" && g.Address == c.IPAddress
	})
	// Pass 2: hostname vs guest name.
	matchOne(func(g GuestFact, c GuestServer) bool {
		return g.Name != "" && c.Hostname != "" && namesMatch(g.Name, c.Hostname)
	})
	return links
}

func namesMatch(guestName, hostname string) bool {
	a := normalizeName(guestName)
	b := normalizeName(hostname)
	if a == "" || b == "" {
		return false
	}
	return a == b
}

// normalizeName lowercases and drops a domain suffix (moharrik-lxc.local -> moharrik-lxc) and
// common punctuation, so "moharrik" (hostname) can still fail to equal "moharrik-lxc" (guest name)
// — exact match only, deliberately conservative.
func normalizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.IndexByte(s, '.'); i > 0 {
		s = s[:i]
	}
	return s
}

// AutoLinkGuests matches a node server's discovered guests to other servers of the team and links
// them (parent_server_id, guest_vmid, guest_type), never overwriting a manual link. Team scope.
func AutoLinkGuests(ctx context.Context, sqlDB *sql.DB, teamID, nodeServerID string, services []agentproto.Service) (int, error) {
	guests := GuestsOf(services)
	if len(guests) == 0 {
		return 0, nil
	}
	n := 0
	err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
		rows, err := q.ServersOfTeamForAutoLink(ctx, db.ServersOfTeamForAutoLinkParams{TeamID: teamID, ExcludeID: nodeServerID})
		if err != nil {
			return err
		}
		candidates := make([]GuestServer, 0, len(rows))
		for _, r := range rows {
			c := GuestServer{ID: r.ID, Manual: r.GuestLinkManual}
			if r.Hostname != nil {
				c.Hostname = *r.Hostname
			}
			if r.IpAddress != nil {
				c.IPAddress = *r.IpAddress
			}
			candidates = append(candidates, c)
		}
		links := MatchGuests(guests, candidates)
		for _, l := range links {
			vmid, _ := parseInt32(l.VMID)
			if _, err := q.AutoLinkServerParent(ctx, db.AutoLinkServerParentParams{
				ID: l.ServerID, TeamID: teamID, ParentServerID: nodeServerID, GuestVmid: vmid, GuestType: l.Type,
			}); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

func parseInt32(s string) (int32, bool) {
	var n int32
	neg := false
	i := 0
	if i < len(s) && (s[i] == '-' || s[i] == '+') {
		neg = s[i] == '-'
		i++
	}
	if i == len(s) {
		return 0, false
	}
	for ; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int32(c-'0')
	}
	if neg {
		n = -n
	}
	return n, true
}
