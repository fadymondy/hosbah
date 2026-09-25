package agentproto

import "errors"

// app.scan (MH-580 J5): a Trivy `image` scan of one app's current release image, run by the agent in
// a throwaway Trivy container on the app's own server (never on the control plane — the image only
// ever exists where it was built/pulled). Like mail.install, the whole call is a fixed argv: nothing
// in the params reaches a shell. Results are a compact summary (severity counts and the top
// findings), never the full Trivy report, so a scan of a large image cannot flood the operations
// table or the team's audit log.
const KindAppScan = "app.scan"

// TrivyImage pins the Trivy image tag the agent runs, the same way MailImage pins Stalwart's: one
// place, so every scan (and every test) agrees on the exact, checked-in version.
const TrivyImage = "aquasec/trivy:0.58.1"

// TrivyCacheVolume is the one named volume every app.scan on a server shares for Trivy's
// vulnerability database, so repeat scans do not re-download it.
const TrivyCacheVolume = NamePrefix + "trivy-cache"

// DefaultScanSeverities is what a scan checks when Severity is empty.
var DefaultScanSeverities = []string{"CRITICAL", "HIGH", "MEDIUM", "LOW"}

var scanSeverities = map[string]bool{"CRITICAL": true, "HIGH": true, "MEDIUM": true, "LOW": true, "UNKNOWN": true}

// MaxScanFindings bounds how many individual findings an app.scan result carries; the severity
// counts still report the true totals even when the finding list is truncated.
const MaxScanFindings = 25

// AppScanParams: app.scan. Image is the fully-qualified reference the agent already has locally
// (built or pulled by a prior deploy step) or can pull — the agent never chooses it.
type AppScanParams struct {
	// AppID is carried only for lane routing (a scan shares its app's lane, so it never interleaves
	// with that app's own build/release/start) and for the agent's log lines; the agent itself never
	// looks the app up — Image is everything it needs.
	AppID    string   `json:"app_id,omitempty"`
	Image    string   `json:"image"`
	Severity []string `json:"severity,omitempty" doc:"Defaults to CRITICAL,HIGH,MEDIUM,LOW."`
}

func (p AppScanParams) Validate() error {
	if p.Image == "" {
		return errors.New("app.scan: image is required")
	}
	for _, s := range p.Severity {
		if !scanSeverities[s] {
			return errors.New("app.scan: severity must be one of CRITICAL, HIGH, MEDIUM, LOW, UNKNOWN")
		}
	}
	return nil
}

// ScanFinding is one vulnerability Trivy reported, trimmed to what the app page shows.
type ScanFinding struct {
	ID               string `json:"id"`
	Package          string `json:"package"`
	InstalledVersion string `json:"installed_version"`
	FixedVersion     string `json:"fixed_version,omitempty"`
	Severity         string `json:"severity"`
	Title            string `json:"title,omitempty"`
}

// AppScanResult: app.scan.
type AppScanResult struct {
	TrivyVersion string        `json:"trivy_version,omitempty"`
	Counts       map[string]int `json:"counts"` // severity (upper-case) -> count, every scanned severity present even at 0
	Findings     []ScanFinding `json:"findings,omitempty"` // first MaxScanFindings, worst severity first
	Truncated    bool          `json:"truncated,omitempty"`
}
