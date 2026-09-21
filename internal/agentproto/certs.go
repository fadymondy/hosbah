package agentproto

import (
	"errors"
	"fmt"
	"time"
)

// KindCaddyCerts reads the TLS certificates the server holds for a list of hostnames. Read-only.
const KindCaddyCerts = "caddy.certs"

// MaxCertHostnames bounds one caddy.certs.
const MaxCertHostnames = 50

// CaddyCertsParams: caddy.certs.
type CaddyCertsParams struct {
	Hostnames []string `json:"hostnames"`
}

// Validate checks caddy.certs on both sides: a hostname ends up in a file path and in a TLS
// ClientHello, so only ValidHostname's charset passes.
func (p CaddyCertsParams) Validate() error {
	if len(p.Hostnames) == 0 || len(p.Hostnames) > MaxCertHostnames {
		return fmt.Errorf("hostnames: give 1–%d", MaxCertHostnames)
	}
	for _, h := range p.Hostnames {
		if !ValidHostname(h) {
			return errors.New("hostnames: not a valid hostname")
		}
	}
	return nil
}

// CaddyCert is what the server holds (or presents) for one hostname.
type CaddyCert struct {
	Hostname  string     `json:"hostname"`
	Found     bool       `json:"found"`
	Issuer    string     `json:"issuer,omitempty"`
	NotBefore *time.Time `json:"not_before,omitempty"`
	NotAfter  *time.Time `json:"not_after,omitempty"`
	// Managed: Caddy obtained it from a public CA. False for Caddy's internal CA and for a
	// certificate something else on the server presents.
	Managed bool   `json:"managed"`
	Error   string `json:"error,omitempty"`
}

// CaddyCertsResult: caddy.certs, one entry per hostname asked, in order.
type CaddyCertsResult struct {
	Certificates []CaddyCert `json:"certificates"`
}
