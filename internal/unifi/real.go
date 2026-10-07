package unifi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	defaultCacheTTL    = 8 * time.Second
	defaultRateLimit   = rate.Limit(5) // 5 requests/sec to the controller
	defaultRateBurst   = 10
	loginRetryInterval = 30 * time.Second
)

// RealClient is the production UniFi client.
type RealClient struct {
	cfg    RealConfig
	logger *slog.Logger

	mu          sync.Mutex
	httpClient  *http.Client
	loggedIn    bool
	lastLoginAt time.Time
	csrfToken   string

	cacheMu    sync.RWMutex
	cache      []Client
	cachedAt   time.Time

	deviceCacheMu sync.RWMutex
	deviceCache   map[string]string // MAC → device name
	deviceCachedAt time.Time

	limiter *rate.Limiter
}

// RealConfig is the configuration for RealClient.
type RealConfig struct {
	BaseURL   string
	Username  string
	Password  string
	VerifyTLS bool
	Site      string
}

// NewRealClient creates a new production UniFi client.
func NewRealClient(cfg RealConfig, logger *slog.Logger) *RealClient {
	jar, _ := cookiejar.New(nil)
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: !cfg.VerifyTLS}, //nolint:gosec
	}
	httpClient := &http.Client{
		Jar:       jar,
		Transport: transport,
		Timeout:   15 * time.Second,
	}
	return &RealClient{
		cfg:        cfg,
		logger:     logger,
		httpClient: httpClient,
		limiter:    rate.NewLimiter(defaultRateLimit, defaultRateBurst),
	}
}

// Ping checks whether the controller is reachable and the session is valid.
func (c *RealClient) Ping(ctx context.Context) error {
	if err := c.ensureLoggedIn(ctx); err != nil {
		return err
	}
	return nil
}

// FindClientByIP returns the active client matching ip from the UniFi controller.
func (c *RealClient) FindClientByIP(ctx context.Context, ip string) (*Client, error) {
	clients, err := c.fetchClients(ctx)
	if err != nil {
		return nil, err
	}

	var matches []Client
	for _, cl := range clients {
		if cl.IP == ip {
			matches = append(matches, cl)
		}
	}

	switch len(matches) {
	case 0:
		return nil, &NotFoundError{IP: ip}
	case 1:
		cl := matches[0]
		if cl.APMAC != "" {
			if name, err := c.lookupAPName(ctx, cl.APMAC); err == nil {
				cl.APName = name
			}
		}
		return &cl, nil
	default:
		return nil, &AmbiguousError{IP: ip, Count: len(matches)}
	}
}

// fetchClients returns all active clients, using a short TTL cache.
func (c *RealClient) fetchClients(ctx context.Context) ([]Client, error) {
	c.cacheMu.RLock()
	if time.Since(c.cachedAt) < defaultCacheTTL && c.cache != nil {
		clients := c.cache
		c.cacheMu.RUnlock()
		return clients, nil
	}
	c.cacheMu.RUnlock()

	// Cache miss — fetch from controller.
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, &UnavailableError{Cause: err}
	}

	if err := c.ensureLoggedIn(ctx); err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/api/s/%s/stat/sta", c.cfg.BaseURL, c.cfg.Site)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, &UnavailableError{Cause: err}
	}
	req.Header.Set("Accept", "application/json")

	c.mu.Lock()
	csrf := c.csrfToken
	c.mu.Unlock()
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.mu.Lock()
		c.loggedIn = false
		c.mu.Unlock()
		return nil, &UnavailableError{Cause: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		resp.Body.Close()
		// Session expired — clear state and retry once with a fresh login.
		c.mu.Lock()
		c.loggedIn = false
		c.mu.Unlock()
		if err := c.ensureLoggedIn(ctx); err != nil {
			return nil, err
		}
		req2, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, &UnavailableError{Cause: err}
		}
		req2.Header.Set("Accept", "application/json")
		c.mu.Lock()
		if c.csrfToken != "" {
			req2.Header.Set("X-CSRF-Token", c.csrfToken)
		}
		c.mu.Unlock()
		resp, err = c.httpClient.Do(req2)
		if err != nil {
			return nil, &UnavailableError{Cause: err}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, &UnavailableError{Cause: fmt.Errorf("unexpected status %d after re-login", resp.StatusCode)}
		}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &UnavailableError{Cause: fmt.Errorf("unexpected status %d from stat/sta", resp.StatusCode)}
	}

	// Capture updated CSRF token if present.
	if t := resp.Header.Get("X-CSRF-Token"); t != "" {
		c.mu.Lock()
		c.csrfToken = t
		c.mu.Unlock()
	}
	if t := resp.Header.Get("X-Updated-Csrf-Token"); t != "" {
		c.mu.Lock()
		c.csrfToken = t
		c.mu.Unlock()
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &UnavailableError{Cause: err}
	}

	clients, err := parseStaResponse(body)
	if err != nil {
		c.logger.Warn("unifi: failed to parse stat/sta response", "error", err)
		return nil, &UnavailableError{Cause: err}
	}

	c.cacheMu.Lock()
	c.cache = clients
	c.cachedAt = time.Now()
	c.cacheMu.Unlock()

	c.logger.Debug("unifi: fetched active clients", "count", len(clients))
	return clients, nil
}

