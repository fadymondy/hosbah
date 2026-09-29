package agentproto

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// P2 operation kinds: app deploys. Typed like everything else — each has fixed params below, and
// nothing in them reaches a shell: the agent turns them into fixed argv lists and Caddy JSON.
const (
	KindAppDetect   = "app.detect"   // clone a ref and say how it would build (Dockerfile / Railpack provider)
	KindAppBuild    = "app.build"    // clone, build hosbah/<app>:<deploy_id> with BuildKit, prune to the newest N
	KindAppRelease  = "app.release"  // run the release command in a one-off container from the new image
	KindAppStart    = "app.start"    // start one process of an image; web gets an ephemeral port and a health gate
	KindRouteSet    = "route.set"    // point the app's Caddy route (admin API, localhost:2019) at an upstream
	KindAppDrain    = "app.drain"    // stop (SIGTERM + grace) and remove the app's containers except the kept ones
	KindAppRollback = "app.rollback" // start a kept image, health-gate it, switch the route, drain the rest — one round trip
	KindAppLogs     = "app.logs"     // docker logs --follow of the app's containers, bounded in lines and time
	KindAppRemove   = "app.remove"   // remove the app's route, containers and images (the app was deleted)
	KindOpCancel    = "op.cancel"    // cancel a running operation (handled at once, not queued behind it)
	KindAppExec     = "app.exec"     // run an argv inside the live release's web container (docker exec, no TTY), bounded in time and output
	// KindAppFastBuild (MH-573 A7): the fast track. Reuses a cached checkout, runs the app's own
	// install/build/migrate steps inside its CURRENT live image (no host toolchains, no BuildKit
	// rebuild), and commits the result to a new tag. See FastBuildParams / FastBuildResult.
	KindAppFastBuild = "app.fast_build"
	// KindAppProcesses (MH-574 G1): status, restart, stop or start of one or every process of an
	// app's live release — the "Workers" page's manage actions. Fixed docker argv only: inspect and
	// stats to report, restart/stop/start by container name found through the app's labels (the
	// same way app.drain and app.logs find them).
	KindAppProcesses = "app.processes"
)

