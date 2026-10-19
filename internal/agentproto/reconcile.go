package agentproto

// RouteReport is one TypeRoutes entry: an app's (or a proxy host's) upstream as the agent now
// routes it. Docker hands a restarted container a new loopback port (publishArg asks the kernel
// for one), so after a host, LXC or Docker restart the agent re-points its Caddy routes at the
// ports the same containers publish now and tells the control plane, which keeps the live
// release's host_port in step — otherwise its next route.set (a domain added) would bring the old
// port, and the 502s, back.
type RouteReport struct {
	AppID   string `json:"app_id,omitempty"`
	ProxyID string `json:"proxy_id,omitempty"`
	// Container is the container now behind Upstream. The control plane applies a report only when
	// it is the live release's own web (or realtime) container, so a report racing a deploy that
	// already switched to a new release never moves it back.
	Container string `json:"container,omitempty"`
	// Upstream / Previous are 127.0.0.1:<port>; empty when unchanged (a WARN-only report).
	Upstream string `json:"upstream,omitempty"`
	Previous string `json:"previous,omitempty"`
	// Realtime: the same for the app's realtime worker route.
	RealtimeContainer string `json:"realtime_container,omitempty"`
	RealtimeUpstream  string `json:"realtime_upstream,omitempty"`
	RealtimePrevious  string `json:"realtime_previous,omitempty"`
	// Level is "info" (re-pointed) or "warn" (the upstream refused connections and nothing running
	// could replace it, or it was re-pointed only after the health check found it down).
	Level   string `json:"level"`
	Message string `json:"message"`
}

// Route report levels.
const (
	RouteReportInfo = "info"
	RouteReportWarn = "warn"
)

// MaxRouteReports bounds one TypeRoutes frame (the control plane drops anything past it).
const MaxRouteReports = 200
