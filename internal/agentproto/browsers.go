package agentproto

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
)

// A site's browsers, in two kinds. KindHeadless (the default, unchanged): chromedp/headless-shell
// containers the site drives over CDP/WebSocket. Typed like everything else: the container name is
// DERIVED from the browser id, the image is a constant both sides compare against, and the token
// travels in a box sealed to the agent — never in the params.
//
// The headless image is chromedp/headless-shell (MIT; Chromium itself is BSD). It was chosen over
// browserless's Chromium image because browserless is "SSPL-1.0 OR Browserless Commercial License":
// running it for the closed-source sites of a hosting product needs a purchased licence.
// headless-shell speaks bare CDP with no authentication, so the container publishes its port on
// 127.0.0.1 ONLY and the agent's gateway — which listens on the server's private address, the one a
// site's containers reach data services on — checks the token, caps concurrent sessions and drops
// idle ones before anything reaches Chromium.
//
// KindDesktop is a FULL interactive desktop: lscr.io/linuxserver/chromium, which wraps the same
// Chromium in a KasmVNC web desktop (a whole X session over noVNC-style HTTP/WebSocket, not just
// CDP). The linuxserver image is GPL-licensed and run here unmodified (only pulled and run, never
// changed or redistributed), which is exactly the unmodified-use the licence needs no purchase for
// — the same reasoning as headless-shell's MIT/BSD, just a different licence family. KasmVNC has no
// authentication of its own, so the same rule as headless applies: the container's port is published
// on 127.0.0.1 ONLY, and it is never reachable except through a hostname Caddy protects with
// forward_auth to Hosbah (internal/deploy's desktop-browser hostname wiring, internal/api's
// /api/browsers/auth). There is no token gateway for a desktop browser: the whole point is a human
// driving a real browser window, so the access control is the session-derived forward_auth check,
// not a bearer token in a WebSocket URL.
const (
	KindBrowserCreate = "browser.create" // pull the image, docker run, wait ready, open the token gateway (headless only)
	KindBrowserRemove = "browser.remove" // stop and remove the container (and the gateway, for headless); idempotent
	KindBrowserStatus = "browser.status" // read-only: running?, restart count, memory, open sessions

	// BrowserKindHeadless and BrowserKindDesktop are the two browser kinds a create may ask for.
	BrowserKindHeadless = "headless"
	BrowserKindDesktop  = "desktop"

	// BrowserImage is THE image of a headless browser, pinned by tag. Never user input.
	BrowserImage = "chromedp/headless-shell:151.0.7922.109"
	// BrowserCDPPort is the port headless-shell serves CDP on inside the container.
	BrowserCDPPort = 9222

	// DesktopImage is THE image of a desktop browser, pinned by tag. Never user input. Check
	// https://github.com/linuxserver/docker-chromium for the current tag before bumping it.
	DesktopImage = "lscr.io/linuxserver/chromium:7ac003b2-ls53"
	// DesktopHTTPPort is the port KasmVNC serves its web desktop on inside the container (plain
	// HTTP; Caddy terminates TLS at the edge, so the loopback hop does not need KasmVNC's own
	// self-signed :3001).
	DesktopHTTPPort = 3000
	// DesktopBind is the ONLY address a desktop browser publishes on. KasmVNC has no auth of its own,
	// and the Docker bridge address (the headless gateway's) is reachable from every container on the
	// server, so only the host's Caddy — through forward_auth to Hosbah — may reach it.
	DesktopBind = "127.0.0.1"
	// DesktopShmMB is the container's /dev/shm: a full GiB, fixed (a real desktop's compositor and
	// Chromium both need more than the memory-scaled BrowserShmMB headless gets by with).
	DesktopShmMB = 1024
	// DesktopPUID, DesktopPGID: the linuxserver image's own convention (s6-overlay chowns
	// /config to this uid/gid before dropping root). Fixed, not user input.
	DesktopPUID, DesktopPGID = 1000, 1000
	// DesktopDefaultTZ is used when the server's own TZ cannot be read.
	DesktopDefaultTZ = "UTC"

	// BrowserPidsLimit bounds a headless browser's processes and threads (Chromium is thread-hungry).
	BrowserPidsLimit = 1024
	// DesktopPidsLimit is higher: a full desktop session runs a window manager, PulseAudio and more.
	DesktopPidsLimit = 2048

	// Names (cloudy-browser- and cloudy.* before the rename; the agent still finds those).
	BrowserContainerPrefix = NamePrefix + "browser-"
	// DesktopVolumePrefix names a desktop browser's persistent profile volume.
	DesktopVolumePrefix = NamePrefix + "browser-profile-"
	// Labels. NOT hosbah.app: a deploy's drain and a rollback stop every container with that label.
	BrowserLabel    = LabelPrefix + "browser"
	BrowserAppLabel = LabelPrefix + "browser-app"

	MaxAppBrowsers  = 3
	BrowserPortBase = 19222 // the first port the control plane hands out on a server for either kind

	BrowserMinMemoryMB, BrowserMaxMemoryMB, BrowserDefaultMemoryMB = 256, 4096, 1024
	BrowserMinCPUs, BrowserMaxCPUs, BrowserDefaultCPUs             = 0.25, 4.0, 1.0
	BrowserMinSessions, BrowserMaxSessions, BrowserDefaultSessions = 1, 20, 5
	BrowserMinIdle, BrowserMaxIdle, BrowserDefaultIdle             = 30, 3600, 300

	// A desktop browser is a whole interactive session, not a headless worker: it gets a wider
	// memory range and a higher default.
	DesktopMinMemoryMB, DesktopMaxMemoryMB, DesktopDefaultMemoryMB = 512, 8192, 2048
)