// Provision steps added in P2 (params.step of provision.step). Idempotent like P1's.
const (
	StepBuildTools = "build.tools"      // git + the docker-buildx client (BuildKit is Docker's own builder)
	StepCaddy      = "caddy.install"    // the pinned Caddy release, checksum-verified, under systemd, admin API on localhost
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

// GitSource is where an app's code comes from: a URL without credentials, and a ref.
//
// Token, when set, is a GitHub App installation token (valid one hour) for a github.com repository
// of the team's installation: a box sealed to the target agent's own key with GitTokenContext(RepoURL)
// (SealBox), so operations.params only ever holds ciphertext that server alone opens, bound to that
// one repository. The agent hands it to git for the fetch only, through the environment of that one
// command (a credential helper that reads it from there) — never in an argv, a URL, a file or a log.
// Agents from before it existed ignore the field and clone without it.
type GitSource struct {
	RepoURL string `json:"repo_url"`
	Ref     string `json:"ref"`
	Token   string `json:"token,omitempty"`
}

// GitTokenContext binds a sealed clone token to the repository it is for.
func GitTokenContext(repoURL string) string { return "git-token:" + NormalizeRepoURL(repoURL) }

// DetectParams / DetectResult: app.detect.
type DetectParams struct {
	GitSource
	RailpackFrontend string `json:"railpack_frontend,omitempty"`
	// DockerfilePath (MH-579 gap B), when set, is a path relative to the checkout the app's
	// Dockerfile lives at instead of the default "Dockerfile" at the checkout root — validated by
	// ValidBuildPath, and again by the agent once it resolves the checkout's real path (no ".."
	// component and no symlink escape). Empty means the default.
	DockerfilePath string `json:"dockerfile_path,omitempty"`
}

type DetectResult struct {
	Commit       string `json:"commit"`
	Message      string `json:"message,omitempty"`
	Runtime      string `json:"runtime"`            // dockerfile | railpack
	Provider     string `json:"provider,omitempty"` // railpack's detected provider: golang, node, python, php…
	StartCommand string `json:"start_command,omitempty"`
	// HosbahToml is the raw hosbah.toml (or, failing that, cloudy.toml; ≤64 KiB); the control plane
	// parses it. The agent sends it under both keys: control planes that predate the rename read
	// only cloudy_toml. Read it with Toml().
	HosbahToml string `json:"hosbah_toml,omitempty"`
	LegacyToml string `json:"cloudy_toml,omitempty"`
	// TomlFile is the file it came from: "hosbah.toml" or "cloudy.toml".
	TomlFile string `json:"toml_file,omitempty"`
	// Files is the checkout's top-level file and directory names (MH-573 A7b), for the control
	// plane to run deploy.DetectPreset against without a second round trip.
	Files []string `json:"files,omitempty"`
}

// BuildParams / BuildResult: app.build.
type BuildParams struct {
	AppID        string `json:"app_id"`
	App          string `json:"app"`
	DeploymentID string `json:"deployment_id"`
	GitSource
	Image            string   `json:"image"`              // hosbah/<app>:<deployment_id>
	Keep             int      `json:"keep"`               // newest images of hosbah/<app> (and cloudy/<app>) to keep (5)
	Protect          []string `json:"protect,omitempty"`  // images never pruned (the live release)
	RailpackFrontend string   `json:"railpack_frontend"`  // BuildKit frontend image, same version as the CLI
	NoCache          bool     `json:"no_cache,omitempty"` // a requested full rebuild: ignore the layer cache
	// PHPVersion (MH-576 D2) is "" or "auto" (Railpack detects it from composer.json), or a pinned
	// 8.1-8.4, set as RAILPACK_PHP_VERSION on `railpack prepare`. Ignored for a Dockerfile app —
	// it owns its own base image.
	PHPVersion string `json:"php_version,omitempty"`
	// PHPIni (MH-576 D2) is a validated php.ini directive map, written to a php.ini file at the
	// checkout root before `railpack prepare` runs, unless the repository already has one of its
	// own (never overwritten). Ignored for a Dockerfile app.
	PHPIni map[string]string `json:"php_ini,omitempty"`
	// DockerfilePath / BuildContext (MH-579 gap B): "" means the defaults ("Dockerfile" at the
	// checkout root, built with the checkout root as context). Both are validated with
	// ValidBuildPath by the control plane before they ever reach a signed op, and again by the
	// agent once it can resolve the checkout's real path (internal/agent's safeJoin): relative,
	// no "..", no absolute, a restricted charset, and — after resolving symlinks — still inside
	// the checkout. Ignored by the Railpack path (its plan already encodes root-relative paths).
	DockerfilePath string `json:"dockerfile_path,omitempty"`
	BuildContext   string `json:"build_context,omitempty"`
	// Env (MH-579 gap A) is an EnvBox sealed to this agent, context EnvContext(app, deployment,
	// "build"), of the values of the app's build_env-listed variable names: the agent opens it and
	// passes each name as `--build-arg NAME`, the value only ever reaching docker through its own
	// process environment — never an argv, never a log. "" when the app exposes none at build time.
	Env string `json:"env,omitempty"`
}

type BuildResult struct {
	DetectResult
	Image   string   `json:"image"`
	ImageID string   `json:"image_id,omitempty"`
	Pruned  []string `json:"pruned,omitempty"`
}

// FullTrackPatterns match a changed path that forces a full rebuild: build definitions, lockfiles
// and toolchain/version pins that a fast, in-place restart cannot safely pick up. Shared by
// deploy.ChooseTrack (the control plane, before a deploy is dispatched — it never has changed
// paths today) and the agent's app.fast_build handler (which computes changed paths itself, from
// its cached checkout, and aborts to the same rule).
var FullTrackPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^dockerfile`),
	regexp.MustCompile(`(?i)dockerfile[^/]*$`),
	regexp.MustCompile(`(?i)^docker-compose`),
	regexp.MustCompile(`(?i)^\.dockerignore$`),
	regexp.MustCompile(`(?i)^railpack\.(json|toml|jsonc)$`),
	regexp.MustCompile(`(?i)^nixpacks\.toml$`),
	regexp.MustCompile(`(?i)^package-lock\.json$`),
	regexp.MustCompile(`(?i)^pnpm-lock\.yaml$`),
	regexp.MustCompile(`(?i)^yarn\.lock$`),
	regexp.MustCompile(`(?i)^bun\.lockb$`),
	regexp.MustCompile(`(?i)^go\.sum$`),
	regexp.MustCompile(`(?i)^composer\.lock$`),
	regexp.MustCompile(`(?i)^poetry\.lock$`),
	regexp.MustCompile(`(?i)^requirements.*\.txt$`),
	regexp.MustCompile(`(?i)^gemfile\.lock$`),
	regexp.MustCompile(`(?i)^\.tool-versions$`),
	regexp.MustCompile(`(?i)^\.nvmrc$`),
	regexp.MustCompile(`(?i)^apt\.txt$`),
	regexp.MustCompile(`(?i)^packages\.txt$`),
}

// FullTrackMatch reports the first FullTrackPatterns entry a changed path matches, checked
// against the path's base name.
func FullTrackMatch(changed []string) (string, bool) {
	for _, p := range changed {
		base := path.Base(strings.TrimSpace(p))
		if base == "" {
			continue
		}
		for _, re := range FullTrackPatterns {
			if re.MatchString(base) {
				return p, true
			}
		}
	}
	return "", false
}

// FastBuildStep is one enabled step of the app's deploy configuration (deploy.DeployStep,
// mapped over the wire so agentproto stays free of a dependency on package deploy).
type FastBuildStep struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Run  string `json:"run"`
}

// Fast build modes (MH-573 A7b): FastModeDockerfile does a cached `docker build` of the app's own
// Dockerfile — the exact image a full build would produce, just with a warm layer cache.
// FastModeBuilder runs the steps in a separate BuilderImage (bind-mounted checkout, no toolchain
// needed in the runtime image), then copies Outputs onto BaseImage with a generated Dockerfile.
// FastModeRuntime is the original MH-573 A7 behaviour: the steps run directly inside BaseImage and
// the container is committed — the fallback for an app with neither a Dockerfile nor a configured
// builder image (e.g. a static app, or one with nothing to build).
const (
	FastModeDockerfile = "dockerfile"
	FastModeBuilder    = "builder"
	FastModeRuntime    = "runtime"
)

// FastBuildParams / FastBuildResult: app.fast_build.
type FastBuildParams struct {
	AppID        string `json:"app_id"`
	App          string `json:"app"`
	DeploymentID string `json:"deployment_id"`
	GitSource
	Image     string          `json:"image"`      // hosbah/<app>:<deployment_id>, the new tag
	BaseImage string          `json:"base_image"` // the app's current live image (builder and runtime modes)
	Mode      string          `json:"mode"`       // dockerfile | builder | runtime
	Steps     []FastBuildStep `json:"steps,omitempty"`
	// BuilderImage and Outputs apply to FastModeBuilder only: the steps run in a container of
	// BuilderImage with the checkout bind-mounted at /app, then Outputs (paths relative to /app)
	// are copied onto BaseImage.
	BuilderImage string   `json:"builder_image,omitempty"`
	Outputs      []string `json:"outputs,omitempty"`
	// Env is an EnvBox sealed to this agent, context EnvContext(app, deployment, "fast_build"),
	// exported to every step (install, build, migrate) — not the release env, which app.release
	// still seals and opens on its own.
	Env string `json:"env,omitempty"`
}

// FastBuildResult reports what changed since the cached checkout's previous build, and either the
// new image or, when a full-rebuild-triggering path changed, an abort: the control plane falls
// back to app.build (Dockerfile/Railpack) and records why.
type FastBuildResult struct {
	Commit      string   `json:"commit"`
	PrevCommit  string   `json:"prev_commit,omitempty"`
	Message     string   `json:"message,omitempty"`
	Changed     []string `json:"changed,omitempty"`
	Image       string   `json:"image,omitempty"`
	ImageID     string   `json:"image_id,omitempty"`
	Aborted     bool     `json:"aborted,omitempty"`
	AbortPath   string   `json:"abort_path,omitempty"`
	AbortReason string   `json:"abort_reason,omitempty"`
	// DetectedFiles is the checkout's top-level file names, for the control plane to run
	// deploy.DetectPreset against and persist as app_deploy_configs.detected_preset.
	DetectedFiles []string `json:"detected_files,omitempty"`
	// HosbahToml is the checkout's hosbah.toml (or cloudy.toml, named by TomlFile), so a fast
	// release keeps its release command and processes. Empty from agents before MH-573's fix.
	HosbahToml string `json:"hosbah_toml,omitempty"`
	TomlFile   string `json:"toml_file,omitempty"`
}

// Validate checks the fields common to every app op (see StartParams.Validate) plus fast_build's own.
func (p FastBuildParams) Validate() error {
	if !ValidID(p.AppID) || !ValidAppName(p.App) || !ValidID(p.DeploymentID) {
		return errors.New("bad app or deployment id")
	}
	if !IsAppImage(p.App, p.Image) {
		return fmt.Errorf("image %q is not one of this app's", p.Image)
	}
	switch p.Mode {
	case FastModeDockerfile:
		// builds the checkout's own Dockerfile; no base image or steps needed.
	case FastModeBuilder:
		if p.BaseImage == "" {
			return errors.New("no base image to build from")
		}
		if p.BuilderImage == "" {
			return errors.New("no builder image")
		}
		if len(p.Outputs) == 0 {
			return errors.New("no outputs to copy")
		}
	case FastModeRuntime, "":
		if p.BaseImage == "" {
			return errors.New("no base image to build from")
		}
	default:
		return fmt.Errorf("bad fast build mode %q", p.Mode)
	}
	for _, st := range p.Steps {
		if st.Key != "" && !stepKeyRE.MatchString(st.Key) {
			return fmt.Errorf("bad step key %q", st.Key)
		}
	}
	for _, o := range p.Outputs {
		if strings.TrimSpace(o) == "" || strings.Contains(o, "..") || strings.HasPrefix(o, "/") {
			return fmt.Errorf("bad output path %q", o)
		}
	}
	return nil
}

var stepKeyRE = regexp.MustCompile(`^[a-z_]{1,32}$`)

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
	// Mounts are the app's persistent volumes (derived names, validated paths), mounted into web and
	// every worker alike. app.exec reaches them too: it execs into the web container.
	Mounts []Mount `json:"mounts,omitempty"`
	// Realtime, on one worker only: publish this container port on an ephemeral 127.0.0.1 port, the
	// way web's is, so Caddy can send the site's websocket path to it (realtime.go).
	Realtime *RealtimePublish `json:"realtime,omitempty"`
	// Resources (MH-574 E13): memory/CPU caps for this process's container. Zero value = unlimited.
	Resources ResourceLimits `json:"resources,omitempty"`
}

type StartResult struct {
	Container string `json:"container"`
	// HostPort: 127.0.0.1:<host_port> is the upstream Caddy points at — web's, or the realtime
	// worker's. Other workers publish nothing and report 0.
	HostPort int `json:"host_port,omitempty"`
}

// Route is one hostname of an app and its TLS mode (auto | internal | off).
type Route struct {
	Hostname string `json:"hostname"`
	TLS      string `json:"tls"`
}

// RouteParams: route.set. An empty Upstream removes the app's route. The params describe the app's
// whole route: without Realtime, a realtime route the app had is removed.
type RouteParams struct {
	AppID    string         `json:"app_id"`
	Hosts    []Route        `json:"hosts"`
	Upstream string         `json:"upstream,omitempty"` // 127.0.0.1:<port>
	Realtime *RealtimeRoute `json:"realtime,omitempty"`
}

// DrainParams: app.drain — every container labelled with the app (optionally one process) except
// Keep is stopped with SIGTERM, given GraceSeconds, and removed. Label-based rather than a list,
// so a container orphaned by a crash mid-deploy is cleaned up too.
type DrainParams struct {
	AppID   string   `json:"app_id"`
	Process string   `json:"process,omitempty"` // "" = all processes
	Keep    []string `json:"keep,omitempty"`
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

// Process actions of app.processes.
const (
	ProcessActionStatus  = "status"  // report only, no change
	ProcessActionRestart = "restart" // docker restart, one process (or every process when Process is "")
	ProcessActionStop    = "stop"    // docker stop (containers stay, so a later start reuses them); workers only
	ProcessActionStart   = "start"   // docker start of previously stopped containers; workers only
)

// ProcessesParams: app.processes. Process empty means "every process of the app" — allowed for
// status and restart, refused for stop/start (those must name one worker: stopping the web
// process would take the site down, and "start every process" makes no sense when web is always
// meant to be running).
type ProcessesParams struct {
	AppID   string `json:"app_id"`
	Action  string `json:"action"`
	Process string `json:"process,omitempty"`
}

// Validate checks the params on both sides of the wire: the control plane before it signs and
// enqueues, the agent before it runs.
func (p ProcessesParams) Validate() error {
	if !ValidID(p.AppID) {
		return errors.New("bad app id")
	}
	if p.Process != "" && !ValidProcess(p.Process) {
		return fmt.Errorf("bad process %q", p.Process)
	}
	switch p.Action {
	case ProcessActionStatus, ProcessActionRestart:
		return nil
	case ProcessActionStop, ProcessActionStart:
		if p.Process == "" || p.Process == "web" {
			return errors.New("web cannot be stopped or started here — stopping it takes the site down; only a worker process can be")
		}
		return nil
	default:
		return fmt.Errorf("bad action %q", p.Action)
	}
}

// ProcessStatus is one container of the app: its process, its live state, and — while running — its
// resource use. Every field the agent could not read stays at its zero value rather than a guess.
type ProcessStatus struct {
	Process       string  `json:"process"`
	Container     string  `json:"container"`
	State         string  `json:"state" doc:"docker's container state: running, exited, restarting, created…"`
	Status        string  `json:"status,omitempty" doc:"docker's human status line, e.g. \"Up 3 hours\" or \"Exited (0) 2 minutes ago\"."`
	StartedAt     string  `json:"started_at,omitempty"`
	RestartCount  int     `json:"restart_count"`
	CPUPercent    float64 `json:"cpu_percent,omitempty" doc:"Only while running (docker stats)."`
	MemBytes      int64   `json:"mem_bytes,omitempty" doc:"Only while running."`
	MemLimitBytes int64   `json:"mem_limit_bytes,omitempty" doc:"Only while running."`
	Image         string  `json:"image,omitempty"`
	Deployment    string  `json:"deployment,omitempty" doc:"The deployment id the container's label names, when it has one."`
}

// ProcessesResult: app.processes — the app's processes after any requested action.
type ProcessesResult struct {
	Processes []ProcessStatus `json:"processes"`
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
	appNameRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	processRE   = regexp.MustCompile(`^(web|worker:[a-z0-9_-]{1,32})$`)
	refRE       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
	uuidRE      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	hostRE      = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	buildPathRE = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
)

// ValidBuildPath (MH-579 gap B) checks a dockerfile_path or build_context before it is saved
// (internal/deploy) and again before the agent joins it to the checkout (internal/agent's
// safeJoin, which also resolves symlinks — this only checks the string itself): relative (no
// leading "/"), no ".." component, a restricted charset, and no longer than 200 bytes. An empty
// string is not itself valid here — callers treat "" as "not set" (the default) before calling
// this, exactly like BuilderImage.
func ValidBuildPath(s string) bool {
	return buildPathRE.MatchString(s) && !strings.Contains(s, "..") && !strings.HasPrefix(s, "/")
}

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

// IsWildcardHost: "*.<name>", a route for every single-label subdomain of name.
func IsWildcardHost(s string) bool { return strings.HasPrefix(s, "*.") }

// ValidRouteHostname: what an app's route may carry — a ValidHostname, or one leading wildcard
// label over a name with at least two labels ("*.example.com"; never "*.com"). A wildcard is
// served over tls off or internal only: a public certificate for it needs a DNS challenge, which
// on-demand TLS cannot do (ValidRouteHost checks the pair).
func ValidRouteHostname(s string) bool {
	if rest, ok := strings.CutPrefix(s, "*."); ok {
		return strings.Contains(rest, ".") && ValidHostname(rest)
	}
	return ValidHostname(s)
}

// ValidRouteHost checks a route's hostname and TLS mode together.
func ValidRouteHost(h Route) bool {
	if !ValidRouteHostname(h.Hostname) || !ValidTLS(h.TLS) {
		return false
	}
	return !IsWildcardHost(h.Hostname) || h.TLS != "auto"
}

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
	return NamePrefix + app + "-" + strings.ReplaceAll(process, ":", "-") + "-" + short
}

// ImageRepo is the app's image repository on its server. Images built before the rename are in
// LegacyImageRepo, and stay usable (a rollback to one of them).
func ImageRepo(app string) string { return ImagePrefix + app }

// LegacyImageRepo is the repository an agent before the rename built the app's images into.
func LegacyImageRepo(app string) string { return LegacyImagePrefix + app }

// IsAppImage reports whether image is a tag of the app's repository, new or old.
func IsAppImage(app, image string) bool {
	return strings.HasPrefix(image, ImageRepo(app)+":") || strings.HasPrefix(image, LegacyImageRepo(app)+":")
}

// RouteID is the Caddy @id of an app's route.
func RouteID(appID string) string { return NamePrefix + "app-" + appID }

// Validate checks the fields every app op shares, on both sides of the wire.
func (p StartParams) Validate() error {
	switch {
	case !ValidID(p.AppID) || !ValidAppName(p.App) || !ValidID(p.DeploymentID):
		return errors.New("bad app or deployment id")
	case !ValidProcess(p.Process):
		return fmt.Errorf("bad process %q", p.Process)
	case !IsAppImage(p.App, p.Image):
		return fmt.Errorf("image %q is not one of this app's", p.Image)
	case p.Process == "web" && (p.Port < 1 || p.Port > 65535):
		return errors.New("web needs a port")
	case p.Health != nil && (!strings.HasPrefix(p.Health.Path, "/") || p.Health.TimeoutSeconds < 1 || p.Health.TimeoutSeconds > 600):
		return errors.New("bad health check")
	case p.Realtime != nil && p.Process == "web":
		return errors.New("web is already published — realtime is a worker process")
	}
	if p.Realtime != nil {
		if !ValidRealtimeProcess(strings.TrimPrefix(p.Process, "worker:")) {
			return fmt.Errorf("bad realtime process %q", p.Process)
		}
		if err := p.Realtime.Validate(); err != nil {
			return err
		}
	}
	if err := p.Resources.Validate(); err != nil {
		return err
	}
	return ValidateMounts(p.AppID, p.Mounts)
}

// Toml is the app's config file as the agent sent it, whichever key carried it.
func (r DetectResult) Toml() string {
	if r.HosbahToml != "" {
		return r.HosbahToml
	}
	return r.LegacyToml
}
