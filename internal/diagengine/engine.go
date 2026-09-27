// Package diagengine implements deterministic diagnostic rules that combine
// browser-measured performance with UniFi-reported wireless conditions.
// It reports evidence and likely contributing factors — never false certainty.
package diagengine

import (
	"fmt"

	"wifi-diagnostics/internal/diag"
	"wifi-diagnostics/internal/unifi"
)

// Severity indicates how concerning a finding is.
type Severity string

const (
	SeverityOK      Severity = "ok"
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Finding is one piece of diagnostic evidence.
type Finding struct {
	Severity    Severity `json:"severity"`
	Category    string   `json:"category"`
	Description string   `json:"description"`
	Evidence    string   `json:"evidence,omitempty"`
}

// Assessment is the full diagnostic result for a session.
type Assessment struct {
	Findings       []Finding `json:"findings"`
	Summary        string    `json:"summary"`
	OverallSeverity Severity `json:"overall_severity"`
}

// Input holds all available measurement data for a diagnostic session.
type Input struct {
	// UniFi data — may be nil if correlation failed
	UnifiClient *unifi.Client

	// Browser measurements — may be nil if test not completed
	LANLatency     *diag.LatencyResult
	InternetLatency *diag.LatencyResult
	Download       *diag.ThroughputResult
	Upload         *diag.ThroughputResult
}

// Assess runs the diagnostic rules against the available inputs.
func Assess(in Input) Assessment {
	var findings []Finding

	// --- Wi-Fi signal quality ---
	if in.UnifiClient != nil && !in.UnifiClient.IsWired {
		rssi := in.UnifiClient.RSSI
		switch {
		case rssi < -85:
			findings = append(findings, Finding{
				Severity:    SeverityCritical,
				Category:    "signal",
				Description: "Very weak Wi-Fi signal",
				Evidence:    fmt.Sprintf("UniFi RSSI: %d dBm (threshold: < -85 dBm)", rssi),
			})
		case rssi < -75:
			findings = append(findings, Finding{
				Severity:    SeverityWarning,
				Category:    "signal",
				Description: "Weak Wi-Fi signal",
				Evidence:    fmt.Sprintf("UniFi RSSI: %d dBm (threshold: < -75 dBm)", rssi),
			})
		case rssi < -65:
			findings = append(findings, Finding{
				Severity:    SeverityInfo,
				Category:    "signal",
				Description: "Moderate Wi-Fi signal",
				Evidence:    fmt.Sprintf("UniFi RSSI: %d dBm", rssi),
			})
		default:
			findings = append(findings, Finding{
				Severity:    SeverityOK,
				Category:    "signal",
				Description: "Good Wi-Fi signal",
				Evidence:    fmt.Sprintf("UniFi RSSI: %d dBm", rssi),
			})
		}

		// Retry rate
		if in.UnifiClient.Retries > 0 {
			retries := in.UnifiClient.Retries
			switch {
			case retries > 20:
				findings = append(findings, Finding{
					Severity:    SeverityCritical,
					Category:    "retries",
					Description: "Very high wireless retry rate — likely interference or signal edge",
					Evidence:    fmt.Sprintf("UniFi retries: %d%%", retries),
				})
			case retries > 10:
				findings = append(findings, Finding{
					Severity:    SeverityWarning,
					Category:    "retries",
					Description: "Elevated wireless retry rate",
					Evidence:    fmt.Sprintf("UniFi retries: %d%%", retries),
				})
			}
		}
	}

	// --- LAN latency ---
	if in.LANLatency != nil {
		lat := in.LANLatency
		switch {
		case lat.Avg > 100:
			findings = append(findings, Finding{
				Severity:    SeverityCritical,
				Category:    "lan_latency",
				Description: "Very high LAN latency",
				Evidence:    fmt.Sprintf("avg %.1f ms, p95 %.1f ms", lat.Avg, lat.P95),
			})
		case lat.Avg > 30:
			findings = append(findings, Finding{
				Severity:    SeverityWarning,
				Category:    "lan_latency",
				Description: "Elevated LAN latency",
				Evidence:    fmt.Sprintf("avg %.1f ms, p95 %.1f ms", lat.Avg, lat.P95),
			})
		default:
			findings = append(findings, Finding{
				Severity:    SeverityOK,
				Category:    "lan_latency",
				Description: "Good LAN latency",
				Evidence:    fmt.Sprintf("avg %.1f ms", lat.Avg),
			})
		}

		// Jitter
		if lat.Jitter > 20 {
			findings = append(findings, Finding{
				Severity:    SeverityWarning,
				Category:    "jitter",
				Description: "High LAN jitter — connection is unstable",
				Evidence:    fmt.Sprintf("jitter %.1f ms", lat.Jitter),
			})
		}

		// Packet loss
		if lat.Loss > 5 {
			findings = append(findings, Finding{
				Severity:    SeverityCritical,
				Category:    "loss",
				Description: "Significant packet loss on LAN path",
				Evidence:    fmt.Sprintf("%.1f%% loss (%d/%d)", lat.Loss, lat.Sent-lat.Count, lat.Sent),
			})
		} else if lat.Loss > 1 {
			findings = append(findings, Finding{
				Severity:    SeverityWarning,
				Category:    "loss",
				Description: "Some packet loss on LAN path",
				Evidence:    fmt.Sprintf("%.1f%% loss", lat.Loss),
			})
		}
	}

	// --- Internet vs LAN comparison ---
	if in.InternetLatency != nil && in.LANLatency != nil {
		inetLat := in.InternetLatency
		lanLat := in.LANLatency

		if inetLat.Avg > 150 && lanLat.Avg < 20 {
			findings = append(findings, Finding{
				Severity:    SeverityInfo,
				Category:    "internet",
				Description: "High internet latency but healthy LAN — problem is likely beyond the local network",
				Evidence: fmt.Sprintf("internet avg %.1f ms vs LAN avg %.1f ms",
					inetLat.Avg, lanLat.Avg),
			})
		}
	}

	// --- Correlation between RF and performance ---
	if in.UnifiClient != nil && !in.UnifiClient.IsWired && in.LANLatency != nil {
		rssi := in.UnifiClient.RSSI
		retries := in.UnifiClient.Retries
		lat := in.LANLatency

		if (rssi < -70 || retries > 10) && lat.Avg > 20 {
			findings = append(findings, Finding{
				Severity:    SeverityWarning,
				Category:    "correlation",
				Description: "Poor wireless conditions correlate with elevated latency — likely wireless path issue",
				Evidence: fmt.Sprintf("RSSI %d dBm, retries %d%%, LAN avg %.1f ms",
					rssi, retries, lat.Avg),
			})
		}
	}

	// Compute overall severity.
	overall := overallSeverity(findings)
	summary := buildSummary(findings, in, overall)

	return Assessment{
		Findings:        findings,
		Summary:         summary,
		OverallSeverity: overall,
	}
}

func overallSeverity(findings []Finding) Severity {
	sev := SeverityOK
	for _, f := range findings {
		if severityRank(f.Severity) > severityRank(sev) {
			sev = f.Severity
		}
	}
	return sev
}

func severityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

func buildSummary(findings []Finding, in Input, overall Severity) string {
	if len(findings) == 0 {
		return "No measurements available."
	}
	switch overall {
	case SeverityOK:
		return "No significant issues detected. Wi-Fi signal and LAN performance appear healthy."
	case SeverityInfo:
		return "Connection appears functional. Some informational findings noted."
	case SeverityWarning:
		return "One or more potential issues detected. Review the findings for details."
	case SeverityCritical:
		return "Significant issues detected that are likely affecting connectivity or performance."
	}
	return ""
}