// ValidBrowserKind reports whether kind is one of the two a create may ask for.
func ValidBrowserKind(kind string) bool {
	return kind == BrowserKindHeadless || kind == BrowserKindDesktop
}

var (
	browserNameRE  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	browserTokenRE = regexp.MustCompile(`^[A-Za-z0-9]{40}$`)
)

// ValidBrowserName: the user's name of a browser.
func ValidBrowserName(s string) bool { return browserNameRE.MatchString(s) }

// ValidBrowserToken: 40 alphanumerics, generated by the control plane.
func ValidBrowserToken(s string) bool { return browserTokenRE.MatchString(s) }

// BrowserContainerName is the container of a browser. Derived, never user-supplied.
func BrowserContainerName(browserID string) string { return BrowserContainerPrefix + browserID }

// BrowserTokenContext is the box context of a browser's token: {"token": …}.
func BrowserTokenContext(browserID string) string { return "browser:" + browserID + ":token" }

// ValidBrowserBind: the address the gateway listens on — an IPv4 loopback or private address.
// Never 0.0.0.0, never a public address.
func ValidBrowserBind(s string) bool {
	ip, err := netip.ParseAddr(s)
	return err == nil && ip.Is4() && !ip.IsUnspecified() && (ip.IsLoopback() || ip.IsPrivate())
}

// BrowserShmMB is the container's /dev/shm: a quarter of its memory, 64 MiB to 1 GiB. Chromium
// crashes on heavy pages with Docker's default of 64 MiB.
func BrowserShmMB(memoryMB int) int { return min(max(memoryMB/4, 64), 1024) }

// DesktopVolumeName is a desktop browser's persistent profile volume (its KasmVNC/Chromium
// /config), named after the browser so it survives a recreate and goes with a remove. Derived,
// never user-supplied.
func DesktopVolumeName(browserID string) string { return DesktopVolumePrefix + browserID }

// BrowserCreateParams: browser.create. Token is a box for BrowserTokenContext(BrowserID); required
// for KindHeadless, ignored (must be "") for KindDesktop — a desktop browser has no CDP token
// gateway, its access control is the forward_auth hostname Caddy puts in front of it.
type BrowserCreateParams struct {
	BrowserID string `json:"browser_id"`
	AppID     string `json:"app_id"`
	// Kind: BrowserKindHeadless (default, "" reads as headless for pre-kind operations already on
	// the wire) or BrowserKindDesktop.
	Kind               string  `json:"kind,omitempty"`
	Image              string  `json:"image"`
	Bind               string  `json:"bind"`
	Port               int     `json:"port"`
	MemoryMB           int     `json:"memory_mb"`
	CPUs               float64 `json:"cpus"`
	MaxSessions        int     `json:"max_sessions"`
	IdleTimeoutSeconds int     `json:"idle_timeout_seconds"`
	Token              string  `json:"token"`
	// TZ (KindDesktop only): the container's timezone, e.g. "Africa/Cairo". "" reads as DesktopDefaultTZ.
	TZ string `json:"tz,omitempty"`
}

