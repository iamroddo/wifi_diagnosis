// Package unifi provides a client for the self-hosted UniFi Network Application API.
// It targets the classic controller (bare /api/s/{site}/... paths, POST /api/login auth).
package unifi

import (
	"context"
	"time"
)

// Client holds UniFi client/station information returned from stat/sta.
// Fields are mapped defensively — missing or null fields remain zero values.
type Client struct {
	// Identity
	MAC        string `json:"mac"`
	IP         string `json:"ip"`
	Hostname   string `json:"hostname"`
	Name       string `json:"name"`        // user-assigned
	DeviceName string `json:"device_name"` // alternative name field

	// Wireless association
	ESSID    string `json:"essid"`   // SSID
	APMAC    string `json:"ap_mac"`  // AP MAC address
	APName   string `json:"ap_name"` // AP device name (from stat/device-basic)
	Band     string `json:"band"`    // "5G", "2.4G", "6G"
	Channel  int    `json:"channel"` // channel number
	IsWired  bool   `json:"is_wired"`

	// RF metrics
	RSSI         int `json:"rssi"`          // signal strength dBm (negative)
	Signal       int `json:"signal"`        // sometimes used instead of rssi
	TXRate       int `json:"tx_rate"`       // bps
	RXRate       int `json:"rx_rate"`       // bps
	Retries      int `json:"retries"`       // retry count
	Satisfaction int `json:"satisfaction"`  // 0–100

	// Raw extra fields for forward-compatibility
	ChannelWidth string `json:"channel_width"`

	// Internal
	LastSeen  time.Time
	RawFields map[string]any // all raw fields from the API response
}

// DisplayName returns the most useful human-readable name for the client.
func (c *Client) DisplayName() string {
	if c.Name != "" {
		return c.Name
	}
	if c.DeviceName != "" {
		return c.DeviceName
	}
	if c.Hostname != "" {
		return c.Hostname
	}
	return c.MAC
}

// HistorySample is one 5-minute bucket of per-client stats from the controller.
type HistorySample struct {
	Time         time.Time `json:"time"`
	Signal       int       `json:"signal_dbm"`       // dBm (negative)
	TXRate       int       `json:"tx_rate_bps"`      // bps
	RXRate       int       `json:"rx_rate_bps"`      // bps
	TXBytes      int64     `json:"tx_bytes"`
	RXBytes      int64     `json:"rx_bytes"`
	Satisfaction int       `json:"satisfaction"`     // 0–100, 0 = not reported
}

// UniFiClient is the interface for querying the UniFi controller.
// Implementations must be safe for concurrent use.
type UniFiClient interface {
	// FindClientByIP returns the active client with the given IP address,
	// or ErrNotFound if no matching active client exists.
	// Returns ErrAmbiguous if multiple clients share the same IP.
	FindClientByIP(ctx context.Context, ip string) (*Client, error)

	// ClientHistory returns up to 30 minutes of 5-minute stat buckets for the
	// given client MAC. Returns an empty slice (not an error) when the
	// controller has no history for that client.
	ClientHistory(ctx context.Context, mac string) ([]HistorySample, error)

	// Ping verifies connectivity to the controller. Returns nil if reachable.
	Ping(ctx context.Context) error
}

// Sentinel errors.
type NotFoundError struct{ IP string }
type AmbiguousError struct{ IP string; Count int }
type UnavailableError struct{ Cause error }

func (e *NotFoundError) Error() string   { return "unifi: no active client with IP " + e.IP }
func (e *AmbiguousError) Error() string  { return "unifi: multiple active clients with IP " + e.IP }
func (e *UnavailableError) Error() string {
	if e.Cause != nil {
		return "unifi: controller unavailable: " + e.Cause.Error()
	}
	return "unifi: controller unavailable"
}

func IsNotFound(err error) bool {
	_, ok := err.(*NotFoundError)
	return ok
}

func IsAmbiguous(err error) bool {
	_, ok := err.(*AmbiguousError)
	return ok
}

func IsUnavailable(err error) bool {
	_, ok := err.(*UnavailableError)
	return ok
}
