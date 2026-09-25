package agentproto

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// Per-site HTTP rules (MH-578 E4-E6): redirects, response headers, basic auth and IP/bot access
// rules, rendered into an app's or a proxy host's own Caddy route (internal/agent/httprules.go).
// One rules.set op replaces the whole set for one target (an app or a proxy host) — like cache.set,
// it looks the target's hosts/upstream up in the route table rather than being told them again.
//
// Every string here is eventually interpolated into Caddy's JSON config (never into a shell command
// or a regex the way cache.go's VCL is), so the injection surface is different: a JSON string value
// cannot "break out" of the document the way a VCL/shell literal can. Validation here is still kept
// narrow and explicit — a URL that must parse, a path that must start with /, a CIDR that must
// parse, a header name that must be a valid HTTP token — because Caddy's own JSON schema will happily
// accept values that are syntactically fine JSON but nonsense or dangerous config (e.g. a
// "Location" header a browser cannot safely follow, or a redirect target using a scheme that is not
// http/https).

// KindHTTPRulesSet replaces the whole HTTPRules of one target (an app or a proxy host).
const KindHTTPRulesSet = "rules.set"

// HTTPRuleTargetApp / HTTPRuleTargetProxy: HTTPRulesSetParams.TargetType.
const (
	HTTPRuleTargetApp   = "app"
	HTTPRuleTargetProxy = "proxy"
)

// Redirect codes accepted by RedirectRule.Code.
var validRedirectCodes = map[int]bool{301: true, 302: true, 307: true, 308: true}

// Header actions accepted by HeaderRule.Action.
const (
	HeaderActionSet    = "set"
	HeaderActionAdd    = "add"
	HeaderActionRemove = "remove"
)

// Limits: how much config one target's rules can generate.
const (
	MaxRedirects      = 30
	MaxHeaderRules    = 30
	MaxBasicAuthUsers = 50
	MaxExemptPaths    = 20
	MaxIPRules        = 100
)

var (
	// httpRulePathRE: an absolute path, optionally ending in a single trailing "*" to mean "this
	// prefix and anything under it" (Caddy's own path-matcher wildcard syntax). No query string, no
	// scheme, no "..": this is a path matched against a request's already-parsed URL path, not a
	// user-supplied fragment of a URL.
	httpRulePathRE = regexp.MustCompile(`^/[A-Za-z0-9/_.\-]*\*?$`)
	// httpRuleHeaderNameRE: an HTTP header field-name token (RFC 7230 token charset).
	httpRuleHeaderNameRE = regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+\-.^_` + "`" + `|~]+$`)
	// basicAuthUsernameRE: Caddy's http_basic provider keys accounts by username; keep it to a safe,
	// ordinary charset (no whitespace/control characters, nothing that could look like a directive).
	basicAuthUsernameRE = regexp.MustCompile(`^[A-Za-z0-9_.\-@]{1,128}$`)
	// bcryptHashRE: the exact shape of a bcrypt hash ($2a$/$2b$/$2y$, cost, 53 base64-ish chars) —
	// what Caddy's http_basic provider (hash algorithm "bcrypt") requires. A plaintext password can
	// never reach this field and pass validation.
	bcryptHashRE = regexp.MustCompile(`^\$2[aby]\$\d{2}\$[A-Za-z0-9./]{53}$`)
)

// headerValueSafe reports whether s can be a raw HTTP header value: no control characters (CR/LF in
// particular — the classic header-injection vector), nothing else restricted.
func headerValueSafe(s string) bool {
	if len(s) > 4096 {
		return false
	}
	for _, r := range s {
		if r == '\r' || r == '\n' || r == 0 {
			return false
		}
	}
	return true
}

// ValidHTTPRulePath reports whether s is a safe path or "path*" prefix matcher.
func ValidHTTPRulePath(s string) bool { return len(s) <= 512 && httpRulePathRE.MatchString(s) }

// ValidHeaderName reports whether s is a safe HTTP header field name.
func ValidHeaderName(s string) bool { return len(s) <= 128 && httpRuleHeaderNameRE.MatchString(s) }

// ValidBasicAuthUsername reports whether s is a safe basic-auth username.
func ValidBasicAuthUsername(s string) bool { return basicAuthUsernameRE.MatchString(s) }

// ValidBcryptHash reports whether s is a bcrypt hash (never a plaintext password).
func ValidBcryptHash(s string) bool { return bcryptHashRE.MatchString(s) }

// ValidCIDR reports whether s parses as an IPv4/IPv6 CIDR (or a bare IP, normalised to a /32 or /128
// by the caller) that Caddy's remote_ip matcher accepts.
func ValidCIDR(s string) bool {
	if _, _, err := net.ParseCIDR(s); err == nil {
		return true
	}
	// A bare IP address is also accepted (normalised on save; see NormalizeCIDR).
	return net.ParseIP(s) != nil
}