// EffectiveKind is Kind, or BrowserKindHeadless when Kind is "" (pre-kind operations).
func (p BrowserCreateParams) EffectiveKind() string {
	if p.Kind == "" {
		return BrowserKindHeadless
	}
	return p.Kind
}

func (p BrowserCreateParams) Validate() error {
	kind := p.EffectiveKind()
	switch {
	case !ValidID(p.BrowserID) || !ValidID(p.AppID):
		return errors.New("bad browser or app id")
	case !ValidBrowserKind(kind):
		return errors.New("kind must be headless or desktop")
	case kind == BrowserKindHeadless && p.Image != BrowserImage:
		return fmt.Errorf("image %q is not the headless browser image", p.Image)
	case kind == BrowserKindDesktop && p.Image != DesktopImage:
		return fmt.Errorf("image %q is not the desktop browser image", p.Image)
	case !ValidBrowserBind(p.Bind):
		return errors.New("a browser listens on a loopback or private IPv4 address only")
	case kind == BrowserKindDesktop && p.Bind != DesktopBind:
		return errors.New("a desktop browser listens on the loopback only (its only door is Caddy's forward_auth)")
	case p.Port < 1024 || p.Port > 65535:
		return errors.New("bad port")
	case kind == BrowserKindHeadless && (p.MemoryMB < BrowserMinMemoryMB || p.MemoryMB > BrowserMaxMemoryMB):
		return fmt.Errorf("memory_mb is %d to %d", BrowserMinMemoryMB, BrowserMaxMemoryMB)
	case kind == BrowserKindDesktop && (p.MemoryMB < DesktopMinMemoryMB || p.MemoryMB > DesktopMaxMemoryMB):
		return fmt.Errorf("memory_mb is %d to %d", DesktopMinMemoryMB, DesktopMaxMemoryMB)
	case !(p.CPUs >= BrowserMinCPUs && p.CPUs <= BrowserMaxCPUs):
		return fmt.Errorf("cpus is %g to %g", BrowserMinCPUs, BrowserMaxCPUs)
	case p.MaxSessions < BrowserMinSessions || p.MaxSessions > BrowserMaxSessions:
		return fmt.Errorf("max_sessions is %d to %d", BrowserMinSessions, BrowserMaxSessions)
	case p.IdleTimeoutSeconds < BrowserMinIdle || p.IdleTimeoutSeconds > BrowserMaxIdle:
		return fmt.Errorf("idle_timeout_seconds is %d to %d", BrowserMinIdle, BrowserMaxIdle)
	case kind == BrowserKindHeadless && p.Token == "":
		return errors.New("browser.create needs the token")
	case kind == BrowserKindDesktop && p.Token != "":
		return errors.New("a desktop browser has no token")
	}
	return nil
}

type BrowserCreateResult struct {
	Container string `json:"container"`
	HostPort  int    `json:"host_port"` // what the site (headless) or Caddy (desktop) connects to
}

// BrowserRemoveParams: browser.remove.
type BrowserRemoveParams struct {
	BrowserID string `json:"browser_id"`
	AppID     string `json:"app_id"`
}

func (p BrowserRemoveParams) Validate() error {
	if !ValidID(p.BrowserID) || !ValidID(p.AppID) {
		return errors.New("bad browser.remove params")
	}
	return nil
}

type BrowserRemoveResult struct {
	Container string `json:"container"`
	Removed   bool   `json:"removed"` // false: it was already gone
}

// BrowserStatusParams: browser.status.
type BrowserStatusParams struct {
	AppID      string   `json:"app_id"`
	BrowserIDs []string `json:"browser_ids"`
}

func (p BrowserStatusParams) Validate() error {
	if !ValidID(p.AppID) || len(p.BrowserIDs) == 0 || len(p.BrowserIDs) > MaxAppBrowsers {
		return errors.New("bad browser.status params")
	}
	for _, id := range p.BrowserIDs {
		if !ValidID(id) {
			return errors.New("bad browser id")
		}
	}
	return nil
}

// BrowserState is one browser as the server sees it. MemoryBytes is nil when docker stats did not
// report it.
type BrowserState struct {
	BrowserID   string `json:"browser_id"`
	Running     bool   `json:"running"`
	Restarts    int    `json:"restarts"`
	MemoryBytes *int64 `json:"memory_bytes"`
	Sessions    int    `json:"sessions"`
}

type BrowserStatusResult struct {
	Browsers []BrowserState `json:"browsers"`
}
