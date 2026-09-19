package deploy

import (
	"context"
	"errors"
	"net"

	"github.com/fadymondy/cloudy/internal/cloudflare"
	"github.com/fadymondy/cloudy/internal/crypto"
	db "github.com/fadymondy/cloudy/internal/db/gen"
	"github.com/fadymondy/cloudy/internal/provisioning"
)

// A team that saved a Cloudflare credential gets its custom domains wired for it: when the hostname
// is in one of its zones, Cloudy creates the A/AAAA record to the app's server and, since holding a
// token for the zone proves control of it, marks the domain verified without the TXT step.

// teamZone finds the team's Cloudflare credential whose account holds hostname.
func (s *Service) teamZone(ctx context.Context, q *db.Queries, teamID, hostname string) (*cloudflare.Client, cloudflare.Zone, error) {
	creds, err := q.ListCredentialsOfTeam(ctx, teamID)
	if err != nil {
		return nil, cloudflare.Zone{}, err
	}
	var dek []byte
	for _, c := range creds {
		if c.Provider != "cloudflare" {
			continue
		}
		if dek == nil {
			if dek, err = s.ops.TeamDEK(ctx, q, teamID); err != nil {
				return nil, cloudflare.Zone{}, err
			}
		}
		tok, err := crypto.Open(dek, teamID, provisioning.CredentialContext(c.ID), c.SecretSealed)
		if err != nil {
			continue
		}
		cf := s.cloudflare(string(tok))
		cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
		z, err := cf.ZoneFor(cctx, hostname)
		cancel()
		if err == nil {
			return cf, z, nil
		}
	}
	return nil, cloudflare.Zone{}, cloudflare.ErrNotFound
}

// wireTeamDNS points a new custom domain at the app's server through the team's Cloudflare, if it
// can. It reports whether the domain may be marked verified. Failures are logged, never fatal.
func (s *Service) wireTeamDNS(ctx context.Context, q *db.Queries, teamID string, a db.App, hostname string) bool {
	srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: a.ServerID, TeamID: teamID})
	if err != nil {
		return false
	}
	return s.wireTeamDNSTo(ctx, q, teamID, srv, hostname)
}

// wireTeamDNSTo points hostname at srv through the team's Cloudflare (see wireTeamDNS).
func (s *Service) wireTeamDNSTo(ctx context.Context, q *db.Queries, teamID string, srv db.Server, hostname string) bool {
	if srv.IpAddress == nil {
		return false
	}
	if ip := net.ParseIP(*srv.IpAddress); ip == nil || ip.IsPrivate() || ip.IsLoopback() {
		return false // a private address in public DNS helps nobody: tunnel the server instead
	}
	cf, z, err := s.teamZone(ctx, q, teamID, hostname)
	if err != nil {
		if !errors.Is(err, cloudflare.ErrNotFound) {
			s.log.Warn("team DNS: lookup failed", "host", hostname, "err", err)
		}
		return false
	}
	if srv.IpAddress == nil {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	defer cancel()
	if _, _, err := cf.PointHost(cctx, hostname, *srv.IpAddress, false); err != nil {
		s.log.Warn("team DNS: could not create the record", "host", hostname, "zone", z.Name, "err", err)
		return false
	}
	return true
}

// unwireTeamDNS removes the records Cloudy created for a custom domain.
func (s *Service) unwireTeamDNS(ctx context.Context, q *db.Queries, teamID, hostname string) {
	cf, z, err := s.teamZone(ctx, q, teamID, hostname)
	if err != nil {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	defer cancel()
	if err := cf.DeleteManaged(cctx, z.ID, hostname); err != nil {
		s.log.Warn("team DNS: could not remove the record", "host", hostname, "err", err)
	}
}

// cloudflare builds a client for a team token (tests point it at a fake).
func (s *Service) cloudflare(token string) *cloudflare.Client {
	if s.cfOpts != nil {
		return cloudflare.New(token, s.cfOpts...)
	}
	return cloudflare.New(token)
}
