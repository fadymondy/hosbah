package agentproto

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// H8: team log drains. The control plane never ships log lines itself — it only pushes drain
// CONFIGURATION to the server that runs the app(s), sealed to that agent's own key like a deploy's
// env values. The agent tails its containers and ships the lines: syslog (tcp+tls or udp), http
// (NDJSON batches over HTTPS) or loki (its push API). See internal/agent/drains.go.
const (
	KindLogDrainSet    = "log.drains.set"    // upsert or remove one drain's configuration on this server
	KindLogDrainStatus = "log.drains.status" // read one drain's current status from the agent's memory
	KindLogDrainTest   = "log.drains.test"   // ship one synthetic line now, report ok/error
)

// Drain sink kinds.
const (
	DrainKindSyslog = "syslog"
	DrainKindHTTP   = "http"
	DrainKindLoki   = "loki"
)

// Syslog transports.
const (
	SyslogTransportTCPTLS = "tcp+tls"
	SyslogTransportUDP    = "udp"
)

// Limits.
const (
	MaxDrainLabels        = 20
	MaxDrainAuthHeaderLen = 128
	MaxDrainLabelLen      = 64
)

var headerNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,63}$`)
var lokiLabelKeyRE = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,62}$`)

// DrainSecretContext binds a sealed drain secret (an auth header value) to the drain it is for, so
// a box copied onto another drain or server does not open.
func DrainSecretContext(drainID string) string { return "log-drain:" + drainID }

// SyslogSink: a syslog collector (RFC 5424 lines), plain UDP or TLS-wrapped TCP.
type SyslogSink struct {
	Transport string `json:"transport"` // tcp+tls | udp
	Host      string `json:"host"`
	Port      int    `json:"port"`
}

func (s SyslogSink) Validate() error {
	switch s.Transport {
	case SyslogTransportTCPTLS, SyslogTransportUDP:
	default:
		return errors.New("syslog transport is tcp+tls or udp")
	}
	if net.ParseIP(s.Host) == nil && !ValidHostname(s.Host) {
		return errors.New("bad syslog host")
	}
	if s.Port < 1 || s.Port > 65535 {
		return errors.New("bad syslog port")
	}
	return nil
}

// HTTPSink: an HTTPS endpoint that accepts NDJSON batches by POST. AuthSealed, when set, is a box
// (SealBox, DrainSecretContext(DrainID)) the agent opens to get the value of AuthHeader.
type HTTPSink struct {
	URL        string `json:"url"`
	AuthHeader string `json:"auth_header,omitempty"`
	AuthSealed string `json:"auth_sealed,omitempty"`
}

func (h HTTPSink) Validate(internal bool) error {
	return validDrainURL(h.URL, internal, h.AuthHeader)
}

// LokiSink: Grafana Loki's push API (POST .../loki/api/v1/push). Labels are attached to every
// stream the agent pushes (on top of app, process and server, which the agent always adds).
type LokiSink struct {
	URL        string            `json:"url"`
	AuthHeader string            `json:"auth_header,omitempty"`
	AuthSealed string            `json:"auth_sealed,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
}

func (l LokiSink) Validate(internal bool) error {
	if err := validDrainURL(l.URL, internal, l.AuthHeader); err != nil {
		return err
	}
	if len(l.Labels) > MaxDrainLabels {
		return fmt.Errorf("at most %d extra labels", MaxDrainLabels)
	}
	for k, v := range l.Labels {
		if !lokiLabelKeyRE.MatchString(k) {
			return fmt.Errorf("bad label name %q", k)
		}
		if len(v) > MaxDrainLabelLen {
			return fmt.Errorf("label %q is too long", k)
		}
	}
	return nil
}

func validDrainURL(raw string, internal bool, authHeader string) error {
	if authHeader != "" && !headerNameRE.MatchString(authHeader) {
		return errors.New("bad auth header name")
	}
	if len(authHeader) > MaxDrainAuthHeaderLen {
		return errors.New("auth header name is too long")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return errors.New("bad url")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !internal {
			return errors.New("http is only allowed for a drain marked internal — otherwise use https")
		}
	default:
		return errors.New("the url must be http(s)")
	}
	host := u.Hostname()
	if net.ParseIP(host) == nil && !ValidHostname(host) {
		return errors.New("bad url host")
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return errors.New("bad url port")
		}
	}
	return nil
}

// DrainConfig is one team log drain's configuration for one server: log.drains.set's params.
// AppID empty scopes it to every app of the team on this server; set, to one app.
type DrainConfig struct {
	DrainID string `json:"drain_id"`
	TeamID  string `json:"team_id"`
	AppID   string `json:"app_id,omitempty"`
	Kind    string `json:"kind"` // syslog | http | loki
	// Internal allows an http/loki sink whose resolved address is private, loopback or
	// link-local — refused otherwise (SSRF-safe by default; an owner opts a LAN collector in).
	Internal bool        `json:"internal,omitempty"`
	Syslog   *SyslogSink `json:"syslog,omitempty"`
	HTTP     *HTTPSink   `json:"http,omitempty"`
	Loki     *LokiSink   `json:"loki,omitempty"`
	Remove   bool        `json:"remove,omitempty"`
}

// Validate checks the config on both sides of the wire.
func (d DrainConfig) Validate() error {
	if !ValidID(d.DrainID) || !ValidID(d.TeamID) {
		return errors.New("bad drain or team id")
	}
	if d.AppID != "" && !ValidID(d.AppID) {
		return errors.New("bad app id")
	}
	if d.Remove {
		return nil
	}
	switch d.Kind {
	case DrainKindSyslog:
		if d.Syslog == nil {
			return errors.New("a syslog drain needs syslog settings")
		}
		return d.Syslog.Validate()
	case DrainKindHTTP:
		if d.HTTP == nil {
			return errors.New("an http drain needs http settings")
		}
		return d.HTTP.Validate(d.Internal)
	case DrainKindLoki:
		if d.Loki == nil {
			return errors.New("a loki drain needs loki settings")
		}
		return d.Loki.Validate(d.Internal)
	default:
		return errors.New("kind is syslog, http or loki")
	}
}

// DrainStatusParams: log.drains.status.
type DrainStatusParams struct {
	DrainID string `json:"drain_id"`
}

func (p DrainStatusParams) Validate() error {
	if !ValidID(p.DrainID) {
		return errors.New("bad drain id")
	}
	return nil
}

// DrainStatus is what the agent reports about a drain it runs.
type DrainStatus struct {
	DrainID       string     `json:"drain_id"`
	Configured    bool       `json:"configured"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastErrorAt   *time.Time `json:"last_error_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	Shipped       int64      `json:"shipped"`
	Dropped       int64      `json:"dropped"`
}

// DrainTestParams: log.drains.test — ship one synthetic line through the given (not necessarily
// persisted) configuration right now, and report whether the sink accepted it.
type DrainTestParams struct {
	Drain DrainConfig `json:"drain"`
}

func (p DrainTestParams) Validate() error { return p.Drain.Validate() }

// DrainTestResult: log.drains.test's result.
type DrainTestResult struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}
