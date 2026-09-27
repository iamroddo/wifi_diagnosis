package diagengine

import (
	"testing"

	"wifi-diagnostics/internal/diag"
	"wifi-diagnostics/internal/unifi"
)

func TestAssess_HealthyWifi(t *testing.T) {
	in := Input{
		UnifiClient: &unifi.Client{
			RSSI:    -55,
			Retries: 2,
			Band:    "5G",
		},
		LANLatency: &diag.LatencyResult{
			Avg:    5,
			P95:    8,
			Jitter: 1,
			Loss:   0,
			Count:  20,
			Sent:   20,
		},
	}
	a := Assess(in)
	if a.OverallSeverity != SeverityOK {
		t.Errorf("expected ok, got %v; findings: %v", a.OverallSeverity, a.Findings)
	}
}

func TestAssess_WeakSignal(t *testing.T) {
	in := Input{
		UnifiClient: &unifi.Client{
			RSSI:    -82,
			Retries: 15,
		},
		LANLatency: &diag.LatencyResult{
			Avg:    45,
			P95:    80,
			Jitter: 25,
			Loss:   2,
			Count:  18,
			Sent:   20,
		},
	}
	a := Assess(in)
	if severityRank(a.OverallSeverity) < severityRank(SeverityWarning) {
		t.Errorf("expected at least warning, got %v", a.OverallSeverity)
	}
	hasSignal := false
	for _, f := range a.Findings {
		if f.Category == "signal" && severityRank(f.Severity) >= severityRank(SeverityWarning) {
			hasSignal = true
		}
	}
	if !hasSignal {
		t.Error("expected a signal warning finding")
	}
}

func TestAssess_CriticalSignal(t *testing.T) {
	in := Input{
		UnifiClient: &unifi.Client{RSSI: -90},
	}
	a := Assess(in)
	hasCritical := false
	for _, f := range a.Findings {
		if f.Category == "signal" && f.Severity == SeverityCritical {
			hasCritical = true
		}
	}
	if !hasCritical {
		t.Error("expected critical signal finding at -90 dBm")
	}
}

func TestAssess_InternetProblem(t *testing.T) {
	in := Input{
		UnifiClient: &unifi.Client{RSSI: -55, Retries: 1},
		LANLatency: &diag.LatencyResult{
			Avg: 5, P95: 8, Loss: 0, Count: 20, Sent: 20,
		},
		InternetLatency: &diag.LatencyResult{
			Avg: 300, P95: 400, Loss: 2, Count: 18, Sent: 20,
		},
	}
	a := Assess(in)
	hasInetFinding := false
	for _, f := range a.Findings {
		if f.Category == "internet" {
			hasInetFinding = true
		}
	}
	if !hasInetFinding {
		t.Error("expected internet finding when internet latency >> LAN latency")
	}
}

func TestAssess_NoData(t *testing.T) {
	a := Assess(Input{})
	if a.Summary == "" {
		t.Error("expected non-empty summary even with no data")
	}
}

func TestAssess_WiredClient_NoSignalFinding(t *testing.T) {
	in := Input{
		UnifiClient: &unifi.Client{IsWired: true, RSSI: -90},
		LANLatency:  &diag.LatencyResult{Avg: 1, Count: 20, Sent: 20},
	}
	a := Assess(in)
	for _, f := range a.Findings {
		if f.Category == "signal" {
			t.Error("should not report signal findings for wired client")
		}
	}
}