// NormalizeCIDR turns a bare IP into a /32 (IPv4) or /128 (IPv6) CIDR; a CIDR already is left as is.
func NormalizeCIDR(s string) string {
	if _, _, err := net.ParseCIDR(s); err == nil {
		return s
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return s
	}
	if ip.To4() != nil {
		return s + "/32"
	}
	return s + "/128"
}

// RedirectRule: a request whose path matches From is answered with a redirect to To, never
// forwarded to the upstream.
type RedirectRule struct {
	// From is an absolute path, or a path prefix ending in "*" (e.g. "/old", "/old/*").
	From string `json:"from"`
	// To is the absolute URL (http/https) or absolute path to redirect to.
	To string `json:"to"`
	// Code is one of 301, 302, 307, 308. Defaults to 302 when zero.
	Code int `json:"code,omitempty"`
	// PreserveQuery appends the request's own query string to To.
	PreserveQuery bool `json:"preserve_query,omitempty"`
}

func (r RedirectRule) validate() error {
	if !ValidHTTPRulePath(r.From) {
		return fmt.Errorf("redirect from %q must be an absolute path, optionally ending in *", r.From)
	}
	if r.Code != 0 && !validRedirectCodes[r.Code] {
		return fmt.Errorf("redirect code must be 301, 302, 307 or 308")
	}
	to := strings.TrimSpace(r.To)
	if to == "" {
		return errors.New("redirect to must not be empty")
	}
	if !headerValueSafe(to) {
		return errors.New("redirect to has invalid characters")
	}
	if strings.HasPrefix(to, "/") {
		if !ValidHTTPRulePath(strings.TrimSuffix(to, "*")) && to != "/" {
			return fmt.Errorf("redirect to %q is not a valid path", to)
		}
		return nil
	}
	u, err := url.Parse(to)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("redirect to %q must be an absolute URL (http/https) or an absolute path", r.To)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("redirect to %q must use http or https", r.To)
	}
	return nil
}

// EffectiveCode returns Code, defaulting to 302.
func (r RedirectRule) EffectiveCode() int {
	if r.Code == 0 {
		return 302
	}
	return r.Code
}

// HeaderRule: one response-header change, applied to every response the target serves.
type HeaderRule struct {
	Action string `json:"action"` // set | add | remove
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
}

func (h HeaderRule) validate() error {
	if h.Action != HeaderActionSet && h.Action != HeaderActionAdd && h.Action != HeaderActionRemove {
		return errors.New("header action must be set, add or remove")
	}
	if !ValidHeaderName(h.Name) {
		return fmt.Errorf("header name %q is not a valid HTTP header name", h.Name)
	}
	if h.Action != HeaderActionRemove {
		if !headerValueSafe(h.Value) {
			return fmt.Errorf("header value for %q has invalid characters", h.Name)
		}
	}
	return nil
}

// BasicAuthUser: one basic-auth account. Password is NEVER stored — only a bcrypt hash, which is
// exactly what Caddy's http_basic provider accepts as an account's "password" field (hash
// algorithm "bcrypt").
type BasicAuthUser struct {
	Username   string `json:"username"`
	BcryptHash string `json:"bcrypt_hash"`
}

func (u BasicAuthUser) validate() error {
	if !ValidBasicAuthUsername(u.Username) {
		return fmt.Errorf("basic auth username %q is not valid", u.Username)
	}
	if !ValidBcryptHash(u.BcryptHash) {
		return fmt.Errorf("basic auth user %q needs a bcrypt password hash", u.Username)
	}
	return nil
}

// BasicAuthRule: HTTP basic auth on the whole site or a path prefix.
type BasicAuthRule struct {
	Enabled bool `json:"enabled"`
	// PathPrefix: "" or "/" protects the whole site; otherwise only paths under this prefix.
	PathPrefix string `json:"path_prefix,omitempty"`
	// ExemptPaths are always reachable without auth, even under PathPrefix (e.g. /api/health).
	ExemptPaths []string        `json:"exempt_paths,omitempty"`
	Users       []BasicAuthUser `json:"users,omitempty"`
}

func (b *BasicAuthRule) validate() error {
	if b == nil || !b.Enabled {
		return nil
	}
	if b.PathPrefix != "" && b.PathPrefix != "/" && !ValidHTTPRulePath(b.PathPrefix) {
		return fmt.Errorf("basic auth path prefix %q is not a valid path", b.PathPrefix)
	}
	if len(b.ExemptPaths) > MaxExemptPaths {
		return errors.New("too many basic auth exempt paths")
	}
	for _, p := range b.ExemptPaths {
		if !ValidHTTPRulePath(p) {
			return fmt.Errorf("basic auth exempt path %q is not a valid path", p)
		}
	}
	if len(b.Users) == 0 {
		return errors.New("basic auth needs at least one user")
	}
	if len(b.Users) > MaxBasicAuthUsers {
		return errors.New("too many basic auth users")
	}
	seen := map[string]bool{}
	for _, u := range b.Users {
		if err := u.validate(); err != nil {
			return err
		}
		if seen[u.Username] {
			return fmt.Errorf("duplicate basic auth username %q", u.Username)
		}
		seen[u.Username] = true
	}
	return nil
}

