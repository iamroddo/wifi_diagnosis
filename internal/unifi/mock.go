package unifi

import (
	"context"
	"math"
	"sync"
	"time"
)

// MockClient is a deterministic UniFi client for development and testing.
// It is safe for concurrent use.
type MockClient struct {
	mu          sync.RWMutex
	clients     []Client
	unavailable bool
}

// NewMockClient creates a MockClient pre-populated with a set of fake clients.
func NewMockClient() *MockClient {
	return &MockClient{
		clients: []Client{
			{
				MAC:          "aa:bb:cc:dd:ee:01",
				IP:           "192.168.20.47",
				Hostname:     "iPhone-Rod",
				Name:         "Rod's iPhone",
				ESSID:        "Home",
				APMAC:        "78:45:58:aa:bb:01",
				Band:         "5G",
				Channel:      44,
				ChannelWidth: "80",
				RSSI:         -67,
				TXRate:       866000,
				RXRate:       780000,
				Retries:      7,
				Satisfaction: 92,
				LastSeen:     time.Now(),
			},
			{
				MAC:          "aa:bb:cc:dd:ee:02",
				IP:           "192.168.20.48",
				Hostname:     "MacBook-Pro",
				Name:         "Rod's MacBook",
				ESSID:        "Home",
				APMAC:        "78:45:58:aa:bb:01",
				Band:         "5G",
				Channel:      44,
				ChannelWidth: "80",
				RSSI:         -54,
				TXRate:       1300000,
				RXRate:       1300000,
				Retries:      1,
				Satisfaction: 98,
				LastSeen:     time.Now(),
			},
		},
	}
}

// SetUnavailable controls whether the mock simulates a controller outage.
func (m *MockClient) SetUnavailable(v bool) {
	m.mu.Lock()
	m.unavailable = v
	m.mu.Unlock()
}

// AddClient adds a client to the mock, useful in tests.
func (m *MockClient) AddClient(cl Client) {
	m.mu.Lock()
	m.clients = append(m.clients, cl)
	m.mu.Unlock()
}

// SetClients replaces the full client list.
func (m *MockClient) SetClients(clients []Client) {
	m.mu.Lock()
	m.clients = clients
	m.mu.Unlock()
}

// FindClientByIP implements UniFiClient.
func (m *MockClient) FindClientByIP(_ context.Context, ip string) (*Client, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.unavailable {
		return nil, &UnavailableError{}
	}

	var matches []Client
	for _, cl := range m.clients {
		if cl.IP == ip {
			matches = append(matches, cl)
		}
	}
	switch len(matches) {
	case 0:
		return nil, &NotFoundError{IP: ip}
	case 1:
		cl := matches[0]
		return &cl, nil
	default:
		return nil, &AmbiguousError{IP: ip, Count: len(matches)}
	}
}

// ClientHistory implements UniFiClient with synthetic 5-minute buckets for the
// last 24 hours, simulating gradual signal and rate variation.
func (m *MockClient) ClientHistory(_ context.Context, mac string) ([]HistorySample, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.unavailable {
		return nil, &UnavailableError{}
	}

	// Find the client so we can base the mock history around its current values.
	var base *Client
	for i := range m.clients {
		if m.clients[i].MAC == mac {
			base = &m.clients[i]
			break
		}
	}
	if base == nil {
		return nil, nil
	}

	now := time.Now().Truncate(5 * time.Minute)
	const buckets = 288 // 24 h × 12 buckets/h
	samples := make([]HistorySample, 0, buckets)
	for i := buckets - 1; i >= 0; i-- {
		t := now.Add(-time.Duration(i) * 5 * time.Minute)
		// Small sinusoidal wobble + occasional dips so charts look realistic.
		phase := float64(buckets-i) / float64(buckets) * 2 * 3.14159
		signalWobble := int(4 * (0.5 - 0.5*math.Sin(phase*3)))
		rateWobble := int(50000 * math.Sin(phase*2))
		satisfaction := base.Satisfaction + int(3*math.Sin(phase))
		if satisfaction < 0 {
			satisfaction = 0
		} else if satisfaction > 100 {
			satisfaction = 100
		}
		samples = append(samples, HistorySample{
			Time:         t,
			Signal:       base.RSSI + signalWobble,
			TXRate:       base.TXRate + rateWobble,
			RXRate:       base.RXRate - rateWobble/2,
			TXBytes:      int64(200_000 + i*1000),
			RXBytes:      int64(800_000 + i*4000),
			Satisfaction: satisfaction,
		})
	}
	return samples, nil
}

// Ping implements UniFiClient.
func (m *MockClient) Ping(_ context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.unavailable {
		return &UnavailableError{}
	}
	return nil
}
