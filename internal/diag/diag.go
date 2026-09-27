// Package diag implements browser-side diagnostic measurements over WebSocket.
// Tests: latency/jitter, download, upload, stability tracking.
package diag

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const (
	// Throughput test limits
	maxConcurrentThroughput = 2
	downloadChunkSize       = 64 * 1024  // 64 KB per frame
	maxDownloadBytes        = 50_000_000 // 50 MB cap per test
	maxUploadBytes          = 20_000_000 // 20 MB cap per test
	warmupDuration          = 2 * time.Second
	defaultTestDuration     = 10 * time.Second
	latencyPings            = 20
	pingTimeout             = 5 * time.Second
)

// MsgType identifies WebSocket message types.
type MsgType string

const (
	MsgPing      MsgType = "ping"
	MsgPong      MsgType = "pong"
	MsgStartDL   MsgType = "start_download"
	MsgStartUL   MsgType = "start_upload"
	MsgProgress  MsgType = "progress"
	MsgResult    MsgType = "result"
	MsgError     MsgType = "error"
	MsgBusy      MsgType = "busy"
)

// Envelope is the JSON message envelope exchanged over WebSocket.
type Envelope struct {
	Type    MsgType         `json:"type"`
	Seq     int64           `json:"seq,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// LatencyResult holds computed latency statistics.
type LatencyResult struct {
	Samples []float64 `json:"samples_ms"`
	Min     float64   `json:"min_ms"`
	Avg     float64   `json:"avg_ms"`
	Median  float64   `json:"median_ms"`
	P95     float64   `json:"p95_ms"`
	Max     float64   `json:"max_ms"`
	Jitter  float64   `json:"jitter_ms"`
	Loss    float64   `json:"loss_pct"`
	Count   int       `json:"count"`
	Sent    int       `json:"sent"`
}

// ThroughputResult holds throughput measurement results.
type ThroughputResult struct {
	MbpsAvg      float64 `json:"mbps_avg"`
	MbpsPeak     float64 `json:"mbps_peak"`
	BytesTotal   int64   `json:"bytes_total"`
	DurationSecs float64 `json:"duration_secs"`
	WarmupMs     float64 `json:"warmup_ms"`
}

// StabilityEvent records a notable event during the session.
type StabilityEvent struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Detail  string    `json:"detail,omitempty"`
}

// Handler manages WebSocket diagnostic connections with a global throughput concurrency limit.
type Handler struct {
	logger            *slog.Logger
	activeThroughput  atomic.Int32
	throughputQueue   chan struct{}
}

// NewHandler creates a new diagnostic handler.
func NewHandler(logger *slog.Logger) *Handler {
	return &Handler{
		logger:          logger,
		throughputQueue: make(chan struct{}, maxConcurrentThroughput),
	}
}

// ServeWS upgrades the connection and runs the diagnostic protocol.
func (h *Handler) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // CORS handled separately
	})
	if err != nil {
		h.logger.Warn("diag: ws accept failed", "error", err)
		return
	}
	defer conn.CloseNow()

	ctx := r.Context()
	h.logger.Debug("diag: ws connected", "remote", r.RemoteAddr)

	if err := h.runDiagnosticSession(ctx, conn, r); err != nil {
		h.logger.Debug("diag: ws session ended", "error", err)
	}
}

func (h *Handler) runDiagnosticSession(ctx context.Context, conn *websocket.Conn, r *http.Request) error {
	for {
		var env Envelope
		if err := wsjson.Read(ctx, conn, &env); err != nil {
			return err
		}

		switch env.Type {
		case MsgPing:
			// Echo back immediately.
			resp := Envelope{Type: MsgPong, Seq: env.Seq}
			if err := wsjson.Write(ctx, conn, resp); err != nil {
				return err
			}

		case MsgStartDL:
			if err := h.handleDownload(ctx, conn, env); err != nil {
				return err
			}

		case MsgStartUL:
			if err := h.handleUpload(ctx, conn, env); err != nil {
				return err
			}

		default:
			h.logger.Debug("diag: unknown message type", "type", env.Type)
		}
	}
}

// handleDownload streams random data to the client, tracking throughput.
func (h *Handler) handleDownload(ctx context.Context, conn *websocket.Conn, env Envelope) error {
	// Try to acquire a throughput slot.
	select {
	case h.throughputQueue <- struct{}{}:
		defer func() { <-h.throughputQueue }()
	default:
		// No slots available — inform the client.
		waiting := int(h.activeThroughput.Load()) + 1
		payload, _ := json.Marshal(map[string]any{
			"message":  "throughput test slots full",
			"position": waiting,
		})
		return wsjson.Write(ctx, conn, Envelope{Type: MsgBusy, Seq: env.Seq, Payload: payload})
	}
	h.activeThroughput.Add(1)
	defer h.activeThroughput.Add(-1)

	var opts struct {
		DurationSecs int `json:"duration_secs"`
	}
	opts.DurationSecs = 10
	if env.Payload != nil {
		_ = json.Unmarshal(env.Payload, &opts)
	}
	if opts.DurationSecs < 1 || opts.DurationSecs > 30 {
		opts.DurationSecs = 10
	}

	testDur := time.Duration(opts.DurationSecs) * time.Second
	chunk := make([]byte, downloadChunkSize)

	deadline := time.Now().Add(testDur)
	start := time.Now()
	var totalBytes int64
	var warmupEnd time.Time
	var peakMbps float64

	for time.Now().Before(deadline) && totalBytes < maxDownloadBytes {
		if err := conn.Write(ctx, websocket.MessageBinary, chunk); err != nil {
			return err
		}
		totalBytes += int64(len(chunk))

		elapsed := time.Since(start)
		if elapsed >= warmupDuration && warmupEnd.IsZero() {
			warmupEnd = time.Now()
		}

		// Send periodic progress updates.
		if totalBytes%(downloadChunkSize*16) == 0 {
			mbps := float64(totalBytes*8) / elapsed.Seconds() / 1e6
			if mbps > peakMbps {
				peakMbps = mbps
			}
			payload, _ := json.Marshal(map[string]any{
				"bytes":   totalBytes,
				"mbps":    mbps,
				"elapsed": elapsed.Seconds(),
			})
			_ = wsjson.Write(ctx, conn, Envelope{Type: MsgProgress, Seq: env.Seq, Payload: payload})
		}
	}

	elapsed := time.Since(start)
	// Exclude warmup from average calculation.
	measuredBytes := totalBytes
	measuredDur := elapsed
	if !warmupEnd.IsZero() {
		warmupBytes := int64(float64(totalBytes) * warmupDuration.Seconds() / elapsed.Seconds())
		measuredBytes = totalBytes - warmupBytes
		measuredDur = elapsed - warmupDuration
	}

	avgMbps := 0.0
	if measuredDur > 0 {
		avgMbps = float64(measuredBytes*8) / measuredDur.Seconds() / 1e6
	}

	result := ThroughputResult{
		MbpsAvg:      round2(avgMbps),
		MbpsPeak:     round2(peakMbps),
		BytesTotal:   totalBytes,
		DurationSecs: round2(elapsed.Seconds()),
		WarmupMs:     warmupDuration.Seconds() * 1000,
	}
	payload, _ := json.Marshal(result)
	return wsjson.Write(ctx, conn, Envelope{Type: MsgResult, Seq: env.Seq, Payload: payload})
}

// handleUpload receives data from the client, tracking throughput.
func (h *Handler) handleUpload(ctx context.Context, conn *websocket.Conn, env Envelope) error {
	select {
	case h.throughputQueue <- struct{}{}:
		defer func() { <-h.throughputQueue }()
	default:
		waiting := int(h.activeThroughput.Load()) + 1
		payload, _ := json.Marshal(map[string]any{
			"message":  "throughput test slots full",
			"position": waiting,
		})
		return wsjson.Write(ctx, conn, Envelope{Type: MsgBusy, Seq: env.Seq, Payload: payload})
	}
	h.activeThroughput.Add(1)
	defer h.activeThroughput.Add(-1)

	var opts struct {
		DurationSecs int `json:"duration_secs"`
	}
	opts.DurationSecs = 10
	if env.Payload != nil {
		_ = json.Unmarshal(env.Payload, &opts)
	}
	if opts.DurationSecs < 1 || opts.DurationSecs > 30 {
		opts.DurationSecs = 10
	}

	// Signal client to start sending.
	readyPayload, _ := json.Marshal(map[string]any{"ready": true, "duration_secs": opts.DurationSecs})
	if err := wsjson.Write(ctx, conn, Envelope{Type: MsgProgress, Seq: env.Seq, Payload: readyPayload}); err != nil {
		return err
	}

	deadline := time.Now().Add(time.Duration(opts.DurationSecs)*time.Second + 2*time.Second)
	start := time.Now()
	var totalBytes int64
	var warmupEnd time.Time
	var peakMbps float64

	for time.Now().Before(deadline) && totalBytes < maxUploadBytes {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		if typ != websocket.MessageBinary {
			// Upload done signal from client (text frame).
			break
		}
		totalBytes += int64(len(data))

		elapsed := time.Since(start)
		if elapsed >= warmupDuration && warmupEnd.IsZero() {
			warmupEnd = time.Now()
		}

		mbps := float64(totalBytes*8) / elapsed.Seconds() / 1e6
		if mbps > peakMbps {
			peakMbps = mbps
		}
	}

	elapsed := time.Since(start)
	measuredBytes := totalBytes
	measuredDur := elapsed
	if !warmupEnd.IsZero() {
		warmupBytes := int64(float64(totalBytes) * warmupDuration.Seconds() / elapsed.Seconds())
		measuredBytes = totalBytes - warmupBytes
		measuredDur = elapsed - warmupDuration
	}

	avgMbps := 0.0
	if measuredDur > 0 {
		avgMbps = float64(measuredBytes*8) / measuredDur.Seconds() / 1e6
	}

	result := ThroughputResult{
		MbpsAvg:      round2(avgMbps),
		MbpsPeak:     round2(peakMbps),
		BytesTotal:   totalBytes,
		DurationSecs: round2(elapsed.Seconds()),
		WarmupMs:     warmupDuration.Seconds() * 1000,
	}
	payload, _ := json.Marshal(result)
	return wsjson.Write(ctx, conn, Envelope{Type: MsgResult, Seq: env.Seq, Payload: payload})
}

// LatencyStats computes statistics from a slice of round-trip times in milliseconds.
func LatencyStats(samples []float64, sent int) LatencyResult {
	result := LatencyResult{
		Sent:    sent,
		Count:   len(samples),
		Samples: samples,
	}
	if len(samples) == 0 {
		result.Loss = 100
		return result
	}

	sorted := make([]float64, len(samples))
	copy(sorted, samples)
	sort.Float64s(sorted)

	var sum float64
	for _, v := range sorted {
		sum += v
	}
	result.Min = sorted[0]
	result.Max = sorted[len(sorted)-1]
	result.Avg = round2(sum / float64(len(sorted)))
	result.Median = percentile(sorted, 50)
	result.P95 = percentile(sorted, 95)

	// Jitter: mean of absolute differences between successive samples.
	if len(samples) > 1 {
		var jitterSum float64
		for i := 1; i < len(samples); i++ {
			jitterSum += math.Abs(samples[i] - samples[i-1])
		}
		result.Jitter = round2(jitterSum / float64(len(samples)-1))
	}

	if sent > 0 {
		result.Loss = round2(float64(sent-len(samples)) / float64(sent) * 100)
	}
	return result
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := (p / 100) * float64(len(sorted)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi {
		return round2(sorted[lo])
	}
	frac := idx - float64(lo)
	return round2(sorted[lo]*(1-frac) + sorted[hi]*frac)
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// HTTPLatencyHandler serves a simple HTTP ping endpoint as a fallback.
func HTTPLatencyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, `{"pong":true,"ts":%d}`, time.Now().UnixMilli())
}

// HTTPDownloadHandler streams bytes as an HTTP chunked response (WS fallback).
func HTTPDownloadHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	chunk := make([]byte, downloadChunkSize)
	var total int64
	deadline := time.Now().Add(defaultTestDuration)
	ctx := r.Context()

	for time.Now().Before(deadline) && total < maxDownloadBytes {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if _, err := w.Write(chunk); err != nil {
			return
		}
		total += int64(len(chunk))
		flusher.Flush()
	}
}

// Ensure Handler is not accidentally value-copied.
var _ sync.Locker = (*sync.Mutex)(nil)