// ensureLoggedIn logs in if not already authenticated, with a backoff on repeated failures.
func (c *RealClient) ensureLoggedIn(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.loggedIn {
		return nil
	}

	// Don't hammer the controller on repeated failures.
	if !c.lastLoginAt.IsZero() && time.Since(c.lastLoginAt) < loginRetryInterval {
		return &UnavailableError{Cause: fmt.Errorf("login cooldown active")}
	}

	c.lastLoginAt = time.Now()

	payload, _ := json.Marshal(map[string]string{
		"username": c.cfg.Username,
		"password": c.cfg.Password,
	})

	url := c.cfg.BaseURL + "/api/login"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return &UnavailableError{Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &UnavailableError{Cause: fmt.Errorf("login request failed: %w", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &UnavailableError{Cause: fmt.Errorf("login returned HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(body))}
	}
	io.Copy(io.Discard, resp.Body) //nolint:errcheck

	if t := resp.Header.Get("X-CSRF-Token"); t != "" {
		c.csrfToken = t
	}

	c.loggedIn = true
	c.logger.Info("unifi: logged in successfully")
	return nil
}

// staResponse is the envelope returned by /api/s/{site}/stat/sta.
type staResponse struct {
	Data []json.RawMessage `json:"data"`
	Meta struct {
		RC string `json:"rc"`
	} `json:"meta"`
}

// parseStaResponse decodes the stat/sta response, mapping fields defensively.
func parseStaResponse(body []byte) ([]Client, error) {
	var envelope staResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("JSON unmarshal: %w", err)
	}

	clients := make([]Client, 0, len(envelope.Data))
	for _, raw := range envelope.Data {
		// Decode into a generic map first so we get all fields.
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			continue
		}

		cl := Client{
			RawFields: fields,
			LastSeen:  time.Now(),
		}

		cl.MAC = stringField(fields, "mac")
		cl.IP = stringField(fields, "ip")
		cl.Hostname = stringField(fields, "hostname")
		cl.Name = stringField(fields, "name")
		cl.DeviceName = stringField(fields, "device_name")
		cl.ESSID = stringField(fields, "essid")
		cl.APMAC = stringField(fields, "ap_mac")
		cl.Band = stringField(fields, "band")
		cl.ChannelWidth = stringField(fields, "channel_width")
		cl.Channel = intField(fields, "channel")
		cl.IsWired = boolField(fields, "is_wired")
		cl.RSSI = intField(fields, "rssi")
		cl.Signal = intField(fields, "signal")
		cl.TXRate = intField(fields, "tx_rate")
		cl.RXRate = intField(fields, "rx_rate")
		cl.Retries = intField(fields, "retries")
		cl.Satisfaction = intField(fields, "satisfaction")

		// Normalise: some firmware versions use "signal" instead of "rssi".
		if cl.RSSI == 0 && cl.Signal != 0 {
			cl.RSSI = cl.Signal
		}

		clients = append(clients, cl)
	}
	return clients, nil
}

func stringField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

func intField(m map[string]any, key string) int {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

func boolField(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok || v == nil {
		return false
	}
	b, _ := v.(bool)
	return b
}

func int64Field(m map[string]any, key string) int64 {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}

// ClientHistory fetches 5-minute stat buckets for the given client MAC covering
// approximately the last 30 minutes.
func (c *RealClient) ClientHistory(ctx context.Context, mac string) ([]HistorySample, error) {
	if err := c.ensureLoggedIn(ctx); err != nil {
		return nil, err
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, &UnavailableError{Cause: err}
	}

	end := time.Now()
	start := end.Add(-30 * time.Minute)

	payload, _ := json.Marshal(map[string]any{
		"attrs": []string{"signal", "tx_rate", "rx_rate", "tx_bytes", "rx_bytes", "satisfaction"},
		"start": start.Unix(),
		"end":   end.Unix(),
		"macs":  []string{mac},
	})

	url := fmt.Sprintf("%s/api/s/%s/stat/report/5minutes.user", c.cfg.BaseURL, c.cfg.Site)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, &UnavailableError{Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	c.mu.Lock()
	csrf := c.csrfToken
	c.mu.Unlock()
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &UnavailableError{Cause: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		return nil, &UnavailableError{Cause: fmt.Errorf("stat/report/5minutes.user returned HTTP %d", resp.StatusCode)}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &UnavailableError{Cause: err}
	}

	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, &UnavailableError{Cause: fmt.Errorf("JSON unmarshal: %w", err)}
	}

	samples := make([]HistorySample, 0, len(envelope.Data))
	for _, d := range envelope.Data {
		ts := int64Field(d, "time")
		if ts == 0 {
			continue
		}
		s := HistorySample{
			Time:         time.Unix(ts, 0),
			Signal:       intField(d, "signal"),
			TXRate:       intField(d, "tx_rate"),
			RXRate:       intField(d, "rx_rate"),
			TXBytes:      int64Field(d, "tx_bytes"),
			RXBytes:      int64Field(d, "rx_bytes"),
			Satisfaction: intField(d, "satisfaction"),
		}
		samples = append(samples, s)
	}

	c.logger.Debug("unifi: fetched client history", "mac", mac, "samples", len(samples))
	return samples, nil
}

// lookupAPName returns the device name for the given AP MAC, using a cached map.
func (c *RealClient) lookupAPName(ctx context.Context, apMAC string) (string, error) {
	c.deviceCacheMu.RLock()
	if time.Since(c.deviceCachedAt) < defaultCacheTTL && c.deviceCache != nil {
		name := c.deviceCache[apMAC]
		c.deviceCacheMu.RUnlock()
		return name, nil
	}
	c.deviceCacheMu.RUnlock()

	devices, err := c.fetchDeviceNames(ctx)
	if err != nil {
		return "", err
	}
	return devices[apMAC], nil
}

// fetchDeviceNames fetches stat/device-basic and returns a MAC→name map.
func (c *RealClient) fetchDeviceNames(ctx context.Context) (map[string]string, error) {
	if err := c.ensureLoggedIn(ctx); err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/api/s/%s/stat/device-basic", c.cfg.BaseURL, c.cfg.Site)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	c.mu.Lock()
	csrf := c.csrfToken
	c.mu.Unlock()
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		return nil, fmt.Errorf("stat/device-basic returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}

	names := make(map[string]string, len(envelope.Data))
	for _, d := range envelope.Data {
		mac := stringField(d, "mac")
		name := stringField(d, "name")
		if mac != "" && name != "" {
			names[mac] = name
		}
	}

	c.deviceCacheMu.Lock()
	c.deviceCache = names
	c.deviceCachedAt = time.Now()
	c.deviceCacheMu.Unlock()

	return names, nil
}
