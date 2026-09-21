package agentproto

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// P2 operation kinds: app deploys. Typed like everything else — each has fixed params below, and
// nothing in them reaches a shell: the agent turns them into fixed argv lists and Caddy JSON.
const (
	KindAppDetect   = "app.detect"   // clone a ref and say how it would build (Dockerfile / Railpack provider)
	KindAppBuild    = "app.build"    // clone, build cloudy/<app>:<deploy_id> with BuildKit, prune to the newest N
	KindAppRelease  = "app.release"  // run the release command in a one-off container from the new image
	KindAppStart    = "app.start"    // start one process of an image; web gets an ephemeral port and a health gate
	KindRouteSet    = "route.set"    // point the app's Caddy route (admin API, localhost:2019) at an upstream
	KindAppDrain    = "app.drain"    // stop (SIGTERM + grace) and remove the app's containers except the kept ones
	KindAppRollback = "app.rollback" // start a kept image, health-gate it, switch the route, drain the rest — one round trip
	KindAppLogs     = "app.logs"     // docker logs --follow of the app's containers, bounded in lines and time
	KindAppRemove   = "app.remove"   // remove the app's route, containers and images (the app was deleted)
	KindOpCancel    = "op.cancel"    // cancel a running operation (handled at once, not queued behind it)
	KindAppExec     = "app.exec"     // run an argv inside the live release's web container (docker exec, no TTY), bounded in time and output
)

// Provision steps added in P2 (params.step of provision.step). Idempotent like P1's.
const (
	StepBuildTools = "build.tools"     // git + the docker-buildx client (BuildKit is Docker's own builder)
	StepCaddy      = "caddy.install"   // the pinned Caddy release, checksum-verified, under systemd, admin API on localhost
	StepRailpack   = "railpack.install" // the pinned Railpack release, checksum-verified
)

// ToolInstall pins a downloaded tool. It travels inside the signed params, so the control plane's
// signature covers the exact bytes that get installed; a download that does not hash to the
// signed value is discarded. SHA-512 for Caddy (what it publishes), SHA-256 for Railpack.
type ToolInstall struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256,omitempty"`
	SHA512  string `json:"sha512,omitempty"`
}

// ProvisionParams is provision.step's params.
type ProvisionParams struct {
	Step   string       `json:"step"`
	SwapMB int          `json:"swap_mb,omitempty"`
	Tool   *ToolInstall `json:"tool,omitempty"`
	// AskURL is Caddy's on-demand TLS permission endpoint (caddy.install writes it into the base
	// config): the control plane's /internal/tls/allow.
	AskURL string `json:"ask_url,omitempty"`
}

// GitSource is where an app's code comes from. P2: a public URL (no credentials). The GitHub App
// path will add a short-lived installation token here, sealed to the agent like env values.
type GitSource struct {
	RepoURL string `json:"repo_url"`
	Ref     string `json:"ref"`
}

// DetectParams / DetectResult: app.detect.
type DetectParams struct {
	GitSource
	RailpackFrontend string `json:"railpack_frontend,omitempty"`
}

type DetectResult struct {
	Commit       string `json:"commit"`
	Message      string `json:"message,omitempty"`
	Runtime      string `json:"runtime"`            // dockerfile | railpack
	Provider     string `json:"provider,omitempty"` // railpack's detected provider: golang, node, python, php…
	StartCommand string `json:"start_command,omitempty"`
	HosbahToml   string `json:"cloudy_toml,omitempty"` // raw file (≤64 KiB); the control plane parses it
}

// BuildParams / BuildResult: app.build.
type BuildParams struct {
	AppID            string   `json:"app_id"`
	App              string   `json:"app"`
	DeploymentID     string   `json:"deployment_id"`
	GitSource
	Image            string   `json:"image"`             // cloudy/<app>:<deployment_id>
	Keep             int      `json:"keep"`              // newest images of cloudy/<app> to keep (5)
	Protect          []string `json:"protect,omitempty"` // images never pruned (the live release)
	RailpackFrontend string   `json:"railpack_frontend"` // BuildKit frontend image, same version as the CLI
}

