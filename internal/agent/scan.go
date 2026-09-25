package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// app.scan (MH-580 J5): a Trivy `image` scan of one app's release image, run in a throwaway
// container. Trivy's own cache (its vulnerability database) lives in the shared named volume
// agentproto.TrivyCacheVolume, so repeat scans on the same server do not re-download it. The image
// being scanned is never rebuilt or re-pulled here: app.scan only inspects whatever `image` the
// deploy pipeline already has on this server.

// maxTrivyOutput bounds how much of Trivy's JSON report the agent will buffer, so a pathological
// image (thousands of packages) cannot exhaust memory; Trivy's JSON is read incrementally up to
// this many bytes and then truncated — malformed JSON at that point just fails to parse, which
// app.scan reports as an error rather than a partial (and misleading) result.
const maxTrivyOutput = 32 << 20 // 32 MiB

func appScan(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.AppScanParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	if code, err := dbDockerReady(oc); err != nil {
		return nil, code, err
	}
	severities := p.Severity
	if len(severities) == 0 {
		severities = agentproto.DefaultScanSeverities
	}
	if _, err := oc.Run("docker", "volume", "create", agentproto.TrivyCacheVolume); err != nil {
		return nil, 1, errors.New("could not create the Trivy cache volume")
	}
	oc.Info("scanning %s with Trivy %s (severity: %s)", p.Image, agentproto.TrivyImage, strings.Join(severities, ","))
	args := []string{"run", "--rm",
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		"-v", agentproto.TrivyCacheVolume + ":/root/.cache/",
		agentproto.TrivyImage,
		"image", "--format", "json", "--quiet", "--severity", strings.Join(severities, ","), p.Image,
	}
	cmd := exec.CommandContext(oc.Ctx, "docker", args...) //#nosec G204 -- fixed argv; Image is validated, everything else is a constant
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdout, max: maxTrivyOutput}
	cmd.Stderr = &limitedWriter{w: &stderr, max: 1 << 20}
	runErr := cmd.Run()
	if stderr.Len() > 0 {
		oc.Info("trivy: %s", strings.TrimSpace(stderr.String()))
	}
	if runErr != nil {
		return nil, 1, fmt.Errorf("trivy could not scan %s: %w", p.Image, runErr)
	}
	res, err := parseTrivyReport(stdout.Bytes())
	if err != nil {
		return nil, 1, fmt.Errorf("could not read Trivy's report: %w", err)
	}
	oc.Info("scan complete: %d finding(s) (%s)", countFindings(res.Counts), summarizeCounts(res.Counts))
	return res, 0, nil
}

// limitedWriter caps how many bytes it keeps, without erroring once the cap is hit (docker/trivy
// still gets to exit normally; the report is just truncated, which parseTrivyReport treats as a
// scan error rather than a silently wrong one).
type limitedWriter struct {
	w   io.Writer
	max int
	n   int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n >= l.max {
		return len(p), nil
	}
	room := l.max - l.n
	if room > len(p) {
		room = len(p)
	}
	n, err := l.w.Write(p[:room])
	l.n += n
	return len(p), err
}

// trivyReport is the subset of `trivy image --format json` this agent reads.
type trivyReport struct {
	Metadata struct {
		// Trivy's own JSON does not carry its own version; SchemaVersion is reported instead.
	} `json:"Metadata"`
	Results []struct {
		Vulnerabilities []struct {
			VulnerabilityID  string `json:"VulnerabilityID"`
			PkgName          string `json:"PkgName"`
			InstalledVersion string `json:"InstalledVersion"`
			FixedVersion     string `json:"FixedVersion"`
			Severity         string `json:"Severity"`
			Title            string `json:"Title"`
		} `json:"Vulnerabilities"`
	} `json:"Results"`
}

var severityRank = map[string]int{"CRITICAL": 0, "HIGH": 1, "MEDIUM": 2, "LOW": 3, "UNKNOWN": 4}

func parseTrivyReport(raw []byte) (agentproto.AppScanResult, error) {
	raw = bytes.TrimSpace(raw)
	res := agentproto.AppScanResult{Counts: map[string]int{"CRITICAL": 0, "HIGH": 0, "MEDIUM": 0, "LOW": 0, "UNKNOWN": 0}}
	if len(raw) == 0 {
		// No output at all (e.g. the image has no known vulnerabilities table Trivy could scan) is
		// not an error: an all-zero result is exactly what a clean image reports.
		return res, nil
	}
	var rep trivyReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		return agentproto.AppScanResult{}, err
	}
	var findings []agentproto.ScanFinding
	for _, r := range rep.Results {
		for _, v := range r.Vulnerabilities {
			sev := strings.ToUpper(v.Severity)
			if _, ok := res.Counts[sev]; !ok {
				sev = "UNKNOWN"
			}
			res.Counts[sev]++
			findings = append(findings, agentproto.ScanFinding{
				ID: v.VulnerabilityID, Package: v.PkgName, InstalledVersion: v.InstalledVersion,
				FixedVersion: v.FixedVersion, Severity: sev, Title: v.Title,
			})
		}
	}
	sort.SliceStable(findings, func(i, j int) bool { return severityRank[findings[i].Severity] < severityRank[findings[j].Severity] })
	if len(findings) > agentproto.MaxScanFindings {
		res.Truncated = true
		findings = findings[:agentproto.MaxScanFindings]
	}
	res.Findings = findings
	return res, nil
}

func countFindings(counts map[string]int) int {
	n := 0
	for _, c := range counts {
		n += c
	}
	return n
}

func summarizeCounts(counts map[string]int) string {
	return fmt.Sprintf("critical %d, high %d, medium %d, low %d, unknown %d",
		counts["CRITICAL"], counts["HIGH"], counts["MEDIUM"], counts["LOW"], counts["UNKNOWN"])
}