// AccessRules: IP allow/deny lists and bad-bot blocking.
//
// Rate limiting is intentionally NOT part of this struct: core Caddy (the plain release tarball this
// agent installs — see cache.go's design note on why Varnish exists rather than a cache module) ships
// no rate-limit handler, and there is no way to add one without rebuilding Caddy with xcaddy, which
// this agent's install path (tools.go) deliberately avoids. MH-578 E6 ships IP/bot rules only; rate
// limiting is not implemented (see internal/deploy/httprules.go's doc comment for the same note on
// the control-plane side).
type AccessRules struct {
	// IPAllow: when non-empty, ONLY requests from one of these CIDRs are served; everything else
	// gets 403. Checked before IPDeny.
	IPAllow []string `json:"ip_allow,omitempty"`
	// IPDeny: requests from any of these CIDRs get 403.
	IPDeny []string `json:"ip_deny,omitempty"`
	// BlockBadBots: refuse a curated list of well-known bad-bot User-Agent substrings (BadBotUserAgents).
	BlockBadBots bool `json:"block_bad_bots,omitempty"`
}

func (a *AccessRules) validate() error {
	if a == nil {
		return nil
	}
	if len(a.IPAllow)+len(a.IPDeny) > MaxIPRules {
		return errors.New("too many IP allow/deny rules")
	}
	for _, c := range a.IPAllow {
		if !ValidCIDR(c) {
			return fmt.Errorf("ip_allow entry %q is not a valid IP or CIDR", c)
		}
	}
	for _, c := range a.IPDeny {
		if !ValidCIDR(c) {
			return fmt.Errorf("ip_deny entry %q is not a valid IP or CIDR", c)
		}
	}
	return nil
}

// BadBotUserAgents: a small, curated list of well-known bad-bot/scraper User-Agent substrings.
// Matched case-insensitively as a substring of the request's User-Agent header. Not exhaustive —
// meant as a cheap first line of defence, not a bot-detection product.
var BadBotUserAgents = []string{
	"AhrefsBot", "MJ12bot", "SemrushBot", "DotBot", "PetalBot", "BLEXBot",
	"DataForSeoBot", "SerpstatBot", "MegaIndex", "Bytespider", "GPTBot",
	"CCBot", "ClaudeBot", "ImagesiftBot", "Amazonbot", "Barkrowler",
}

// HTTPRules: the whole per-target ruleset.
type HTTPRules struct {
	Redirects []RedirectRule `json:"redirects,omitempty"`
	Headers   []HeaderRule   `json:"headers,omitempty"`
	BasicAuth *BasicAuthRule `json:"basic_auth,omitempty"`
	Access    *AccessRules   `json:"access,omitempty"`
}

// Empty reports whether these rules would change nothing about the target's route.
func (r HTTPRules) Empty() bool {
	return len(r.Redirects) == 0 && len(r.Headers) == 0 &&
		(r.BasicAuth == nil || !r.BasicAuth.Enabled) &&
		(r.Access == nil || (len(r.Access.IPAllow) == 0 && len(r.Access.IPDeny) == 0 && !r.Access.BlockBadBots))
}

// Validate checks a whole HTTPRules on both sides of the wire.
func (r HTTPRules) Validate() error {
	if len(r.Redirects) > MaxRedirects {
		return errors.New("too many redirects")
	}
	for _, rr := range r.Redirects {
		if err := rr.validate(); err != nil {
			return err
		}
	}
	if len(r.Headers) > MaxHeaderRules {
		return errors.New("too many header rules")
	}
	for _, h := range r.Headers {
		if err := h.validate(); err != nil {
			return err
		}
	}
	if err := r.BasicAuth.validate(); err != nil {
		return err
	}
	if err := r.Access.validate(); err != nil {
		return err
	}
	return nil
}

// HTTPRulesSetParams: rules.set. TargetID must already have a route (route.set for an app,
// proxy.set for a proxy host) — like cache.set, the agent looks its hosts up in the route table
// rather than being told them again.
type HTTPRulesSetParams struct {
	TargetType string    `json:"target_type"` // app | proxy
	TargetID   string    `json:"target_id"`
	Rules      HTTPRules `json:"rules"`
}

// Validate checks a rules.set on both sides of the wire.
func (p HTTPRulesSetParams) Validate() error {
	if p.TargetType != HTTPRuleTargetApp && p.TargetType != HTTPRuleTargetProxy {
		return errors.New("target_type must be app or proxy")
	}
	if !ValidID(p.TargetID) {
		return errors.New("bad target id")
	}
	return p.Rules.Validate()
}