type BuildResult struct {
	DetectResult
	Image   string   `json:"image"`
	ImageID string   `json:"image_id,omitempty"`
	Pruned  []string `json:"pruned,omitempty"`
}

// ReleaseParams: app.release — the release command in a one-off container (fail = old release stays).
type ReleaseParams struct {
	AppID        string `json:"app_id"`
	App          string `json:"app"`
	DeploymentID string `json:"deployment_id"`
	Image        string `json:"image"`
	Command      string `json:"command"`
	Env          string `json:"env,omitempty"` // an EnvBox sealed to this agent, context EnvContext(app, deployment, "release")
}

// HealthSpec is the gate: an HTTP GET of Path on the new container that must answer 2xx within
// TimeoutSeconds.
type HealthSpec struct {
	Path           string `json:"path"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

// StartParams / StartResult: app.start.
type StartParams struct {
	AppID        string      `json:"app_id"`
	App          string      `json:"app"`
	DeploymentID string      `json:"deployment_id"`
	Image        string      `json:"image"`
	Process      string      `json:"process"`           // web | worker:<name>
	Command      string      `json:"command,omitempty"` // empty = the image's start command
	Port         int         `json:"port,omitempty"`    // web: the port the app listens on inside (PORT is set to it)
	Env          string      `json:"env,omitempty"`     // EnvBox, context EnvContext(app, deployment, process)
	Health       *HealthSpec `json:"health,omitempty"`  // web only
}

type StartResult struct {
	Container string `json:"container"`
	HostPort  int    `json:"host_port,omitempty"` // web: 127.0.0.1:<host_port> is the upstream Caddy points at
}

// Route is one hostname of an app and its TLS mode (auto | internal | off).
type Route struct {
	Hostname string `json:"hostname"`
	TLS      string `json:"tls"`
}

// RouteParams: route.set. An empty Upstream removes the app's route.
type RouteParams struct {
	AppID    string  `json:"app_id"`
	Hosts    []Route `json:"hosts"`
	Upstream string  `json:"upstream,omitempty"` // 127.0.0.1:<port>
}

// DrainParams: app.drain — every container labelled with the app (optionally one process) except
// Keep is stopped with SIGTERM, given GraceSeconds, and removed. Label-based rather than a list,
// so a container orphaned by a crash mid-deploy is cleaned up too.
type DrainParams struct {
	AppID        string   `json:"app_id"`
	Process      string   `json:"process,omitempty"` // "" = all processes
	Keep         []string `json:"keep,omitempty"`
	// Only, when set, limits the drain to exactly these containers (cleaning up after a failed
	// deploy removes the new ones by name — it never relies on a keep-list naming the live ones).
	Only         []string `json:"only,omitempty"`
	GraceSeconds int      `json:"grace_seconds"`
}

// RollbackParams: app.rollback — start Web (and Workers) from a kept image, gate, switch, drain.
type RollbackParams struct {
	AppID        string        `json:"app_id"`
	Web          StartParams   `json:"web"`
	Workers      []StartParams `json:"workers,omitempty"`
	Hosts        []Route       `json:"hosts"`
	GraceSeconds int           `json:"grace_seconds"`
}

type RollbackResult struct {
	Web     StartResult   `json:"web"`
	Workers []StartResult `json:"workers,omitempty"`
}

// LogsParams: app.logs — runtime logs relayed on demand.
type LogsParams struct {
	AppID         string `json:"app_id"`
	Process       string `json:"process,omitempty"` // "" = web
	Tail          int    `json:"tail"`              // lines of history first (≤ 1000)
	FollowSeconds int    `json:"follow_seconds"`    // then follow for this long (≤ 300)
}

// RemoveParams: app.remove.
type RemoveParams struct {
	AppID string `json:"app_id"`
	App   string `json:"app"`
}

// CancelParams: op.cancel.
type CancelParams struct {
	OpID string `json:"op_id"`
}

// Limits the agent enforces whatever the params say.
const (
	MaxLogTail          = 1000
	MaxLogFollowSeconds = 300
	MaxLogLines         = 5000
	MaxHosbahToml       = 64 << 10
	DefaultKeepImages   = 5
)

var (
	appNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	processRE = regexp.MustCompile(`^(web|worker:[a-z0-9_-]{1,32})$`)
	refRE     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
	uuidRE    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	hostRE    = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// ValidAppName: the app slug (image repository, container prefix).
func ValidAppName(s string) bool { return appNameRE.MatchString(s) }

// ValidProcess: web or worker:<name>.
func ValidProcess(s string) bool { return processRE.MatchString(s) }

// ValidRef: a branch, tag or commit — no leading dash (never an option to git), no "..".
func ValidRef(s string) bool { return refRE.MatchString(s) && !strings.Contains(s, "..") }

// ValidID: a lowercase uuid (ids that end up in names and labels).
func ValidID(s string) bool { return uuidRE.MatchString(s) }

// ValidHostname: a lowercase DNS name with at least one dot.
func ValidHostname(s string) bool { return len(s) <= 253 && hostRE.MatchString(s) }

// ValidTLS: auto | internal | off.
func ValidTLS(s string) bool { return s == "auto" || s == "internal" || s == "off" }

// CheckRepoURL accepts a public git URL: https, http or git, a host, a path, no credentials, no
// query. ssh/file/ext and anything else are refused — the agent clones without a shell, but git's
// own transports (ext::, file://) would still reach the host.
func CheckRepoURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.Opaque != "" {
		return errors.New("repository must be a URL like https://github.com/owner/repo")
	}
	switch u.Scheme {
	case "https", "http", "git":
	default:
		return fmt.Errorf("repository URL scheme %q is not supported (https, http or git)", u.Scheme)
	}
	if u.User != nil {
		return errors.New("repository URL must not contain credentials (private repositories arrive with the GitHub App)")
	}
	if u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") == "" || strings.HasPrefix(u.Host, "-") {
		return errors.New("repository URL must name a repository path, without a query or fragment")
	}
	return nil
}

// NormalizeRepoURL is the stored form: no trailing slash, no .git suffix, lowercase host.
func NormalizeRepoURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return strings.TrimSpace(raw)
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), ".git")
	return u.String()
}

// ContainerName is how a process's container is named on the server: unique per deployment.
func ContainerName(app, process, deploymentID string) string {
	short := deploymentID
	if len(short) > 8 {
		short = short[:8]
	}
	return "cloudy-" + app + "-" + strings.ReplaceAll(process, ":", "-") + "-" + short
}

// ImageRepo is the app's image repository on its server.
func ImageRepo(app string) string { return "cloudy/" + app }

// RouteID is the Caddy @id of an app's route.
func RouteID(appID string) string { return "cloudy-app-" + appID }

// Validate checks the fields every app op shares, on both sides of the wire.
func (p StartParams) Validate() error {
	switch {
	case !ValidID(p.AppID) || !ValidAppName(p.App) || !ValidID(p.DeploymentID):
		return errors.New("bad app or deployment id")
	case !ValidProcess(p.Process):
		return fmt.Errorf("bad process %q", p.Process)
	case !strings.HasPrefix(p.Image, ImageRepo(p.App)+":"):
		return fmt.Errorf("image %q is not one of this app's", p.Image)
	case p.Process == "web" && (p.Port < 1 || p.Port > 65535):
		return errors.New("web needs a port")
	case p.Health != nil && (!strings.HasPrefix(p.Health.Path, "/") || p.Health.TimeoutSeconds < 1 || p.Health.TimeoutSeconds > 600):
		return errors.New("bad health check")
	}
	return nil
}
